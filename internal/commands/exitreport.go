package commands

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// The exit report: what byre noticed changing, in a small fixed set of places
// the HOST runs code from, while a session was running (ADR 0047).
//
// The project mount is the one grant every box has, and the project is a
// directory host tools execute: a git hook, a git config naming a program, an
// env file steering a later process. The human's next ordinary host-side git
// runs whatever the agent left. Most of what an agent writes lands in the diff,
// where reviewing it is already the user's job -- these places do not, so byre
// points them out.
//
// This is NOT containment and must never be worded as if it were. byre is not a
// security product; against an actively malicious agent it takes some simple
// precautions and claims nothing more. The watch set is deliberately small, it
// is incomplete by construction, and the report says so.

// maxEnvFileBytes bounds a read of an agent-writable env file. Real .env files
// are hundreds of bytes; this is pure slack, and the cap is what keeps a device
// node or a planted symlink from streaming into host byre.
const maxEnvFileBytes = 1 << 20 // 1 MiB

// maxHooksEntries bounds the hooks walk. A stock hooks directory holds ~14
// samples. The cap matters for a core.hooksPath redirected at a large in-tree
// directory: byre reports the redirect either way (it is an exec-relevant key),
// so truncating the walk costs nothing that matters.
const maxHooksEntries = 500

// execRelevantConfig reports whether a git config key can put a program on the
// host's path of execution. It is RANKING, not gating: its job is to keep the
// report rare enough to be read, by staying silent on the config churn that
// ends an ordinary session (branch.*.remote from `push -u`, remote.*.url from
// `remote add`, the filter.lfs.* that `git lfs install` writes).
//
// It is incomplete by construction and that is fine -- ADR 0009 rejected an
// exec-capable-key allowlist as a GATE, where incompleteness fails silently.
// Here incompleteness just means byre says less, and the report's own wording
// ("a handful of places, not everything") never claims otherwise.
//
// Keys arrive from `git config --list` already lowercased in section and key
// (subsection case is preserved, hence the suffix matching rather than exact).
func execRelevantConfig(key, value string) bool {
	switch key {
	case "core.hookspath", "core.fsmonitor", "core.sshcommand", "core.pager",
		"core.editor", "credential.helper", "init.templatedir", "gpg.program",
		"ssh.variant", "include.path":
		return true
	}
	// alias.<name> only when it shells out: `!sh -c ...`. A plain alias
	// (alias.co = checkout) is ordinary configuration and stays quiet.
	if strings.HasPrefix(key, "alias.") {
		return strings.HasPrefix(strings.TrimSpace(value), "!")
	}
	// Subsectioned forms: filter.<name>.smudge, diff.<name>.textconv,
	// credential.<url>.helper, url.<name>.insteadof, includeif.<cond>.path.
	for _, suffix := range []string{
		".clean", ".smudge", ".process", ".textconv", ".command",
		".helper", ".insteadof", ".path",
	} {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		switch {
		case strings.HasPrefix(key, "filter."),
			strings.HasPrefix(key, "diff."),
			strings.HasPrefix(key, "credential."),
			strings.HasPrefix(key, "url."),
			strings.HasPrefix(key, "includeif."):
			return true
		}
	}
	return false
}

// exitSnapshot is what the report compares. Each map is keyed by a DISPLAY
// path (what the user will read), so a worktree session naming the main tree's
// git dir cannot be mistaken for something inside its own checkout.
type exitSnapshot struct {
	hooks  map[string]string            // display path -> content signature
	config map[string]map[string]string // display path -> git config key -> value
	env    map[string]map[string]string // display path -> env key -> value
}

// snapshotExit derives the watch set fresh and reads it. Derived fresh on BOTH
// sides on purpose: a session can create a .env.local, or point core.hooksPath
// somewhere new, and a set captured only at the start would miss it.
//
// Every read rides hostopen through a contained root (CLAUDE.md's rule for
// host-side reads of agent-writable paths), so a planted FIFO or a swapped
// symlink degrades that one entry instead of hanging or ballooning host byre.
// Nothing here blocks or fails a session: an unreadable target just sits out
// the comparison.
func snapshotExit(paths project.Paths) exitSnapshot {
	s := exitSnapshot{
		hooks:  map[string]string{},
		config: map[string]map[string]string{},
		env:    map[string]map[string]string{},
	}

	gitDir := commonGitDirOf(paths)
	if gitDir != "" {
		// Config files: the common config, plus every config.worktree that
		// extensions.worktreeConfig can put exec-capable keys in -- the main
		// worktree's and each linked worktree's admin dir. `git config
		// --worktree core.hooksPath ...` opens the whole channel without ever
		// touching `config`.
		for _, cfg := range gitConfigFiles(gitDir) {
			if kv, ok := readGitConfig(cfg); ok {
				s.config[exitDisplay(paths, cfg)] = kv
			}
		}
		hooksDirs := []string{filepath.Join(gitDir, "hooks")}
		// A redirected core.hooksPath is watched only where it stays inside a
		// tree byre already reaches. Outside that, the redirect is REPORTED
		// (core.hookspath is exec-relevant) but never traversed: the target can
		// be $HOME or /tmp, and walking an arbitrary host directory is a cost
		// and a privacy problem byre has no business taking on.
		if p, ok := containedHooksPath(paths, gitDir); ok {
			hooksDirs = append(hooksDirs, p)
		}
		for _, dir := range hooksDirs {
			snapshotHooks(s.hooks, paths, dir)
		}
	}

	for _, f := range envFiles(paths.WorkDir) {
		if kv, ok := readEnvKeys(f); ok {
			s.env[exitDisplay(paths, f)] = kv
		}
	}
	return s
}

// commonGitDirOf resolves the git admin directory to watch. For a linked
// worktree that is the COMMON dir (the main tree's), not the worktree's own
// `.git` -- which is a FILE holding a gitdir pointer, with no config or hooks
// of its own. Bound rw into every worktree box (runparams.go), so a worktree
// session's writes land where the human's main-tree git will read them.
func commonGitDirOf(paths project.Paths) string {
	if paths.CommonGitDirHost != "" {
		return paths.CommonGitDirHost
	}
	// Standalone checkout: <WorkDir>/.git, and only when it really is a
	// directory. A `.git` file (submodule) or a missing one just means there is
	// no admin dir here to watch -- degrade, never guess.
	dir := filepath.Join(paths.WorkDir, ".git")
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}

// gitConfigFiles lists the config files that can carry exec-capable keys for
// this repository: the common config, the main worktree's config.worktree, and
// each linked worktree's. Missing files are simply absent from the snapshot.
func gitConfigFiles(gitDir string) []string {
	files := []string{
		filepath.Join(gitDir, "config"),
		filepath.Join(gitDir, "config.worktree"),
	}
	entries, err := os.ReadDir(filepath.Join(gitDir, "worktrees"))
	if err != nil {
		return files
	}
	for _, e := range entries {
		if e.IsDir() {
			files = append(files, filepath.Join(gitDir, "worktrees", e.Name(), "config.worktree"))
		}
	}
	return files
}

// readGitConfig reads one config file's key/value pairs using git's OWN parser
// rather than a hand-rolled one: subsections, quoting, continuations and
// includes are git's business, and a parser that got them subtly wrong would
// fail by staying quiet. Rides gitProbe, so it is bounded and time-capped
// against a hostile file and degrades (ok=false) when git is absent or the file
// is unreadable -- an unsolicited probe never blocks a session.
func readGitConfig(path string) (map[string]string, bool) {
	out, err := gitProbe("config", "--file", path, "--list", "-z")
	if err != nil {
		return nil, false
	}
	kv := map[string]string{}
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		// `--list -z` emits "key\nvalue" per NUL-terminated record; a valueless
		// key has no newline at all.
		key, value, _ := strings.Cut(rec, "\n")
		kv[key] = value
	}
	return kv, true
}

// containedHooksPath resolves core.hooksPath and returns it only when it stays
// inside a tree byre already reaches (the worktree, or the main checkout).
// Relative values resolve against the working tree root, which for a linked
// worktree is the WORKTREE -- not the main tree and not the git dir.
func containedHooksPath(paths project.Paths, gitDir string) (string, bool) {
	out, err := gitProbe("config", "--file", filepath.Join(gitDir, "config"), "--get", "core.hooksPath")
	if err != nil {
		return "", false
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", false
	}
	p := raw
	if !filepath.IsAbs(p) {
		p = filepath.Join(paths.WorkDir, p)
	}
	for _, root := range []string{paths.WorkDir, paths.Canonical} {
		if root != "" && inTreeByIdentity(root, p) {
			return filepath.Clean(p), true
		}
	}
	return "", false
}

// snapshotHooks records a signature per file under dir. Enumeration rides a
// contained, no-follow root so the walk cannot be steered outside the directory
// that was selected, and stops at maxHooksEntries.
func snapshotHooks(into map[string]string, paths project.Paths, dir string) {
	root, err := hostopen.OpenDirRootNoFollow(dir)
	if err != nil {
		return // no hooks dir, or it was swapped for a symlink: sit it out
	}
	defer root.Close()
	n := 0
	fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if n >= maxHooksEntries {
			return fs.SkipAll
		}
		n++
		into[exitDisplay(paths, filepath.Join(dir, p))] = fileSig(root, p, d)
		return nil
	})
}

// envFiles lists .env and .env.* in the project root. Literal names, never a
// walk: .env.local and .env.development are worth naming, a directory tree is
// not. `.envrc` is deliberately absent -- direnv already gates it (its allow
// record is a hash of path AND content, so an edited .envrc re-blocks until
// `direnv allow` runs), and duplicating that would add noise, not safety.
func envFiles(workDir string) []string {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); n == ".env" || strings.HasPrefix(n, ".env.") {
			out = append(out, filepath.Join(workDir, n))
		}
	}
	sort.Strings(out)
	return out
}

// readEnvKeys extracts key names and values from a KEY=value file. Values are
// read ONLY to detect change and never leave this package -- see reportExit.
// Unparseable lines are skipped rather than failing the file: this is a notice,
// not a linter.
func readEnvKeys(path string) (map[string]string, bool) {
	f, fi, err := hostopen.OpenRegular(path, false)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if fi.Size() > maxEnvFileBytes {
		return nil, false
	}
	kv := map[string]string{}
	sc := bufio.NewScanner(io.LimitReader(f, maxEnvFileBytes))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key != "" {
			kv[key] = value
		}
	}
	if sc.Err() != nil {
		return nil, false
	}
	return kv, true
}

// exitDisplay renders a watched path for the user. Anything under the box's own
// /workspace mount prints repo-relative; anything else -- above all a worktree
// session naming the MAIN tree's git dir -- prints as an abbreviated absolute
// path, so it can never be mistaken for a file in the current checkout.
func exitDisplay(paths project.Paths, p string) string {
	if rel, err := filepath.Rel(paths.WorkDir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return packages.DisplayPath(p)
}

// reportExit prints what changed between two snapshots. Silent unless something
// worth saying changed -- which is what makes it worth reading when it appears.
//
// Attribution is passive ("these changed"), never "the agent changed": the
// common git dir is shared with concurrent sibling worktree sessions, and the
// user's own host-side git can write it mid-session. reportSelfEditChanges
// makes the same choice for the store, for the same reason.
func reportExit(w io.Writer, before, after exitSnapshot) {
	var lines []string

	for _, l := range diffKeyed(before.config, after.config, execRelevantConfig) {
		lines = append(lines, l)
	}
	lines = append(lines, diffHooks(before.hooks, after.hooks)...)
	for _, l := range diffKeyed(before.env, after.env, nil) {
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "⚠ we thought you should know -- a few things changed during this run")
	fmt.Fprintln(w, "  (byre checks a handful of places, not everything):")
	for _, l := range lines {
		fmt.Fprintln(w, "   "+l)
	}
}

// diffHooks names every hook added, changed or removed. Unlike config, no
// filtering: almost nothing ordinary writes here, and when something does it is
// usually a real hook installer (husky, pre-commit, lefthook, git-lfs) that the
// user genuinely wants to know ran.
func diffHooks(before, after map[string]string) []string {
	var out []string
	for p, sig := range after {
		switch b, ok := before[p]; {
		case !ok:
			out = append(out, fmt.Sprintf("%s was added -- your git runs this, on your machine", p))
		case b != sig:
			out = append(out, fmt.Sprintf("%s changed -- your git runs this, on your machine", p))
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			out = append(out, fmt.Sprintf("%s was removed", p))
		}
	}
	sort.Strings(out)
	return out
}

// diffKeyed reports per-file key changes. `speaks` filters which keys are worth
// mentioning (exec-relevant ones for git config); nil means every key speaks,
// which is what env files do -- naming the key that moved turns a bare "this
// file changed" into something the reader can actually judge.
//
// VALUES ARE NEVER PRINTED for env files. They hold secrets, and a value echoed
// here would land in the terminal, in scrollback, and in any captured log --
// an exposure byre would have created. Git config values ARE shown, because the
// whole point of naming core.hooksPath is saying where it now points, and git
// config is not where secrets live (credential.helper names a helper, it does
// not hold the credential).
func diffKeyed(before, after map[string]map[string]string, speaks func(k, v string) bool) []string {
	var out []string
	for file, akv := range after {
		bkv := before[file]
		var added, changed, removed []string
		for k, av := range akv {
			bv, had := bkv[k]
			if had && bv == av {
				continue
			}
			if speaks != nil && !speaks(k, av) {
				continue
			}
			if speaks != nil {
				// Exec-relevant: say where it now points, that's the point.
				if had {
					out = append(out, fmt.Sprintf("%s: %s is now %s", file, k, av))
				} else {
					out = append(out, fmt.Sprintf("%s: %s is set to %s", file, k, av))
				}
				continue
			}
			if had {
				changed = append(changed, k)
			} else {
				added = append(added, k)
			}
		}
		for k, bv := range bkv {
			if _, still := akv[k]; still {
				continue
			}
			if speaks != nil {
				if speaks(k, bv) {
					out = append(out, fmt.Sprintf("%s: %s was removed", file, k))
				}
				continue
			}
			removed = append(removed, k)
		}
		// Key NAMES only -- never a value. See the doc comment.
		for _, g := range []struct {
			verb string
			keys []string
		}{{"added", added}, {"changed", changed}, {"removed", removed}} {
			if len(g.keys) == 0 {
				continue
			}
			sort.Strings(g.keys)
			out = append(out, fmt.Sprintf("%s: %s %s", file, g.verb, strings.Join(g.keys, ", ")))
		}
	}
	sort.Strings(out)
	return out
}
