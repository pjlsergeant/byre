#!/bin/bash
# claude-shared-auth first-run hook (ADR 0017) — one shared Claude login for
# every byre project on this machine. The identity volume holds a static
# setup-token; when it's absent and stdin is interactive, ask the user to mint
# one and paste it. Declining (or no TTY) degrades to the ordinary per-project
# login — this hook must never block a launch. Runs as the dev user; installed
# with a 00- prefix so companion hooks sort before agent-skill hooks.
IDENTITY_DIR="/home/dev/.byre-identity/claude"
TOKEN_FILE="$IDENTITY_DIR/token"

[ -s "$TOKEN_FILE" ] && exit 0
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
echo "byre: saved — every byre project on this machine now launches Claude logged in."
exit 0
