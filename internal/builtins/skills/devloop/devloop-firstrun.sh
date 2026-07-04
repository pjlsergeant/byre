#!/bin/sh
# devloop firstrun hook — runs as the dev user, each launch, before the agent
# starts. Ensures the project's .devloop/ scratch dir exists and is self-ignoring
# (see byre_devloop_dir in the shared lib), so the agent diary and review log
# persist via the workspace mount but never land in git, with no per-project
# .gitignore entry. Idempotent; best-effort (must never block launch).
ws=/workspace
[ -d "$ws" ] || exit 0
. /usr/local/lib/byre-devloop-lib.sh 2>/dev/null || exit 0
byre_devloop_dir "$ws" 2>/dev/null || true
