#!/bin/bash
# claude onboarding hook -- runs as the dev user before the agent launches.
# A fresh CLAUDE_CONFIG_DIR has no .claude.json, so claude runs its first-run
# wizard without consulting an env credential (CLAUDE_CODE_OAUTH_TOKEN,
# ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN). Seed the onboarding marker only
# for a fresh config with an env credential; an existing config belongs to
# claude. Same publish as claude-shared-auth's firstrun hook; the trade is
# explained there. Never prompts; always exits 0.
[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] || [ -n "${ANTHROPIC_API_KEY:-}" ] ||
  [ -n "${ANTHROPIC_AUTH_TOKEN:-}" ] || exit 0
cfg="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}"
marker="$cfg/.claude.json"
if [ ! -e "$marker" ] && [ ! -L "$marker" ]; then
  mkdir -p -- "$cfg" 2>/dev/null || exit 0
  tmp=$(mktemp -- "$cfg/.claude.json.byre-XXXXXX" 2>/dev/null) || exit 0
  if printf '{"hasCompletedOnboarding": true}\n' >"$tmp" 2>/dev/null; then
    ln -n -- "$tmp" "$marker" 2>/dev/null || true
  fi
  rm -f -- "$tmp"
fi
exit 0
