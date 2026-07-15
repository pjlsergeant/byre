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

# A leftover per-project login alongside the shared token is a time bomb:
# interactive Claude quietly prefers the stored credential over the env token
# and stops refreshing it, so the box starts failing with "401 Invalid
# authentication credentials" roughly 8h after that login, while /status still
# claims env-token auth (host-verified 2026-07-07, three boxes). The file is
# Claude's, not ours, so moving it aside needs the user's yes: offer when stdin
# is interactive (default Y -- declining is the deliberate act), warn-only
# otherwise. This lives HERE (firstrun.d, executed every launch) rather than in
# env.sh (sourced env-setter): a prompt + file move is a command, not an env
# export, and sourcing env.d into every login shell must never re-fire it. The
# read is bounded -- a TTY does not imply an attending human, and a hook must
# not hang the launch; timeout falls back to warn-only. The TTY override is a
# test seam (the launch gate's env-override precedent).
remediate_stale_login() {
  creds="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}/.credentials.json"
  [ -s "$creds" ] || return 0
  # The hijacker is a stored INFERENCE login — the claudeAiOauth block.
  # .credentials.json also holds MCP server OAuth tokens (mcpOAuth), and in a
  # shared-token box the file is typically mcpOAuth-ONLY (MCP auth creates it;
  # inference never wrote a login). That file is healthy, load-bearing state:
  # moving it aside would silently log the box out of its MCP servers every
  # launch (verified 2026-07-15: env-token box + MCP OAuth = top-level
  # mcpOAuth key only). Detect the actual hijacker, not file presence.
  grep -q '"claudeAiOauth"' "$creds" 2>/dev/null || return 0
  {
    echo "byre: warning — this box has a per-project Claude login alongside the shared token."
    echo "      Claude prefers the stored login and stops refreshing it, so this box will 401"
    echo "      roughly 8h after that login."
  } >&2
  # Collateral disclosure: moving the whole file aside also takes any MCP
  # server logins with it (they re-auth in-session via /mcp). Surgical JSON
  # editing needs tooling the claude image doesn't carry; disclose instead.
  if grep -q '"mcpOAuth"' "$creds" 2>/dev/null; then
    echo "      (This file also holds MCP server logins — moving it aside signs those out too;" >&2
    echo "      re-auth them in-session via /mcp.)" >&2
  fi
  if [ -t 0 ] || [ -n "${BYRE_ASSUME_TTY:-}" ]; then
    printf "byre: move it aside now (to .credentials.json.bak) so the shared token wins? [Y/n] " >&2
    IFS= read -r -t 60 ans || ans="n"
    case "$ans" in
      ""|[Yy]*)
        if mv -f -- "$creds" "$creds.bak" 2>/dev/null; then
          echo "byre: moved — this session runs on the shared token." >&2
        else
          echo "byre: move failed — fix by hand: mv \"$creds\"{,.bak} and relaunch." >&2
        fi
        return 0
        ;;
    esac
  fi
  echo "byre: left in place — fix later with: mv \"$creds\"{,.bak} and relaunch." >&2
}

if [ -s "$TOKEN_FILE" ]; then
  seed_onboarding
  remediate_stale_login
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
