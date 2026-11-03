#!/bin/bash
# byre-grok-headless -- the grok skill's headless command. One --prompt-file
# turn; grok prints a single JSON document at the end, and an error document
# is the reported failure. grok runs through byre-grok-launch, so the agent
# context matches an interactive session. A login whose refresh has died can
# wait on a device prompt: `byre run --timeout` bounds that. Contract:
# docs/SKILLS.md.
set -u

# run_child and every subshell close 5 and 6.
result_file=$BYRE_RUN_RESULT_FILE status_file=$BYRE_RUN_STATUS_FILE
unset BYRE_RUN_RESULT_FILE BYRE_RUN_STATUS_FILE
run_child() { "$@" 5>&- 6>&-; }

# Helpers on the system PATH; the agent PATH is restored only for grok's probe
# and the launch adapter. grok is the skill's root-owned link, by path.
agent_path=${PATH-}
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
grok=/usr/local/bin/grok
launch=/usr/local/bin/byre-grok-launch

say() { printf 'byre-grok-headless: %s\n' "$*" >&2; }

missing=""
if [ ! -x "$grok" ]; then
  missing=$grok
elif [ ! -x "$launch" ]; then
  missing=$launch
elif ! command -v jq >/dev/null 2>&1; then
  missing=jq
fi
if [ -n "$missing" ]; then
  say "$missing is not installed in this box; rebuild it with the grok skill enabled"
  exit 15
fi

no_credential() {
  say "no Grok credential${1:+ ($1)}."
  say "Give the box one: XAI_API_KEY through env_from_host or \`byre credentials set\`, log in once in an interactive session (grok login --device-auth), or enable grok-shared-auth."
  exit 13
}

home="" log=""
cleanup() {
  if [ -n "$home" ]; then run_child rm -rf -- "$home"; fi
  if [ -n "$log" ]; then run_child rm -f -- "$log"; fi
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# --- credential preflight -----------------------------------------------------

# grok has no login-status probe, so the preflight is presence: a key in the
# environment, the grok-shared-auth broker's provider command, or a stored
# login. A stored login is judged as the grok login hook judges it -- a
# regular auth.json (never a symlink, which the hook removes as planted)
# holding a "key" member, the shape a credential-bearing file has. A login
# that can no longer refresh passes this check and fails at use.
grok_home="${GROK_HOME:-/home/dev/.grok-home}"
cred="$grok_home/auth.json"
if [ -n "${XAI_API_KEY:-}" ] || [ -n "${GROK_AUTH_PROVIDER_COMMAND:-}" ]; then
  :
elif [ -f "$cred" ] && [ ! -L "$cred" ] && run_child grep -q '"key"' "$cred" 2>/dev/null; then
  :
else
  no_credential
fi

# --- the key's own GROK_HOME --------------------------------------------------

# A stored login shadows XAI_API_KEY, so a key runs under a per-run GROK_HOME.
# The config may hold secrets, so only the bundled-skills link is carried
# into it. Why: ADR 0061.
if [ -n "${XAI_API_KEY:-}" ]; then
  # No temp dir: exit 1, which the chassis reports as 14.
  home=$(exec 5>&- 6>&-; run_child mktemp -d "${TMPDIR:-/tmp}/byre-grok-home.XXXXXXXX") || {
    say "cannot create a temporary GROK_HOME for the run"
    exit 1
  }
  run_child ln -s /home/dev/.grok/bundled "$home/bundled" 2>/dev/null
  export GROK_HOME="$home"
fi

version=$(exec 5>&- 6>&-; run_child timeout -k 5 10 env PATH="$agent_path" "$grok" --version </dev/null 2>/dev/null | run_child head -n 1)
if [ -n "$version" ]; then
  printf 'agent_version=%s\n' "$version" >>"$status_file"
fi

# --- the run ------------------------------------------------------------------

# A private copy of the output, for the verdict: the chassis's transcript
# lives in the agent-writable result dir, and this script reads nothing from
# there.
# No temp file: exit 1, which the chassis reports as 14.
log=$(exec 5>&- 6>&-; run_child mktemp "${TMPDIR:-/tmp}/byre-grok-headless.XXXXXXXX") || {
  say "cannot create a temp file for grok's output"
  exit 1
}

# --output-format json prints ONE document when grok finishes, so the stream
# the chassis relays is silent until the end; --prompt-file makes the run
# single-turn. Stdin is /dev/null: grok reads the task by path.
args=(--prompt-file "$BYRE_RUN_TASK_FILE" --always-approve --output-format json)
if [ -n "${BYRE_RUN_MODEL:-}" ]; then
  args+=(-m "$BYRE_RUN_MODEL")
fi
(
  exec 5>&- 6>&-
  run_child env PATH="$agent_path" "$launch" "${args[@]}" </dev/null | run_child tee -- "$log"
  exit "${PIPESTATUS[0]}"
)
rc=$?

# --- the verdict --------------------------------------------------------------

# Success is a document carrying the answer at .text; a reported failure is
# {"type":"error","message":...}. grok exits 1 when it reports a failure, so
# the document is judged before the exit status: a reported failure is 10
# whatever the status, and its message is written as the answer, because the
# chassis prints it either way. jq writes the answer to its stdout, which the
# script opens on the result file; jq itself never holds fd 5.
doc=$(exec 5>&- 6>&-; run_child jq -c 'select(type == "object")' <"$log" 2>/dev/null | run_child tail -n 1)
if [ -n "$doc" ] && run_child jq -e '.type == "error"' <<<"$doc" >/dev/null 2>&1; then
  run_child jq -j '.message // "" | if type == "string" then . else tojson end' <<<"$doc" >"$result_file" 2>/dev/null
  exit 10
fi
answered=""
if [ -n "$doc" ] && run_child jq -e '.text | type == "string"' <<<"$doc" >/dev/null 2>&1; then
  answered=1
  run_child jq -j '.text' <<<"$doc" >"$result_file" 2>/dev/null
fi
# A nonzero status without a reported failure, or any status without an
# answer document, is grok failing as a process.
if [ "$rc" != 0 ] || [ -z "$answered" ]; then
  printf 'backend_exit=%s\n' "$rc" >>"$status_file"
  exit 11
fi
exit 0
