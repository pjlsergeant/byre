#!/bin/bash
# byre-claude-headless -- the claude skill's headless command: the program the
# byre-headless chassis runs for `byre run`. It gives claude the task once,
# non-interactively, and translates the outcome into the chassis contract:
#
#   0  claude answered           10 claude reported failure (is_error)
#   11 claude failed without reporting why (backend_exit=<rc> is in the
#      status file)              13 no usable credential
#   15 claude, its launch adapter, or jq is not in the box
#
# The chassis sets BYRE_RUN_TASK_FILE (the task text, given to claude on
# stdin), BYRE_RUN_RESULT_FILE (a descriptor the answer is written to, once),
# BYRE_RUN_STATUS_FILE (key=value lines the chassis reads) and, optionally,
# BYRE_RUN_MODEL. This script's stdout is the live event stream the chassis
# records and relays; nothing here reads a terminal.
#
# claude runs through byre-claude-launch, the same adapter the interactive
# command uses, so the injected agent context, MCP servers and Claude Skills
# reach a headless run exactly as they reach an interactive one.
set -u

say() { printf 'byre-claude-headless: %s\n' "$*" >&2; }

for tool in claude byre-claude-launch jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    say "$tool is not installed in this box; rebuild it with the claude skill enabled"
    exit 15
  fi
done

no_credential() {
  say "no Claude credential${1:+ ($1)}."
  say "Give the box one: CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY through env_from_host in the project config, or stored with \`byre credentials set\`; or enable claude-shared-auth for one login shared by every project on this machine."
  exit 13
}

# --- credential preflight -----------------------------------------------------

# claude does not validate an env token before a turn (`claude auth status`
# reports any non-empty one as logged in), so for the env credentials presence
# is the whole check. A stored login is judged by the key claude writes for an
# inference login; .credentials.json also holds MCP server logins (mcpOAuth),
# which do not authenticate inference. The status probe covers any other login
# claude knows of, and is bounded: a hang is a failed preflight, never a
# stalled run.
cfg="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}"
creds="$cfg/.credentials.json"
env_cred=""
if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] || [ -n "${ANTHROPIC_API_KEY:-}" ] ||
  [ -n "${ANTHROPIC_AUTH_TOKEN:-}" ]; then
  env_cred=1
elif [ -f "$creds" ] && [ ! -L "$creds" ] && grep -q '"claudeAiOauth"' "$creds" 2>/dev/null; then
  :
else
  auth=$(timeout -k 5 30 claude auth status </dev/null 2>/dev/null)
  rc=$?
  if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
    no_credential "\`claude auth status\` did not answer within 30 s"
  fi
  if [ "$rc" != 0 ] || ! printf '%s' "$auth" | jq -e '.loggedIn == true' >/dev/null 2>&1; then
    no_credential "\`claude auth status\` reports no login"
  fi
fi

# A fresh config dir has no .claude.json, and claude's first-run wizard gates
# on it rather than on an env credential. The claude onboarding hook seeds the
# marker before launch; this is the same seed, so a headless run cannot reach
# the wizard even where that hook did not run. Fresh dirs only: never touch an
# existing .claude.json (claude owns it), and never write through a symlink
# (`-L` catches the dangling one `-e` misses). The bytes go to a mktemp'd name
# and `ln -n` claims the final one with link(2), which fails on any node raced
# in and never follows a symlink. A lost claim leaves nothing behind.
if [ -n "$env_cred" ] && [ ! -e "$cfg/.claude.json" ] && [ ! -L "$cfg/.claude.json" ]; then
  if mkdir -p -- "$cfg" 2>/dev/null && seed=$(mktemp -- "$cfg/.claude.json.byre-XXXXXX" 2>/dev/null); then
    if printf '{"hasCompletedOnboarding": true}\n' >"$seed" 2>/dev/null; then
      ln -n -- "$seed" "$cfg/.claude.json" 2>/dev/null || true
    fi
    rm -f -- "$seed"
  fi
fi

version=$(timeout -k 5 10 claude --version </dev/null 2>/dev/null | head -n 1)
if [ -n "$version" ]; then
  printf 'agent_version=%s\n' "$version" >>"$BYRE_RUN_STATUS_FILE"
fi

# --- the run ------------------------------------------------------------------

# A private copy of the stream, for the result event: the chassis's transcript
# lives in the agent-writable result dir, and this script reads nothing from
# there. A box that cannot make a temp file is broken in a way no exit in the
# contract names, so this exits 1 and the chassis's verdict -- the headless
# command failed -- is the true one.
log=$(mktemp "${TMPDIR:-/tmp}/byre-claude-headless.XXXXXXXX") || {
  say "cannot create a temp file for the event stream"
  exit 1
}
trap 'rm -f -- "$log"' EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# stream-json under -p requires --verbose. The task arrives on stdin, which
# -p reads as the prompt.
args=(-p --dangerously-skip-permissions --mcp-config /etc/byre/mcp.json
  --add-dir /etc/byre/claude-skills --output-format stream-json --verbose)
if [ -n "${BYRE_RUN_MODEL:-}" ]; then
  args+=(--model "$BYRE_RUN_MODEL")
fi
# The result and status descriptors (5 and 6) and the variables naming them
# are this script's alone: claude and its tee never get them, so nothing
# claude leaves running can rewrite the answer or append status lines after
# the script's own writes.
{
  (
    unset BYRE_RUN_RESULT_FILE BYRE_RUN_STATUS_FILE
    exec byre-claude-launch "${args[@]}"
  ) <"$BYRE_RUN_TASK_FILE" | tee -- "$log"
  rc=${PIPESTATUS[0]}
} 5>&- 6>&-

# --- the verdict --------------------------------------------------------------

# The last result event carries the answer (.result) and the failure flag
# (.is_error). Its subtype is no signal: a failed turn still says "success".
# claude exits 1 when it reports a failure, so the event is judged before the
# exit status: a reported failure is 10 whatever the status, and its message
# is still written as the answer, because the chassis prints it either way.
result=$(jq -cR 'fromjson? | select(type == "object" and .type == "result")' <"$log" 2>/dev/null | tail -n 1)
if [ -n "$result" ]; then
  printf '%s' "$result" |
    jq -j '.result // "" | if type == "string" then . else tojson end' >"$BYRE_RUN_RESULT_FILE" 2>/dev/null
  if printf '%s' "$result" | jq -e '.is_error == true' >/dev/null 2>&1; then
    exit 10
  fi
fi
# A nonzero status without a reported failure, or any status without a result
# event, is claude failing as a process.
if [ "$rc" != 0 ] || [ -z "$result" ]; then
  printf 'backend_exit=%s\n' "$rc" >>"$BYRE_RUN_STATUS_FILE"
  exit 11
fi
exit 0
