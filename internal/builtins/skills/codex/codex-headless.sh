#!/bin/bash
# byre-codex-headless -- the codex skill's headless command: the program the
# byre-headless chassis runs for `byre run`. It gives codex the task once
# (`codex exec`), non-interactively, and translates the outcome into the
# chassis contract:
#
#   0  codex answered            10 codex reported failure (turn.failed)
#   11 codex failed without reporting why (backend_exit=<rc> is in the
#      status file)              13 no usable credential
#   15 codex, its launch adapter, or jq is not in the box
#
# The chassis sets BYRE_RUN_TASK_FILE (the task text, given to codex on
# stdin), BYRE_RUN_RESULT_FILE (a descriptor the answer is written to, once),
# BYRE_RUN_STATUS_FILE (key=value lines the chassis reads) and, optionally,
# BYRE_RUN_MODEL. This script's stdout is the live event stream the chassis
# records and relays; nothing here reads a terminal.
#
# codex runs through byre-codex-mcp-launch, the same adapter the interactive
# command uses, so byre's MCP servers and agent context reach a headless run
# exactly as they reach an interactive one.
set -u

say() { printf 'byre-codex-headless: %s\n' "$*" >&2; }

for tool in codex byre-codex-mcp-launch jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    say "$tool is not installed in this box; rebuild it with the codex skill enabled"
    exit 15
  fi
done

no_credential() {
  say "no Codex credential${1:+ ($1)}."
  say "Give the box one: OPENAI_API_KEY or CODEX_ACCESS_TOKEN through env_from_host in the project config, or stored with \`byre credentials set\`; or log in once in an interactive session (codex login --device-auth), or enable codex-shared-auth for one login shared by every project on this machine."
  exit 13
}

home="" out="" log=""
cleanup() {
  if [ -n "$home" ]; then rm -rf -- "$home"; fi
  if [ -n "$out" ]; then rm -f -- "$out"; fi
  if [ -n "$log" ]; then rm -f -- "$log"; fi
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

real_home="${CODEX_HOME:-$HOME/.codex}"

# --- credential ---------------------------------------------------------------

# codex reads its credential only from a login written into CODEX_HOME; a key
# in the environment is never used on its own. So a key given to the box is
# logged into a CODEX_HOME made for this one run and removed when it ends --
# never into the real one, which is the project's state volume. The reason is
# codex-shared-auth: its reconciler runs at every launch and publishes any
# valid credential it finds in a project's CODEX_HOME to every project on the
# machine, preferring an API-key file over a shared ChatGPT login. A key left
# in the state volume would therefore bill other projects' work to it, hand
# their boxes a credential nobody granted them, and displace the shared login
# -- one launch later, without a prompt. That matters on machines that persist
# (a laptop, a self-hosted runner); a discarded hosted runner never relaunches.
#
# A key in the environment wins over an existing login: the run uses the key
# even when the real CODEX_HOME holds a working one. OPENAI_API_KEY is tried
# before CODEX_ACCESS_TOKEN. Only config.toml (the user's preferences) is
# copied into the run's home; auth.json and the rest stay where they are.
#
# The run's home sits in TMPDIR, so codex warns on stderr that it skips its
# helper-binary PATH aliases there; the run does not need them. A key codex
# rejects can appear masked (first and last few characters) in the event
# stream's error messages; the full key is only ever in the run's own home.
key="" login_flag=""
if [ -n "${OPENAI_API_KEY:-}" ]; then
  key=$OPENAI_API_KEY login_flag=--with-api-key
elif [ -n "${CODEX_ACCESS_TOKEN:-}" ]; then
  key=$CODEX_ACCESS_TOKEN login_flag=--with-access-token
fi

if [ -n "$key" ]; then
  # A box that cannot make a temp dir is broken in a way no exit in the
  # contract names, so this exits 1 and the chassis's verdict -- the headless
  # command failed -- is the true one.
  home=$(mktemp -d "${TMPDIR:-/tmp}/byre-codex-home.XXXXXXXX") || {
    say "cannot create a temporary CODEX_HOME for the run"
    exit 1
  }
  if [ -f "$real_home/config.toml" ] && [ ! -L "$real_home/config.toml" ]; then
    cp -- "$real_home/config.toml" "$home/config.toml" 2>/dev/null ||
      say "could not copy config.toml from $real_home; the run uses codex's defaults"
  fi
  printf '%s' "$key" | CODEX_HOME=$home timeout 30 codex login "$login_flag" >&2
  rc=$?
  if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
    no_credential "\`codex login $login_flag\` did not finish within 30 s"
  elif [ "$rc" != 0 ]; then
    no_credential "\`codex login $login_flag\` refused the key (exit $rc)"
  fi
  export CODEX_HOME="$home"
else
  CODEX_HOME=$real_home timeout 30 codex login status </dev/null >/dev/null 2>&1
  rc=$?
  if [ "$rc" = 124 ] || [ "$rc" = 137 ]; then
    no_credential "\`codex login status\` did not answer within 30 s"
  elif [ "$rc" != 0 ]; then
    no_credential "\`codex login status\` reports no login in $real_home"
  fi
  export CODEX_HOME="$real_home"
fi

version=$(timeout 10 codex --version </dev/null 2>/dev/null | head -n 1)
if [ -n "$version" ]; then
  printf 'agent_version=%s\n' "$version" >>"$BYRE_RUN_STATUS_FILE"
fi

# --- the run ------------------------------------------------------------------

# codex writes its last message to a path; a script-owned file is written
# whole and then copied to the result descriptor once. The log is a private
# copy of the stream for the failure check: the chassis's transcript lives in
# the agent-writable result dir, and this script reads nothing from there.
out=$(mktemp "${TMPDIR:-/tmp}/byre-codex-answer.XXXXXXXX") &&
  log=$(mktemp "${TMPDIR:-/tmp}/byre-codex-headless.XXXXXXXX") || {
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
byre-codex-mcp-launch "${args[@]}" <"$BYRE_RUN_TASK_FILE" | tee -- "$log"
rc=${PIPESTATUS[0]}

# --- the verdict --------------------------------------------------------------

cat -- "$out" >"$BYRE_RUN_RESULT_FILE" 2>/dev/null

# turn.failed is codex's one failure signal. Top-level error events and
# error items also appear on runs that go on to succeed (reconnect retries,
# a model-metadata fallback), so they are not failures. codex exits 1 when it
# reports a failure, so the event is judged before the exit status: a reported
# failure is 10 whatever the status.
if jq -enR 'any(inputs | fromjson? | select(type == "object"); .type == "turn.failed")' <"$log" >/dev/null 2>&1; then
  exit 10
fi
if [ "$rc" != 0 ]; then
  printf 'backend_exit=%s\n' "$rc" >>"$BYRE_RUN_STATUS_FILE"
  exit 11
fi
exit 0
