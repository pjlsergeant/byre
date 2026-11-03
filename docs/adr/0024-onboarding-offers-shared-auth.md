# Onboarding offers shared auth when a companion is ready

> Superseded in part by [ADR 0025](0025-shared-auth-offer-is-per-box.md)
> (2026-07-12): the offer is per box, and what a yes or a saved answer
> writes is ADR 0025's. The readiness gate and the offer's placement below
> stand.

Decided 2026-07-11: the first-run picker asks one more question -- when
the chosen agent has a **ready** shared-auth companion skill (ADR 0017),
it offers to enable it. Shared auth was otherwise buried in hand-edited
config or the skills screen, and per-project login taxes exactly the use
byre is pitched on. The moment the user picks an agent is the natural
moment to ask, and an explicit, default-No question is *more* aligned
with 0017's "explicit hand-over, never ambient inheritance" than a
setting discovered later. ADR 0007 stays closed: the offer changes where
the user answers a question, not where credentials come from.

- **Readiness is declared, not inferred.** Offer only a unique companion
  declaring `shared_auth_for = "<agent>"` in its skill.toml -- the
  author's vouch that the mechanism is ready to put in front of every user
  of that agent. The `<agent>-shared-auth` naming convention was rejected
  as the trigger because a name cannot express readiness: a skill that
  failed its field gate (ADR 0023) or is gate-pending doesn't declare the
  key and isn't offered, while staying hand-enableable. Two skills
  claiming one agent is refused (no offer): sort order must not pick which
  skill a "y" enables, and a hand-dropped near-namesake must not shadow
  the vetted builtin.
- **The offer's placement and interactivity contract.** It sits
  directly after the agent question it belongs to; it appears only on a
  TTY, only when unanswered, and only on runs that were already
  interactive -- a fully-flagged onboarding keeps its zero-prompt
  contract. Every answer is collected before anything is written, so
  EOF anywhere in the picker aborts onboarding with no side effects.
