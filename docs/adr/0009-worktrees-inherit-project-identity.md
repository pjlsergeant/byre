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
  nothing is rewritten.

Consequences: detection parses `.git` pointer + `commondir` files
directly (no git binary dependency; submodules excluded, dangling
metadata is a hard error -- never a silent standalone fallback);
`develop`/`shell` filter sessions by the per-worktree label while
`reset`/`forget` sweep the project label (shared volumes = project-wide
blast radius, announced loudly); `rehome` refuses from a worktree and
points at the main tree. Background mechanics: `docs/agent-volume-sharing.md`
(historical).
