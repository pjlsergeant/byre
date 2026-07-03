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
	if !isGitRepo(projectDir) {
		return fmt.Errorf("not inside a git repository — run `byre worktree` in a repo (git init / byre develop there first)")
	}
	// paths.Canonical is the MAIN worktree even when run from a linked worktree,
	// so the default sibling path and the inherited identity both anchor on the
	// repo root, not the current worktree.
	paths, err := project.Resolve(projectDir)
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
	if err := createWorktree(projectDir, name, target); err != nil {
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

// createWorktree runs `git worktree add`, creating branch <name> if it does not
// exist yet or checking it out if it does. git's progress goes to stderr so
// stdout stays clean.
func createWorktree(projectDir, name, target string) error {
	args := []string{"-C", projectDir, "worktree", "add"}
	if branchExists(projectDir, name) {
		args = append(args, target, name) // check out the existing branch
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

// isGitRepo reports whether dir is inside a git working tree.
func isGitRepo(dir string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run() == nil
}

// branchExists reports whether a local branch named name already exists.
func branchExists(dir, name string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name).Run() == nil
}
