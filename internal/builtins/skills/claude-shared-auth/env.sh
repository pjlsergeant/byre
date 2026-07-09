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
    # boxes). The file is Claude's, not ours, so moving it aside needs the
    # user's yes: offer when stdin is interactive (default Y — declining is
    # the deliberate act), warn-only otherwise. The TTY override is a test
    # seam, per the firstrun hook's precedent.
    _byre_creds="${CLAUDE_CONFIG_DIR:-/home/dev/.claude}/.credentials.json"
    if [ -s "$_byre_creds" ]; then
      {
        echo "byre: warning — this box has a per-project Claude login alongside the shared token."
        echo "      Claude prefers the stored login and stops refreshing it, so this box will 401"
        echo "      roughly 8h after that login."
      } >&2
      _byre_fixed=
      if [ -t 0 ] || [ -n "${BYRE_ASSUME_TTY:-}" ]; then
        printf "byre: move it aside now (to .credentials.json.bak) so the shared token wins? [Y/n] " >&2
        IFS= read -r _byre_ans || _byre_ans="n"
        case "$_byre_ans" in
          ""|[Yy]*)
            if mv -f -- "$_byre_creds" "$_byre_creds.bak" 2>/dev/null; then
              echo "byre: moved — this session runs on the shared token." >&2
              _byre_fixed=1
            else
              echo "byre: move failed — fix by hand: mv \"$_byre_creds\"{,.bak} and relaunch." >&2
            fi
            ;;
        esac
        unset _byre_ans
      fi
      if [ -z "$_byre_fixed" ]; then
        echo "byre: left in place — fix later with: mv \"$_byre_creds\"{,.bak} and relaunch." >&2
      fi
      unset _byre_fixed
    fi
    unset _byre_creds
  else
    unset CLAUDE_CODE_OAUTH_TOKEN
  fi
fi
unset _byre_token_file
