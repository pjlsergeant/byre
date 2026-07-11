# Onboarding offers shared auth, once per agent

The first-run picker now asks one more question: when the chosen agent
has a **ready** shared-auth companion skill (ADR 0017), it offers to
enable it — "Share one claude login across all byre projects on this
machine (claude-shared-auth)? [y/N]" — one line carrying the whole
decision: one login, every project, this machine, and the mechanism's
name. Yes appends the companion to `skills`
in `~/.byre/default.config`; no is recorded there too
(`shared_auth_declined`), so the offer is made **at most once per
agent**, never per project. Decided 2026-07-11.

Shared auth existed but was buried: enabling it meant hand-editing
`default.config` or finding the checkbox in the config UI's skills
screen, and per-project login taxes exactly the use byre is pitched on
(ADR 0017's own framing). The moment the user picks an agent is the
natural moment to ask — and an explicit, default-No question is *more*
aligned with 0017's "explicit hand-over, never ambient inheritance"
than a setting discovered later. ADR 0007 stays closed: the offer
changes where the user answers a question, not where credentials come
from — byre still reads nothing from the host, and Claude's token is
still pasted by the user at a box's firstrun prompt.

Three choices carry the design:

- **A "yes" has no key of its own.** It is the companion skill's
  presence in `default.config`'s `skills` list — the exact
  representation a hand-enabled companion uses, so there is no second
  source of truth for "shared auth is on". The write is surgical (the
  `SaveDefault` philosophy: touch only what was answered, keep the
  user's comments), via a list editor that re-parses its own edit and
  verifies the value actually landed before writing anything; a file
  shape it can't follow is refused with a do-it-by-hand instruction,
  never guessed at. It lands in `default.config`, not the project's
  `byre.config`, because a machine-scoped identity volume IS
  machine-wide — writing it per-project would misstate the scope. The
  question's wording carries that scope for the same reason (the
  ADR 0018 worry: onboarding quietly writing a layer the user didn't
  think they were configuring).
- **A "no" is remembered, or the picker nags.** `shared_auth_declined`
  is picker-owned state in `default.config`, exactly like the
  template/agent favourites: the resolver zeroes the default layer's
  copy so it can't leak into a resolved config, and only onboarding
  reads it. Deleting the entry re-arms the offer. (Rejected: a
  `!companion` removal marker in `skills` as the decline sentinel —
  it abuses merge vocabulary for state-keeping and would actively
  fight a template that legitimately enables the skill.)
- **Readiness is declared, not inferred.** A companion skill says
  `shared_auth_for = "claude"` in its skill.toml — the author's vouch
  that the mechanism is ready to put in front of every user of that
  agent. The `<agent>-shared-auth` naming convention was rejected as
  the trigger: it can't express readiness, and readiness is the whole
  gate — grok-shared-auth is BROKEN (failed its field gate) and
  gemini's OAuth path is gate-pending, so neither declares the key and
  neither is offered, while both remain hand-enableable by someone who
  has read their skill.toml. Two skills claiming one agent is refused
  (no offer — the `network_posture` stance): sort order must not pick
  which skill a "y" enables machine-wide, and a hand-dropped
  near-namesake must not shadow the vetted builtin.

Consequences: the picker asks up to three questions plus the offer
(template, agent, save-as-default, shared auth). The offer only appears
on a TTY, only when unanswered, and only on runs that were already
interactive — a fully-flagged `--template X --agent Y` onboarding keeps
its zero-prompt contract and is never asked. EOF (Ctrl-D) at the offer
skips it without failing the develop (byre.config is already written by
then) and records nothing. `config.Config` gains the
`shared_auth_declined` key; the resolver strips it from every resolved
config whatever layer carried it — it is inert outside onboarding.
Existing installs pick the offer up via `byre skill update` (the store
materialization is deliberately non-clobbering, so an already-installed
companion skill.toml keeps its pre-0023 content until updated — the
standard pickup path for every shipped skill change). When gemini's
OAuth gate passes or grok's rebuild lands, declaring `shared_auth_for`
in their skill.toml is the entire rollout of their offer.
