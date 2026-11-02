#!/bin/bash
# claude onboarding hook -- runs as the dev user before the agent launches.
# A credential in the environment (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY,
# ANTHROPIC_AUTH_TOKEN) authenticates inference, but a fresh CLAUDE_CONFIG_DIR
# has no .claude.json, so interactive Claude runs its first-run wizard (login
# step included) without consulting it, and a launch with nobody at the
# terminal sits in that wizard. Seeding the onboarding-complete marker lets
# Claude use the env credential directly. Same marker and trade as the
# claude-shared-auth hook, which seeds it for the shared token: skipping the
# wizard skips the theme picker; /config re-opens it in-session.
#
# Fresh config dirs only: never touch an existing .claude.json (Claude owns it
# and rewrites it via temp+rename), and never write through a symlink at that
# name -- `-e` is false for a dangling one, so `-L` is tested too. A node
# raced in after the test is refused by the publish itself: the bytes go to a
# mktemp'd dotfile (O_EXCL, a fresh name), and `ln -n` claims the final name
# with link(2), which fails EEXIST on any existing node and never follows a
# symlink (-n keeps even a link to a directory from being read as one; a real
# directory planted there takes the marker inside it, out of everyone's way).
# Noclobber would not do: it refuses only an existing regular file, and opens
# a symlink to a FIFO, blocking the launch. A lost claim leaves nothing
# behind. Never prompts; always exits 0.
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
