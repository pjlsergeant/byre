# The config editor shows effective state; every cascade list has an off-switch

Decided 2026-07-08. Two halves of one decision: the config UI's screens
show the *effective* resolved state while editing exactly one layer, and
the cascade's list semantics are completed so that "turn this inherited
thing off from here" is expressible everywhere the UI implies it.

## The problem

The editor is layer-scoped (it edits one file), but presents itself as
"your config". Before this decision only the skills screen told the
truth: it rendered effective checkboxes with `(inherited)` tags and
mapped each toggle onto one legible change to the open layer (found live
2026-07-07 -- a globally-enabled skill showed as unchecked in every
project). apt/env/mounts/ports still showed the open layer's raw
entries: inherited entries were invisible, and the form's summary counts
were layer deltas masquerading as totals. Skill-contributed runtime
state (a mount, an env var) was invisible in exactly the screens
claiming to list mounts and env. For a tool whose product is legibility
(PRINCIPLES 4), screens that misreport what the box will actually get
are broken product, not missing polish.

## Decision

**Editor principle.** Every configui screen shows the merged effective
state -- lower layers, the open layer, and skill contributions -- and
every interaction edits the open layer only, peeling one layer of state
per press (the skills screen's model, generalized). Inherited rows carry
a provenance tag (`(default)`, `(template:go)`); skill-contributed rows
show read-only as `(skill:name)` -- their off-switch is disabling the
skill, and the tag names which one. Where the cascade offers no
off-switch for a field, the row says so and names the layer to edit; the
editor never writes another layer's file. On the list screens, enter
opens a per-row action menu offering only what that row supports (terse
labels -- Edit, Delete, Override here, Remove in this project, Restore --
plus a "where it's set" attribution line), with single keys kept as
accelerators; the skills screen stays a plain checkbox toggle, since
toggle is its only verb. Summary rows count effective
state, not raw layer entries (`5 packages (3 inherited)`), same as the
skills summary. List screens resolve skills the way the volumes screen
already does, and degrade the same way when the engine/config won't
resolve.

**Cascade semantics.** Completing the off-switches:

- `apt` (and `npm_global`) gain `!name` removal, the same marker skills,
  mounts, and volumes already use. Unambiguous: `packageRe` has never
  admitted a leading `!`, so no real package collides with the marker.
- `ports` gain removal via a field -- `remove = true` on an entry, keyed
  by **container port alone**:

  ```toml
  [[ports]]
  container = 3000
  remove = true
  ```

  A port has no string identity for a `!` prefix to ride (`container` is
  an int), and the user intent is "this project must not expose the
  inherited 3000" regardless of how a lower layer bound it -- so removal
  deliberately ignores interface and host port.
- `env` stays override-only. A project can override an inherited key's
  value (including to empty) but cannot unset it. Deferred, not refused:
  override-to-empty covers the known cases, and the natural spellings
  are bad (`env` is a TOML table, so `!KEY` doesn't parse as a key and a
  magic value like `KEY = "!"` collides with legitimate values). Design
  it when a real "must not exist" case shows up.
- Raw blocks stay append-only (unchanged).

## Rejected

- **A `!` spelling for port removal** (e.g. `container = "!3000"` or a
  marker smuggled into `interface`): buys idiom purity at the price of
  changing a field's type or corrupting docker-facing grammar with a
  byre word -- the same trap ADR 0015 refused for mounts. The glossary
  carries the two-vocabulary rule instead: `!name` where identity is a
  string, `remove = true` where it's structured.
- **Leaving apt/ports as visible dead-ends** (show inherited, offer
  nothing): legibility without agency -- the UI would display a problem
  it refuses to let you fix, and the fix (edit the shared default, which
  changes every project) is disproportionate to "not in this project".
- **Cross-layer editing** (the project editor writing default.config on
  your behalf): breaks the one-file save model and makes ctrl+s's blast
  radius invisible. Pointing at the right layer keeps every write
  scoped to the file on screen.

## Boundaries left intact

- `disabled` on a mount (ADR 0015) stays a same-entry switch, distinct
  from removal: disabled entries remain visible config; removed entries
  are gone from the resolved set.
- Skill-contributed state stays unaddressable from config entries; the
  trust story for skill grants is the skill trust surface (TODO), not
  per-row toggles.
- The `!name` marker's merge position is unchanged: removals apply after
  additions within a layer, so `["x", "!x"]` still resolves off.
