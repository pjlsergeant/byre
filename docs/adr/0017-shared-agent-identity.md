# Shared agent identity: machine-scoped volumes + companion skills

An agent login can be shared across all of a user's projects by enabling
an opt-in **companion skill** (`claude-shared-auth`, `codex-shared-auth`,
`gemini-shared-auth`) alongside the agent skill. Each companion
contributes a **machine-scoped volume** -- new general config grammar,
`scope = "machine"` on a `[[volumes]]` entry, Docker name
`byre-machine-u<uid>-<name>` -- holding only that agent's identity, plus
the small wiring that connects it (a firstrun hook; for Claude a launch
env hook from the new `/etc/byre/env.d/` chassis mechanism). Decided
2026-07-07; built and live-verified the same day (verification record at
the end). Evidence in `docs/agent-credential-mechanics.md`; the build-plan
doc (`shared-auth-design.md`) served its purpose and was absorbed and
deleted per its lifecycle, like `firewall-design.md` before it.

Per-project login was the cost of per-project state volumes, and it
taxes exactly the use byre is pitched on: dropping an agent into many
folders. The fix had to hold three constraints at once: work for all
three agents (a Claude-only story undersells the project), stay opt-in
and legible (a shared credential must be visible, never ambient), and
keep byre out of the host-credential business -- **ADR 0007 remains
closed**: byre reads nothing from the host and copies nothing. Codex and
Gemini logins still happen inside a box (the shared volume just makes it
the last one); Claude's shared token is minted by the user with
`claude setup-token` -- wherever a browser is handy, host included --
and handed over at an explicit prompt. An explicit hand-over, never
ambient inheritance.

The mechanism is per-agent because the vendors' credential mechanics
differ (research-verified): Claude Code's refresh tokens are single-use
-- two boxes refreshing one file cascade-logout each other -- and it
replaces symlinked files via temp+rename, so for Claude the shared
volume carries a **static one-year inference-only token** (minted by
`claude setup-token`, pasted at a firstrun prompt, exported as
`CLAUDE_CODE_OAUTH_TOKEN` by an env.d hook). Codex writes `auth.json` in
place and OpenAI endorses moving it between machines, so its companion
is one idempotently-asserted **symlink** into the identity volume.
Gemini is symlink-shaped too, but Google's OAuth refresh-rotation
behavior is unverified: `gemini-shared-auth` shipped with its
description saying so -- the API-key path is verified and
rotation-immune (a static key has no refresh tokens; see the
verification record below), while OAuth sharing stays behind the
empirical gate (two concurrent boxes, forced refresh, neither session
dies). If OAuth rotation ever proves Claude-shaped, OAuth sharing stays
unsupported, since Google offers no env-token fallback.

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
access is root-equivalent, which docs/SECURITY.md now states plainly.

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
config and byre's hands -- TODO 6's passthrough stays a separate
generic feature); a host-side token broker (revocation theater, and
byre is structurally daemon-less); sharing whole state dirs (cwd-keyed
state collides across projects -- everything is /workspace).

**Verification record (2026-07-07, live on the maintainer's host).**
Claude: verified end-to-end -- with one fix found live: interactive
Claude Code's first-run gate is its ONBOARDING state, not its auth state,
so the skill seeds `hasCompletedOnboarding` into fresh config dirs (the
env token alone never got consulted). Codex: verified (one login,
second box authenticated; the logout-fork heal works). Gemini: the
API-KEY path is verified and rotation-immune (a static key has no
refresh tokens); OAuth sharing remains unverified and the rotation gate
stands open, optional -- two concurrent boxes, forced refresh, neither
dies -- with an accepted residual either way: each new project shows
gemini's auth-method picker once, credential prefilled
(`selectedAuthType` is per-project settings.json; seeding it was
consciously deferred). The build also corrected the evidence base:
gemini-cli 0.49 stores its credential ENCRYPTED
(`gemini-credentials.json`, key derived from hostname+username), so the
gemini skill pins `--hostname byre` -- without which the login died on
EVERY rebuild, shared or not. All live findings are version-stamped in
`docs/agent-credential-mechanics.md`.
