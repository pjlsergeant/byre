# Credential seeding is out for now -- agents log in in the box

> **Amended by ADR 0057** (2026-08-13): project credentials — user-set values
> stored encrypted inline in the config files and delivered per launch — are a
> different thing from the agent-login seeding this ADR bans, and do not
> reopen it: byre still reads no host credential files and seeds no agent
> logins.

> **Amended by ADR 0059** (2026-10-02): the ban is on byre-INITIATED seeding.
> `byre restore` pours a volume of the user's own box back, at the user's
> instruction, and that volume may hold a login -- two copies of one rotating
> token are then exactly the failure below, and byre says nothing about it
> (P1: the threat model is the agent, never the user). byre still reads no
> host credential file and seeds no login.

byre does not currently copy host agent credentials into a box. A
`--seed-creds` feature existed and was removed after it broke in
practice: all three agent CLIs use rotating OAuth tokens, so a naive
*copy* creates two independent holders of one single-use refresh token,
and the first refresh anywhere invalidates the rest ("refresh token
already used" -- this bricked codex reviews). Instead, agents log in once
**in the box** (codex via a device-auth first-run hook) and the
per-project `state` volume persists the login. Sharing one volume is
safe where copying is not (see ADR 0009 for the worktree case).

**This is a "not now", not a doctrine.** What's dead is specifically
byre-INITIATED copy-semantics for rotating tokens -- byre reading a host
credential file and seeding it into a box of its own accord. A future
credential-sharing design on byre's initiative could work -- it would need
something other than an independent copy byre makes on your behalf (move
semantics, a shared source of truth, or per-agent handling of token
formats) -- but that's fiddly machinery against a 30-second in-box login,
so it isn't being targeted yet. The user moving their OWN box's state
volume is outside the ban entirely (ADR 0059).

Consequences (as of the removal): the volume `seed` mechanism is for
non-credential data, and `seed_prefs` copies only skill-curated,
structurally secret-free files (ADR 0013). A fresh project means one
login per agent.
