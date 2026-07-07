## Shared Claude login (claude-shared-auth)

This box authenticates Claude with a machine-wide token shared by every
byre project on this machine: the launcher exports
`CLAUDE_CODE_OAUTH_TOKEN` from `~/.byre-identity/claude/token`. The token
was minted with `claude setup-token`, is scoped to inference only, and
lasts about a year. The env token takes precedence over any in-box login.

If Claude starts failing auth (401s, "please log in", token-expired
errors), the shared token has likely expired or been revoked. The fix is
the user's, not yours -- tell them:

1. Run `claude setup-token` again (on the host or in `byre shell`).
2. Replace the token: overwrite `~/.byre-identity/claude/token` with the
   new value, or delete that file and relaunch byre -- the launch prompt
   asks for a paste again.

Do not try to work around an expired shared token with an in-box
`/login`; the env token wins and the confusion compounds.

Two more facts worth knowing:
- On a fresh project volume byre seeds `.claude.json` with
  `hasCompletedOnboarding: true` -- the interactive setup wizard would
  otherwise demand a login without consulting the env token. The trade
  is no theme picker at first run; if the user wants a theme, point
  them at `/config` (or `claude config set theme <name>`).
- The folder-trust prompt is per-project and unrelated to auth; it is
  expected on a project's first launch.
