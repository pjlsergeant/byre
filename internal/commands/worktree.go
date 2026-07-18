package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
)

// Worktree implements `byre worktree <name>`: create a linked git worktree for
// branch <name> and `byre develop` in it — a parallel agent session that
// inherits the repo's config, volumes, and image, in one step. It needs git on
// PATH (it runs `git worktree add`).
//
// The location comes from --path, or else the configured worktree_base
// ("sibling" = beside the repo, or a base dir), with the leaf <repo>-<name>;
// unset, byre refuses rather than guess. Run from the main worktree or a
// linked one: identity resolves to the main worktree either way.
func Worktree(s Streams, projectDir, name, path string, selfEdit bool) error {
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
	// the location leaf and the inherited identity both anchor on the repo root,
	// not the current worktree.
	paths, err := project.Resolve(top)
	if err != nil {
		return err
	}
	// Location: --path (explicit) wins; else the configured worktree_base. byre
	// will NOT guess a location (least surprise — no directories created where you
	// didn't ask). Resolved before any git work so we never half-create.
	target := path
	if target == "" {
		parent, berr := worktreeParent(top, paths.Canonical)
		if berr != nil {
			return berr
		}
		if parent == "" {
			return fmt.Errorf("byre worktree needs a location. Set one with `byre config --global` — tick “sibling of repo” or " +
				"give a base path — or pass --path <dir> for a one-off. byre won't guess where to create worktrees")
		}
		target = filepath.Join(parent, worktreeLeaf(paths.Canonical, name))
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
	if err := createWorktree(s.Err, top, name, target); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: created worktree at %s (branch %s); starting a session…\n", target, name)
	// Hand off to develop in the new worktree. If it fails, the worktree is still
	// valid — retry with `byre develop` there, or drop it with `git worktree
	// remove` — so we don't roll back a successful checkout on a develop error.
	return Develop(s, target, "", "", nil, selfEdit)
}

// worktreeLeaf is the single-directory name for a worktree: <repo>-<name>, with
// branch-name slashes flattened so it stays one dir under the base.
func worktreeLeaf(mainDir, name string) string {
	return filepath.Base(mainDir) + "-" + strings.ReplaceAll(name, "/", "-")
}

// worktreeSibling is the worktree_base sentinel meaning "beside the repo".
const worktreeSibling = "sibling"

// worktreeParent resolves the directory new worktrees are created under, from
// config: "" (unset -> caller refuses), the sibling of mainDir (the "sibling"
// sentinel), or a configured base path. A malformed config surfaces its error
// (not masked as "no location"); a set-but-invalid base path errors too, since
// the user asked for a specific place.
func worktreeParent(dir, mainDir string) (string, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return "", err
	}
	switch v := strings.TrimSpace(cfg.WorktreeBase); v {
	case "":
		return "", nil
	case worktreeSibling:
		return filepath.Dir(mainDir), nil
	default:
		return expandHostPath(v)
	}
}

// createWorktree runs `git worktree add`. If <name> already names a branch —
// local OR remote-tracking — git checks it out (DWIM-creating a local tracking
// branch for a remote-only one); otherwise a fresh branch is created with -b.
// Passing -b unconditionally would fork a divergent local branch off HEAD when a
// remote branch of that name exists, silently starting the agent on wrong code.
// git's progress goes to stderr so stdout stays clean.
func createWorktree(w io.Writer, dir, name, target string) error {
	args := []string{"-C", dir, "worktree", "add"}
	exists := branchExists(dir, name)
	if !exists {
		remote, err := remoteBranchExists(dir, name)
		if err != nil {
			// A probe refusal is NOT a negative answer: guessing "no" here
			// would -b a fresh branch from HEAD while origin/<name> exists —
			// a silently divergent checkout (codex, round 4).
			return fmt.Errorf("could not determine whether %s exists on a remote: %w", name, err)
		}
		exists = remote
	}
	if exists {
		args = append(args, target, name) // check out existing (local or remote) branch
	} else {
		args = append(args, "-b", name, target) // create a new branch
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree add failed: %w", err)
	}
	return nil
}

// gitToplevel returns the working tree's root dir for dir (its main or linked
// worktree root), and false if dir is not inside a git repository.
func gitToplevel(dir string) (string, bool) {
	out, err := gitProbe("-C", dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	return top, top != ""
}

// branchExists reports whether a local branch named name already exists.
func branchExists(dir, name string) bool {
	_, err := gitProbe("-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// remoteBranchExists reports whether a remote-tracking branch <remote>/<name>
// exists. The query targets the NAME (for-each-ref pattern) instead of
// listing every remote ref, so a legitimately huge ref set can never hit
// gitProbe's output cap; a probe refusal surfaces as the error it is.
// (Like the prior first-slash comparison, remotes with slashes in their own
// names are not matched — parity, not a regression.)
func remoteBranchExists(dir, name string) (bool, error) {
	out, err := gitProbe("-C", dir, "for-each-ref", "--count=1", "--format=%(refname)", "refs/remotes/*/"+name)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}
