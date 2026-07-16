# Named layers and the extends chain

A **named layer** is a user-authored cascade layer at
`~/.byre/layers/<name>/layer.config` (directory form -- payload files
for `files = {...}` may sit beside it). Any layer file -- and the
project config, which is the chain's leaf -- may name at most ONE
parent via the scalar `extends = "<name>"`; byre walks the pointers to
the root and merges root-first, so the cascade is

```
default ⊕ template ⊕ chain(root … parent) ⊕ project
```

Chains are arbitrary length and strictly linear: no lists, so no
diamonds and no linearization rule to explain. Cycles and dangling
parents are hard errors -- the cycle error names the loop, the dangling
error names the exact path to create. Decided 2026-07-16 (grilling
session, Pete + Claude).

## Why

One employer, many projects: a central baseline (skills, egress, env,
mounts -- the FULL config vocabulary), each project adding on. Today's
cascade was fixed at three layers (`default ⊕ template ⊕ project`, ADR
0003's stores); there was nowhere between the global default and a
project to put a shared, user-authored baseline. Presets don't cover
it: a preset is an apply-time snapshot with a consent ceremony, and the
baseline needs to be LIVE -- edit the employer layer once, every
extending project's next develop picks it up.

## The decisions

**Live layers, not preset-copies.** Layers resolve at every develop,
like the template slot. Edits propagate with NO ceremony -- no drift
notes, no re-review. Same trust position as editing `default.config`:
the threat model is the agent, never the user. Presets remain the
apply-time consent ceremony and may reference layers (`extends` in a
preset body); a preset naming a layer the machine doesn't have fails
loudly at apply with the exact path to create -- a hard failure, not
the missing-package warn-and-continue, because the chain feeds the
grant review and a review that hasn't seen the layer would vouch for a
box it hasn't seen.

**Plain files, not packages.** No `[package]` table, no version, no
pack/install/fork verbs, no digests (ADR 0029's boundary stays where it
is: packages are the distributable, third-party-trust shape).
Distribution is sending someone the file. If installable layers are
ever wanted, that is a future ADR facing the third-party-composition
consent question head-on.

**The template slot survives unchanged.** Orthogonal stacks compose
without multi-parent extends: the employer chain rides `extends`, the
language shape rides `template`. Widening `extends` to a list is a
backward-compatible future step if a real two-chain need appears; not
paid for now (proportionality).

**Key bans.** `template` is banned in a layer file (loud parse error) --
shape selection has exactly one owner, the project config. `extends` is
banned in `template.config` (a distributable package must not pull in
machine-local composition) and in `default.config` (the chain slot is
the project's; a second, global chain would be a silent surprise in
every project). `extends` is the only pointer key a layer may carry;
picker-state strip rules (`shared_auth` etc.) apply unchanged, and a
resolved config never carries `extends` -- resolution consumes it.

**Reserved names.** A layer may not take a BUNDLED package bare name
(`byre layer new` refuses; a hand-dropped squatter dir is never loaded
and `byre layer list` says why) -- a layer named `go` or `claude` would
look like the template/skill of the same name. Retired names are
deliberately NOT reserved (Pete, 2026-07-16): the retired table exists
to protect package-namespace continuity, and layers are a new namespace
nothing predates -- `codereview` is a fine layer name. No shadowing
rule needed because shadowing cannot arise: `extends` only ever names
layers.

**Merge mechanics: nothing new.** Each chain layer is one more fold
step under ADR 0018's exact rules -- scalars last-wins, lists union,
`!name` / `remove = true` removals apply against everything merged so
far, later layers can re-add. `[sources]` hints are stamped with the
contributing layer's name (`hint from layer torn`).

**Consent surface.** Attribution everywhere names the layer: `byre
status` prints the resolved chain (`Extends: torn -> torn-frontend ->
project`), the preset review prints it and shows layer-contributed
grants like any cascade grant, the config UI tags inherited rows
`layer:<name>`, missing-package remedies carry the hinting layer.

**Self-edit exclusion.** Layer files are OUTSIDE the `--self-edit`
writable set (which mounts only `~/.byre/projects/<id>/`): a boxed
agent must never edit a file that propagates into other projects'
sandboxes. A test pins the layers dir out of every bind. The escape
hatch, if a box legitimately needs to edit layers, is an explicit rw
mount of `~/.byre` -- a visible grant that documents itself.

**CLI surface.** `byre layer new <name>` (stub + the reserved-name
gate), `byre layer list` (enumerate, flag broken with the reason),
`byre layer validate [name]` (parse + ban list + chain walk). `byre
config` gains an EXTENDS section (pick/change/clear the parent; the
picker offers loadable layers and preserves a dangling value rather
than dropping it) and chain-aware attribution; `byre config --layer
<name>` is the same effective-state editor pointed at a layer --
resolving `default ⊕ ancestors ⊕ <name>`, writing to the layer's file,
descendants deliberately not in view, and its EXTENDS picker excludes
anything that would loop back through the layer. NOT built: `layer
edit` ($EDITOR does it), `layer show` (status/grant review own the
composed view), pack/install/fork. The first-run picker never writes
`extends` (keys with teeth are never picker-written).

## Cites

- ADR 0003 -- host-side stores (layers live under `~/.byre`, outside
  every rw-mounted tree).
- ADR 0018 -- merge semantics and off-switches (chain steps reuse them
  verbatim; the config UI's effective-state machinery gained one more
  sublayer kind).
- ADR 0029 -- the package boundary (layers deliberately stay on the
  plain-file side of it).
