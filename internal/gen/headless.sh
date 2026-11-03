#!/bin/bash
# byre-headless [script] -- the headless-run chassis, the container command
# of `byre run`. It validates the staged inputs, holds byre's own result-dir
# files as descriptors, supervises the agent skill's headless command, and
# owns the verdict. Its own exits are 12, 14 and 16; the command's set and the
# full table: ADR 0061 / the commands page. The command's contract:
# docs/SKILLS.md.
#
# The optional positional is the test seam; production runs the baked
# /etc/byre/agent-headless. No env seam: a skill's [runtime].env can set any
# BYRE_* name, but it cannot reach the container argv.
#
# The dir is agent-writable, so after the exclusive opens below every write
# goes to a held descriptor and every read is a bounded read through
# /dev/fd/N: a FIFO or symlink planted at a name cannot block or redirect
# PID 1. The one exception is the best-effort final result.json publish.
set -u

# Helpers resolve on the system PATH; the saved PATH is restored for the
# headless command only (ADR 0061).
byre_saved_path=${PATH-}
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export PATH

exec </dev/null

SECONDS=0
started_at=$(date -u +%FT%TZ)

say() { printf 'byre-headless: %s\n' "$1" >&2; }

# The box's real stdout, saved on fd 9 by the launcher before any hook ran.
# Run outside the launcher there is no fd 9, and the chassis's own stdout is
# the real one.
if ! { : >&9; } 2>/dev/null; then
  exec 9>&1
fi

refuse() {
  say "$1"
  say "run ${BYRE_RUN_ID:-?}: result_dir, exit 16"
  exit 16
}

# uptime_cs sets now_cs to the monotonic clock: /proc/uptime's first field
# (seconds since boot, two decimals) as whole centiseconds. A builtin read of
# a kernel file, so it is safe inside a trap handler.
uptime_cs() {
  local up rest
  read -r up rest </proc/uptime || return 1
  [[ $up =~ ^[0-9]+\.[0-9][0-9]$ ]] || return 1
  now_cs=$((10#${up/./}))
}

# --- JSON -------------------------------------------------------------------

# json_str prints its argument as a JSON string: quote, backslash and every
# control character escaped; bytes >= 0x80 pass through, so UTF-8 survives.
# Byte-wise (C locale), so a control byte can never match inside a multibyte
# sequence. The replacements are quoted: bash 5.2's patsub_replacement would
# otherwise read an & in them as the matched text.
json_str() {
  local LC_ALL=C s=$1 bs='\' q='"' i o c r
  s=${s//"$bs"/"$bs$bs"}
  s=${s//"$q"/"$bs$q"}
  for ((i = 1; i < 32; i++)); do
    printf -v o '%03o' "$i"
    printf -v c "\\$o"
    case $s in *"$c"*)
      printf -v r '\\u%04x' "$i"
      s=${s//"$c"/"$r"}
      ;;
    esac
  done
  printf -v c '\177'
  s=${s//"$c"/"\\u007f"}
  printf '"%s"' "$s"
}

json_opt() {
  if [ -n "$1" ]; then json_str "$1"; else printf 'null'; fi
}

json_int() {
  if [ -n "$1" ]; then printf '%d' "$1"; else printf 'null'; fi
}

# result_json prints result.json (schema 1) from the run's globals.
result_json() {
  printf '{"schema":1,"run_id":%s,"status":%s,"exit_code":%s,"agent":%s,"agent_version":%s,"model":%s,"backend_exit_code":%s,"script_exit_code":%s,"task_sha256":%s,"started_at":%s,"finished_at":%s,"duration_s":%s,"timeout":%s,"result_file":"result.md","transcript_file":"transcript.jsonl","stderr_file":"stderr.log","message":%s}\n' \
    "$(json_opt "${BYRE_RUN_ID:-}")" \
    "$(json_str "$status")" \
    "$(json_int "$code")" \
    "$(json_opt "${BYRE_RUN_AGENT:-}")" \
    "$(json_opt "$agent_version")" \
    "$(json_opt "${BYRE_RUN_MODEL:-}")" \
    "$(json_int "$backend_exit")" \
    "$(json_int "$script_exit")" \
    "$(json_str "$want_sha")" \
    "$(json_str "$started_at")" \
    "$(json_opt "$finished_at")" \
    "$(json_int "$duration")" \
    "$(json_opt "$timeout_spec")" \
    "$(json_str "$message")"
}

status=started code="" agent_version="" backend_exit="" script_exit=""
finished_at="" duration="" message="the run started"

# --- 1. validate the inputs ---------------------------------------------------

[ "$#" -le 1 ] || refuse "usage: byre-headless [script] (got $# arguments)"

dir=${BYRE_RUN_DIR:-}
case $dir in
/?*) ;;
*) refuse "BYRE_RUN_DIR must be an absolute path to the result dir (got $(printf '%q' "$dir"))" ;;
esac
while [ "${dir%/}" != "$dir" ]; do dir=${dir%/}; done
[ ! -L "$dir" ] || refuse "BYRE_RUN_DIR is a symlink, not the result dir: $(printf '%q' "$dir")"
[ -d "$dir" ] || refuse "BYRE_RUN_DIR is not a directory: $(printf '%q' "$dir")"

task=${BYRE_RUN_TASK_FILE:-}
task_name=${task##*/}
if [ -z "$task_name" ] || [ "$task_name" = . ] || [ "$task_name" = .. ] || [ "$task" != "$dir/$task_name" ]; then
  refuse "BYRE_RUN_TASK_FILE must name a file directly inside BYRE_RUN_DIR (got $(printf '%q' "$task"))"
fi
[ ! -L "$task" ] || refuse "BYRE_RUN_TASK_FILE is a symlink: $(printf '%q' "$task")"
[ -f "$task" ] || refuse "BYRE_RUN_TASK_FILE is not a regular file: $(printf '%q' "$task")"

want_sha=${BYRE_RUN_TASK_SHA256:-}
[[ $want_sha =~ ^[0-9a-f]{64}$ ]] || refuse "BYRE_RUN_TASK_SHA256 is not a lowercase sha256 hex digest"
# Repo code (a pending worktree checkout's hooks and filters) runs in the
# launcher before this point and can rewrite the task; nothing untrusted runs
# concurrently, so a match means the agent gets the bytes the host hashed.
# The read stops one byte past the host's 1 MiB cap, so an oversized file
# cannot match.
got_sha=$(head -c 1048577 <"$task" | sha256sum)
got_sha=${got_sha%% *}
[ "$got_sha" = "$want_sha" ] || refuse "the task's sha256 does not match BYRE_RUN_TASK_SHA256 (the task file changed after the host staged it)"

timeout_spec=${BYRE_RUN_TIMEOUT:-}
if [ -n "$timeout_spec" ]; then
  [[ $timeout_spec =~ ^([0-9]+(\.[0-9]*)?|\.[0-9]+)[smhd]?$ ]] ||
    refuse "BYRE_RUN_TIMEOUT is not a duration (a number with an optional s/m/h/d suffix): $(printf '%q' "$timeout_spec")"
  # Zero means no timer, as if the variable were unset.
  if [[ ${timeout_spec%[smhd]} =~ ^[0.]*$ ]]; then timeout_spec=""; fi
fi

# timeout_cs offsets a deadline on the monotonic clock (/proc/uptime);
# timeout_secs only range-checks 1s..3650d. The fraction keeps nine digits and
# both are floored, so a value within 1e-10 of a boundary can move one
# centisecond -- accepted. The integer digits are counted before multiplying,
# so 3650d (31,536,000,000 cs) is the largest product and none wraps 64 bits.
timeout_max_secs=315360000
timeout_secs=0 timeout_cs=0
if [ -n "$timeout_spec" ]; then
  num=${timeout_spec%[smhd]}
  case $timeout_spec in
  *m) mult=60 ;;
  *h) mult=3600 ;;
  *d) mult=86400 ;;
  *) mult=1 ;;
  esac
  int=${num%%.*} frac=""
  case $num in *.*) frac=${num#*.} ;; esac
  while [[ $int == 0* ]]; do int=${int#0}; done
  [ "${#int}" -le 9 ] ||
    refuse "BYRE_RUN_TIMEOUT is over the largest timeout, 3650d: $(printf '%q' "$timeout_spec")"
  frac=${frac:0:9}
  while [ "${#frac}" -lt 9 ]; do frac+=0; done
  timeout_secs=$(((10#${int:-0}) * mult + (10#$frac) * mult / 1000000000))
  [ "$timeout_secs" -ge 1 ] ||
    refuse "BYRE_RUN_TIMEOUT is under the smallest timeout, 1s: $(printf '%q' "$timeout_spec")"
  [ "$timeout_secs" -le "$timeout_max_secs" ] ||
    refuse "BYRE_RUN_TIMEOUT is over the largest timeout, 3650d: $(printf '%q' "$timeout_spec")"
  timeout_cs=$(((10#${int:-0}) * mult * 100 + (10#$frac) * mult / 10000000))
  uptime_cs || refuse "cannot read the monotonic clock in /proc/uptime, which a timeout needs"
fi

# --- 2. claim byre's own files, exclusively, once -----------------------------

# bash's noclobber is O_EXCL only for a name that does not exist: a
# pre-existing FIFO, device, or symlink to one is opened WITHOUT O_EXCL (so
# `>/dev/null` keeps working), and a planted FIFO would block the open. So
# every name is refused when ANY node is there first -- `-L` covers the
# dangling symlink `-e` misses -- and set -C still refuses a regular file. The
# check and the open are not atomic; nothing untrusted runs between them (the
# same window the hash check relies on), and the host emptied the dir.
claimable() {
  if [ -e "$dir/$1" ] || [ -L "$dir/$1" ]; then
    refuse "the result dir already holds $1; the host stages an empty dir, so something planted it"
  fi
}
set -C
claimable transcript.jsonl
{ exec 3>"$dir/transcript.jsonl"; } 2>/dev/null || refuse "cannot create transcript.jsonl in the result dir"
claimable stderr.log
{ exec 4>"$dir/stderr.log"; } 2>/dev/null || refuse "cannot create stderr.log in the result dir"
claimable result.md
{ exec 5>"$dir/result.md"; } 2>/dev/null || refuse "cannot create result.md in the result dir"
claimable status
{ exec 6>"$dir/status"; } 2>/dev/null || refuse "cannot create status in the result dir"
claimable result.json
{ result_json >"$dir/result.json"; } 2>/dev/null || refuse "cannot create result.json in the result dir"
set +C

# --- 3. resolve the headless command -----------------------------------------

cmd=${1:-/etc/byre/agent-headless}
timed_out=0
child="" timer="" killer="" deadline_cs=""

# --- 4. supervise -------------------------------------------------------------

# Every helper process (the tees, the timer, the KILL follow-up) and the
# headless command itself start with the descriptors they must not hold
# closed: a relay pipe held open elsewhere keeps its tee from seeing EOF, and
# fd 9 is the chassis's alone -- the headless command never writes the box's
# real stdout.

# tee_to appends a stream to a held file and, unless BYRE_RUN_QUIET, relays it
# to the box's stderr. It ignores TERM/INT so a signal aimed at the run never
# cuts the record short; it ends at EOF.
tee_to() {
  trap '' INT TERM
  if [ -n "${BYRE_RUN_QUIET:-}" ]; then
    exec tee -a "/dev/fd/$1" >/dev/null
  else
    exec tee -a "/dev/fd/$1" >&2
  fi
}

on_signal() {
  interrupted=1
  if [ -n "$child" ]; then kill "-$1" -- "-$child" 2>/dev/null; fi
}

# on_timeout (USR1, see the traps): TERM the group, KILL 15 s later.
on_timeout() {
  interrupted=1
  [ -n "$deadline_cs" ] || return
  uptime_cs || return
  [ "$now_cs" -ge "$deadline_cs" ] || return
  [ "$timed_out" = 0 ] || return
  timed_out=1
  kill -TERM -- "-$child" 2>/dev/null
  (
    trap 'kill $! 2>/dev/null; exit 0' TERM
    sleep 15 &
    wait $!
    kill -KILL -- "-$child" 2>/dev/null
  ) 3>&- 4>&- 5>&- 6>&- 7>&- 8>&- 9>&- &
  killer=$!
}

# stop_helper ends a timer or KILL follow-up the run no longer needs (its trap
# kills the sleep it is waiting on, so no sleep outlives it).
stop_helper() {
  if [ -n "$1" ]; then
    kill "$1" 2>/dev/null
    wait "$1" 2>/dev/null
  fi
}

if [ ! -x "$cmd" ]; then
  code=15 status=backend_unavailable
  message="no headless command at $cmd"
else
  export BYRE_RUN_RESULT_FILE=/dev/fd/5
  export BYRE_RUN_STATUS_FILE=/dev/fd/6

  exec 7> >(exec 4>&- 5>&- 6>&- 9>&-; tee_to 3)
  tee_out=$!
  exec 8> >(exec 3>&- 5>&- 6>&- 7>&- 9>&-; tee_to 4)
  tee_err=$!

  # USR1 is the timer's signal, but any same-uid process can send it: it
  # counts only after the monotonic deadline and only while a timer is armed,
  # and is ignored otherwise (its default action kills the chassis). A
  # trapped signal interrupts `wait`, which resumes to the child's real status.
  trap 'on_signal TERM' TERM
  trap 'on_signal INT' INT
  if [ -n "$timeout_spec" ]; then
    trap 'on_timeout' USR1
  else
    trap '' USR1
  fi

  # Its own session, so $! is the group the signals go to: setsid(1) does not
  # fork, as a non-interactive shell's background child is never a group
  # leader. INT, QUIT and USR1 are reset first, because bash starts it with
  # untrapped ones ignored and an ignored signal stays ignored across exec.
  setsid_bin=$(command -v setsid)
  (
    trap - INT QUIT USR1
    PATH=$byre_saved_path
    exec "$setsid_bin" -- "$cmd"
  ) </dev/null >&7 2>&8 3>&- 4>&- 7>&- 8>&- 9>&- &
  child=$!
  exec 7>&- 8>&-

  # Not timeout(1): a command's own exit 124 or 137 must not read as a timeout.
  if [ -n "$timeout_spec" ]; then
    # A failed re-read keeps the earlier, sooner reading.
    uptime_cs
    deadline_cs=$((now_cs + timeout_cs))
    (
      trap 'kill $! 2>/dev/null; exit 0' TERM
      sleep "$timeout_spec" &
      wait $! && kill -USR1 $$
    ) 3>&- 4>&- 5>&- 6>&- 9>&- &
    timer=$!
  fi

  # Resume an interrupted wait (bash keeps a reaped child's status).
  while :; do
    interrupted=0
    wait "$child"
    rc=$?
    [ "$interrupted" = 1 ] || break
  done
  # Ignored, not reset: a timer firing before its stop must not kill PID 1.
  trap - TERM INT
  trap '' USR1
  stop_helper "$timer"
  stop_helper "$killer"
  # The run is over: whatever the headless command left in its group has no
  # one to report to, and would hold the relay pipes open.
  kill -KILL -- "-$child" 2>/dev/null

  # Let the tees drain to EOF, bounded: a process that left the group with a
  # relay pipe still open must not hold PID 1 forever. KILL, because the tees
  # ignore TERM.
  for ((i = 0; i < 100; i++)); do
    kill -0 "$tee_out" 2>/dev/null || kill -0 "$tee_err" 2>/dev/null || break
    sleep 0.05
  done
  kill -KILL "$tee_out" "$tee_err" 2>/dev/null
  wait "$tee_out" "$tee_err" 2>/dev/null

  # status: key=value lines the headless command appended to fd 6. Only these
  # two keys are read, from a bounded read of the held descriptor.
  status_text=$(head -c 4096 /dev/fd/6 2>/dev/null)
  while IFS= read -r line; do
    case $line in
    backend_exit=*)
      v=${line#backend_exit=}
      if [[ $v =~ ^[0-9]{1,3}$ ]]; then backend_exit=$((10#$v)); fi
      ;;
    agent_version=*) agent_version=${line#agent_version=} ;;
    esac
  done <<<"$status_text"

  # --- 5. map ----------------------------------------------------------------
  if [ "$timed_out" = 1 ]; then
    code=12 status=timeout message="the run exceeded the timeout of $timeout_spec"
  elif [ "$rc" = 0 ]; then
    if [ -s /dev/fd/5 ]; then
      code=0 status=success message="the agent finished with an answer"
    else
      code=14 status=script_failed script_exit=0 message="the headless command exited 0 without a result"
    fi
  elif [ "$rc" = 10 ]; then
    code=10 status=agent_error message="the agent reported failure"
  elif [ "$rc" = 11 ]; then
    code=11 status=agent_exit
    if [ -n "$backend_exit" ]; then
      message="the agent process failed (backend exit $backend_exit)"
    else
      message="the agent process failed (no backend_exit recorded)"
    fi
  elif [ "$rc" = 13 ]; then
    code=13 status=no_credentials message="the agent's credential preflight failed"
  elif [ "$rc" = 15 ]; then
    code=15 status=backend_unavailable message="the agent backend is not present"
  else
    code=14 status=script_failed script_exit=$rc message="the headless command exited $rc, outside the contract's exit set"
  fi
  if [ "$code" != 11 ]; then backend_exit=""; fi
fi

finished_at=$(date -u +%FT%TZ)
duration=$SECONDS

# Final result.json by temp + rename: rename(2) replaces a planted symlink or
# FIFO rather than following or opening it. A planted DIRECTORY would swallow
# the mv (POSIX mv moves INTO it and reports success), so it is probed with
# -d first, and -f AFTER the move catches one landing in between. Best-effort:
# the verdict a script may trust is the exit code, never this file.
published=""
if tmp=$(mktemp "$dir/.result.json.XXXXXXXX" 2>/dev/null); then
  # mktemp creates 0600; the other files take the umask, and a human reads
  # this one beside them.
  if { result_json >|"$tmp"; } 2>/dev/null &&
    chmod 644 "$tmp" 2>/dev/null &&
    [ ! -d "$dir/result.json" ] &&
    mv -f "$tmp" "$dir/result.json" 2>/dev/null &&
    [ -f "$dir/result.json" ]; then
    published=1
  else
    rm -f "$tmp" 2>/dev/null
  fi
fi
if [ -z "$published" ]; then
  say "could not publish the final result.json (the temp write or the rename failed, or a non-file holds the name); the exit code is the verdict"
fi

# --- 6. the one stdout write --------------------------------------------------

# The answer, from the held descriptor, whatever the exit code: a reported
# failure still has a message worth reading, and an empty fd prints nothing.
cat /dev/fd/5 >&9 2>/dev/null

# A failed quiet run prints stderr.log's tail, bounded by bytes before lines so
# one huge line cannot flood stderr; exits 0 and 10 already carry the answer.
if [ -n "${BYRE_RUN_QUIET:-}" ] && [ "$code" != 0 ] && [ "$code" != 10 ]; then
  tail -c 8192 /dev/fd/4 2>/dev/null | tail -n 40 | while IFS= read -r line || [ -n "$line" ]; do
    say "stderr| $line"
  done
fi

say "run ${BYRE_RUN_ID:-?}: $status, exit $code ($message)"
exit "$code"
