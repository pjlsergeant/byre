#!/bin/bash
# codex first-run auth hook — runs as the dev user, before the agent launches, on
# a fresh box. If the codex credential is missing, trigger the device-auth login so
# byre-codereview (and any codex use) works out of the box. Mirrors how moarcode
# did it in its entrypoint.
#
# --device-auth prints a URL + code to authorize in your browser — no in-box
# browser needed. The credential lands in the .codex state volume, so this runs
# once per project (and survives rebuilds). Best-effort: skip with Ctrl-C (or if
# it fails/times out) and the box still launches — re-auth later with
# `codex login --device-auth` (NOT plain `codex login`, which needs a browser the
# box doesn't have). Codex creds are a rotating token and are NOT seedable, so
# device-auth is the only way back in.
command -v codex >/dev/null 2>&1 || exit 0
export CODEX_HOME="${CODEX_HOME:-/home/dev/.codex-home}"
# Already authenticated? Ask codex itself (`codex login status`) rather than
# testing the file. "auth.json is non-empty" can't tell a usable credential from
# a corrupt/partial one (an interrupted prior login), so file-presence wrongly
# skips a needed re-auth. For ChatGPT credentials that Codex would proactively
# refresh, live_probe below asks Codex's own app-server to do that refresh before
# the main agent starts.
cred="$CODEX_HOME/auth.json"
diag_dir="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/codex"
diag_log="$diag_dir/byre-auth-diagnostic.log"
# Seconds app-server gets to finish an in-place auth write before TERM, and
# again before KILL. Default 1; tests shorten it.
reap_grace="${BYRE_CODEX_REAP_GRACE:-1}"
snapshot=""

# The filesystem primitives this hook judges a planted credential with (the
# identity route, the newline-safe link read, the absent-or-regular target
# test) live in one library five skills ship to this path. BYRE_SHARED_AUTH_LIB
# is a test seam, not a user knob: byre refuses BYRE_* names in a project
# [env] (internal/config/config.go). Without the library this hook cannot
# judge a symlinked credential at all, so it says so and offers no login
# rather than running one through whatever is there.
BYRE_SA_LIB="${BYRE_SHARED_AUTH_LIB:-/usr/local/lib/byre-shared-auth-lib.sh}"
if [ ! -r "$BYRE_SA_LIB" ]; then
  echo "byre: cannot read the shared-auth library ($BYRE_SA_LIB); not offering the codex login" >&2
  exit 0
fi
. "$BYRE_SA_LIB"

cleanup_snapshot() {
  [ -z "${snapshot:-}" ] || rm -f -- "$snapshot"
}
trap cleanup_snapshot EXIT

# Gates every write this hook makes BY the identity dir's spelling -- the
# diagnostic log's mkdir/append and the shared-auth lock -- with reconcile.sh's
# rule: neither the dir nor any ancestor a symlink. BYRE_IDENTITY_BASE is a
# test seam, and a seam value must itself be a physical path.
identity_dir_safe() {
  byre_sa_identity_route_safe "$diag_dir"
}

# Complements codex-shared-auth's diagnostics so the shared log shows whether
# this later hook accepted or removed the asserted link. Deliberately records
# no credential contents, hashes, token metadata, or environment values.
diag_event() {
  [ -n "${CODEX_AUTH_DIAGNOSTIC_BYRE:-}" ] || return 0
  identity_dir_safe || return 0
  mkdir -p -- "$diag_dir" 2>/dev/null || return 0
  : >>"$diag_log" 2>/dev/null || return 0
  chmod -- 600 "$diag_log" 2>/dev/null || true
  printf '%s component=codex-login box=%s project=%s pid=%s event=%s\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || printf unknown)" \
    "$(hostname 2>/dev/null || printf unknown)" "${COMPOSE_PROJECT_NAME:-unknown}" \
    "$$" "$1" >>"$diag_log" 2>/dev/null || true
}

diag_path() {
  [ -n "${CODEX_AUTH_DIAGNOSTIC_BYRE:-}" ] || return 0
  if [ -L "$cred" ]; then
    kind=symlink
  elif [ -e "$cred" ]; then
    kind=non_symlink
  else
    kind=absent
  fi
  meta=""
  if [ "$kind" != absent ]; then
    meta="$(stat -c 'dev=%d ino=%i mode=%a uid=%u gid=%g size=%s mtime=%Y' "$cred" 2>/dev/null || printf stat_failed)"
  fi
  diag_event "credential_state_${1}_${kind}_${meta}"
}

# Narrow Codex 0.146's proactive-refresh policy to avoid a network request on
# every launch: a decodable JWT wins and probes only within five minutes of
# expiry; non-JWT access tokens fall back to last_refresh older than eight days.
# Missing timestamps are left to Codex. API-key/PAT credentials never probe.
needs_live_probe() {
  [ "$(jq -r '.auth_mode // empty' "$cred" 2>/dev/null)" = chatgpt ] || return 1
  token="$(jq -r '.tokens.access_token // empty' "$cred" 2>/dev/null)" || return 1
  [ -n "$token" ] || return 1

  payload="${token#*.}"
  if [ "$payload" != "$token" ] && [ "${payload#*.}" != "$payload" ]; then
    payload="${payload%%.*}"
    payload="${payload//-/+}"
    payload="${payload//_/\/}"
    case $((${#payload} % 4)) in
      2) payload="${payload}==" ;;
      3) payload="${payload}=" ;;
    esac
    # jq's decoder is portable across GNU/BSD hosts (`base64 -d` is GNU;
    # macOS spells it `-D`). The skill already requires jq.
    exp="$(printf '%s' "$payload" | jq -Rr '@base64d | fromjson | .exp | select(type == "number") // empty' 2>/dev/null)"
    if [[ "$exp" =~ ^[0-9]+$ ]]; then
      [ "$exp" -le "$(($(date +%s) + 300))" ]
      return
    fi
  fi

  refreshed="$(jq -r '.last_refresh // empty' "$cred" 2>/dev/null)" || return 1
  [ -n "$refreshed" ] || return 1
  # Codex writes nanosecond RFC3339 timestamps; BSD date's strptime rejects
  # fractional seconds, so normalize the UTC form before its fallback parser.
  case "$refreshed" in
    *.*Z) refreshed_bsd="${refreshed%%.*}Z" ;;
    *) refreshed_bsd="$refreshed" ;;
  esac
  refreshed_epoch="$(date -d "$refreshed" +%s 2>/dev/null ||
    date -j -f '%Y-%m-%dT%H:%M:%SZ' "$refreshed_bsd" +%s 2>/dev/null)" || return 1
  [ "$refreshed_epoch" -le "$(($(date +%s) - 8 * 24 * 60 * 60))" ]
}

# Return 0 for a usable account, 10 for the one response shape that says OpenAI
# auth is required but unavailable, and 20 for every ambiguous outcome. Raw RPC
# output may contain account email/plan data, so it is never printed or logged.
live_probe() {
  # Bash 3.2 (the macOS CI shell) has no coproc. Two FIFOs plus fixed FDs keep
  # the hook portable; the parent's read/write opens prevent FIFO-open deadlock,
  # while the child closes those extra copies before exec so closing fd 7 still
  # delivers EOF. exec makes rpc_pid the setsid/app-server process itself.
  rpc_dir="$(mktemp -d "${TMPDIR:-/tmp}/byre-codex-rpc.XXXXXX")" || return 20
  rpc_in="$rpc_dir/in"
  rpc_out="$rpc_dir/out"
  if ! mkfifo -- "$rpc_in" "$rpc_out" || ! exec 7<>"$rpc_in" || ! exec 8<>"$rpc_out"; then
    { exec 7>&-; } 2>/dev/null || true
    { exec 8>&-; } 2>/dev/null || true
    rm -rf -- "$rpc_dir"
    return 20
  fi
  ( exec 7>&- 8>&-; exec setsid codex app-server 2>/dev/null ) <"$rpc_in" >"$rpc_out" &
  rpc_pid=$!
  # A startup crash or older Codex closing stdin must classify as ambiguous,
  # not terminate the whole hook with SIGPIPE before EXIT cleanup can run.
  trap '' PIPE
  printf '%s\n' \
    '{"method":"initialize","id":0,"params":{"clientInfo":{"name":"byre-auth-probe","title":"Byre auth probe","version":"1"}}}' \
    '{"method":"initialized","params":{}}' \
    '{"method":"account/read","id":1,"params":{"refreshToken":true}}' >&7 || true
  trap - PIPE

  outcome=20
  deadline=$((SECONDS + 8))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if ! IFS= read -r -t 1 line <&8; then
      kill -0 "$rpc_pid" 2>/dev/null || break
      continue
    fi
    [ "$(printf '%s' "$line" | jq -r '.id // empty' 2>/dev/null)" = 1 ] || continue
    if printf '%s' "$line" | jq -e '.result.account != null' >/dev/null 2>&1; then
      outcome=0
    elif printf '%s' "$line" | jq -e '.result.account == null and .result.requiresOpenaiAuth == true' >/dev/null 2>&1; then
      outcome=10
    fi
    break
  done

  { exec 7>&-; } 2>/dev/null || true
  { exec 8>&-; } 2>/dev/null || true
  # EOF normally stops app-server. Give it time to finish any in-place auth
  # write before escalating, and target the dedicated session so plugin/git
  # children cannot outlive the credential lock.
  sleep "$reap_grace"
  kill -TERM -- "-$rpc_pid" 2>/dev/null || kill "$rpc_pid" 2>/dev/null || true
  sleep "$reap_grace"
  kill -KILL -- "-$rpc_pid" 2>/dev/null || kill -KILL "$rpc_pid" 2>/dev/null || true
  wait "$rpc_pid" 2>/dev/null || true
  rm -rf -- "$rpc_dir"
  return "$outcome"
}

# Device login begins with Codex's logout-with-revoke. In shared-auth mode,
# remove ONLY this box's symlink first so a false prompt or Ctrl-C cannot revoke
# or delete the machine-wide credential. A successful login writes a local
# regular file; the existing post-login reconciler publishes it.
detach_shared_link() {
  [ -n "$shared_auth" ] || return 0
  if [ -L "$cred" ]; then
    rm -f -- "$cred" || return 1
    diag_event shared_link_detached_before_login
  fi
}

restore_shared_link() {
  [ -n "$shared_auth" ] || return 0
  # Never overwrite a regular file left by a partially completed login.
  if [ ! -e "$cred" ] && [ ! -L "$cred" ]; then
    ln -s -- "$target" "$cred" 2>/dev/null || return 1
    diag_event shared_link_restored_after_login_skip
  fi
}

diag_event hook_start
diag_path initial
# A symlinked credential must never count — drop it so a clean re-login writes a
# fresh regular file a planted link can't redirect. ONE exception (ADR 0017):
# codex-shared-auth's own link into the identity volume is legitimate, and a
# DANGLING one is its expected first-login state. It is accepted here so status
# and the live probe can read it; detach_shared_link removes only that local link
# before device login, preventing Codex's implicit logout from revoking shared
# auth. The narrowing is accepted: the agent can already read the credential the
# link would redirect.
shared_auth=""
if [ -L "$cred" ]; then
  # Canonicalize the target's PARENT dir (the final auth.json may be absent --
  # dangling is the expected first-login state); a lexical prefix check would
  # accept planted ..-traversals and reject legitimate relative links.
  # Relative targets resolve from the link's own directory, and may start with
  # "-", so every path-taking tool here takes `--`. The library's read refuses
  # a target holding a newline, which leaves tdir empty and takes the removal
  # path below.
  target=""
  tdir=""
  if target=$(byre_sa_link_target "$cred"); then
    tdir="$(cd -- "$CODEX_HOME" 2>/dev/null && cd -- "$(dirname -- "$target")" 2>/dev/null && pwd -P)" || tdir=""
  fi
  tfile="/home/dev/.byre-identity/codex/auth.json"
  # EQUALITY against the FULL canonical target — codex's OWN identity dir AND
  # the auth.json basename (codex-shared-auth links exactly that file) — not a
  # /home/dev/.byre-identity/* wildcard: a broader match would trust a link
  # into a SIBLING agent's identity dir, through which a `codex login` would
  # overwrite that agent's machine-wide credential with codex's incompatible
  # store; and a dir-only match would trust a link to any OTHER name inside
  # codex's dir. AND the target object absent (dangling: first login) or a
  # regular non-symlink file: a link planted AT the identity dir's auth.json
  # would chain `codex login status` and a login's write onward to a file of
  # the planter's choosing (reconcile.sh refuses that shared path too, so the
  # link is dropped here and the login writes a local regular file). One
  # conjunction, so no half can be dropped alone. Mirrors the opencode-login
  # hook.
  if [ "$tdir" = "/home/dev/.byre-identity/codex" ] && [ "$(basename -- "$target")" = "auth.json" ] && ! byre_sa_shared_unsafe "$tfile"; then
    shared_auth=1
    diag_event shared_link_accepted
  else
    diag_event foreign_link_remove_begin
    # A failed removal (an unwritable CODEX_HOME) must not fall through: the
    # rejected link would still be there, and `codex login status` and a
    # login would read and write THROUGH it. Stop without offering the login.
    if ! rm -f -- "$cred"; then
      diag_event foreign_link_remove_failed
      echo "byre: could not remove the symlinked codex credential $cred; not offering the login -- remove it in byre shell" >&2
      exit 0
    fi
    diag_path after_foreign_link_remove
  fi
  # The accepted link is checked by its TARGET; the shared-auth path below
  # also works BY the identity dir's spelling (mkdir -p, the auth.lock <>
  # open), which a planted symlink on the dir or an ancestor would carry to
  # the far side. reconcile.sh refuses that dir outright; so does this hook:
  # shared auth is dropped for this launch, and so is the link -- keeping it
  # unlocked would let the device login's implicit logout revoke the
  # machine-wide credential through it -- so the hook goes on per-project.
  if [ -n "$shared_auth" ] && ! identity_dir_safe; then
    shared_auth=""
    echo "byre: the Codex identity dir ($diag_dir) is or resolves through a symlink; not using shared auth this launch -- continuing with a per-project login." >&2
    if ! rm -f -- "$cred"; then
      echo "byre: could not remove the symlinked codex credential $cred; not offering the login -- remove it in byre shell" >&2
      exit 0
    fi
  fi
fi
# Anything else that is not a regular file (a FIFO, socket, directory) is
# not a credential codex wrote. Stop BEFORE `codex login status`, jq or the
# live probe open it: opening a FIFO blocks, and with no tty guard on this
# path a headless launch would sit there until the timeout. Never delete an
# unknown object -- say so. Same guard as opencode-login.sh.
if [ -e "$cred" ] && [ ! -L "$cred" ] && [ ! -f "$cred" ]; then
  diag_event credential_not_regular
  echo "byre: codex credential path $cred is not a regular file; not reading it and not offering the login -- inspect it in byre shell" >&2
  exit 0
fi
needs_login=""
if codex login status >/dev/null 2>&1; then
  diag_event login_status_authenticated
  if ! needs_live_probe; then
    diag_event live_probe_not_due
    diag_path authenticated
    exit 0
  fi

  diag_event live_probe_due
  if [ -n "$shared_auth" ]; then
    lock="$diag_dir/auth.lock"
    mkdir -p -- "$diag_dir" 2>/dev/null || true
    # Same treatment as the reconciler's lock open (its comment has the full
    # story): the path is dev-writable in every sharing box, so replace
    # non-regular debris, then open <> — O_RDWR never blocks on a raced-in
    # FIFO on Linux and carries no O_TRUNC for a raced-in symlink's target.
    if [ -L "$lock" ] || { [ -e "$lock" ] && [ ! -f "$lock" ]; }; then
      rm -f -- "$lock" 2>/dev/null || true
      diag_event live_probe_lock_nonregular_replaced
    fi
    { exec 9<>"$lock"; } 2>/dev/null || true
    if ! flock -w 3 -x 9 2>/dev/null; then
      diag_event live_probe_lock_unavailable
      echo "byre: could not lock the shared Codex credential for a startup refresh; launching without changing it." >&2
      exit 0
    fi
    # A sibling launch may have refreshed while this one waited.
    if ! needs_live_probe; then
      diag_event live_probe_refreshed_while_waiting
      flock -u 9 2>/dev/null || true
      exit 0
    fi
    snapshot="$(mktemp "${TMPDIR:-/tmp}/byre-codex-auth.XXXXXX")" || snapshot=""
    if [ -z "$snapshot" ] || ! cp -L -- "$cred" "$snapshot" 2>/dev/null; then
      diag_event live_probe_snapshot_failed
      [ -n "$snapshot" ] && rm -f -- "$snapshot"
      snapshot=""
      flock -u 9 2>/dev/null || true
      echo "byre: could not safely inspect the shared Codex credential; launching without changing it." >&2
      exit 0
    fi
  else
    snapshot=""
  fi

  live_probe
  probe_result=$?
  if [ "$probe_result" -eq 0 ]; then
    if needs_live_probe; then
      # account/read can retain cached account data after a transient refresh
      # failure. This is usable enough to launch, but do not claim rotation was
      # persisted when the local freshness signal did not advance.
      diag_event live_probe_account_present_refresh_unconfirmed
      echo "byre: Codex account is present, but its startup refresh was not persisted; launching with the existing credential." >&2
    else
      diag_event live_probe_refreshed
    fi
  elif [ "$probe_result" -eq 10 ]; then
    # A running Codex may have won the server-side rotation but not written the
    # shared file yet. Give that write a moment before treating this credential
    # as dead.
    [ -n "$shared_auth" ] && sleep 1
    if [ -n "$snapshot" ] && ! cmp -s -- "$snapshot" "$cred"; then
      diag_event live_probe_sibling_changed_credential
    else
      diag_event live_probe_auth_unavailable
      needs_login=1
      if ! detach_shared_link; then
        diag_event shared_link_detach_failed
        needs_login=""
        echo "byre: Codex authentication needs repair, but the shared credential could not be detached safely; launching without changing it." >&2
      fi
    fi
  else
    diag_event live_probe_ambiguous
    echo "byre: could not verify the stale Codex credential; launching without changing it." >&2
  fi
  cleanup_snapshot
  snapshot=""
  if [ -n "$shared_auth" ]; then
    flock -u 9 2>/dev/null || true
    exec 9>&-
  fi
  [ -n "$needs_login" ] || exit 0
else
  needs_login=1
  diag_event login_status_unauthenticated
fi
diag_path before_device_login

if ! detach_shared_link; then
  diag_event shared_link_detach_failed
  echo "byre: Codex login is required, but the shared credential could not be detached safely; launching without changing it." >&2
  exit 0
fi

# Clean skip on Ctrl-C: handle SIGINT and exit 0 so we don't propagate a
# signal-death toward the launcher — the box proceeds to the agent regardless.
trap 'restore_shared_link || echo "byre: Codex login skipped, but the shared credential link could not be restored; it will retry next launch." >&2; echo; echo "byre: codex login skipped. To do it later, open another terminal and run '\''byre shell'\'', then '\''codex login --device-auth'\''."; exit 0' INT

echo ""
echo "=== byre: Codex login (for the agent and byre-codereview) ==="
if [ -n "$shared_auth" ]; then
  echo "Authorize below; stored machine-wide (shared-auth: all your byre projects). Ctrl-C to skip."
else
  echo "Authorize below; stored per-project, survives rebuilds. Ctrl-C to skip."
fi
echo ""
# Bound the wait so a stale/unused device code can't hold the box open for long
# (codex polls until you authorize); on timeout/failure we fall through to launch.
# --foreground keeps codex in the terminal's foreground process group so a Ctrl-C
# reaches it immediately (without it, timeout runs the child in its own group and
# the interrupt wouldn't land until the timeout elapsed).
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout --foreground 600"
if $TO codex login --device-auth; then
  diag_event device_login_succeeded
  reconcile="${BYRE_CODEX_AUTH_RECONCILE:-/usr/local/lib/byre-codex-auth-reconcile}"
  if [ -r "$reconcile" ]; then
    # Same hard bound as the firstrun call site: adversarial-filesystem
    # stalls inside the shared identity volume must not hang the launch.
    RTO=""
    command -v timeout >/dev/null 2>&1 && RTO="timeout 30"
    $RTO bash "$reconcile" post_device_login ||
      echo "byre: Codex login succeeded, but its machine-wide shared-auth reconciliation failed; it will retry next launch." >&2
  fi
else
  echo "byre: codex login didn't complete. To do it later, open another terminal and run 'byre shell', then 'codex login --device-auth'." >&2
  restore_shared_link ||
    echo "byre: the shared Codex credential link could not be restored; it will retry next launch." >&2
fi
diag_path after_device_login
diag_event hook_end
exit 0
