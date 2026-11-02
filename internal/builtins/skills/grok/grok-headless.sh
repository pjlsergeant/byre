#!/bin/bash
# byre-grok-headless -- the grok skill's headless command: the program the
# byre-headless chassis runs for `byre run`. It gives grok the task once
# (`--prompt-file`, single-turn), non-interactively, and translates the
# outcome into the chassis contract:
#
#   0  grok answered             10 grok reported failure (an error document)
#   11 grok failed without reporting why (backend_exit=<rc> is in the
#      status file)              13 no usable credential
#   15 grok, its launch adapter, or jq is not in the box
#
# The chassis sets BYRE_RUN_TASK_FILE (the task text, which grok reads by
# path), BYRE_RUN_RESULT_FILE (a descriptor the answer is written to, once),
# BYRE_RUN_STATUS_FILE (key=value lines the chassis reads) and, optionally,
# BYRE_RUN_MODEL. This script's stdout is the agent's output stream the
# chassis records and relays; nothing here reads a terminal.
#
# grok runs through byre-grok-launch, the same adapter the interactive
# command uses, so the injected agent context reaches a headless run exactly
# as it reaches an interactive one.
#
# grok has no default time limit, and a login whose refresh chain has died
# can leave it waiting on a device prompt nobody will answer. `byre run
# --timeout` is the bound for that; this script sets none of its own.
set -u

# The result and status descriptors (5 and 6) and the variables naming them
# are this script's alone. The names move into script locals before anything
# runs, and every program the script starts -- grok, its version probe, jq,
# tee, the file helpers -- runs through run_child, which closes both
# descriptors for it. So nothing grok or a probe leaves running can rewrite
# the answer or append status lines after the script's own writes.
#
# A subshell or command substitution is a forked copy of this shell: it holds
# both descriptors for as long as the program it starts runs, as that
# program's parent. So each one closes both before anything else runs, and
# the script's own data reaches a program as a here-string, never through a
# pipeline element that is such a copy.
result_file=$BYRE_RUN_RESULT_FILE status_file=$BYRE_RUN_STATUS_FILE
unset BYRE_RUN_RESULT_FILE BYRE_RUN_STATUS_FILE
run_child() { "$@" 5>&- 6>&-; }

# The script's own helpers (timeout, env, jq, tee, mktemp, ...) run from the
# image's system directories only: the inherited PATH starts with the
# agent-writable ~/.local/bin, which repository code could write before the
# chassis ran, and a helper found there would run inside the verdict -- a
# planted timeout voids the version probe's bound, a planted jq judges the
# answer. grok is named by the root-owned link the skill installs, as the
# adapter names it, so a grok planted on the inherited PATH never answers in
# its place. The inherited PATH is the one everything grok starts needs:
# grok's probe and its launch adapter run with it restored through env, both
# named by absolute path. The adapter treats the PATH it is given as the
# agent's, pins its own, and hands this one to grok.
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
  say "Give the box one: XAI_API_KEY through env_from_host in the project config, or stored with \`byre credentials set\`; or log in once in an interactive session (grok login --device-auth), or enable grok-shared-auth for one login shared by every project on this machine."
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

# grok's rule is that a stored session login in $GROK_HOME/auth.json takes
# precedence over XAI_API_KEY, so on a box that ever completed `grok login` the
# key given to the run would not be what runs -- and a login whose refresh
# chain has died would pass the preflight above and then wait on a device
# prompt. So a key in the environment runs with a GROK_HOME made for this one
# run and removed when it ends, where no stored login can shadow it; the real
# one is not touched. Nothing is copied in: grok's config.toml can carry
# credentials ([custom_endpoints] api_key), which is why the skill seeds no
# prefs from it either. The one entry made is the link the grok-bundled hook
# keeps in the real home, to the bundled skills grok extracts into the image,
# so a run with the key sees the skills an interactive session does.
#
# The grok-shared-auth broker (GROK_AUTH_PROVIDER_COMMAND) keeps its store in
# the machine identity volume and never reads GROK_HOME, so without a key it
# runs against the real home as an interactive session does, and with one the
# temporary home changes nothing for it.
if [ -n "${XAI_API_KEY:-}" ]; then
  # A box that cannot make a temp dir is broken in a way no exit in the
  # contract names, so this exits 1 and the chassis's verdict -- the headless
  # command failed -- is the true one.
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
# there. A box that cannot make a temp file is broken in a way no exit in the
# contract names, so this exits 1 and the chassis's verdict -- the headless
# command failed -- is the true one.
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
