#!/bin/bash
# byre's grok launch adapter (ADR 0046): inject the baked agent context plus
# the launcher's per-session additions via --append-system-prompt (alias of
# --rules — appended after grok's default prompt in a <human_rules> block;
# source-verified). byre writes NOTHING into $GROK_HOME: its AGENTS.md is the
# user's file.
#
# The value rides ONE argv string, and Linux caps a single exec argument at
# MAX_ARG_STRLEN (~128 KiB) — far under byre's context bounds (config
# prose is disclosure-tiered, never capped; the technical read ceiling is
# 16 MiB). A
# legal-but-large context must DEGRADE loudly, not kill the exec: cap with a
# disclosure pointing at the baked file, which the agent can read in-box.
set -eu

# The adapter's own helpers (cat, head, wc, iconv) run from the image's system
# directories only: the inherited PATH starts with the agent-writable
# ~/.local/bin, and repository code (a worktree checkout's hooks) runs in the
# launcher before this adapter, so a helper found there would decide what grok
# is told. grok itself is named by the root-owned link the skill installs: its
# binary lives under ~/.grok, not on the inherited PATH's first entry, so a
# grok planted in ~/.local/bin would otherwise run in its place. The inherited
# PATH is restored on the exec of grok alone, because everything grok starts
# needs the full image PATH.
agent_path=${PATH-}
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
grok=/usr/local/bin/grok

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
  exec env PATH="$agent_path" "$grok" --append-system-prompt "$ctx_text" "$@"
fi
exec env PATH="$agent_path" "$grok" "$@"
