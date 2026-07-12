# The shared-auth offer is per box

The onboarding shared-auth offer (ADR 0024) is rescoped: the question is
"Opt this box into <agent> shared credentials? [y/N]", a **yes** puts the
companion skill in **this project's `byre.config`** `skills` list, and a
**no** records nothing at all -- the next project's onboarding asks about
its own box. Nothing machine-level is written either way. Decided
2026-07-12, superseding ADR 0024's recording mechanics; the readiness
gate (`shared_auth_for`, the author's vouch) and the offer's placement
directly after the agent question are unchanged.

ADR 0024 scoped the answer to the machine because the identity volume is
machine-scoped -- but that conflated the *mechanism's* scope with the
*decision's*. The user at a first-run picker is onboarding one project;
the only thing they can meaningfully consent to at that moment is
whether THIS box rides the shared login. Recording their answer in
`default.config` stretched one box's "y" into every future box's silent
default, and one box's "n" (`shared_auth_declined`) into a permanent
machine-wide never-ask -- a decline the Enter-through-the-picker default
made without the user ever consciously choosing it. That is a default
grant (or a default refusal) manufactured from a single project's
answer: exactly what deny-by-default forbids. Consent is per box;
mentioning the wider scope in the wording was visibility, not consent.

The mechanics follow the scope:

- **A "yes" is the companion skill in the project's `byre.config`** --
  the same first-class representation a hand-enabled skill uses, written
  by the same `WriteProjectConfig` that writes template/agent, so the
  whole onboarding outcome lands in one atomic file creation. No second
  writer, no machine-level side effects, and EOF anywhere in the picker
  still aborts with nothing written.
- **A "no" is not recorded.** Onboarding runs once per project, so the
  question is naturally asked once per box -- no never-nag bookkeeping
  is needed. The never-ask-again record and the surgical
  `default.config` list editor that maintained it are deleted.
- **The offer is skipped when the companion is already enabled
  machine-wide** (in `default.config`'s `skills`, hand-set or a v0.1.7
  "y"): the cascade gives that box shared credentials regardless, so the
  per-box question would be offering a switch already thrown.
  Machine-wide enablement remains a legitimate, explicit, hand-edited
  (or `byre config`) choice -- onboarding just never makes it for you.
- **`shared_auth_declined` is vestigial.** v0.1.7 pickers wrote it;
  nothing reads it anymore. The key stays decodable (config parsing
  rejects unknown keys, and configs v0.1.7 wrote must still parse) and
  the resolver still strips it from every resolved config. An existing
  entry simply no longer suppresses anything -- the affected user is
  re-asked once per new box, which is the intended behavior.

Consequences: the picker's question count is unchanged, but the offer
can now appear at every project's onboarding (when the companion isn't
on machine-wide) instead of at most once per machine -- the per-box ask
IS the feature, and a "y" costs one keystroke. The fully-flagged
zero-prompt contract is unchanged (no prompts, no offer, and now also
provably no machine-level writes). The wording deliberately drops the
companion skill's name: which skill implements the mechanism is config
plumbing, legible in the written `byre.config`, not part of the
decision.
