# Onboarding offers shared auth, once per agent

> Superseded in part by ADR 0025 (2026-07-12): the offer survives, but
> its scope was wrong -- the question is now asked per box ("Opt this
> box into <agent> shared credentials?"), a yes lands only in that
> project's `byre.config`, and the saved answer is a favourite (the
> picker-owned `shared_auth` list, prefilling future offers), never a
> machine-level grant; `shared_auth_declined` is vestigial. The
> readiness gate (`shared_auth_for`) and the offer's placement are
> unchanged.

Decided 2026-07-11: the first-run picker asks one more question — when
the chosen agent has a **ready** shared-auth companion skill (ADR 0017),
it offers to enable it. As decided here, a yes appended the companion to
`skills` in `~/.byre/default.config` and a no was recorded there too
(`shared_auth_declined`), making the offer at most once per agent —
this machine-wide recording is the part ADR 0025 replaced.

Shared auth existed but was buried: enabling it meant hand-editing
`default.config` or finding the checkbox in the config UI's skills
screen, and per-project login taxes exactly the use byre is pitched on
(ADR 0017's own framing). The moment the user picks an agent is the
natural moment to ask -- and an explicit, default-No question is *more*
aligned with 0017's "explicit hand-over, never ambient inheritance"
than a setting discovered later. ADR 0007 stays closed: the offer
changes where the user answers a question, not where credentials come
from -- byre still reads nothing from the host, and Claude's token is
still pasted by the user at a box's firstrun prompt.

What survives of this design (the rest is ADR 0025's):

- **Readiness is declared, not inferred.** A companion skill says
  `shared_auth_for = "claude"` in its skill.toml -- the author's vouch
  that the mechanism is ready to put in front of every user of that
  agent. The `<agent>-shared-auth` naming convention was rejected as
  the trigger: it can't express readiness, and readiness is the whole
  gate -- a skill that failed its field gate (ADR 0023) or is
  gate-pending doesn't declare the key and isn't offered, while staying
  hand-enableable by someone who has read its skill.toml. Two skills
  claiming one agent is refused (no offer): sort order must not pick
  which skill a "y" enables, and a hand-dropped near-namesake must not
  shadow the vetted builtin. Declaring `shared_auth_for` is the entire
  rollout of an agent's offer.
- **The offer's placement and interactivity contract.** It sits
  directly after the agent question it belongs to; it appears only on a
  TTY, only when unanswered, and only on runs that were already
  interactive -- a fully-flagged onboarding keeps its zero-prompt
  contract. Every answer is collected before anything is written, so
  EOF anywhere in the picker aborts onboarding with no side effects.

The superseded machinery -- the machine-wide `default.config` write, the
`shared_auth_declined` decline sentinel, and their surgical-write rules
-- is replaced by ADR 0025's per-box grant and favourite; see there for
what a yes writes today and how v0.1.7-era configs are honored.
