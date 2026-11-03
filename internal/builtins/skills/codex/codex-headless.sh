#!/bin/bash
# byre-codex-headless -- the codex skill's headless command. One `codex exec`
# turn, the task on stdin; `turn.failed` is the reported failure. codex runs
# through byre-codex-mcp-launch, so MCP servers and agent context match an
# interactive session. Contract: docs/SKILLS.md.
set -u

# run_child and every subshell close 5 and 6.
result_file=$BYRE_RUN_RESULT_FILE status_file=$BYRE_RUN_STATUS_FILE
unset BYRE_RUN_RESULT_FILE BYRE_RUN_STATUS_FILE
run_child() { "$@" 5>&- 6>&-; }

# Helpers on the system PATH; the agent PATH (codex may live in ~/.local/bin)
# is restored only for codex's login, probes and the launch adapter.
agent_path=${PATH-}
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH
launch=/usr/local/bin/byre-codex-mcp-launch

say() { printf 'byre-codex-headless: %s\n' "$*" >&2; }

missing=""
if ! (exec 5>&- 6>&-; PATH=$agent_path && command -v codex) >/dev/null 2>&1; then
  missing=codex
elif [ ! -x "$launch" ]; then
  missing=$launch
elif ! command -v jq >/dev/null 2>&1; then
  missing=jq
fi
if [ -n "$missing" ]; then
  say "$missing is not installed in this box; rebuild it with the codex skill enabled"
  exit 15
fi

no_credential() {
  say "no Codex credential${1:+ ($1)}."
  say "Give the box one: OPENAI_API_KEY or CODEX_ACCESS_TOKEN through env_from_host or \`byre credentials set\`, log in once in an interactive session (codex login --device-auth), or enable codex-shared-auth."
  exit 13
}

home="" out="" log=""
cleanup() {
  if [ -n "$home" ]; then run_child rm -rf -- "$home"; fi
  if [ -n "$out" ]; then run_child rm -f -- "$out"; fi
  if [ -n "$log" ]; then run_child rm -f -- "$log"; fi
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

real_home="${CODEX_HOME:-$HOME/.codex}"

# --- credential ---------------------------------------------------------------

# An env key is logged into a CODEX_HOME made for this run, never the state
# volume, so codex-shared-auth cannot publish it machine-wide; only
# config.toml is copied in. Why: ADR 0061.
key="" login_flag=""
if [ -n "${OPENAI_API_KEY:-}" ]; then
  key=$OPENAI_API_KEY login_flag=--with-api-key
elif [ -n "${CODEX_ACCESS_TOKEN:-}" ]; then
  key=$CODEX_ACCESS_TOKEN login_flag=--with-access-token
fi

if [ -n "$key" ]; then
  # No temp dir: exit 1, which the chassis reports as 14.
  home=$(exec 5>&- 6>&-; run_child mktemp -d "${TMPDIR:-/tmp}/byre-codex-home.XXXXXXXX") || {
    say "cannot create a temporary CODEX_HOME for the run"
    exit 1
  }
  if [ -f "$real_home/config.toml" ] && [ ! -L "$real_home/config.toml" ]; then
    run_child cp -- "$real_home/config.toml" "$home/config.toml" 2>/dev/null ||
      say "could not copy config.toml from $real_home; the run uses codex's defaults"
  fi
  (
    exec 5>&- 6>&-
    printf '%s' "$key" | CODEX_HOME=$home run_child timeout -k 5 30 env PATH="$agent_path" codex login "$login_flag" >&2
  )
  rc=$?
  if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
    no_credential "\`codex login $login_flag\` did not finish within 30 s"
  elif [ "$rc" != 0 ]; then
    no_credential "\`codex login $login_flag\` refused the key (exit $rc)"
  fi
  export CODEX_HOME="$home"
else
  CODEX_HOME=$real_home run_child timeout -k 5 30 env PATH="$agent_path" codex login status </dev/null >/dev/null 2>&1
  rc=$?
  if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
    no_credential "\`codex login status\` did not answer within 30 s"
  elif [ "$rc" != 0 ]; then
    no_credential "\`codex login status\` reports no login in $real_home"
  fi
  export CODEX_HOME="$real_home"
fi

version=$(exec 5>&- 6>&-; run_child timeout -k 5 10 env PATH="$agent_path" codex --version </dev/null 2>/dev/null | run_child head -n 1)
if [ -n "$version" ]; then
  printf 'agent_version=%s\n' "$version" >>"$status_file"
fi

# --- the run ------------------------------------------------------------------

# codex writes its last message to a path; a script-owned file is written
# whole and then copied to the result descriptor once. The log is a private
# copy of the stream for the failure check: the chassis's transcript lives in
# the agent-writable result dir, and this script reads nothing from there.
out=$(exec 5>&- 6>&-; run_child mktemp "${TMPDIR:-/tmp}/byre-codex-answer.XXXXXXXX") &&
  log=$(exec 5>&- 6>&-; run_child mktemp "${TMPDIR:-/tmp}/byre-codex-headless.XXXXXXXX") || {
  say "cannot create a temp file for the run"
  exit 1
}

# codex's own sandbox is off, as in the interactive command: the box is the
# sandbox. `-` reads the prompt from stdin, which is the task file.
args=(exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check
  --json --output-last-message "$out")
if [ -n "${BYRE_RUN_MODEL:-}" ]; then
  args+=(-m "$BYRE_RUN_MODEL")
fi
args+=(-)
(
  exec 5>&- 6>&-
  run_child env PATH="$agent_path" "$launch" "${args[@]}" <"$BYRE_RUN_TASK_FILE" | run_child tee -- "$log"
  exit "${PIPESTATUS[0]}"
)
rc=$?

# --- the verdict --------------------------------------------------------------

run_child cat -- "$out" >"$result_file" 2>/dev/null

# turn.failed is codex's one failure signal. Top-level error events and
# error items also appear on runs that go on to succeed (reconnect retries,
# a model-metadata fallback), so they are not failures. codex exits 1 when it
# reports a failure, so the event is judged before the exit status: a reported
# failure is 10 whatever the status.
if run_child jq -enR 'any(inputs | fromjson? | select(type == "object"); .type == "turn.failed")' <"$log" >/dev/null 2>&1; then
  exit 10
fi
if [ "$rc" != 0 ]; then
  printf 'backend_exit=%s\n' "$rc" >>"$status_file"
  exit 11
fi
exit 0
