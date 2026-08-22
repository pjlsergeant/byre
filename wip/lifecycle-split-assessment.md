# Full Layer/Resolved type split: pays or doesn't? (assessment for Pete)

Provenance: written 2026-08-23 at the end of the overnight ADR 0049
closeout, per the ruling "staged move + written assessment; the split
itself waits". Delete on ruling (wip/ discipline): the decision lands in
ADR 0049's staged-work disposition, not here.

## What the staged move (shipped tonight) achieved

- `config.Config` is a RAW LAYER again: the four closure fields
  (`EgressClosed`, `MCPClosed`, `ClaudeSkillsClosed`, `ContextsClosed`)
  are gone from it. Merge bookkeeping lives in `config.Closures`,
  produced beside the merged Config and threaded through the fold.
- `MergeStep(base, baseClosures, over)` replaced `Merge(base, over)`:
  `over` is always a raw layer by contract, so the fold never eats its
  own output — the exact invariant ADR 0049 staged this for.
- `config.Merged{Config; Closures}` is the resolved view. `Load` and
  `ResolveProposed` return it; skills' subtraction (`MCPSet`,
  `ClaudeSkillSet`), the build pipeline, commands' `resolved` view, and
  configui's live re-merge (`lowerFold`/`lowerNow`) all consume it.
- `SourceHint.From` deliberately did NOT move (grok's L1, taken): it is
  layer ATTRIBUTION stamped before the fold, legitimately present on raw
  layers — different genus from closure bookkeeping.
- Growth guards updated on purpose: `TestMergeCoversEveryField` covers
  Config; `TestMergeStepThreadsEveryClosureGenus` covers the accumulator;
  the commands claims guard walks `config.Closures` beside `config.Config`.

## What a FULL type split would add

Distinct `Layer` and `Resolved` TYPES (not one `Config` playing both
roles), so the compiler refuses a raw layer where resolved semantics are
assumed and vice versa. Tonight's `Merged` embeds `Config`, so promotion
keeps every field read working — which also means a bare `Config` can
still be handed to code expecting resolved content (e.g. through the
`Config.Validate` shape-check shim, or helpers like `resolveHostEnv`
that take `.Config` off a `Merged`).

## The evidence from tonight's migration

- ~40 files touched; the overwhelming majority of call sites needed only
  a type rename or a `.Config`/`.Closures` selector. Embedding carried
  the rest. A full split would repeat a migration of this size while
  also duplicating the field set (or introducing a shared core struct
  both types embed — which reintroduces the promotion ambiguity the
  split exists to kill).
- The bug CLASS the ADR named — merge state riding the persisted struct,
  Merge tolerating its own output, a re-fold dropping closures — is
  closed structurally by the accumulator. No recorded byre bug is of the
  residual class (raw layer consumed as resolved); the near-misses in
  the review record were all closure-dropping, which the sidecar fixes.
- The validator split (`ValidateLayer` vs `Merged.Validate`) already
  encodes the lifecycle distinction at the one place it repeatedly bit.

## Recommendation: the full split DOESN'T pay now

The staged move bought the invariant; the split would buy compile-time
enforcement against a mistake class with no recorded instance, at the
cost of a second ~40-file migration and a permanent two-type API.
Revive trigger: the first real bug where a raw `Config` was consumed as
resolved (or a `Merged` re-entered a fold as a layer) — that is the
evidence the compile-time fence earns its keep.

## The other deferred decision: full catalog signature-threading (item B)

Ruled "decided in this assessment". Recommendation: not now. The loud
nil-loader error closed the defect (silence); production has exactly one
entrypoint and it installs the loader at init. Residual: `commands`
builds a catalog and `config.Load` builds a second via the seam (one
redundant store walk per resolve). Threading `*packages.Catalog` through
`Load`'s signature kills that double-load and is mechanical — do it if
(a) resolve latency ever shows up in use, or (b) a second binary/
entrypoint appears. Both triggers are observable; neither is tonight.
