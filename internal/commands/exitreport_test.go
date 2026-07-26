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
		_, dir := paths, paths.WorkDir
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
		name  string
		key   string
		value string
	}{
		{"hooksPath redirect", "core.hooksPath", ".husky/_"},
		{"credential helper", "credential.helper", "!/tmp/x"},
		{"ssh command", "core.sshCommand", "/tmp/ssh"},
		{"fsmonitor", "core.fsmonitor", "/tmp/mon"},
		{"filter smudge", "filter.evil.smudge", "/tmp/s"},
		{"diff textconv", "diff.evil.textconv", "/tmp/t"},
		{"init templateDir", "init.templateDir", "/tmp/tpl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, dir := exitRepo(t)
			got := exitReport(t, paths, func() { gitIn(t, dir, "config", tc.key, tc.value) })
			// The rule: the exec-relevant key is named, and so is where it now
			// points -- that is the whole reason for saying anything.
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.key)) {
				t.Errorf("expected %s to be named, got:\n%s", tc.key, got)
			}
			if !strings.Contains(got, tc.value) {
				t.Errorf("expected the new value %q, got:\n%s", tc.value, got)
			}
		})
	}

	t.Run("a plain alias is quiet, a shelling alias speaks", func(t *testing.T) {
		paths, dir := exitRepo(t)
		if got := exitReport(t, paths, func() { gitIn(t, dir, "config", "alias.co", "checkout") }); got != "" {
			t.Errorf("a plain alias must not speak, got:\n%s", got)
		}
		if got := exitReport(t, paths, func() { gitIn(t, dir, "config", "alias.pwn", "!sh -c 'curl evil'") }); !strings.Contains(got, "alias.pwn") {
			t.Errorf("a ! alias must speak, got:\n%s", got)
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
		case <-timeAfterSeconds(20):
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
	// It must not read as a path inside the worktree's own checkout.
	if strings.HasPrefix(strings.TrimSpace(got), ".git/") {
		t.Errorf("main-tree path rendered as worktree-relative:\n%s", got)
	}
	if !strings.Contains(got, main) && !strings.Contains(got, "~") {
		t.Errorf("expected an unambiguous location for the main tree, got:\n%s", got)
	}
}

// timeAfterSeconds keeps the hang test readable without importing time into
// every case above.
func timeAfterSeconds(n int) <-chan time.Time { return time.After(time.Duration(n) * time.Second) }
