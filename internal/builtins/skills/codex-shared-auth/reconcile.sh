#!/bin/bash
# Reconcile Codex's per-box auth path with the machine-wide credential.
#
# Codex 0.146 clears (and revokes) existing auth before OAuth login. Clearing a
# symlink removes only the link; a successful login then writes a fresh regular
# local auth.json. That local file is often the only live credential, so a
# shared-always-wins policy restores revoked bytes and destroys the good login.
#
# This helper is intentionally non-interactive. Callers run it at startup and
# immediately after a Byre-managed login. It never logs credential contents.
set -u

IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/codex"
SHARED="$IDENTITY_DIR/auth.json"
LOCK="$IDENTITY_DIR/auth.lock"
DIAG_LOG="$IDENTITY_DIR/byre-auth-diagnostic.log"
export CODEX_HOME="${CODEX_HOME:-/home/dev/.codex-home}"
cred="$CODEX_HOME/auth.json"
reason="${1:-unspecified}"

diag_event() {
  [ -n "${CODEX_AUTH_DIAGNOSTIC_BYRE:-}" ] || return 0
  printf '%s component=shared-auth box=%q project=%q pid=%s reason=%s event=%s codex_home=%q identity_dir=%q\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || printf unknown)" \
    "$(hostname 2>/dev/null || printf unknown)" "${COMPOSE_PROJECT_NAME:-unknown}" \
    "$$" "$reason" "$1" "$CODEX_HOME" "$IDENTITY_DIR" >>"$DIAG_LOG" 2>/dev/null || true
}

diag_path() {
  [ -n "${CODEX_AUTH_DIAGNOSTIC_BYRE:-}" ] || return 0
  local label="$1" path="$2" kind target="" meta=""
  if [ -L "$path" ]; then
    kind=symlink
    target="$(readlink "$path" 2>/dev/null || true)"
  elif [ -e "$path" ]; then
    kind=non_symlink
  else
    kind=absent
  fi
  if [ "$kind" != absent ]; then
    meta="$(stat -c 'dev=%d ino=%i mode=%a uid=%u gid=%g size=%s mtime=%Y' "$path" 2>/dev/null || printf stat_failed)"
  fi
  printf '%s component=shared-auth box=%q project=%q pid=%s reason=%s state=%s path=%q kind=%s target=%q %s\n' \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || printf unknown)" \
    "$(hostname 2>/dev/null || printf unknown)" "${COMPOSE_PROJECT_NAME:-unknown}" \
    "$$" "$reason" "$label" "$path" "$kind" "$target" "$meta" >>"$DIAG_LOG" 2>/dev/null || true
}

auth_valid() {
  [ -f "$1" ] && [ ! -L "$1" ] && jq -e '
    def nonblank: type == "string" and test("\\S");
    type == "object" and (
      ((.tokens | type == "object") and
        (.tokens.access_token | nonblank) and
        (.tokens.refresh_token | nonblank)) or
      (.OPENAI_API_KEY | nonblank) or
      ((.agent_identity | nonblank) or
        ((.agent_identity | type == "object") and
          (.agent_identity.agent_runtime_id | nonblank) and
          (.agent_identity.agent_private_key | nonblank))) or
      (.personal_access_token | nonblank) or
      ((.bedrock_api_key | type == "object") and
        (.bedrock_api_key.api_key | nonblank) and
        (.bedrock_api_key.region | nonblank))
    )
  ' "$1" >/dev/null 2>&1
}

refresh_epoch() {
  local stamp stamp_bsd
  stamp="$(jq -r '.last_refresh // empty' "$1" 2>/dev/null)" || return 1
  [ -n "$stamp" ] || return 1
  case "$stamp" in
    *.*Z) stamp_bsd="${stamp%%.*}Z" ;;
    *) stamp_bsd="$stamp" ;;
  esac
  date -d "$stamp" +%s 2>/dev/null ||
    date -j -f '%Y-%m-%dT%H:%M:%SZ' "$stamp_bsd" +%s 2>/dev/null
}

mtime_epoch() {
  stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null
}

# Prints local or shared. Both inputs have already passed auth_valid.
choose_winner() {
  local local_refresh="" shared_refresh="" local_mtime="" shared_mtime=""
  local_refresh="$(refresh_epoch "$cred" 2>/dev/null || true)"
  shared_refresh="$(refresh_epoch "$SHARED" 2>/dev/null || true)"
  if [ -n "$local_refresh" ] && [ -n "$shared_refresh" ] &&
     [ "$local_refresh" -ne "$shared_refresh" ]; then
    if [ "$local_refresh" -gt "$shared_refresh" ]; then
      printf local
    else
      printf shared
    fi
    return
  fi

  # API-key auth has no last_refresh, and timestamps can tie at one-second
  # precision. File mtime is the deterministic fallback; a tie favors local
  # because clear-before-login produces exactly local-regular + shared-old.
  # Mixed presence (one side has last_refresh, the other does not) lands here
  # deliberately: preferring the stamped side would silently revert a user's
  # deliberate API-key login. The accepted cost is that a hand-restored stale
  # backup with a fresh mtime can win; auth.json.prev caps that blast radius.
  local_mtime="$(mtime_epoch "$cred" 2>/dev/null || printf 0)"
  shared_mtime="$(mtime_epoch "$SHARED" 2>/dev/null || printf 0)"
  if [ "$local_mtime" -ge "$shared_mtime" ]; then
    printf local
  else
    printf shared
  fi
}

publish_local() {
  local tmp
  tmp="$(mktemp "$IDENTITY_DIR/.auth.json.tmp.XXXXXX" 2>/dev/null)" || {
    echo "byre codex-shared-auth: cannot create a temporary credential in $IDENTITY_DIR; keeping the local login." >&2
    diag_event publish_temp_failed
    return 1
  }
  # Cap the blast radius of a wrong winner pick: keep the displaced shared
  # bytes as auth.json.prev so recovery is a file restore, not a re-login on
  # every box. Best-effort by design; retention failure never blocks publish.
  if [ -f "$SHARED" ]; then
    cp -- "$SHARED" "$IDENTITY_DIR/auth.json.prev" 2>/dev/null &&
      chmod 600 "$IDENTITY_DIR/auth.json.prev" 2>/dev/null || true
  fi
  if ! cp -- "$cred" "$tmp" 2>/dev/null || ! chmod 600 "$tmp" 2>/dev/null ||
     ! mv -f -- "$tmp" "$SHARED" 2>/dev/null; then
    rm -f -- "$tmp" 2>/dev/null || true
    echo "byre codex-shared-auth: cannot publish the local Codex login machine-wide; keeping the local login." >&2
    diag_event publish_failed
    return 1
  fi
  diag_event local_published
  return 0
}

assert_link() {
  if [ -L "$cred" ] && [ "$(readlink "$cred" 2>/dev/null)" = "$SHARED" ]; then
    return 0
  fi
  rm -f -- "$cred" 2>/dev/null || {
    echo "byre codex-shared-auth: cannot replace $cred with the machine-wide credential link." >&2
    diag_event local_remove_failed
    return 1
  }
  if ! ln -s "$SHARED" "$cred" 2>/dev/null; then
    echo "byre codex-shared-auth: cannot link $cred to the machine-wide credential." >&2
    diag_event link_failed
    return 1
  fi
  diag_event link_asserted
}

if ! mkdir -p "$IDENTITY_DIR" "$CODEX_HOME" 2>/dev/null; then
  echo "byre codex-shared-auth: cannot create $IDENTITY_DIR or $CODEX_HOME — shared auth not asserted this launch." >&2
  exit 1
fi
if [ -n "${CODEX_AUTH_DIAGNOSTIC_BYRE:-}" ]; then
  : >>"$DIAG_LOG" 2>/dev/null || true
  chmod 600 "$DIAG_LOG" 2>/dev/null || true
fi

diag_event reconcile_start
diag_path local_before "$cred"
diag_path shared_before "$SHARED"

lock_open=false
if { exec 9>"$LOCK"; } 2>/dev/null; then
  lock_open=true
  chmod 600 "$LOCK" 2>/dev/null || true
fi
if [ "$lock_open" = true ] && command -v flock >/dev/null 2>&1; then
  if flock -w 3 -x 9 2>/dev/null; then
    diag_event lock_acquired
  else
    # A timeout is positive evidence of a live contender mid-write, and racing
    # it on the shared file is the one hazard the lock exists to prevent. Skip
    # the whole reconciliation: local auth stays untouched and usable, the
    # caller's warning fires, and the next launch retries. flock releases when
    # its holder dies (kernel, on FD close), so a persistent hold means a live
    # process is deliberately sitting on it.
    echo "byre codex-shared-auth: waited 3s for $LOCK; skipped credential reconciliation this launch." >&2
    echo "byre codex-shared-auth: another launch may be mid-reconcile, so a plain relaunch retries. If it persists, stop the box holding the lock, or remove it from inside any box sharing the login: rm $LOCK" >&2
    diag_event lock_timeout
    exit 1
  fi
else
  # Deliberate asymmetry with the timeout arm above, not drift: an unopenable
  # lock file or missing flock is an environmental defect with no evidence of
  # a live contender (util-linux is a declared dep and the dir was just
  # mkdir -p'd), while skipping here would leave a fresh login permanently
  # unpublished on a hand-broken box. Proceed unserialized, loudly.
  echo "byre codex-shared-auth: WARNING — cannot lock $LOCK; reconciling without cross-box serialization." >&2
  diag_event lock_unavailable
fi

local_regular=false
[ -f "$cred" ] && [ ! -L "$cred" ] && local_regular=true
shared_regular=false
[ -f "$SHARED" ] && [ ! -L "$SHARED" ] && shared_regular=true
result=0

if [ "$local_regular" = true ]; then
  local_valid=false
  auth_valid "$cred" && local_valid=true
  shared_valid=false
  [ "$shared_regular" = true ] && auth_valid "$SHARED" && shared_valid=true

  if [ "$local_valid" = true ] && [ "$shared_valid" = false ]; then
    echo "byre codex-shared-auth: publishing this box's Codex login as the machine-wide credential" >&2
    diag_event winner_local_shared_missing_or_invalid
    if publish_local; then
      assert_link || result=1
    else
      result=1
    fi
  elif [ "$local_valid" = false ] && [ "$shared_valid" = true ]; then
    echo "byre codex-shared-auth: local Codex auth is malformed; retaining the valid machine-wide credential" >&2
    diag_event winner_shared_local_invalid
    assert_link || result=1
  elif [ "$local_valid" = true ] && [ "$shared_valid" = true ]; then
    winner="$(choose_winner)"
    if [ "$winner" = local ]; then
      echo "byre codex-shared-auth: promoting the newer local Codex login to the machine-wide credential" >&2
      diag_event winner_local_newer
      if publish_local; then
        assert_link || result=1
      else
        result=1
      fi
    else
      echo "byre codex-shared-auth: retaining the newer machine-wide Codex login over this box's stale local copy" >&2
      diag_event winner_shared_newer
      assert_link || result=1
    fi
  else
    echo "byre codex-shared-auth: local Codex auth is malformed and no valid shared credential exists; leaving it untouched for recovery." >&2
    diag_event no_valid_credential
    result=1
  fi
else
  # Missing local auth proves only that the path disappeared. It does not prove
  # that the shared token was revoked, so never delete shared on this signal.
  diag_event no_local_regular
  assert_link || result=1
fi

[ -f "$SHARED" ] && chmod 600 "$SHARED" 2>/dev/null || true
diag_path local_final "$cred"
diag_path shared_final "$SHARED"
diag_event reconcile_end
exit "$result"
