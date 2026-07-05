# Credential seeding is out for now -- agents log in in the box

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
copy-semantics for rotating tokens. A future credential-sharing design
could work -- it would need something other than an independent copy
(move semantics, a shared source of truth, or per-agent handling of
token formats) -- but that's fiddly machinery against a 30-second
in-box login, so it isn't being targeted yet.

Consequences (as of the removal): the volume `seed` mechanism is for
non-credential data, and `seed_prefs` copies only skill-curated,
structurally secret-free files (ADR 0013). A fresh project means one
login per agent.
