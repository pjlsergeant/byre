package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// Worktree implements `byre worktree <name>`: create a linked git worktree for
// branch <name> and `byre develop` in it — a parallel agent session that
// inherits the repo's config, volumes, and image, in one step.
//
// Every git operation on the repo runs INSIDE the box — one rule, no
// exceptions (ADR 0009). The registration (`git worktree add`) runs in a
// short-lived creation container from the project image (runner.WorktreeAdd),
// and the checkout happens at the first session's start (the launcher's
// populate step). The host side is reduced to: resolve a location, ensure the
// image, make the mount-point directory, run the create container, hand off
// to develop. Host-side git is limited to bounded read-only probes (gitProbe).
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
	// Comma in the path would corrupt the create container's and develop's
	// docker --mount values; fail before creating anything.
	if strings.Contains(target, ",") {
		return fmt.Errorf("target path contains a comma, which docker --mount cannot express: %q", target)
	}
	// Recognize the leftovers of an interrupted create for this exact target
	// (registration is a git-side fact, the directory a filesystem one; a kill
	// mid-create can leave either without the other). Both probes DEGRADE on
	// refusal — an unanswerable probe falls back to the plain behavior, and the
	// in-box add fails loudly on a stale registration anyway.
	if _, lerr := os.Lstat(target); lerr == nil {
		if reg, perr := worktreeRegistered(paths.Canonical, target); perr == nil && reg {
			return fmt.Errorf("%s is already a registered worktree of this repo — continue with `byre develop` there, "+
				"or remove it first: git -C %s worktree remove --force %s", target, paths.Canonical, target)
		}
		return fmt.Errorf("target path already exists: %s (pass --path to choose another location)", target)
	} else if reg, perr := worktreeRegistered(paths.Canonical, target); perr == nil && reg {
		return fmt.Errorf("%s is registered as a worktree but missing on disk — a previous create was likely interrupted. "+
			"Clear stale registrations with `git -C %s worktree prune` (it drops only entries whose directory is gone), then retry", target, paths.Canonical)
	}
	// Refuse before creating anything if there's no engine: creation itself now
	// runs in the box (the registration container), and so does the checkout
	// that populates it — without an engine there is nothing to run either in.
	// The manual route is plain `git worktree add`. (Engine binary absence only —
	// a daemon that is down mid-build fails the build loudly instead, which
	// avoids a check-to-build race.)
	cfg, cerr := config.Load(top)
	if cerr != nil {
		return cerr
	}
	eng, derr := runner.Detect(cfg.Engine, nil)
	if derr != nil {
		return fmt.Errorf("byre worktree needs a container engine — it creates and checks out the worktree inside the box (where the repo's git hooks and filters run contained, not on the host): %w.\n"+
			"Start Docker or Podman, or run `git worktree add %s %s` yourself (that runs on the host, running the repo's own git hooks and filters there)", derr, target, name)
	}
	if err := worktreeCreate(runner.New(eng), s, paths, top, name, target); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: created worktree at %s (branch %s); starting a session…\n", target, name)
	// Hand off to develop in the new worktree. If it fails, the worktree is still
	// valid — retry with `byre develop` there, or drop it with `git worktree
	// remove` — so we don't roll back a successful creation on a develop error.
	return Develop(s, target, "", "", nil, selfEdit)
}

// worktreeCreate is the engine-facing half of Worktree: ensure the project
// image, make the (empty) target directory as the bind-mount point, and run
// the one-shot creation container that registers the worktree and drops the
// pending-checkout marker — all git mutation in the box. Split from Worktree
// so it runs end-to-end against a fake engine.
func worktreeCreate(r engineRunner, s Streams, paths project.Paths, projectDir, name, target string) error {
	commonTarget, commonHost, err := worktreeCommonGitDir(paths)
	if err != nil {
		return err
	}
	// The create container's binds are byre-assembled --mount values; a comma
	// anywhere in them cannot be expressed (same check develop applies).
	for _, p := range []string{paths.Canonical, commonTarget, commonHost} {
		if strings.Contains(p, ",") {
			return fmt.Errorf("path contains a comma, which docker --mount cannot express: %q", p)
		}
	}
	image, ident, err := ensureProjectImage(r, s, paths, projectDir)
	if err != nil {
		return err
	}
	// The target is made host-side only as the bind-mount point (the engine
	// needs a source to bind); the box does all the git. Modern git accepts a
	// `worktree add` into an existing empty directory. The LEAF is made with
	// os.Mkdir, not MkdirAll: exactly one invocation can create it, so it is
	// this create's OWNERSHIP TOKEN — a concurrent create of the same target
	// is refused here, before any container runs, and the failure path below
	// may remove the dir knowing it is this invocation's own (codex review: a
	// second create's cleanup must never unlink the first's live mount source,
	// and its container must never race the first's registration).
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("target path %s appeared while preparing the worktree — another `byre worktree` for it may be in flight; wait for it (or clear the path) and retry", target)
		}
		return err
	}
	if err := r.WorktreeAdd(image, worktreeCreateName(target), ident, commonHost, commonTarget, paths.Canonical, target, name); err != nil {
		// Establish what remains rather than blindly deleting: a failed add
		// leaves no registration (git rolls its own partial state back, and the
		// script removes only a worktree it successfully added), so normally
		// only this invocation's empty mount-point dir is left — remove exactly
		// that (ours by the Mkdir token above; os.Remove refuses a non-empty
		// dir; never RemoveAll — partial state is diagnostically useful) so a
		// retry isn't refused by the exists check. If a registration survived
		// (create killed mid-remove), say so with the targeted remedy.
		_ = os.Remove(target)
		if reg, perr := worktreeRegistered(paths.Canonical, target); perr == nil && reg {
			return fmt.Errorf("creating the worktree in the box failed, and it is still registered: %w\n"+
				"remove it with `git -C %s worktree remove --force %s`, then retry", err, paths.Canonical, target)
		}
		return fmt.Errorf("creating the worktree in the box failed: %w", err)
	}
	return nil
}

// ensureProjectImage resolves the project's config and builds its image under
// the setup lock — the image preparation a create step needs, without
// develop's session concerns (no onboarding prompts, no volume seeding, no
// live-session check). It composes the same primitives develop's own flow
// uses (resolve, resolveIdentity, buildImage); develop keeps its build inline
// because its build+seed+create must share ONE lock hold (the reset/forget
// race documented there), which a self-locking helper cannot join.
//
// It deliberately does NOT run onboarding. On the first-ever `byre worktree`
// in a never-developed repo that means the create builds from the
// resolved-default config, and the hand-off to Develop then onboards and may
// rebuild — a cheap cache-warm second build against an already-registered
// worktree. Accepted (2026-07-19): pulling onboarding (an interactive session
// concern) into a plumbing create step is exactly the split this change set
// out to remove, and the redundant build is one-time and nearly free. The
// common path — a worktree of a repo you've already developed — builds once.
func ensureProjectImage(r engineRunner, s Streams, paths project.Paths, projectDir string) (string, runner.Identity, error) {
	if err := paths.Bootstrap(); err != nil {
		return "", runner.Identity{}, err
	}
	rv, err := resolve(paths, projectDir, s.Err)
	if err != nil {
		return "", runner.Identity{}, err
	}
	warnNonDebianBase(s.Err, rv.cfg.Base)
	ident, err := resolveIdentity(s.Err, r)
	if err != nil {
		return "", runner.Identity{}, err
	}
	image := imageTag(paths.ID, ident.UID, ident.GID)
	if err := withSetupLock(s.Err, paths.LockFile, func() error {
		return buildImage(r, paths, rv.cfg, rv.skills, image, false, ident)
	}); err != nil {
		return "", runner.Identity{}, err
	}
	return image, ident, nil
}

// worktreeCommonGitDir picks the common-git-dir bind for the create container:
// target = the git-recorded path (what in-box pointers must resolve against),
// host = the mount source.
//
// For a linked worktree both come from project.Resolve, whose values are
// structurally validated (SameFile inode checks) and whose source is
// symlink-resolved against that validated inode. When `byre worktree` runs
// from the MAIN tree the common dir is derived here as <canonical>/.git — and
// its leaf is gated with Lstat, NEVER resolved: `.git` sits in the
// agent-writable tree, so following a symlink there would let a planted
// `.git -> /victim/dir` become an arbitrary rw host mount into a container
// that runs repo hooks (grok review, 2026-07-19 — the same attacker-shaped-
// path class detectWorktree refuses by inode validation). Anything but a
// plain directory — a symlink, a gitfile (git init --separate-git-dir), any
// special file — is refused with the manual route; a clear refusal beats
// chasing repo-authored pointers. Source == target: canonical is already
// symlink-resolved, so what remains after the Lstat gate is only the
// byre-wide check-to-mount pathname residual every bind shares (ADR 0009).
func worktreeCommonGitDir(paths project.Paths) (target, host string, err error) {
	if paths.IsWorktree {
		return paths.CommonGitDir, paths.CommonGitDirHost, nil
	}
	gd := filepath.Join(paths.Canonical, ".git")
	info, lerr := os.Lstat(gd)
	if lerr != nil {
		return "", "", fmt.Errorf("cannot read the repo git dir %q for mounting: %w", gd, lerr)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%s is not a plain directory — byre worktree supports repos whose .git is a directory "+
			"(for a symlinked or separate git dir, run `git worktree add` yourself and then `byre develop` in the worktree)", gd)
	}
	return gd, gd, nil
}

// worktreeRegistered reports whether target is a registered worktree of the
// repo at mainDir, via a bounded read-only probe (`worktree list --porcelain`
// prints one `worktree <path>` line per registration). Paths are compared
// canonicalized, since git records its own absolute spelling.
func worktreeRegistered(mainDir, target string) (bool, error) {
	out, err := gitProbe("-C", mainDir, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	want, err := project.Canonicalize(target)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		p, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		if got, cerr := project.Canonicalize(p); cerr == nil && got == want {
			return true, nil
		}
	}
	return false, nil
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
