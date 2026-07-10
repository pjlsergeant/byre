#!/bin/sh
# devloop firstrun hook — runs as the dev user, each launch, before the agent
# starts. Ensures the project's .byre-devlog/ dir exists and is self-ignoring
# (see byre_devlog_dir in the shared lib — it also migrates a pre-rename
# .devloop/), so the agent diary and review log persist via the workspace mount
# but never land in git, with no per-project .gitignore entry. Idempotent;
# best-effort (must never block launch).
ws=/workspace
[ -d "$ws" ] || exit 0
. /usr/local/lib/byre-devlog-lib.sh 2>/dev/null || exit 0
byre_devlog_dir "$ws" 2>/dev/null || true
