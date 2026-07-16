#!/bin/bash
# byre grok-auth-broker (ADR 0036) — the machine-wide Grok credential broker.
# grok invokes it via GROK_AUTH_PROVIDER_COMMAND (its documented external-auth
# seam, user guide 02-authentication.md) whenever it needs a credential; stdout
# must be the token (we always emit the JSON form, with expires_in, so grok
# tracks real expiry instead of the 30-day bare-token fallback), stderr is
# login-flow UX, exit 0 = success. The shared OAuth pair lives in the
# machine-scoped identity volume as a grok-NATIVE auth.json (seeded by the
# firstrun hook writing through GROK_AUTH_PATH), and this broker is its only
# refresher: one flock serializes refreshes machine-wide, so exactly one
# process ever spends the single-use refresh token. Boxes' own $GROK_HOME
# auth.json holds only broker-issued access tokens (no refresh token) — with
# the provider command set, grok's OIDC refresher is never constructed
# (build_refresher branches on the command), so no box can rotate the chain
# behind the broker's back.
#
# Source-verified constraints this script is shaped by (grok-build tree,
# facts recorded in docs/AGENT-CREDENTIAL-MECHANICS.md §6):
# - Refresh-path invocations (GROK_AUTH_EXPIRED=1) are KILLED at 5s and their
#   stderr is swallowed. Everything on that path is budgeted to finish well
#   under 5s — a kill mid-refresh-POST could strand a spent refresh token —
#   and failures are also written to broker.log next to the store.
# - Refresh-token reuse revokes the whole token family (invalid_grant), so
#   the flock is held across the refresh POST and the store write.
# - The refresh grant is standard OIDC: POST token_endpoint (from issuer
#   discovery, cached next to the store) with grant_type=refresh_token +
#   client_id (+ principal_type/principal_id when the pair carries them).
# - invalid_grant/invalid_client is PERMANENT: the store is moved aside
#   (self-healing v1's dead credential too) and the user is told to re-seed.
#   A transient failure degrades to emitting the cached access token while
#   it plausibly lives, so one flaky refresh never breaks a running session.
set -u
umask 077

BASE="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/grok"
STORE="$BASE/auth.json"
LOCK="$BASE/broker.lock"
LOG="$BASE/broker.log"
EPCACHE="$BASE/token_endpoint"

# Refresh when less than MARGIN seconds remain: grok treats a token as expired
# 300s early (GROK_AUTH_EARLY_INVALIDATION_SECS default) and its proactive
# loop adds up to 60s jitter, so anything above 360 keeps us ahead of every
# grok-initiated call. Never emit a token with less than MIN_EMIT left.
MARGIN=420
MIN_EMIT=30

# GROK_AUTH_EXPIRED=1 accompanies every 5s-budget refresh invocation (grok
# sets it whenever it considers the stored token expired — occasionally that
# includes the patient login path, where the tight budget is merely
# conservative); scale every wait to the least-patient caller that can carry
# the flag. Unflagged calls are initial/login ones (60-300s budgets).
if [ "${GROK_AUTH_EXPIRED:-}" = "1" ]; then
  LOCK_WAIT=1.5; DISC_TIME=0; POST_TIME=2.5
else
  LOCK_WAIT=20; DISC_TIME=5; POST_TIME=10
fi

note() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$LOG" 2>/dev/null || true; }
fail() { note "FAIL: $*"; echo "grok-auth-broker: $*" >&2; exit 1; }

# --- store readers -----------------------------------------------------------
# The store is grok's own auth.json shape: a scope-keyed map whose OIDC entry
# carries key/refresh_token/expires_at/oidc_issuer/oidc_client_id (+ optional
# principal_type/principal_id). The refresh-token-bearing entry is the chain.
entry_key() { jq -r 'to_entries | map(select((.value.refresh_token // "") != "")) | first | .key // empty' "$STORE" 2>/dev/null; }
field() { jq -r --arg k "$1" ".[\$k].$2 // empty" "$STORE" 2>/dev/null; }

remaining() { # seconds of access-token life left for entry $1 (0 if unknown)
  local exp epoch
  exp=$(field "$1" expires_at | sed 's/\.[0-9]*Z$/Z/; s/\.[0-9]*+/+/')
  [ -n "$exp" ] || { echo 0; return; }
  epoch=$(date -u -d "$exp" +%s 2>/dev/null) || { echo 0; return; }
  echo $((epoch - $(date -u +%s)))
}

emit() { # print the provider JSON for entry $1 with $2 seconds remaining
  jq -cn --arg t "$(field "$1" key)" --arg i "$(field "$1" oidc_issuer)" --argjson e "$2" \
    '{access_token:$t, expires_in:$e} + (if $i != "" then {issuer:$i} else {} end)'
}

reseed_help="the shared Grok credential needs a fresh login: relaunch the box (the first-run hook re-seeds), or run 'grok login --device-auth' in any box and relaunch to adopt it."

mkdir -p "$BASE" 2>/dev/null || fail "cannot create identity dir $BASE (is the grok-identity volume mounted?)"

# --- serialize ---------------------------------------------------------------
# One flock, held from here through any refresh POST and store write: this is
# the cross-box mutex grok's own PID-based lock cannot be (PID liveness is
# meaningless across container namespaces — ADR 0023). Losers block the few
# hundred ms a refresh takes, wake, and find a fresh pair below.
# (A failed redirection exits a non-interactive shell by itself; bash prints
# the reason to stderr.)
exec 9>>"$LOCK"
if ! flock -w "$LOCK_WAIT" 9; then
  # Couldn't serialize in budget (a slow holder). Degrade: the cached access
  # token keeps this session alive; grok re-asks as its expiry nears.
  k=$(entry_key)
  [ -n "$k" ] || fail "$reseed_help"
  r=$(remaining "$k")
  [ "$r" -gt "$MIN_EMIT" ] || fail "lock busy and the cached token is dead — $reseed_help"
  note "lock busy; emitted cached token (${r}s left)"
  emit "$k" "$r"
  exit 0
fi

[ -s "$STORE" ] || fail "no shared Grok credential yet — $reseed_help"
KEY=$(entry_key)
[ -n "$KEY" ] || fail "shared store has no refresh-token entry — $reseed_help"

REMAIN=$(remaining "$KEY")
if [ "$REMAIN" -gt "$MARGIN" ]; then
  emit "$KEY" "$REMAIN"
  exit 0
fi

# --- refresh (under the lock) ------------------------------------------------
ISSUER=$(field "$KEY" oidc_issuer)
CLIENT=$(field "$KEY" oidc_client_id)
[ -n "$ISSUER" ] && [ -n "$CLIENT" ] || fail "store entry lacks oidc_issuer/oidc_client_id — re-seed: $reseed_help"

ENDPOINT=$(cat "$EPCACHE" 2>/dev/null || true)
if [ -z "$ENDPOINT" ]; then
  # The endpoint cache is seeded at firstrun; rebuilding it here only fits the
  # patient (non-5s) budget. On the refresh path, degrade like a lock miss.
  if [ "$DISC_TIME" -gt 0 ]; then
    ENDPOINT=$(curl -sf --max-time "$DISC_TIME" "$ISSUER/.well-known/openid-configuration" | jq -r '.token_endpoint // empty') || true
    if [ -n "$ENDPOINT" ]; then
      printf '%s' "$ENDPOINT" >"$EPCACHE.tmp" && mv "$EPCACHE.tmp" "$EPCACHE"
    fi
  fi
  if [ -z "$ENDPOINT" ]; then
    [ "$REMAIN" -gt "$MIN_EMIT" ] && { note "no token endpoint cached; emitted cached token (${REMAIN}s left)"; emit "$KEY" "$REMAIN"; exit 0; }
    fail "cannot discover the token endpoint and the cached token is dead — check egress to auth.x.ai, then: $reseed_help"
  fi
fi

ARGS=(-s --max-time "$POST_TIME" --connect-timeout 1 -w '\n%{http_code}'
  -d grant_type=refresh_token
  --data-urlencode "refresh_token=$(field "$KEY" refresh_token)"
  --data-urlencode "client_id=$CLIENT")
PT=$(field "$KEY" principal_type); PID_=$(field "$KEY" principal_id)
[ -n "$PT" ] && ARGS+=(--data-urlencode "principal_type=$PT")
[ -n "$PID_" ] && ARGS+=(--data-urlencode "principal_id=$PID_")

RESP=$(curl "${ARGS[@]}" "$ENDPOINT" 2>/dev/null) || RESP=""
HTTP=${RESP##*$'\n'}
BODY=${RESP%$'\n'*}

if [ "$HTTP" = "200" ]; then
  AT=$(jq -r '.access_token // empty' <<<"$BODY")
  RT=$(jq -r '.refresh_token // empty' <<<"$BODY")
  EXPIN=$(jq -r '.expires_in // empty' <<<"$BODY")
  [ -n "$AT" ] || fail "token endpoint returned 200 without an access_token"
  case "$EXPIN" in ''|*[!0-9]*) note "no expires_in in refresh response; assuming 21600"; EXPIN=21600;; esac
  NOW=$(date -u +%s)
  # Persist BEFORE emitting: a rotated refresh token that never reaches the
  # store is a lost chain. Same temp+rename grok itself uses.
  jq --arg k "$KEY" --arg at "$AT" --arg rt "$RT" \
     --arg exp "$(date -u -d "@$((NOW + EXPIN))" +%FT%TZ)" --arg ct "$(date -u -d "@$NOW" +%FT%TZ)" \
     '.[$k].key=$at | .[$k].expires_at=$exp | .[$k].create_time=$ct
      | (if $rt != "" then .[$k].refresh_token=$rt else . end)' \
     "$STORE" >"$STORE.tmp" || fail "store rewrite failed"
  mv "$STORE.tmp" "$STORE" || fail "store rename failed"
  note "refreshed (rotated=$([ -n "$RT" ] && echo yes || echo no), expires_in=${EXPIN}s)"
  emit "$KEY" "$EXPIN"
  exit 0
fi

ERR=$(jq -r '.error // empty' <<<"$BODY" 2>/dev/null)
if [ "$ERR" = "invalid_grant" ] || [ "$ERR" = "invalid_client" ]; then
  # Permanent (source-verified: the only two terminal codes). The chain is
  # dead — move the corpse aside so the next launch's firstrun hook re-seeds
  # instead of retrying it forever. This is also the self-heal for the v1
  # identity volume's dead credential (ADR 0023). A failed move must say so:
  # a corpse left in place looks like a valid seed to the firstrun hook, and
  # the claim "moved aside" would send the user in the wrong direction.
  if mv "$STORE" "$STORE.dead-$(date -u +%s)" 2>/dev/null; then
    fail "the shared credential was rejected ($ERR) and has been moved aside — $reseed_help"
  fi
  fail "the shared credential was rejected ($ERR) and could NOT be moved aside — remove $STORE by hand, then re-seed: $reseed_help"
fi

# Transient (network down, 5xx, timeout): keep the session alive on the
# cached token if it plausibly lives; grok retries the refresh in ~300s.
if [ "$REMAIN" -gt "$MIN_EMIT" ]; then
  note "transient refresh failure (http=${HTTP:-none} err=${ERR:-none}); emitted cached token (${REMAIN}s left)"
  emit "$KEY" "$REMAIN"
  exit 0
fi
fail "refresh failed (http=${HTTP:-none} err=${ERR:-none}) and the cached token is dead — check egress to auth.x.ai; if this persists: $reseed_help"
