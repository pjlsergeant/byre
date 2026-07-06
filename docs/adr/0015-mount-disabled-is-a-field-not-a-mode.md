# Mount disabling is a field, not a mode value

A mount can be switched off while staying in the config: `disabled = true`
on the entry. It keeps its place in `byre.config` and in `byre status`
(marked), produces no bind, and is skipped before host-path expansion so
an absent host path (unplugged drive, unmounted share) can't block
develop. Decided 2026-07-06 for long-lived mounts toggled on and off
without deleting them.

The natural spelling was a third `mode` value -- the config UI's picker
literally reads `[ro] [rw] [disabled]` -- and it was rejected for the
TOML schema. `mode` is docker's own bind grammar passed through to the
engine verbatim; "disabled" is byre's word, and smuggling it into
docker's grammar would make every `Mode` consumer (status, the bind
builder) learn a value the engine must never see. Worse, it destroys
information: `mode = "disabled"` overwrites the `rw`/`ro` underneath, so
re-enabling a hand-edited entry means remembering what it was. As a
separate bool, the toggle is one field flipped in place and the mode
survives the off state. The UI still shows one tri-state picker; picking
`disabled` sets the bool and preserves the stored mode.

Boundaries this leaves intact: `!target` removal stays the cascade's
deletion mechanism (a later layer removing an inherited entry, invisible
afterward) -- `disabled` is a same-entry switch, visible by design.
Disabled entries still validate and still collide with skill mounts at
the same target: they are config, just not grants (nothing is widened,
so they stay out of grant accounting). Disabling *skill*-contributed
mounts is out of scope -- skill mounts join after the cascade and aren't
addressable from config entries.
