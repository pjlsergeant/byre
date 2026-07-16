#!/bin/sh
# grok-shared-auth firstrun hook (ADR 0036) — makes sure the machine-wide
# shared Grok credential store exists and is seeded. Runs before the grok
# skill's own login hook (00- prefix sorts first); that hook stands down
# entirely when GROK_AUTH_PROVIDER_COMMAND is set, so this hook owns all
# login UX for shared-auth boxes.
#
# The store is a grok-NATIVE auth.json inside the machine-scoped identity
# volume. Seeding writes it directly through grok itself: GROK_AUTH_PATH
# relocates the credential file for grok's whole auth subsystem
# (source-verified — reads, writes, login persistence and its lock all follow
# it), so `grok login --device-auth` lands the OAuth pair in the shared store
# with no copying and no translation. The provider command is unset for that
# one call so the login can't recurse into the broker. The base override is a
# test seam (the launcher's gate-file precedent).
BASE="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/grok"
STORE="$BASE/auth.json"
EPCACHE="$BASE/token_endpoint"
export GROK_HOME="${GROK_HOME:-/home/dev/.grok-home}"

command -v grok >/dev/null 2>&1 || exit 0
command -v jq >/dev/null 2>&1 || exit 0
umask 077
mkdir -p "$BASE" 2>/dev/null || exit 0

# Does file $1 hold a refresh-token-bearing entry (a seedable OAuth pair)?
# Broker-issued tokens never carry a refresh_token, so any hit is a real login.
has_pair() {
  [ -s "$1" ] && jq -e 'to_entries | map(select((.value.refresh_token // "") != "")) | length > 0' "$1" >/dev/null 2>&1
}

# Keep the broker's token-endpoint cache warm (its 5s refresh budget has no
# room for discovery). Best-effort; the broker can rebuild it on patient calls.
seed_endpoint_cache() {
  [ -s "$EPCACHE" ] && return 0
  iss=$(jq -r 'to_entries | map(select((.value.refresh_token // "") != "")) | first | .value.oidc_issuer // empty' "$STORE" 2>/dev/null)
  [ -n "$iss" ] || return 0
  ep=$(curl -sf --max-time 10 "$iss/.well-known/openid-configuration" 2>/dev/null | jq -r '.token_endpoint // empty')
  [ -n "$ep" ] && printf '%s' "$ep" >"$EPCACHE.tmp" 2>/dev/null && mv "$EPCACHE.tmp" "$EPCACHE" 2>/dev/null
  return 0
}

# 1. Store already seeded → nothing to do.
if has_pair "$STORE"; then
  seed_endpoint_cache
  exit 0
fi

# 2. Adopt this box's own login: a refresh token in the LOCAL auth.json means
# a real `grok login` happened here (typically the re-seed after a dead shared
# chain — the broker's error text points people at exactly that). Promote it:
# the pair becomes THE machine credential, and the local file is dropped so no
# second copy of the chain exists anywhere. The store mutation runs under the
# BROKER'S flock (same lock file), so it can never interleave with a broker
# read/refresh or another box's promotion, and it is re-checked + published by
# atomic temp+rename so a concurrent reader only ever sees a complete file.
local_cred="$GROK_HOME/auth.json"
if has_pair "$local_cred"; then
  (
    exec 9>>"$BASE/broker.lock" || exit 3
    flock -w 10 9 || exit 3
    # Re-check under the lock: another box may have seeded while we waited.
    # Keep the store's chain (it is live machine-wide); the local pair stays
    # in place, unused past its access token's life — with the broker env set
    # no box ever spends a local refresh token, so it is inert, not a rival.
    has_pair "$STORE" && exit 2
    cp "$local_cred" "$STORE.tmp" && mv "$STORE.tmp" "$STORE" || exit 3
    exit 0
  )
  case $? in
  0)
    echo "byre grok-shared-auth: promoted this box's Grok login to the machine-wide shared credential" >&2
    # Publish first, then drop the local copy; a failed removal is loud (the
    # duplicate is inert under the broker, but nobody should trust that
    # silently).
    rm -f "$local_cred" 2>/dev/null \
      || echo "byre grok-shared-auth: WARNING — could not remove $local_cred after promotion; remove it by hand" >&2
    ;;
  2) echo "byre grok-shared-auth: shared credential already seeded elsewhere; leaving this box's login in place (it goes inert)" >&2 ;;
  *) echo "byre grok-shared-auth: WARNING — promotion failed; grok keeps its per-box login for now" >&2 ;;
  esac
  seed_endpoint_cache
  exit 0
fi

# 3. Nothing anywhere → seed interactively, once per MACHINE. Skippable with
# Ctrl-C; the box still launches (the broker then reports the missing store
# with the same re-seed instructions). XAI_API_KEY users skip file auth.
# Accepted residual (not worth machinery): two boxes launched for the very
# first time in the same minute both prompt; each login writes the store via
# grok's own atomic temp+rename, so the last one wins whole — the loser's
# chain simply goes unused (a fresh, independent family; nothing revokes).
# The lock is deliberately NOT held here: an interactive login can take
# minutes, and a held broker lock would stall every other box's refresh.
[ -n "$XAI_API_KEY" ] && exit 0
trap 'echo; echo "byre: grok login skipped. To seed shared auth later, relaunch, or run '\''grok login --device-auth'\'' in any box and relaunch."; exit 0' INT

echo ""
echo "=== byre: first-run Grok login (shared across all your projects) ==="
echo "Authorize below; stored machine-wide, survives rebuilds. Ctrl-C to skip."
echo ""
TO=""
command -v timeout >/dev/null 2>&1 && TO="timeout --foreground 600"
# GROK_AUTH_PATH points grok's credential file at the shared store for this
# one login; the provider command is unset so the flow can't consult the
# broker it is trying to seed.
if env -u GROK_AUTH_PROVIDER_COMMAND GROK_AUTH_PATH="$STORE" $TO grok login --device-auth; then
  chmod 600 "$STORE" 2>/dev/null
  seed_endpoint_cache
else
  echo "byre: grok login didn't complete. Relaunch to retry, or run 'grok login --device-auth' from 'byre shell' and relaunch to adopt it." >&2
fi
exit 0
