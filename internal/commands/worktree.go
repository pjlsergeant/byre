package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"byre/internal/project"
)

// Worktree implements `byre worktree <name>`: create a linked git worktree for
// branch <name> as a sibling of the repo, then `byre develop` in it — a parallel
// agent session that inherits the repo's config, volumes, and image, in one
// step. It needs git on PATH (it runs `git worktree add`).
//
// path (--path) overrides the default location, a sibling dir named <repo>-<name>.
// Run from either the main worktree or an existing linked one: identity resolves
// to the main worktree, so the new worktree is always a sibling of the repo root.
func Worktree(projectDir, name, path string, selfEdit bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a worktree name (the branch) is required: byre worktree <name>")
	}
	// Anchor on the repo top level, not the cwd, so `byre worktree` works from a
	// subdirectory (else project.Resolve of a subdir sees no .git and the default
	// path lands INSIDE the repo instead of beside it).
	top, ok := gitToplevel(projectDir)
	if !ok {
		return fmt.Errorf("not inside a git repository — run `byre worktree` in a repo (git init / byre develop there first)")
	}
	// paths.Canonical is the MAIN worktree even when top is a linked worktree, so
	// the default sibling path and the inherited identity both anchor on the repo
	// root, not the current worktree.
	paths, err := project.Resolve(top)
	if err != nil {
		return err
	}
	target := path
	if target == "" {
		target = defaultWorktreePath(paths.Canonical, name)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	// Comma in the path would corrupt develop's docker --mount later; fail before
	// creating the worktree rather than leaving one develop can't run.
	if strings.Contains(target, ",") {
		return fmt.Errorf("target path contains a comma, which docker --mount cannot express: %q", target)
	}
	if _, lerr := os.Lstat(target); lerr == nil {
		return fmt.Errorf("target path already exists: %s (pass --path to choose another location)", target)
	}
	if err := createWorktree(top, name, target); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "byre: created worktree at %s (branch %s); starting a session…\n", target, name)
	// Hand off to develop in the new worktree. If it fails, the worktree is still
	// valid — retry with `byre develop` there, or drop it with `git worktree
	// remove` — so we don't roll back a successful checkout on a develop error.
	return Develop(target, "", "", selfEdit)
}

// defaultWorktreePath places the worktree beside the main repo dir, named
// <repo>-<name>. Branch-name slashes are flattened so it stays a single dir.
func defaultWorktreePath(mainDir, name string) string {
	leaf := filepath.Base(mainDir) + "-" + strings.ReplaceAll(name, "/", "-")
	return filepath.Join(filepath.Dir(mainDir), leaf)
}

// createWorktree runs `git worktree add`. If <name> already names a branch —
// local OR remote-tracking — git checks it out (DWIM-creating a local tracking
// branch for a remote-only one); otherwise a fresh branch is created with -b.
// Passing -b unconditionally would fork a divergent local branch off HEAD when a
// remote branch of that name exists, silently starting the agent on wrong code.
// git's progress goes to stderr so stdout stays clean.
func createWorktree(dir, name, target string) error {
	args := []string{"-C", dir, "worktree", "add"}
	if branchOrRemoteExists(dir, name) {
		args = append(args, target, name) // check out existing (local or remote) branch
	} else {
		args = append(args, "-b", name, target) // create a new branch
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree add failed: %w", err)
	}
	return nil
}

// gitToplevel returns the working tree's root dir for dir (its main or linked
// worktree root), and false if dir is not inside a git repository.
func gitToplevel(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	return top, top != ""
}

// branchExists reports whether a local branch named name already exists.
func branchExists(dir, name string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name).Run() == nil
}

// branchOrRemoteExists reports whether name is already a branch — a local branch,
// or a remote-tracking branch <remote>/<name> — so `git worktree add` should
// check it out rather than create a new branch.
func branchOrRemoteExists(dir, name string) bool {
	if branchExists(dir, name) {
		return true
	}
	out, err := exec.Command("git", "-C", dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes").Output()
	if err != nil {
		return false
	}
	for _, ref := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// ref is "<remote>/<branch>"; compare the part after the first slash.
		if i := strings.IndexByte(ref, '/'); i >= 0 && ref[i+1:] == name {
			return true
		}
	}
	return false
}
