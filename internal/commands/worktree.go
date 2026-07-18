package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
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
	// Refuse before creating anything if there's no engine to populate the
	// checkout: byre worktree materializes the working tree INSIDE the box
	// (never on the host — where the repo's hooks/filters run contained), so
	// without a box there is nothing to populate it. Checked here, pre-create,
	// so a no-engine machine is never left with an empty worktree; the manual
	// route is plain `git worktree add`. (Engine binary absence only — a daemon that
	// is down mid-build is absorbed by the resumable marker, not predicted here,
	// which avoids a check-to-build race.)
	cfg, cerr := config.Load(top)
	if cerr != nil {
		return cerr
	}
	if _, derr := runner.Detect(cfg.Engine, nil); derr != nil {
		return fmt.Errorf("byre worktree needs a container engine — it checks out the worktree's files inside the box (where the repo's git hooks and filters run contained, not on the host): %w.\n"+
			"Start Docker or Podman, or run `git worktree add %s %s` yourself (that checks out on the host, running the repo's own git hooks and filters there)", derr, target, name)
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

// needsCheckoutMarker is the per-worktree file byre drops in the worktree's
// git admin dir to signal "created --no-checkout; populate me in the box". The
// launcher consumes it (git checkout in the container), clearing it only on
// success — so a develop that never started leaves a resumable pending
// worktree, not a dead empty one. Kept in the admin dir, which is bind-mounted
// into the box at its host path, so the launcher sees it without extra wiring.
const needsCheckoutMarker = "byre-needs-checkout"

// createWorktree runs `git worktree add`. If <name> already names a branch —
// local OR remote-tracking — git checks it out (DWIM-creating a local tracking
// branch for a remote-only one); otherwise a fresh branch is created with -b.
// Passing -b unconditionally would fork a divergent local branch off HEAD when a
// remote branch of that name exists, silently starting the agent on wrong code.
// git's progress goes to stderr so stdout stays clean.
//
// CONTAINMENT: a git checkout runs the repository's own code — the
// post-checkout hook and smudge/process filters a committed .gitattributes
// selects — and byre's model keeps a repo's code inside the box. A host-side
// `worktree add` checkout would run it on the host instead, the wrong place.
// Two flags keep it off the host, and BOTH are load-bearing (verified on git
// 2.39.5):
//   - --no-checkout skips the working-tree write, so the checkout-time code
//     never runs on the host: the post-checkout hook and smudge/process filters.
//   - -c core.hooksPath=<empty> is NOT reinforcement: `worktree add` performs
//     ref updates that run the reference-transaction hook (and any other
//     non-checkout hook) even under --no-checkout. Emptying hooksPath is the
//     only thing that keeps those in the box too. A command-line -c also beats
//     a repo-config core.hooksPath. Dropping EITHER flag puts some back on the host.
//
// The actual checkout happens later, inside the box, where the repo's code
// runs contained by design (see the launcher's populate step; ADR 0009).
func createWorktree(w io.Writer, dir, name, target string) error {
	emptyHooks, err := os.MkdirTemp("", "byre-nohooks-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(emptyHooks)

	args := []string{"-C", dir, "-c", "core.hooksPath=" + emptyHooks, "worktree", "add", "--no-checkout"}
	exists, err := branchExists(dir, name)
	if err != nil {
		// A probe refusal is NOT a negative answer — never mutate after an
		// indeterminate probe (codex rounds 4+5; guessing "no" would -b a
		// fresh branch where one already exists, or diverge from a remote).
		return fmt.Errorf("could not determine whether branch %s exists: %w", name, err)
	}
	if !exists {
		remote, err := remoteBranchExists(dir, name)
		if err != nil {
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
	// The worktree exists now but is unpopulated; the marker is what makes the
	// in-box populate happen. The write is anchored on the TRUSTED common git
	// dir — the symlink-resolved value byre's OWN validated resolver
	// (project.Resolve) computes for the bind mount, NOT a fresh rev-parse
	// against the just-created worktree's mutable .git pointer, which a
	// concurrent agent could rewrite to point the anchor at a dir it controls
	// (codex review). If the mark can't be written, roll the worktree back so
	// no dangling empty one is left (grok review; never-half-create).
	// Any failure from here on rolls the worktree back and reports uniformly:
	// a rollback that ALSO fails must never be swallowed (else an empty
	// registered worktree lingers against never-half-create — codex review),
	// so it rides the error with manual-cleanup guidance.
	rollbackOnErr := func(cause error) error {
		if rberr := rollbackWorktree(dir, emptyHooks, target); rberr != nil {
			return fmt.Errorf("%w (and rolling it back failed: %v — remove %s by hand)", cause, rberr, target)
		}
		return cause
	}
	wt, rerr := project.Resolve(target)
	if rerr != nil {
		return rollbackOnErr(fmt.Errorf("resolving the new worktree: %w", rerr))
	}
	if !wt.IsWorktree || wt.CommonGitDirHost == "" {
		return rollbackOnErr(fmt.Errorf("the new worktree %s has no resolvable common git dir", target))
	}
	if err := markNeedsCheckout(wt.CommonGitDirHost, target); err != nil {
		return rollbackOnErr(fmt.Errorf("preparing the worktree checkout: %w", err))
	}
	return nil
}

// rollbackWorktree removes a just-created worktree, with the SAME empty
// core.hooksPath the add used — the rollback is another host-side mutating git
// command, and the invariant "no host git in this flow runs agent-config hooks"
// stays uniform rather than resting on `worktree remove` happening not to fire
// one on today's git (codex review).
func rollbackWorktree(dir, emptyHooks, target string) error {
	return exec.Command("git", "-C", dir, "-c", "core.hooksPath="+emptyHooks, "worktree", "remove", "--force", target).Run()
}

// markNeedsCheckout drops the pending-checkout marker in the worktree's admin
// dir, anchored on the TRUSTED commonHost (project.Resolve's symlink-resolved
// common git dir). Only the admin subdir NAME is taken from git — a single path
// component, contained by the os.Root below whatever it is.
func markNeedsCheckout(commonHost, target string) error {
	adminOut, err := gitProbe("-C", target, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("locating the worktree git dir: %w", err)
	}
	adminName := filepath.Base(strings.TrimSpace(string(adminOut)))
	// "" / "." / ".." / separator are not real subdir names: "." and ".."
	// stay inside the os.Root (no escape) but would put the marker where the
	// launcher won't look (a silent-ineffective mark) — reject them (grok
	// review; git is not expected to emit them, belt-and-suspenders).
	if adminName == "" || adminName == "." || adminName == ".." || adminName == string(os.PathSeparator) {
		return fmt.Errorf("could not determine the worktree admin dir for %s", target)
	}
	return writeCheckoutMarker(commonHost, adminName)
}

// writeCheckoutMarker creates the marker at worktrees/<adminName>/ under the
// TRUSTED commonHost, with no followed symlink anywhere below it. Everything
// under the common git dir is agent-writable (bound rw for a worktree), so the
// path is resolved THROUGH an os.Root anchored on commonHost: a swapped
// `worktrees` or `<adminName>` component that escapes the root is refused, and
// O_EXCL|O_NOFOLLOW refuses a pre-planted marker entry. commonHost is byre's
// existing trust boundary — the same symlink-resolved path the worktree bind
// mount is taken from (ADR 0009) — so the ONE residual (a swap of commonHost
// itself) is exactly the disclosed same-path-mount residual, identical to the
// mount's, and unclosable host-side. adminName is a basename, no separators.
func writeCheckoutMarker(commonHost, adminName string) error {
	root, err := os.OpenRoot(commonHost)
	if err != nil {
		return err
	}
	defer root.Close()
	rel := filepath.Join("worktrees", adminName, needsCheckoutMarker)
	f, err := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return fmt.Errorf("writing the worktree checkout marker: %w", err)
	}
	return f.Close()
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

// branchExists reports whether a local branch named name already exists. A
// clean negative is EXACTLY exit 1 from `rev-parse --verify --quiet` (the
// documented missing-ref code); any other refusal — timeout kill, output
// cap, exit 128 repo errors — is an error, never a "no".
func branchExists(dir, name string) (bool, error) {
	_, err := gitProbe("-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
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
