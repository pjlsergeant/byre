#!/bin/bash
# byre-claude-headless -- the claude skill's headless command. One -p turn,
# the task on stdin, stream-json output; the answer and the failure flag come
# from the last `result` event. claude runs through byre-claude-launch, so the
# agent context, MCP servers and Claude Skills match an interactive session.
# Contract: docs/SKILLS.md.
set -u

# run_child and every subshell close 5 and 6.
result_file=$BYRE_RUN_RESULT_FILE status_file=$BYRE_RUN_STATUS_FILE
unset BYRE_RUN_RESULT_FILE BYRE_RUN_STATUS_FILE
run_child() { "$@" 5>&- 6>&-; }

# Helpers on the system PATH; the agent PATH is restored only for claude's
# probes and the launch adapter (by absolute path).
agent_path=${PATH-}
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
launch=/usr/local/bin/byre-claude-launch

say() { printf 'byre-claude-headless: %s\n' "$*" >&2; }

missing=""
if ! (exec 5>&- 6>&-; PATH=$agent_path && command -v claude) >/dev/null 2>&1; then
  missing=claude
elif [ ! -x "$launch" ]; then
  missing=$launch
elif ! command -v jq >/dev/null 2>&1; then
  missing=jq
fi
if [ -n "$missing" ]; then
  say "$missing is not installed in this box; rebuild it with the claude skill enabled"
  exit 15
fi

no_credential() {
  say "no Claude credential${1:+ ($1)}."
  say "Give the box one: CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY through env_from_host or \`byre credentials set\`, or enable claude-shared-auth."
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
elif [ -f "$creds" ] && [ ! -L "$creds" ] && run_child grep -q '"claudeAiOauth"' "$creds" 2>/dev/null; then
  :
else
  auth=$(exec 5>&- 6>&-; run_child timeout -k 5 30 env PATH="$agent_path" claude auth status </dev/null 2>/dev/null)
  rc=$?
  if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
    no_credential "\`claude auth status\` did not answer within 30 s"
  fi
  if [ "$rc" != 0 ] || ! run_child jq -e '.loggedIn == true' <<<"$auth" >/dev/null 2>&1; then
    no_credential "\`claude auth status\` reports no login"
  fi
fi

# The onboarding seed claude-firstrun.sh writes (the publish trade is
# explained in claude-shared-auth's firstrun hook), so the wizard never runs.
if [ -n "$env_cred" ] && [ ! -e "$cfg/.claude.json" ] && [ ! -L "$cfg/.claude.json" ]; then
  if run_child mkdir -p -- "$cfg" 2>/dev/null && seed=$(exec 5>&- 6>&-; run_child mktemp -- "$cfg/.claude.json.byre-XXXXXX" 2>/dev/null); then
    if printf '{"hasCompletedOnboarding": true}\n' >"$seed" 2>/dev/null; then
      run_child ln -n -- "$seed" "$cfg/.claude.json" 2>/dev/null || true
    fi
    run_child rm -f -- "$seed"
  fi
fi

version=$(exec 5>&- 6>&-; run_child timeout -k 5 10 env PATH="$agent_path" claude --version </dev/null 2>/dev/null | run_child head -n 1)
if [ -n "$version" ]; then
  printf 'agent_version=%s\n' "$version" >>"$status_file"
fi

# --- the run ------------------------------------------------------------------

# A private copy of the stream, for the result event: the chassis's transcript
# lives in the agent-writable result dir, and this script reads nothing from
# there.
# No temp file: exit 1, which the chassis reports as 14.
log=$(exec 5>&- 6>&-; run_child mktemp "${TMPDIR:-/tmp}/byre-claude-headless.XXXXXXXX") || {
  say "cannot create a temp file for the event stream"
  exit 1
}
trap 'run_child rm -f -- "$log"' EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# stream-json under -p requires --verbose. The task arrives on stdin, which
# -p reads as the prompt.
args=(-p --dangerously-skip-permissions --mcp-config /etc/byre/mcp.json
  --add-dir /etc/byre/claude-skills --output-format stream-json --verbose)
if [ -n "${BYRE_RUN_MODEL:-}" ]; then
  args+=(--model "$BYRE_RUN_MODEL")
fi
(
  exec 5>&- 6>&-
  run_child env PATH="$agent_path" "$launch" "${args[@]}" <"$BYRE_RUN_TASK_FILE" | run_child tee -- "$log"
  exit "${PIPESTATUS[0]}"
)
rc=$?

# --- the verdict --------------------------------------------------------------

# The last result event carries the answer (.result) and the failure flag
# (.is_error). Its subtype is no signal: a failed turn still says "success".
# claude exits 1 when it reports a failure, so the event is judged before the
# exit status: a reported failure is 10 whatever the status, and its message
# is still written as the answer, because the chassis prints it either way.
# jq writes the answer to its stdout, which the script opens on the result
# file; jq itself never holds fd 5.
result=$(exec 5>&- 6>&-; run_child jq -cR 'fromjson? | select(type == "object" and .type == "result")' <"$log" 2>/dev/null | run_child tail -n 1)
if [ -n "$result" ]; then
  run_child jq -j '.result // "" | if type == "string" then . else tojson end' <<<"$result" >"$result_file" 2>/dev/null
  if run_child jq -e '.is_error == true' <<<"$result" >/dev/null 2>&1; then
    exit 10
  fi
fi
# A nonzero status without a reported failure, or any status without a result
# event, is claude failing as a process.
if [ "$rc" != 0 ] || [ -z "$result" ]; then
  printf 'backend_exit=%s\n' "$rc" >>"$status_file"
  exit 11
fi
exit 0
