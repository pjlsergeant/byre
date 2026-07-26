# Spec: the claim surface gets a growth guard and a render-from-effect rule

**Status: v3, codex-APPROVED 2026-07-27; the `[env]` x
reserved-`BYRE_*` fork ruled by Pete the same day (Option A, three
tiers -- below). Ready for implementation on Pete's dispatch.** One
point codex reserved for implementation review, recorded so it
survives to that day: tests must demonstrate that each reserved skill
override degrades EVERY claim it can affect -- a key merely appearing
somewhere in status does not discharge tier 2. This is the fix for `philosophical-problems.md` theme 2.
Delete on absorb: mechanism lands in code, rationale in a short ADR,
this file goes.

v1 called move 1 a "completeness invariant." Codex's review made the
correction this project's own doctrine demands: the mechanism is a
**classification growth guard** -- it forces every config field to carry
a reviewable answer, it does not prove any answer true. Overselling it
would be written reasoning wearing the authority of enforcement, i.e.
the exact disease theme 2 diagnoses. Framing below is corrected
throughout; the stronger P4 mechanism is move 2, and the guard is the
fence around it.

## Problem

P4 makes `byre status` the security mechanism -- it stands where a gate
would otherwise stand. But nothing structural connects "a config field
can affect what the box can do" to "a status claim accounts for that
field." The 2026-07-27 review found four independent holes of this one
class (`review-findings.md`); codex's spec review found a fifth
(`exposureOf` counts empty host sources).

## Move 1 -- the claim-classification registry (a growth guard)

A registry in `internal/commands`, next to `statusInfo`: every
`config.Config` top-level field (including the derived `toml:"-"`
closure sets -- they subtract from claims, so they are claim inputs)
maps to either `rendered("<renderer/row family a reviewer can grep>")`
or `inert("<the argument it cannot affect any claim>")`.

Guard: `TestEveryConfigFieldHasClaimClassification` -- reflect over
`config.Config`, fail on any unclassified field or ghost entry. The
name says exactly what it enforces; it does NOT arm P4, and P4's
doctrine-index line stays `[no arm]` until the move-2 work earns
better. What the guard buys: "nobody ever answered the question" stops
being a possible state; the answer is in a diff where review (and the
doctrine check) can call it a lie. Same bargain as the merge guard and
the hostopen allowlist.

**Nested fields (codex change 2):** aggregate types are covered by an
explicit atomic-coverage policy, not recursion: for each aggregate
(`Mount`, `Port`, `Volume`, `MCP`, ...), the registry entry names the
renderer that consumes the *whole effective object*, and a focused test
per aggregate pins that (e.g. a disabled Mount renders disabled, a
closed MCP renders closed). A new field added to an aggregate lands
inside a renderer that already consumes the whole struct -- and the
per-aggregate test is where its omission surfaces. If that ever proves
leaky in practice, recursion is the escalation, on evidence.

## The `[env]` x reserved-`BYRE_*` ruling (Pete, 2026-07-27)

Codex enumerated the real reserved surface: the launcher and profile
shim consume at least `BYRE_WORKSPACE_DIR`, `BYRE_CONTEXT_DIR`,
`BYRE_FIRSTRUN_DIR`, `BYRE_IMAGE_PATH_FILE`, `BYRE_LAUNCH_GATE_TIMEOUT`,
`BYRE_ENVD_DIR`, `BYRE_LAUNCH_GATE_FILE`, `BYRE_EGRESS`. v1's plan
(reassert three, classify `[env]` inert) was unsound -- `Env`
classifies `rendered`, never inert. Ruled: **reserved vocabulary,
never reserved capability** -- three tiers:

1. **User `[env]`: refused at validation.** A `BYRE_*` key in `[env]`
   is a config error. The message carries the exact deliberate
   spelling, not a hint, and says the honest path stays honest:

   ```
   byre.config [env]: BYRE_LAUNCH_GATE_FILE is byre's runtime vocabulary and can't be set here.
   To override it deliberately: run_args = ["-e", "BYRE_LAUNCH_GATE_FILE=/dev/null"]
   (byre status will show the raw flag and degrade the claims it affects.)
   ```

   Doctrine: grammar, the `dockerfile=` precedent (ADR 0014) -- the
   P1 right rides `run_args`, untouched and MORE powerful (runtime env
   beats image ENV; ADR 0006 last-wins). Today's `[env]` route was
   never an exercise of the P1 right, because that right comes bundled
   with degraded claims and `[env]` delivered the weakening without
   the degradation.
2. **Skill typed `[runtime].env`: accepted -- skills are trusted
   machinery with no guarantees on byre's part (P2, the warranty
   model) -- but rendered, and affected claims degrade.** Accept is
   not silent: byre declines to lie on the skill's behalf. Refusing
   here would be theater anyway (raw Dockerfile lines are open to
   every skill); it would block the legible spelling and leave the
   opaque one.
3. **`run_args` / raw blocks: unchanged.** The existing raw-tier
   machinery already shows the flag verbatim and degrades posture
   claims.

The boundary is armable end-to-end: a test greps the generated
launcher/profile scripts for `BYRE_*` reads and fails if any consumed
variable is missing from the Go-owned reserved set, closing the
scripts<->Go coupling codex flagged. The absorbing ADR must state the
line explicitly -- reserved *vocabulary*, never reserved *capability* --
so the first refusal of a semantically-valid config value cannot later
be stretched toward nannying.

## Move 2 -- render and count effective state (the real P4 work)

The general rule, split into codex's two subrules:

1. **Runtime-dependent claims consume shared resolution.** Anything
   resolved at launch is resolved once, into a result carrying source
   and outcome; `runParams` and every status consumer read that result.
   For host env: `resolveRuntimeEnv` with per-key attribution -- the
   result State is a four-way enum, `disabled | overriddenByEnv |
   resolvedEmpty | delivered` (a bare `present bool` invites callers to
   reconstruct precedence wrong), deterministic ordering. Consumers:
   runtime env assembly, `statusInfo.EnvProvided`, `hostEnvRow`, and
   `exposureOf` -- the last was missing from v1 and would have kept the
   exposure tally lying about empty sources. A non-delivered source
   renders as `not passed -- source resolved empty`, never as a grant.
2. **Summaries count only rows marked effective.** The egress row bug
   is not a launch-resolution instance; it is `kind` overloaded with
   provenance, editability, AND effectiveness. Rows get an explicit
   effectiveness state separate from provenance; `rowCounts` /
   `exposureNow` consume effectiveness, so a door closed by a lower
   layer stops counting as an active skill grant while still showing
   its provenance.

No big-bang refactor: the migration list is the status section of
`review-findings.md` plus codex's `exposureOf` addition, worked
finding by finding.

## Out of scope, named

- grok's instance (ADR residuals reaching the user page): shipped
  separately 2026-07-27 as the doctrine-index maintenance rule plus
  backfills.
- Correctness proofs per renderer beyond the per-aggregate atomic
  tests: not this mechanism's job.

## Closure

When this lands: a short ADR absorbs the registry rule ("every config
field answers the claim question at birth") and gets its index line
with `[arm: TestEveryConfigFieldHasClaimClassification]`. P4's line
stays `[no arm]` -- honestly -- until someone can name a mechanism that
actually proves claim truth, which move 2 approaches one claim at a
time. This file is then deleted.
