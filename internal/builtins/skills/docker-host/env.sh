# docker-host launch env hook -- sets COMPOSE_PROJECT_NAME so every byre box
# on a shared host daemon gets a distinct compose project. Compose defaults to
# the cwd basename, and every box's cwd is /workspace, so without this two
# worktrees (or projects) collide on host containers/networks/volumes and one
# box's `compose down` tears down another's stack.
#
# Keyed on BYRE_WORKTREE (per-worktree id; equals BYRE_PROJECT for a plain
# project), NOT BYRE_PROJECT (shared across worktrees -- see ADR 0009) and NOT
# the container hostname (changes every rebuild, would orphan stacks). User
# override of COMPOSE_PROJECT_NAME is respected. Sourced code: no `exit`.
if [ -n "${BYRE_WORKTREE:-}" ]; then
  export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-byre-$BYRE_WORKTREE}"
fi
