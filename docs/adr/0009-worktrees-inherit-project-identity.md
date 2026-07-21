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

## Worktree creation runs in the box, never on the host (2026-07-18/19)

A **containment** correctness issue: `byre worktree` ran `git worktree
add` on the **host**, and a git checkout runs the repository's own code --
the `post-checkout` hook, and `smudge`/`process` filters named by a
committed `.gitattributes` plus a repo-config `filter.<n>.smudge`. That is
exactly the code byre's model keeps inside the box (the project tree
defines it, and a worktree's common git dir is bound rw), so a host-side
checkout ran it in the wrong place -- on the host rather than in the box.
Not the retarget residual above; a plain misplacement of where the
checkout happens.

Decided: `byre worktree` runs **no mutating git against the repo on the
host at all** -- host-side git is reduced to bounded read-only probes
(toplevel, is-this-registered). Creation is staged, both halves in the
box:

- **Registration** (the branch DWIM and `git worktree add --no-checkout`)
  runs in a short-lived **creation container** from the project image:
  its own entrypoint (never the launcher; no session gate, hooks, or
  agent), no session labels, the box identity/userns, and a minimal
  hermetic mount set -- the main working tree, the common git dir, and
  the (host-created, empty) target, each rw at its host path, and
  **nothing else**: no user mounts, volumes, ports, env passthrough,
  caps, skill `run_args` -- and **no network** (`--network none`):
  registration is purely local git, and the hooks a `worktree add` fires
  are repo-authored code. From a main tree the common-dir mount source is
  `<canonical>/.git` gated by `Lstat` to a **plain directory** -- a
  symlinked or gitfile `.git` is refused, never followed: `.git` is
  agent-writable, and resolving a planted symlink there would hand the
  hook-running container an arbitrary rw host mount. Cleanup removes only
  state the invocation itself created (a failed add needs none -- git
  rolls its own partial state back). Host-side, the `mkdir` of the target
  leaf is the create's **ownership token** -- exactly one invocation can
  create it -- and on failure the host removes only that empty
  mount-point directory, never recursively. The host's role is reduced
  to: resolve a location, ensure the image, `mkdir` the mount point, run
  the container, hand off to develop.
- **Population** (the actual checkout) happens at the first session's
  start via the launcher, keyed on the pending-checkout marker
  (`byre-needs-checkout`) the creation container drops in the worktree's
  git admin dir. The launcher checks out and clears it **only on
  success**, so a develop that never started leaves a *resumable*
  pending worktree, not a dead empty one. The marker is a hint, not a
  source of truth: a concurrent box sharing the common git dir can
  delete or add a sibling's marker, but both outcomes stay inside the
  box, and the launcher warns when a linked worktree looks unpopulated.

Consequences and rulings:

- **Refuse-without-engine.** With no container engine there is nothing to
  create the worktree in, so `byre worktree` refuses **before** creating
  anything and names `git worktree add` as the escape hatch. Gated on the
  engine binary's absence only; a daemon that dies later fails loudly
  instead (predicting it would add a check-to-build race).
- **Populate failure is best-effort, not fail-closed**: a failed or
  git-less populate warns loudly and still launches, rather than locking
  the user out of the box -- the box is the repair environment (fix the
  cause, re-develop; the marker resumes), matching the launcher's
  never-block-a-launch culture. Containment is identical either way.
- **Behavior users may notice:** a freshly created worktree's files
  appear at first box start, not at `byre worktree` time; **git itself
  must be in the box image** (a git-less image gets a loud error, never a
  silently missing worktree); and a filter's tooling (e.g. git-lfs) must
  live in the **box**, since the checkout runs there now.

Not adopted: enumerating and disabling every exec-capable git config key
host-side (hooks, smudge, `core.fsmonitor`, …) -- a losing allowlist
against a moving target. Containing the checkout is the invariant. An
interim host-side hardening of the add (emptied `core.hooksPath` under
`--no-checkout`) was built and superseded the next day by the in-box
design; git history keeps it.

## Toplevel is derived structurally, not via `git rev-parse` (2026-07-22)

The one remaining host-side git call for the toplevel probe
(`git -C <dir> rev-parse --show-toplevel`) honored a `.git/config`
**core.worktree**, which the box's agent can write (the repo's `.git` is
rw in the box). Set to an absolute or relative path, it made
`--show-toplevel` report an **unrelated** working tree, so `byre worktree`
would `project.Resolve`/`config.Load`/mount-and-mutate *that* host repo --
a stable cross-session retarget, not the concurrent-rename residual above.
The forged-`.git` inode checks never caught it: they validate whatever
`top` was already chosen, and here `top` itself is the poison, naming a
genuine but wrong repo.

Decided: `gitToplevel` walks ancestors of the invocation dir for the
nearest `.git` **entry** -- a directory (main worktree) or a regular file
(a linked worktree's `gitdir:` pointer), each rooting the tree at that
ancestor; a symlinked `.git` is refused. No git binary, no `--show-toplevel`
trust. This preserves linked worktrees (their `.git` *file* is their legit
root; `detectWorktree` still maps identity to the main tree) and cannot
select a root outside the invocation dir's ancestors, where the agent
cannot plant a `.git` anyway. Residual: a repository whose only working
tree is defined purely via `core.worktree` with no `.git` in any ancestor
of the cwd is unsupported by `byre worktree` -- not a shape `git init` or
linked worktrees produce.
