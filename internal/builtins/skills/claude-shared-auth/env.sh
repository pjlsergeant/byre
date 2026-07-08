# claude-shared-auth launch env hook (ADR 0017) — sourced by the launcher just
# before it execs the agent, so the export lands in Claude's process. When the
# shared token is absent or empty this exports nothing and Claude falls back to
# the per-project login untouched. Sourced code: no `exit` here, ever.
# The base override is a test seam (the firstrun hook's precedent).
_byre_token_file="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/claude/token"
if [ -s "$_byre_token_file" ]; then
  CLAUDE_CODE_OAUTH_TOKEN="$(tr -d '[:space:]' <"$_byre_token_file" 2>/dev/null)"
  if [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]; then
    export CLAUDE_CODE_OAUTH_TOKEN
    # A leftover per-project login alongside the shared token is a time bomb:
    # interactive Claude quietly prefers the stored credential over the env
    # token and stops refreshing it, so the box starts failing with "401
    # Invalid authentication credentials" roughly 8h after that login, while
    # /status still claims env-token auth (host-verified 2026-07-07, three
    # boxes). Warn, don't fix — the file is Claude's, not ours; the firstrun
    # hook offers a consented move when a TTY is present, so this warning is
    # the non-interactive fallback (and the trail for a declined offer).
    _byre_creds="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}/.credentials.json"
    if [ -s "$_byre_creds" ]; then
      {
        echo "byre: warning — this box has a per-project Claude login alongside the shared token."
        echo "      Claude prefers the stored login and stops refreshing it, so this box will 401"
        echo "      roughly 8h after that login. Fix: mv \"$_byre_creds\"{,.bak} and relaunch."
      } >&2
    fi
    unset _byre_creds
  else
    unset CLAUDE_CODE_OAUTH_TOKEN
  fi
fi
unset _byre_token_file
