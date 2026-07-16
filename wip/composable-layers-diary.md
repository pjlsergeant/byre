# Diary

## 2026-07-16 -- composable layers design pass (grilling)

Grilled the "Composable box configurations" TODO item with Pete.
Full decision record: `wip/composable-layers.md` (awaiting his
confirmation before implementation). Headlines: named layers are plain
user-authored files under `~/.byre/layers/<name>/` (NOT packages), full
config vocabulary, live-resolved every develop. Chaining via scalar
`extends` (linear chains, arbitrary length, no lists/diamonds); the
project config is itself a layer and may extend one parent. Template
slot survives unchanged (not subsumed). Cascade:
default ⊕ template ⊕ chain ⊕ project. No ceremony on layer edits;
layers excluded from --self-edit writable set. CLI: `byre layer
new|list|validate`, `byre config --layer <name>`, EXTENDS section in
`byre config`.

Design dead-ends worth remembering: I first proposed a `layers = [...]`
list in the project config + flat no-nesting rule; Pete pushed to
single-parent `extends` chains with the project config as just another
layer. Multi-parent extends was considered and rejected (template slot
already covers the orthogonal-shape combination; list widening stays
available later, backward-compatibly).

Next: Pete confirms -> implement per the sketch in the wip file
(config chain loader first, golden merge tests, then CLI + docs + ADR).
