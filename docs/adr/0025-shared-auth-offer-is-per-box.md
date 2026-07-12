# The shared-auth offer is per box; save-default carries the preference

The onboarding shared-auth offer (ADR 0024) is rescoped: the question is
"Opt this box into <agent> shared credentials? [y/N]", and its answer
applies to **this box** -- a yes puts the companion skill in **this
project's `byre.config`** `skills`, a no enables nothing. The offer
itself never writes machine-level state. The existing "Save these as
your default for new projects?" question is what scales the answer up:
saying yes to it saves ALL the answers just given -- template, agent,
and, when the offer was asked, the shared-auth answer. A saved yes puts
the companion in `default.config`'s `skills` (new boxes get shared
credentials from the cascade; the offer stops appearing); a saved no
records the agent in `shared_auth_declined` (new boxes aren't offered).
Decided 2026-07-12, superseding ADR 0024's recording mechanics; the
readiness gate (`shared_auth_for`, the author's vouch) and the offer's
placement directly after the agent question are unchanged.

ADR 0024 scoped the answer to the machine because the identity volume is
machine-scoped -- but that conflated the *mechanism's* scope with the
*decision's*. The user at a first-run picker is onboarding one project;
the only thing the offer can meaningfully ask is whether THIS box rides
the shared login. Recording that answer machine-wide stretched one box's
"y" into every future box's silent default, and one box's "n" into a
permanent machine-wide never-ask -- a decline the Enter-through-the-
picker default made without the user ever consciously choosing it. That
is a default grant (or refusal) manufactured from a single project's
answer: exactly what deny-by-default forbids. Machine-wide IS still a
preference worth one question -- so it rides the question that already
exists for exactly this ("save these as your default"), where saying
nothing saves nothing.

The mechanics follow the scope:

- **The offer's "yes" is the companion skill in the project's
  `byre.config`** -- the same first-class representation a hand-enabled
  skill uses, written by the same `WriteProjectConfig` that writes
  template/agent, so the per-box outcome lands in one atomic file
  creation. The offer's "no" writes nothing. EOF anywhere in the picker
  still aborts with nothing written.
- **Save-default is the only writer of machine-level shared-auth
  state.** A saved yes appends the companion to `default.config`'s
  `skills` via the surgical list editor (re-parses and verifies its own
  edit; refuses shapes it can't follow); a saved no appends the agent to
  the picker-owned `shared_auth_declined` the same way. The save
  confirmation says out loud what new boxes will do and where to undo
  it -- this is the one save whose effect is not a mere pre-selection.
  Deleting either entry re-arms the offer.
- **The offer is skipped when a machine default settles it**: the
  companion already in `default.config`'s `skills` (saved yes, or
  hand-set -- the cascade gives the box shared credentials regardless),
  or the agent in `shared_auth_declined` (saved no). The save-default
  question in turn always appears when the offer was asked: the gate
  guarantees an asked offer's answer isn't recorded anywhere yet, so
  "these" -- all the answers, shared auth included -- is always news.
- **v0.1.7 records keep their meaning.** A 0.1.7 "y" is a companion in
  `default.config` `skills`; a 0.1.7 "n" is a `shared_auth_declined`
  entry. Both now read as saved defaults -- suppressing the offer, as
  they did -- so upgrading changes no one's effective behavior; what
  changes is that new answers only go machine-wide through the explicit
  save consent.

Consequences: the picker's question count is unchanged (the save
question now also fires when only the shared-auth answer is news --
e.g. a favourites user Enter-ing through template/agent). The
fully-flagged zero-prompt contract is unchanged (no prompts, no offer,
no machine-level writes). The wording deliberately drops the companion
skill's name: which skill implements the mechanism is config plumbing,
legible in the written config, not part of the decision.
