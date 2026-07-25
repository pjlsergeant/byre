# seed_prefs is an ordinary tri-state scalar

Decided 2026-07-25, grilled with the maintainer. `seed_prefs` -- the opt-in
for the one-time copy of an agent's curated non-secret pref files into a
fresh state volume (ADR 0013) -- now merges like every other scalar: an
explicit later value wins, `false` included; unset inherits. A project
under a machine-wide `seed_prefs = true` can finally say "not this one".

Principles: dependencies don't make design decisions (P7) -- this wart was
the type case of a decode-representation gap leaking into user-facing
cascade semantics and then being documented as intent.

## What it was

The field was a plain Go bool, so decode could not distinguish "unset"
from "explicitly false", and Merge compensated with `base || over` -- a
monotonic opt-in no later layer could turn off. The code comment admitted
the cause ("a bool can't distinguish unset from false"); the user docs
then sold the consequence as design: "one deliberate exception:
seed_prefs is a monotonic opt-in." The 2026-07-25 audit (absorbed into
ADR 0044) classified this as a limitation laundered into a decision: the
TOML binding was never even the limiter -- key-presence detection existed
and was already used elsewhere in the same file.

## The decision

Represent the tri-state honestly: `SeedPrefs *bool`. The decoder sets the
pointer only when the key is present, Merge is plain pointer-override
(`over` non-nil wins), and consumers read `SeedPrefsEnabled()`. The
config editor's reconcile writes an explicit `true` or `false` and
removes the key for "inherit".

Behavior change, deliberate: an explicit `seed_prefs = false` in a
project config was a silent no-op under an inheriting `true`; it now
wins. Nothing else moves -- the seed itself remains one-shot,
off-by-default, fresh-volumes-only (ADR 0013 untouched).

The alternative -- keeping monotonicity but re-justifying it as product
("a one-shot seed is deliberately hard to un-ask") -- was considered and
rejected: the cascade's core promise is that a later layer can overrule
an earlier one, this was the only scalar that broke that promise, and no
recorded reason wanted it broken.
