#!/bin/bash
# byre's claude launch adapter (ADR 0046): inject the baked agent context plus
# the launcher's per-session additions ($BYRE_SESSION_CONTEXT) into the system
# prompt as ONE --append-system-prompt-file. One flag, not two: the Claude CLI
# rejects --append-system-prompt alongside --append-system-prompt-file
# ("Cannot use both ... Please use only one"), and an EMPTY second flag slips
# that check (falsy), so a two-flag command only dies on boxes whose session
# var is non-empty (--self-edit, a firewall) — the merge here removes the pair
# entirely. The file route rather than grok's one-argv-string route: a file
# value never meets Linux's ~128 KiB per-argument exec limit, so no
# truncation tier is needed. byre writes NOTHING into ~/.claude: the merged
# file is a launch-owned temp file, never the user's memory (ADR 0046).
set -eu

CTX=${BYRE_AGENT_CONTEXT:-/etc/byre/agent-context.md}

# ONE launch-owned path, replaced on every start: exec replaces this shell,
# so nothing can clean up after claude exits — a fresh mktemp name per
# launch would accumulate context copies across container restarts. One
# launcher runs per container, so the fixed name needs no uniqueness, and
# the stable path stays readable in-box. The compose still goes through a
# mktemp file and an atomic rename onto the fixed name: a plain `>` to a
# predictable path would FOLLOW anything planted there (a symlink at the
# fixed name would route byre's write into the link's target; rename
# replaces the plant instead). A planted DIRECTORY would swallow the mv —
# POSIX mv moves the file INTO it — so probe with -d first (BSD mv has no
# -T to refuse it, and the wrapper test runs on macOS hosts too; -d
# follows symlinks, covering the symlink-to-directory arm). The probe→mv
# window is not raceless like -T was, but losing it only lands the merge
# file inside an agent-made TMPDIR directory: nothing is overwritten and
# the content is the context the agent already reads.
# Best-effort throughout: context is informational, so a failure composing
# it must never block the launch — degrade to the baked file alone, then to
# no injection at all, dropping the partial write rather than injecting it.
# NOT `if ! { ...; } > file`: bash treats a failure of the redirect itself
# as success there (verified on 5.2.15), which would hand claude a dead
# path instead of degrading — the || form takes the failure branch.
merged="${TMPDIR:-/tmp}/byre-agent-context.md"
tmp=""
if tmp=$(mktemp "$merged.XXXXXXXX" 2>/dev/null); then
  { [ -r "$CTX" ] && cat "$CTX"; printf '%s' "${BYRE_SESSION_CONTEXT:-}"; } > "$tmp" 2>/dev/null &&
    [ ! -d "$merged" ] &&
    mv -f "$tmp" "$merged" 2>/dev/null ||
    { rm -f "$tmp" 2>/dev/null || true; merged=""; }
else
  merged=""
fi

if [ -n "$merged" ]; then
  exec claude --append-system-prompt-file "$merged" "$@"
fi
if [ -r "$CTX" ]; then
  exec claude --append-system-prompt-file "$CTX" "$@"
fi
exec claude "$@"
