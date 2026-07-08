# Egress doors ship closed: offered, not open

Decided 2026-07-08 (Pete's ruling, emphatic). A deny-by-default firewall
must mean it: **nothing opens a hole in the wall except explicit user
intent, or a skill's own functional requirement.** Convenience endpoints
-- git hosting, apt mirrors, language registries -- ship *declared but
closed*, one keypress from open.

## The problem

ADR 0019 made egress legible and user-extendable, but the shipped
defaults contradicted the promise: enabling the firewall opened git
hosting, apt mirrors, and every language registry (the firewall skill's
base list), and the plan to move registries into language templates
would have kept them auto-open for template users. Someone who selects
the go template and enables the firewall is saying "deny by default" --
finding proxy.golang.org, storage.googleapis.com (all of GCS -- an
exfiltration-grade hole), github and apt already open is exactly the
surprise the firewall exists to prevent. Attribution and one-press
closing (ADR 0018/0019) make the holes visible and fixable, but
visibility is not consent.

## Decision

**The `egress_offered` key.** A second plain string list, same
`host[:port]` grammar, valid in config layers and in a skill's
`[runtime]`. Entries are ALWAYS inert at enforcement time -- they never
reach BYRE_EGRESS. They exist for the UI: the Egress screen shows each
as a closed switch, attributed to its source
(`[ ] proxy.golang.org  (offered by template:go)`), and toggling one
writes the plain entry into the open layer's `egress`. The opened door
is then user-authored, user-attributed, and closable like any other
entry. Merge semantics are the existing string-list ones (union,
`!entry` removal).

**What opens automatically vs. what is offered.** The rule is
functional requirement vs. convenience:

- A skill's `[runtime] egress` is for endpoints the skill ITSELF needs
  to function -- enabling the skill is the intent. The canonical case is
  an agent skill's API endpoints: shipping those closed makes every
  firewalled box dead on first launch.
- Everything else is convenience and ships in `egress_offered`. The
  firewall skill's entire base list (git hosting, apt mirrors,
  registries) moves there -- the firewall needs none of it to function.
  Language templates carry their registries as offered (go:
  proxy.golang.org, sum.golang.org, and storage.googleapis.com only if
  verified still needed, commented as the GCS-wide hole it is; node:
  registry.npmjs.org; python: pypi.org, files.pythonhosted.org).

**Consequence, stated plainly.** A fresh firewalled box reaches its
agent's API and nothing else. `git pull`, `apt-get`, `go get` all hang
until the user opens their doors -- a 30-second trip to the Egress
screen, and that moment of friction is the product working. The
firewall's in-box context tells the agent to direct the user there.

**Status.** Offered entries never print in `byre status` (they are
inert -- not grants); the config UI's Egress summary may count them
("3 more offered") so discovery doesn't depend on entering the screen.

## Rejected

- **Structured egress entries** (`{host = "...", disabled = true}`,
  ADR 0015-style): forces the two-day-old string list through a type
  migration and mixed-type TOML awkwardness for the same UX two string
  lists deliver.
- **Surfacing instead of closing** (louder summaries, launch lines):
  explicitly ruled out -- an open hole you can see is still an open
  hole nobody asked for.
- **Ship-closed agent endpoints** (maximal purity): a box whose agent
  cannot launch serves no one; enabling the agent is the intent for its
  endpoints, the same way enabling a skill is trusting it.
