# Shared agent identity: machine-scoped volumes + companion skills

An agent login can be shared across all of a user's projects by enabling
an opt-in **companion skill** (`claude-shared-auth`, `codex-shared-auth`,
`gemini-shared-auth`) alongside the agent skill. Each companion
contributes a **machine-scoped volume** -- new general config grammar,
`scope = "machine"` on a `[[volumes]]` entry, Docker name
`byre-machine-u<uid>-<name>` -- holding only that agent's identity, plus
the small wiring that connects it (a firstrun hook; for Claude a launch
env hook from the `/etc/byre/env.d/` chassis mechanism). Decided
2026-07-07 for the three agents byre then shipped; OpenCode's companion
follows the same model and Grok's is ADR 0036. Every vendor fact this
rests on, with its verification record, is in
[AGENT-CREDENTIAL-MECHANICS](../AGENT-CREDENTIAL-MECHANICS.md).

Per-project login was the cost of per-project state volumes, and it
taxes exactly the use byre is pitched on: dropping an agent into many
folders. The fix had to hold three constraints at once: work for all
three agent skills byre shipped at the decision date, stay opt-in
and legible (a shared credential must be visible, never ambient), and
keep byre out of the host-credential business -- **ADR 0007 remains
closed**: byre reads nothing from the host and copies nothing. Codex and
Gemini logins still happen inside a box (the shared volume just makes it
the last one); Claude's shared token is minted by the user with
`claude setup-token` -- wherever a browser is handy, host included --
and handed over at an explicit prompt. An explicit hand-over, never
ambient inheritance.

The mechanism is per-agent because the vendors' credential mechanics
differ:

- **Claude**: refresh tokens are single-use (two boxes refreshing one file
  cascade-logout each other) and files are replaced via temp+rename, so
  the shared volume carries a **static one-year inference-only token**
  (minted by `claude setup-token`, pasted at a firstrun prompt, exported
  as `CLAUDE_CODE_OAUTH_TOKEN` by an env.d hook).
- **Codex**: refreshes `auth.json` in place and OpenAI endorses moving it
  between machines, so the companion is one idempotently-asserted
  **symlink** into the identity volume. Because Codex's own login revokes
  and removes the existing file first, the companion reconciles under a
  machine-volume lock: the newer credential wins (`last_refresh`, mtime
  fallback), publication is an atomic rename inside the identity volume,
  and the local path returns to the symlink. Missing local auth never
  authorizes deleting shared auth. byre-managed device login reconciles
  before returning; an unwrapped mid-session `codex login` converges on
  the next box launch.
- **Gemini**: symlink-shaped too, and its OAuth refresh tokens are
  non-rotating, so concurrent refresh cannot cascade-logout. The
  companion seeds the auth-method selection so the picker never deletes
  a shared login. Its vouch (`shared_auth_for`, ADR 0025) still waits on
  the live two-box gate; until then it declares only `companion_for`
  (ADR 0034).

This deliberately reverses two prior negatives. The parked "machine-wide
shared volume scope" was dropped for lacking a natural boundary across
unrelated projects -- agent identity turned out to be that boundary (it
was always machine-scoped by nature; that is the whole pain). The
retired creds/history split returns in its cheapest form: identity moves
to the shared volume, everything cwd-keyed stays in the per-project
volume, and the root-level mixed-scope files that made a full split
impossible stay put, untouched. "Machine" scope is really
per-user-per-machine -- the uid-qualified name (the ImageTag precedent,
ADR 0008) prevents two users on a shared box from silently sharing one
login. It cannot prevent deliberate cross-user mounting: Docker daemon
access is root-equivalent, which the security-model page states plainly.

Lifecycle honesty: `byre status` prints machine-scoped volumes on their
own row; `reset` and `forget` skip them and say so, naming the
deliberate route (`byre config` -> Volumes -> clear, which doubles as
the logout story and refuses while any byre session runs). A shared
credential is not classified a grant -- no host reach is widened, and
per-project credentials were already account-capable -- but
cross-project sharing is never invisible.

Ruled out: variant agent skills (whole-skill duplication per variant);
credential-file seeding from the host (refresh collisions + reopens
0007); host env passthrough as the auth path (year-long token in host
config and byre's hands -- `env_from_host` (ADR 0026) stays a separate
generic feature); a host-side token broker (revocation theater, and
byre is structurally daemon-less); sharing whole state dirs (cwd-keyed
state collides across projects -- everything is /workspace).
