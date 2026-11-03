# No credential seeding -- agents log in in the box

> **Amended by ADR 0057** (2026-08-13) and **Amended by ADR 0059**
> (2026-10-02): project credentials and `byre restore` are not the action
> this ADR bans.

**byre does not initiate copying a rotating agent login.** What is banned
is byre-INITIATED copy-semantics for rotating tokens: byre reading a host
credential file and seeding it into a box of its own accord. A
`--seed-creds` feature did exactly that and was removed after it broke in
practice: Claude Code and Codex rotate their OAuth refresh tokens, so a
naive *copy* creates two independent holders of one single-use refresh
token, and the first refresh anywhere invalidates the rest ("refresh token
already used" -- this bricked codex reviews). Per-vendor rotation facts,
including Gemini's non-rotating refresh, are in
[AGENT-CREDENTIAL-MECHANICS](../AGENT-CREDENTIAL-MECHANICS.md). Instead,
agents log in once **in the box** (codex via a device-auth first-run hook)
and the per-project `state` volume persists the login. Sharing one volume
is safe where copying is not (see ADR 0009 for the worktree case; ADR 0017
for shared identity across projects).

Two user-directed actions are outside the ban entirely. Project
credentials (ADR 0057) are values the user sets, stored encrypted inline in
the config files and delivered per launch; they are not agent logins.
`byre restore` (ADR 0059) pours a volume of the user's own box back at the
user's instruction, and that volume may hold a login -- two copies of one
rotating token are then exactly the failure above, and byre says nothing
about it (P1: the threat model is the agent, never the user). In both
cases byre still reads no host credential file and seeds no login.

Consequences: the volume `seed` mechanism is for non-credential data, and
`seed_prefs` copies only skill-curated, structurally secret-free files
(ADR 0013). Without a shared-auth companion (ADR 0017), a fresh project
means one in-box login per agent.
