# claude-shared-auth launch env hook (ADR 0017) — sourced by the launcher just
# before it execs the agent, so the export lands in Claude's process. When the
# shared token is absent or empty this exports nothing and Claude falls back to
# the per-project login untouched. Sourced code: no `exit` here, ever.
if [ -s /home/dev/.byre-identity/claude/token ]; then
  CLAUDE_CODE_OAUTH_TOKEN="$(tr -d '[:space:]' </home/dev/.byre-identity/claude/token 2>/dev/null)"
  if [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]; then
    export CLAUDE_CODE_OAUTH_TOKEN
  else
    unset CLAUDE_CODE_OAUTH_TOKEN
  fi
fi
