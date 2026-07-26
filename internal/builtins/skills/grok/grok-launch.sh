#!/bin/bash
# byre's grok launch adapter (ADR 0046): inject the baked agent context plus
# the launcher's per-session additions via --append-system-prompt (alias of
# --rules — appended after grok's default prompt in a <human_rules> block;
# source-verified). byre writes NOTHING into $GROK_HOME: its AGENTS.md is the
# user's file.
#
# The value rides ONE argv string, and Linux caps a single exec argument at
# MAX_ARG_STRLEN (~128 KiB) — well under byre's 1 MiB context budget. A
# legal-but-large context must DEGRADE loudly, not kill the exec (grok
# review find, probed 2026-07-26): cap with a disclosure pointing at the
# baked file, which the agent can read in-box.
set -eu

CTX=${BYRE_AGENT_CONTEXT:-/etc/byre/agent-context.md}
ctx_text=""
[ -r "$CTX" ] && ctx_text="$(cat "$CTX")"

# Whole-argument byte budget: 100000 (headroom under the ~131072 per-string
# exec limit). The SESSION additions have priority (operational: the
# enforced allowlist, the self-edit note) and in the absurd corner where
# they alone bust the budget they truncate too — never a dead exec; the
# baked text gets the remaining budget, truncated to a VALID UTF-8 prefix
# (a raw head -c can split a codepoint; iconv -c drops the orphan bytes)
# with a disclosure pointing at the baked file the agent can read in-box.
utf8_prefix() { iconv -f UTF-8 -t UTF-8 -c 2>/dev/null || cat; }
budget=100000
session="${BYRE_SESSION_CONTEXT:-}"
sbytes=$(printf '%s' "$session" | wc -c)
if [ "$sbytes" -gt "$budget" ]; then
  session="$(printf '%s' "$session" | head -c "$budget" | utf8_prefix)

[byre: session context truncated at this agent's argv limit]"
  sbytes=$(printf '%s' "$session" | wc -c)
fi
bbudget=$((budget - sbytes))
[ "$bbudget" -lt 0 ] && bbudget=0
if [ "$(printf '%s' "$ctx_text" | wc -c)" -gt "$bbudget" ]; then
  ctx_text="$(printf '%s' "$ctx_text" | head -c "$bbudget" | utf8_prefix)

[byre: instructions truncated at this agent's argv limit — full text: $CTX]"
fi
ctx_text="${ctx_text}${session}"

if [ -n "$ctx_text" ]; then
  exec grok --append-system-prompt "$ctx_text" "$@"
fi
exec grok "$@"
