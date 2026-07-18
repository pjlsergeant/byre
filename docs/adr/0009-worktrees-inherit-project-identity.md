# Worktrees inherit the project identity (no volume scope knob)

A linked git worktree resolves its byre identity from the **main
worktree's** canonical path: config, volumes, image, and the setup lock
come from the project; only the container (name + `byre.workdir` label)
and the `/workspace` mount stay per-worktree. So a worktree session
inherits the agent login and caches, and runs **concurrently** with the
main tree's session -- the headline goal.

Considered and rejected:

- **A volume *scope* tier** (per-repo or machine-wide `shared` volumes) --
  built, then removed. Sharing across unrelated projects has no natural
  boundary; the one real case (worktrees) is an *identity* question, not
  a volume-scope one.
- **A creds/history split** (share credentials, isolate history) --
  unnecessary: agents already handle concurrent access to one state dir
  (same as two CLI processes sharing `~/.claude` on a host). Sharing one
  volume is safe; *copying* credentials is what breaks (ADR 0007).
- **An inherit/standalone opt-out** -- YAGNI; `git clone` is the escape
  hatch (a clone is not a linked worktree).
- **Remounting the common git dir at a byre-chosen path** -- git worktree
  metadata holds absolute host paths in both directions; rewriting or
  dangling them lets in-box `git worktree repair`/`prune` corrupt *host*
  metadata. Decided: **same-path mounting** (main `.git` and the worktree
  bound at their host-absolute paths, rw) so every pointer resolves and
  nothing is rewritten. Same-path constrains the mount TARGET (the in-box
  path pointers resolve against); the common git dir's mount SOURCE is
  taken symlink-resolved, since its git-recorded path is derived from the
  agent-controlled `.git` pointer and a mutable symlink component would be
  a check-to-mount retarget race (worktree.go). Source and target differ
  only when the recorded path contains symlinks. Accepted residual
  (2026-07-17, shared by EVERY byre bind, WorkDir included): the resolved
  source is still a pathname, not an inode-pinned handle, so a concurrent
  rw session that can rename an ancestor of it during another launch's
  detect-to-mount window could redirect the bind. Not closable from byre:
  the docker/podman CLI-to-daemon contract is a pathname, resolved in the
  daemon's own namespace (a VM under Docker Desktop), so a host-side fd
  pin cannot cross it. Disclosed here so it isn't re-raised per-mount.

Consequences: detection parses `.git` pointer + `commondir` files
directly (no git binary dependency; submodules excluded, dangling
metadata is a hard error -- never a silent standalone fallback);
`develop`/`shell` filter sessions by the per-worktree label while
`reset`/`forget` sweep the project label (shared volumes = project-wide
blast radius, announced loudly); `rehome` refuses from a worktree and
points at the main tree.

## The checkout runs in the box, never on the host (2026-07-18)

A **containment** correctness issue: `byre worktree` ran `git worktree
add` on the **host**, and a git checkout runs the repository's own code --
the `post-checkout` hook, and `smudge`/`process` filters named by a
committed `.gitattributes` plus a repo-config `filter.<n>.smudge`. That is
exactly the code byre's model keeps inside the box (the project tree
defines it, and a worktree's common git dir is bound rw), so a host-side
checkout ran it in the wrong place -- on the host rather than in the box.
Not the retarget residual above; a plain misplacement of where the
checkout happens. Found 2026-07-18.

Decided: the host-side add runs **none of the repo's checkout code**, via
TWO flags that are **both load-bearing** (verified on git 2.39.5 --
dropping either puts some of it back on the host):

- `--no-checkout` skips the working-tree write, so the checkout-time code
  never runs: the `post-checkout` hook and `smudge`/`process` filters.
- `-c core.hooksPath=<empty>` is **not** reinforcement. `worktree add`
  performs ref updates that run the `reference-transaction` hook (and any
  other non-checkout hook) even under `--no-checkout`; emptying
  `hooksPath` is the only thing that keeps those in the box too. (A
  command-line `-c` also overrides a repo-config `core.hooksPath`.)

The working tree is materialized **inside the box** by the launcher, where
the repo's hooks and filters run contained like all its other code -- the
checkout is where it always should have been.

Mechanics:

- **A pending-checkout marker** (`byre-needs-checkout`) is dropped in the
  worktree's git admin dir, which is bound into the box at its host path.
  The launcher checks out and clears it **only on success**, so a develop
  that never started (build failure, daemon down mid-build) leaves a
  *resumable* pending worktree, not a dead empty one -- the next `byre
  develop` there finishes the job. The marker write is anchored on the
  **common git dir's validated, symlink-resolved host source** (the same
  `project.Resolve` value the worktree bind mount uses -- NOT a fresh
  `rev-parse` against the just-created worktree's mutable `.git` pointer,
  which is repo-writable and a repointed pointer would move the anchor).
  Only the admin subdir *name* comes from git (a single basename); the
  `worktrees/<name>/marker` path is resolved THROUGH an `os.Root` on that
  trusted source with `O_EXCL|O_NOFOLLOW`, so every repo-writable component
  below the common dir is contained and a pre-planted marker symlink is
  refused. The sole residual -- a swap of the common dir source itself --
  is *exactly* the disclosed same-path-mount residual, no wider
  (codex/grok review).
- **The marker is a hint, not a source of truth** (codex + grok review). A
  concurrent box sharing the common git dir can delete a sibling worktree's
  marker (→ that worktree launches unpopulated) or add one (→ a redundant,
  *contained* checkout). Both stay inside the box: the worst case is an
  empty or re-checked-out tree in a box the same agent already drives.
  The launcher warns when a linked worktree looks unpopulated, so a lost
  marker still surfaces rather than launching silently into emptiness.
- **Refuse-without-engine.** With no container engine there is no box to
  populate the checkout, so `byre worktree` refuses **before** creating
  anything (no empty worktree left behind) and names `git worktree add` as
  the escape hatch. Gated on the engine binary's absence only; a daemon
  that dies mid-build is absorbed by the resumable marker, not predicted
  (which would add a check-to-build race).
- **Populate failure is best-effort, not fail-closed** (both reviewers
  raised the fork; decided 2026-07-18): a failed or git-less populate
  warns loudly and still launches, rather than locking the user out of
  the box. The box is a repair environment (fix the cause, re-develop —
  the marker resumes); a `none` box with no git especially must not be
  unenterable. Containment is identical either way (the tree stays in the
  box); this trades strict state-integrity for not trapping the user,
  matching the launcher's never-block-a-launch culture.
- **A behavior change users may notice:** a freshly created worktree's
  files appear at first box start (one status line), not at `byre worktree`
  time; and a filter's tooling (e.g. git-lfs) must live in the **box**, not
  the host, since the checkout runs there now.

Not adopted: enumerating and disabling every exec-capable git config key
host-side (hooks, smudge, `core.fsmonitor`, …) -- a losing allowlist
against a moving target. Containing the checkout is the invariant.
