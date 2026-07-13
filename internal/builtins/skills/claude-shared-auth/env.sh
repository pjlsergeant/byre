# claude-shared-auth launch env hook (ADR 0017) -- a PURE env-setter. env.d
# hooks are sourced (by the launcher before the agent, and by every login shell
# via /etc/profile.d/byre-env.sh), so they must ONLY export environment: no
# commands, no interactive reads, no file mutations. The stale-per-project-login
# remediation this hook used to smuggle in now lives in firstrun.sh, where an
# every-launch executed hook is the right home for a prompt + file move. When
# the shared token is absent/empty this exports nothing and Claude falls back to
# the per-project login. Sourced code: no `exit`, ever. The base override is a
# test seam (the firstrun hook's precedent).
_byre_token_file="${BYRE_IDENTITY_BASE:-/home/dev/.byre-identity}/claude/token"
if [ -s "$_byre_token_file" ]; then
  CLAUDE_CODE_OAUTH_TOKEN="$(tr -d '[:space:]' <"$_byre_token_file" 2>/dev/null)"
  if [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]; then
    export CLAUDE_CODE_OAUTH_TOKEN
  else
    unset CLAUDE_CODE_OAUTH_TOKEN
  fi
fi
unset _byre_token_file
