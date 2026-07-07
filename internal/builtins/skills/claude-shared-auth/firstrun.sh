#!/bin/bash
# claude-shared-auth first-run hook (ADR 0017) — one shared Claude login for
# every byre project on this machine. The identity volume holds a static
# setup-token; when it's absent and stdin is interactive, ask the user to mint
# one and paste it. Declining (or no TTY) degrades to the ordinary per-project
# login — this hook must never block a launch. Runs as the dev user; installed
# with a 00- prefix so companion hooks sort before agent-skill hooks.
# The base override is a test seam (the launcher's gate-file precedent).
IDENTITY_DIR="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/claude"
TOKEN_FILE="$IDENTITY_DIR/token"

# The env token authenticates inference but does NOT satisfy interactive
# Claude's first-run wizard: a fresh CLAUDE_CONFIG_DIR has no .claude.json, so
# the wizard runs (login step included) without ever consulting the token
# (host-verified 2026-07-07). Seeding the onboarding-complete marker makes
# Claude use the token directly. FRESH volumes only — never touch an existing
# .claude.json (Claude owns it; it rewrites via temp+rename). Trade recorded:
# skipping the wizard skips the theme picker; /config re-opens it in-session.
seed_onboarding() {
  cfg="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}"
  if [ -s "$TOKEN_FILE" ] && [ ! -e "$cfg/.claude.json" ]; then
    mkdir -p "$cfg" 2>/dev/null || return 0
    printf '{"hasCompletedOnboarding": true}\n' >"$cfg/.claude.json" 2>/dev/null || true
  fi
}

if [ -s "$TOKEN_FILE" ]; then
  seed_onboarding
  exit 0
fi
[ -t 0 ] || exit 0

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
exit 0
