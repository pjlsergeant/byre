# Prefs seeding copies only skill-curated, structurally secret-free files

`seed_prefs = true` opts into a one-time copy of agent preferences
(theme, keybindings) into a **fresh** state volume -- and the copied set
is a per-file allowlist curated by the agent skill (`[agent.prefs]`),
never a directory copy. Only files *structurally incapable* of holding a
secret qualify: for Claude that's `keybindings.json` + `themes/`, while
`settings.json` (mixes theme with env/apiKeyHelper/hooks/MCP) and
`~/.claude.json` (OAuth tokens, trust) are excluded.

Why this exists at all: prefs are the one volume-init category that is
neither credentials (not copied today -- ADR 0007) nor history, so a plain
opt-in seed sidesteps the rotation/sharing problems that killed
credential seeding. Why curated: agent config files routinely hide
secrets, so "copy the config dir" is exactly the accident the footgun
doctrine says byre must prevent (PRINCIPLES.md #1) -- while the curated
list stays a user-visible, skill-vouched contract.

Consequences: the loader validates shape only (relative, non-escaping
paths, requires a state volume); the skill author vouches the files are
secret-free. Acts only on a fresh volume; a failed seed rolls the volume
back.
