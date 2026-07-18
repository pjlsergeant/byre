package runner

import "fmt"

// worktreeAddScript registers a linked git worktree from INSIDE the box — the
// creation half of the staged worktree flow (ADR 0009: register here, populate
// at first session). It runs in a short-lived container over exactly the repo
// mounts (see worktreeAddArgs), so every git side effect — ref updates, the
// hooks and config-selected code a `worktree add` runs, the metadata writes —
// happens contained, never on the host.
//
// Inputs ride env vars (BYRE_WT_MAIN / BYRE_WT_TARGET / BYRE_WT_BRANCH), never
// interpolation into the script, so a hostile branch name or path can't inject
// shell.
//
// The branch DWIM matches the old host-side behavior: an existing local branch
// is checked out, else an existing remote-tracking branch (git DWIM-creates the
// local tracking branch), else -b creates a fresh one. An indeterminate probe
// is never a "no" — rev-parse --verify --quiet's documented missing-ref code is
// exactly 1, so anything else aborts rather than guess (guessing would -b a
// divergent branch over an existing one).
//
// The add itself stays --no-checkout: population is the first session's job
// (the launcher's populate step), keyed on the byre-needs-checkout marker this
// script drops in the worktree's admin dir — the same literal
// internal/gen/launcher.sh consumes. All paths are same-path mounts, so the
// marker and metadata this container writes are exactly where the host and the
// next session expect them.
//
// Cleanup removes ONLY state this script created (codex review). A failed
// `worktree add` needs no cleanup — git rolls its own partial state back — and
// running `worktree remove --force` there would instead destroy whatever
// ALREADY holds the target path (a concurrent invocation's registration, or a
// pre-existing worktree the add collided with). The one remove kept is the
// marker-write failure path, where OUR add just succeeded — so the
// registration being removed is provably this script's own; a remove that
// itself fails is surfaced with manual guidance rather than swallowed
// (never-half-create).
const worktreeAddScript = `set -u
command -v git >/dev/null 2>&1 || {
  echo "byre: this project's box image has no git, so byre cannot register the worktree in it." >&2
  echo "byre: add the git package to the box (byre config), then re-run byre worktree." >&2
  exit 1
}
main="$BYRE_WT_MAIN"
target="$BYRE_WT_TARGET"
branch="$BYRE_WT_BRANCH"
git -C "$main" rev-parse --verify --quiet "refs/heads/$branch" >/dev/null
rc=$?
if [ "$rc" -gt 1 ]; then
  echo "byre: could not determine whether branch $branch exists (git exited $rc)" >&2
  exit 1
fi
exists=$rc
if [ "$exists" -eq 1 ]; then
  if remote=$(git -C "$main" for-each-ref --count=1 --format='%(refname)' "refs/remotes/*/$branch"); then
    [ -n "$remote" ] && exists=0
  else
    echo "byre: could not determine whether branch $branch exists on a remote" >&2
    exit 1
  fi
fi
if [ "$exists" -eq 0 ]; then
  set -- "$target" "$branch"
else
  set -- -b "$branch" "$target"
fi
if ! git -C "$main" worktree add --no-checkout "$@"; then
  exit 1
fi
if gitdir=$(git -C "$target" rev-parse --absolute-git-dir) && [ -n "$gitdir" ] && touch "$gitdir/byre-needs-checkout"; then
  exit 0
fi
echo "byre: could not mark the new worktree for its first-session checkout — removing it again" >&2
if ! git -C "$main" worktree remove --force "$target"; then
  echo "byre: removing it failed too — clear it with: git -C $main worktree remove --force $target" >&2
fi
exit 1`

// WorktreeAdd registers a linked git worktree by running worktreeAddScript in
// a one-shot container from the project's own image — the same git and filter
// tooling the first-session checkout will use. name is the container name
// (distinct from any session's, so lifecycle queries — which go by session
// labels this container deliberately does not carry — never mistake a create
// step for a live box, and two creates of one target collide loudly).
//
// The mount set is minimal and hermetic — what `git worktree add` needs and
// nothing else: the main working tree, the common git dir, and the (empty,
// host-created) target, each bound rw at its own host path so the metadata
// git writes records paths that are valid on the host and in every later
// session (same-path mounting, ADR 0009). commonHost/commonTarget differ only
// on the linked-worktree path, when the git-recorded common-dir spelling
// contains symlinks (there the caller's source is resolved against a
// structurally validated inode; from a main tree the two are identical). No
// user mounts, volumes, ports, env passthrough, network, caps, or skill
// run_args ride along.
//
// It runs as the box identity (same uid/gid and userns mode the image was
// built for), so everything it writes lands owned by the dev user. Output is
// streamed: git's progress and errors belong to the invoking user.
func (r *Runner) WorktreeAdd(image, name string, id Identity, commonHost, commonTarget, mainDir, target, branch string) error {
	return r.stream(string(r.engine), worktreeAddArgs(image, name, id, commonHost, commonTarget, mainDir, target, branch)...)
}

// worktreeAddArgs builds the create-container argv (pure, for testing).
func worktreeAddArgs(image, name string, id Identity, commonHost, commonTarget, mainDir, target, branch string) []string {
	args := []string{"run", "--rm",
		"--name", name,
		"--entrypoint", "sh",
		// No network: registration is a purely local git operation (the DWIM
		// reads already-fetched remote-tracking refs), and this container runs
		// repo-authored code (hooks fire on `worktree add`'s ref updates) —
		// deny-by-default means a step that needs no egress gets none,
		// whatever the project's session posture is (grok review).
		"--network", "none",
		"-u", fmt.Sprintf("%d:%d", id.UID, id.GID),
	}
	args = appendUserns(args, id.Userns())
	return append(args,
		"-e", "BYRE_WT_MAIN="+mainDir,
		"-e", "BYRE_WT_TARGET="+target,
		"-e", "BYRE_WT_BRANCH="+branch,
		// git refuses repos whose stat uid doesn't match the euid ("dubious
		// ownership"), and bind mounts can misalign the two (Docker Desktop's
		// VM). This is byre's own one-shot invocation over byre-chosen mounts,
		// so trust them wholesale — inside this container only (env-scoped,
		// nothing written to any config file). Same stance as the launcher's
		// safe.directory line for /workspace.
		"-e", "GIT_CONFIG_COUNT=1",
		"-e", "GIT_CONFIG_KEY_0=safe.directory",
		"-e", "GIT_CONFIG_VALUE_0=*",
		// Main tree before the common dir: when the common dir nests inside the
		// main tree (the normal <main>/.git), the deeper target must layer over
		// the shallower one on engines that process mounts in argv order.
		"--mount", "type=bind,source="+mainDir+",target="+mainDir,
		"--mount", "type=bind,source="+commonHost+",target="+commonTarget,
		"--mount", "type=bind,source="+target+",target="+target,
		image, "-c", worktreeAddScript)
}
