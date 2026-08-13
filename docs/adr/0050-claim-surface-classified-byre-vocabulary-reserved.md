# The claim surface is classified, and BYRE_ is reserved vocabulary

> **Amended by ADR 0057** (2026-08-13): credential rows are classified
> rendered (status rows + the exposure tally's credentials segment; ciphertext
> elides and values render nowhere). BYRE_CRED_EXPECT / BYRE_CRED_DIR /
> BYRE_CRED_WAIT join the reserved chassis vocabulary as launch-claim knobs
> with wait/export protocol meaning ONLY — and since the wait is fail-closed,
> skewing them costs a box that refuses to run its agent, never one running
> without the credentials it claimed.

Decided 2026-07-27, absorbing the theme-2 spec from the first outside
review (four independent reviewers; the spec itself was
codex-approved before implementation). P4 made `byre status` the
security mechanism -- the thing standing where a gate would otherwise
stand -- and the review found four independent holes in that claim
surface with one common cause: nothing ever forced the question "does
this input bear on a claim?" to be answered. Each hole was individually
small; the class was structural.

## The registry (a growth guard, deliberately not a proof)

Every `config.Config` field carries a classification in
`internal/commands/claims.go`: `rendered("<where a reviewer can
grep>")` or `inert("<the argument it cannot affect any claim>")`.
`TestEveryConfigFieldHasClaimClassification` fails on an unclassified
field, a ghost entry, or an empty note -- so a new config key cannot
ship without its claim answer appearing in a reviewable diff. The
guard proves the question was ANSWERED, not that the answer is true;
truth is the review's job plus the per-claim tests. Same bargain as
`TestMergeCoversEveryField` and the hostopen conformance allowlist.
Rejected framing: "completeness invariant" -- the reviewer's own
correction; an enumeration guard must not wear a proof's name.

## Render from effect, count effective state

Two subrules, both applied and both citable against new code:

1. **Runtime-dependent claims consume shared resolution.** Anything
   resolved at launch resolves ONCE into a result carrying source and
   outcome; the runtime and every reporting surface read that result.
   `resolveHostEnv` is the type specimen: four states (disabled /
   overridden by `[env]` / resolved empty / delivered), consumed by
   `runParams`, the status Host env row, `providedEnv`, and the
   exposure tally. A configured source that resolved empty renders
   `NOT passed`, never as a delivered grant.
2. **Summaries count only rows marked effective.** Effectiveness is
   its own row property, never inferred from provenance kind: a skill
   egress row closed by a lower layer keeps its menu-less kind but
   tallies as a closed door (`listRow.closed`), so WHO closed a door
   cannot change whether it counts.

## The reserved namespace: vocabulary, never capability

The chassis scripts are parameterized by `BYRE_*` variables (the
launch-gate file, the context dir, the announced egress -- 18 knobs at
count), and config `[env]` bakes straight into their environment: a
one-line config key could switch off the launch gate while status
printed `deny-by-default` unhedged. Ruled three tiers:

1. **User `[env]`: refused at validation.** The whole `BYRE_` prefix,
   with the exact deliberate spelling in the message
   (`run_args = ["-e", "BYRE_X=..."]`). This is byre's first refusal
   of a semantically-valid config value, so the line is drawn here on
   the record: byre reserves its own protocol VOCABULARY (the
   `dockerfile=` shape, ADR 0014); it never reserves the capability --
   the raw tier carries the intent and already degrades the claims it
   affects (ADR 0006 last-wins; P1). This precedent does not stretch
   to refusing config because of what it does.
2. **Skill typed `[runtime].env`: accepted, rendered, degraded.**
   Skills are trusted machinery (P2) -- refusal would block the
   legible spelling while raw Dockerfile lines stay open. But accept
   is not silent: each reserved key gets an attributed `Reserved env`
   row and every claim it can skew stops asserting (network under the
   gate/egress knobs, context/MCP delivery under theirs; unknown
   future knobs degrade conservatively).
3. **Raw tier: unchanged.** Verbatim rows, degraded posture claims --
   the machinery that already existed.

The boundary is pinned from both sides:
`TestChassisScriptKnobsRideReservedPrefix` fails if a chassis script
ever reads an unassigned all-caps knob outside the `BYRE_` prefix
(where `[env]` could reach it around the reservation), and
`BYRE_EGRESS` stays re-asserted at run time so no env source skews
what the box is told byre enforces.

## Consent surface

The apply reviewer is part of the claim surface: a preset's `[env]`
table gets a grant-summary line (keys, baked-into-image caveat), not
just its raw TOML rendering -- consented-to-but-not-authored content
carries the same weight whichever table it rides in (P5's sentence on
rendered grants). This dissolved the review's "third trust class"
question without new vocabulary: no principle change, two renderer
obligations.

## Residuals, accepted

- The registry covers `config.Config` top-level fields; aggregate
  types (Mount, Port, ...) are covered atomically by the renderer
  named in their entry plus that renderer's tests. A nested field
  bearing on a claim independently of its parent's renderer is caught
  at that renderer's review, not by reflection. Revisit on evidence.
- Claims also depend on inputs outside config (skill fields, CLI
  flags, engine capabilities); those ride the existing per-claim
  machinery, not the registry. The registry guards the surface that
  produced all four review findings.
- An `inert()` argument can be wrong; the guard makes it attackable
  in a diff, nothing more.
