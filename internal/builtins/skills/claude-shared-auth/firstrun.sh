#!/bin/bash
# claude-shared-auth first-run hook (ADR 0017) — one shared Claude login for
# every byre project on this machine. The identity volume holds a static
# setup-token; when it's absent and stdin is interactive, ask the user to mint
# one and paste it. Declining (or no TTY) degrades to the ordinary per-project
# login — this hook must never block a launch. Once the token exists, the hook
# also offers (interactively, with consent) to move aside a leftover
# per-project login that would otherwise shadow the token. Runs as the dev
# user; installed with a 00- prefix so companion hooks sort before agent-skill
# hooks. The env overrides are test seams (the launcher's gate-file precedent).
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/claude"
TOKEN_FILE="$IDENTITY_DIR/token"
CFG_DIR="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}"

# "Token present" must mean what env.sh means by it: non-empty after stripping
# whitespace. A whitespace-only file exports nothing there, so it must not
# gate onboarding seeding or the creds-move offer here either.
token_present() { [ -n "$(tr -d '[:space:]' <"$TOKEN_FILE" 2>/dev/null)" ]; }

# Both prompts below gate on this: never wait for input nobody can give. The
# override is a test seam; a user exporting it on a non-TTY launch is blocking
# their own launch on a read from nothing (footgun doctrine).
interactive() { [ -t 0 ] || [ -n "${BYRE_ASSUME_INTERACTIVE:-}" ]; }

# The env token authenticates inference but does NOT satisfy interactive
# Claude's first-run wizard: a fresh CLAUDE_CONFIG_DIR has no .claude.json, so
# the wizard runs (login step included) without ever consulting the token
# (host-verified 2026-07-07). Seeding the onboarding-complete marker makes
# Claude use the token directly. FRESH volumes only — never touch an existing
# .claude.json (Claude owns it; it rewrites via temp+rename). Trade recorded:
# skipping the wizard skips the theme picker; /config re-opens it in-session.
seed_onboarding() {
  if token_present && [ ! -e "$CFG_DIR/.claude.json" ]; then
    mkdir -p "$CFG_DIR" 2>/dev/null || return 0
    printf '{"hasCompletedOnboarding": true}\n' >"$CFG_DIR/.claude.json" 2>/dev/null || true
  fi
}

# A leftover per-project login (`/login` before this box adopted the shared
# token) quietly outranks the env token AND stops refreshing, so the box 401s
# ~8h after that login (host-verified 2026-07-07; see env.sh / context.md).
# The env hook warns on every launch; here, with a user present, we can offer
# the actual fix — a consented, reversible rename. The file is Claude's, so we
# never touch it without a yes; declining or any failure falls through to the
# launch (and the env hook's warning). Runs on every launch the combination
# exists — catching a /login done AFTER adopting the shared token — and on the
# adoption launch itself, right after the paste is saved.
offer_creds_move() {
  creds="$CFG_DIR/.credentials.json"
  [ -s "$creds" ] || return 0
  interactive || return 0
  echo ""
  echo "=== byre: claude-shared-auth — leftover per-project Claude login ==="
  echo "This box has $creds"
  echo "alongside the shared token. Claude quietly prefers that stored login and"
  echo "stops refreshing it, so this box will start failing with 401s roughly 8h"
  echo "after that login. Moving it aside makes Claude run on the shared token."
  echo "(To keep a separate login for this box instead, turn the skill off for"
  echo "this project: add \"!claude-shared-auth\" to skills in byre.config.)"
  printf "Move it aside (to .credentials.json.bak)? [Y/n] "
  # Only an explicit yes moves the file: plain Enter or y…. Anything else —
  # including EOF (Ctrl-D), a stray typed-ahead line meant for the agent, or
  # "  n" — declines. A credential move must never ride on a non-answer.
  if IFS= read -r reply; then
    reply="$(printf '%s' "$reply" | tr -d '[:space:]')"
  else
    reply="n"
    echo ""
  fi
  case "$reply" in
  "" | [yY]*) ;;
  *)
    echo "byre: left in place — expect 401s ~8h after that login. Fix later with:"
    echo "      mv \"$creds\"{,.bak} and relaunch."
    return 0
    ;;
  esac
  # Never clobber an earlier backup — nothing gets deleted, ever.
  bak="$creds.bak"
  n=1
  while [ -e "$bak" ]; do
    bak="$creds.bak.$n"
    n=$((n + 1))
  done
  if mv "$creds" "$bak" 2>/dev/null; then
    echo "byre: moved to $bak — Claude runs on the shared token from this launch."
  else
    echo "byre: could not move it; launching anyway. The fix stays:"
    echo "      mv \"$creds\"{,.bak} and relaunch."
  fi
  return 0
}

if token_present; then
  seed_onboarding
  offer_creds_move
  exit 0
fi
interactive || exit 0

echo ""
echo "=== byre: claude-shared-auth — one Claude login for all your projects ==="
echo "Mint a long-lived token (about a year, inference-only) by running:"
echo ""
echo "    claude setup-token"
echo ""
echo "on your host or in another terminal ('byre shell') — wherever a browser"
echo "is handy. Paste it below to share it machine-wide, or press Enter to"
echo "skip (this project then logs in on its own, as usual)."
printf "token: "
# Silent read: a year-long credential must not sit in terminal scrollback.
IFS= read -rs token || token=""
echo ""
token="$(printf '%s' "$token" | tr -d '[:space:]')"
if [ -z "$token" ]; then
  echo "byre: skipped — using this project's own login."
  exit 0
fi
# Warn-don't-block: token formats aren't ours to enforce.
case "$token" in
sk-ant-*) ;;
*) echo "byre: note — that doesn't look like a Claude setup-token (expected sk-ant-…); saving anyway." ;;
esac
mkdir -p "$IDENTITY_DIR"
umask 077
printf '%s\n' "$token" >"$TOKEN_FILE"
chmod 600 "$TOKEN_FILE" 2>/dev/null || true
seed_onboarding
echo "byre: saved. Boxes with claude-shared-auth enabled launch Claude with this login from their next develop."
# The adoption launch is the one most likely to still hold a per-project
# /login — offer the move now, not on the next launch.
offer_creds_move
exit 0
