## Shared Claude login (claude-shared-auth)

This box authenticates Claude with a machine-wide token shared by every
byre project on this machine: the launcher exports
`CLAUDE_CODE_OAUTH_TOKEN` from `~/.byre-identity/claude/token`. The token
was minted with `claude setup-token`, is scoped to inference only, and
lasts about a year. Caveat on precedence: `/status` claims the env token
is in use, but when `~/.claude/.credentials.json` exists, interactive
Claude actually rides that stored login for its requests -- see below.

If Claude starts failing auth (401s, "please log in", token-expired
errors), check the likelier cause first:

1. **A leftover per-project login** (`~/.claude/.credentials.json` from a
   `/login` before this box adopted the shared token). Interactive Claude
   quietly prefers it over the env token AND stops refreshing it, so the
   box 401s roughly 8h after that login -- and an in-box `/login` only
   resets the 8h clock (host-verified 2026-07-07, three boxes). The fix:
   `mv ~/.claude/.credentials.json{,.bak}` and relaunch; Claude then runs
   on the shared token alone. On an interactive launch byre offers to do
   that move itself (consent prompt); non-interactive launches only warn.
   So the cheapest advice is often: relaunch and answer Y.
2. **The shared token itself expired or was revoked** (it lasts about a
   year). This fix is the user's, not yours -- tell them: run
   `claude setup-token` again (on the host or in `byre shell`), then
   overwrite `~/.byre-identity/claude/token` with the new value, or
   delete that file and relaunch byre -- the launch prompt asks for a
   paste again. Do not work around it with an in-box `/login`.

To tell them apart: a headless probe isolates the token from the stored
login -- `CLAUDE_CONFIG_DIR=$(mktemp -d) claude -p 'say ok'` (the env
token is already exported). Works => the token is fine, suspect cause 1.

Two more facts worth knowing:
- On a fresh project volume byre seeds `.claude.json` with
  `hasCompletedOnboarding: true` -- the interactive setup wizard would
  otherwise demand a login without consulting the env token. The trade
  is no theme picker at first run; if the user wants a theme, point
  them at `/config` (or `claude config set theme <name>`).
- The folder-trust prompt is per-project and unrelated to auth; it is
  expected on a project's first launch.
