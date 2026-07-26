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

# 100000 BYTES (wc -c, not ${#} — that counts characters, and multi-byte
# prose under 100k chars can still blow the ~131072-byte per-string limit;
# grok review round 2). Only the BAKED part is capped; the per-session
# additions (small: egress list, self-edit note) always append after the
# disclosure, so they are never silently dropped.
if [ "$(printf '%s' "$ctx_text" | wc -c)" -gt 100000 ]; then
  ctx_text="$(printf '%s' "$ctx_text" | head -c 100000)

[byre: instructions truncated at this agent's argv limit — full text: $CTX]"
fi
ctx_text="${ctx_text}${BYRE_SESSION_CONTEXT:-}"

if [ -n "$ctx_text" ]; then
  exec grok --append-system-prompt "$ctx_text" "$@"
fi
exec grok "$@"
