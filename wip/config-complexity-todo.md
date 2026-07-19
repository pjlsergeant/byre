# Config-complexity review: actionable todo

Status: ACTIVE -- distilled from the 2026-07-18 configuration/feature-complexity
review. Delete when the items below have shipped or been explicitly killed.

Stance (maintainer ruling): **we keep the features we have**. No product cuts --
the package manager, the removal vocabulary, stale-marker rows, one-consumer
skill rails all stay. What we accept from the review is (a) consolidation of
duplicated implementation, (b) retirement of pre-1.0 compatibility layers, and
(c) guardrails against further accretion. Line-count reduction is a side
effect, not a goal.

## Accepted -- do these

### 1. Guardrails (immediate, zero code)

Working rules until the consolidation below lands:

- [ ] No new top-level config classes until MCP/Claude Skills share rails (item 2).
- [ ] No new removal/absence spelling; new absence semantics reuse an existing one.
- [ ] No new compatibility path without a stated removal release.
- [ ] New typed skill fields need a stated justification: a second consumer, a
      security reason raw fields can't carry, or a legibility reason status must
      understand it. (Existing fields -- `containment`, `sock_groups` -- stay.)

These are working rules for now; if they survive contact with a few features,
promote the durable ones into `docs/PRINCIPLES.md` via an ADR.

### 2. Extract the named-declaration genus (MCP + Claude Skills)

The best-bounded win: the docs already call Claude Skills "the MCP genus"; the
code implements it twice, ~verbatim. Unify behind a small internal
generic/callback service (no grand public interface):

- [x] Config layer: shared split/replace/merge/reopen/close/stale-marker ops
      (`internal/config/nameddecl.go`, `namedDeclOps`).
- [x] Effective-set construction incl. skill-contribution union and post-union
      closures (`internal/skills/nameddecl.go`, `declClaims`).
- [x] UI: one effective-row state machine feeding both
      (`internal/configui/effective.go`, `namedDeclRows` over `declRowItem`).
- [x] Commands: one layer-edit lifecycle shared by the mcp and claude-skill
      verbs (`internal/commands/nameddecl.go`, `declVerbs`); parsing/
      validation/rendering/delivery stay per-kind.
- [x] Acceptance: a third named-declaration class plugs in namedDeclOps +
      declClaims labels + declRowItem adapters + declVerbs — no re-implemented
      state machines.

### 3. Compatibility sunset (the one removal we endorse)

Adopt a window (proposal: two minor releases or 90 days, warning in the last
supported release; removal = parser field + migration + command + catalog/UI
state + tests + docs together, with release-note recovery path). Then execute
the first bundle:

- [x] Remove `SharedAuthDeclined`: field/merge/strip machinery gone; the stale
      key parses as a tolerated retired key (ignored).
- [ ] Drop the legacy shared-auth array shape (`internal/config/sharedauth.go`).
      DEFERRED: still round-tripped by EncodeTOMLLine and needs a warning
      release first; no parse-time warning channel exists today.
- [x] Retire adoption-record migration / decline-record deletion (removed;
      old records are inert files, CHANGES carries the recovery path).
- [ ] Stop accepting repo-root `byre.config` as a legacy preset name.
      DEFERRED: the in-product rename note IS the warning; remove at the end
      of its window (a release-time decision, not a code call).
- [x] Retire `skill update` (removed, command page regenerated) and the
      `devloop` compat stub (now a RetiredNames tombstone with the pinned
      install remedy). Note: `grok-shared-auth` turned out NOT to be a stub —
      it's the live v2 auth broker; the review's stub list was stale.
- [ ] Schedule the legacy-materialized-package machinery (ProvLegacy rows,
      `skill archive-legacy`, store-setup detection, retired-name protection)
      for the end of its window -- keep until then, it's user-facing recovery.
- [ ] Inventory each remaining compat feature: origin release, last supported
      release, recovery path. Track it here until an ADR records the policy.

### 4. Descriptorize the config UI (list fields only)

Not a rewrite: absorb the repeated list-field plumbing (apt, env, mounts,
ports, egress, MCP, Claude Skills) into field descriptors owning label, kind,
section, legal targets, and the row/start/commit/remove/delete/render hooks.
Genuinely specialized screens (base image, agent, volumes, skills, raw blocks)
stay specialized.

- [x] Centralize field metadata (`fields.go` fieldInfos: label, kind, TOML-key
      hint, item title, noun — was five independent structures). Target/section
      placement stays in newModel: it is target-specific prose with per-target
      section titles, and a data table would obscure it, not shrink it.
- [ ] Fold the `listitem.go` per-field operation switches into descriptor
      hooks. DEFERRED as a judgment call: after the genus extraction removed
      the mcp/claude-skill duplication, the remaining switches are one
      switch per operation — field-specific behavior in one place each, not
      copies. Folding them adds indirection without deleting code. Revisit
      if a new list field lands and the touch-point count still hurts.
- [ ] Acceptance test: adding a simple list field is one descriptor + tests, not
      ~20 touch points. (Partially met: identity is one table row; behavior
      is still one case per operation switch.)

### 5. Explicit dependencies instead of callback globals

- [x] Stage-2 parser hooks are now an explicit LoadCatalog argument
      (packages.Stage2Hooks); both init() wirings (skills, config) are gone.
      config's three func globals collapsed into ONE documented seam,
      `config.CatalogLoader`, constructed by builtins (the composition point:
      embedded content + version + full parser set). Residual: that one seam
      is still installed by builtins' init — config cannot import builtins
      (cycle); fully removing it requires threading a catalog into
      config.Load's signature, which is item 7's territory.

### 6. Comment hygiene (opportunistic)

- [ ] In files touched by the work above, keep invariant comments, delete
      process provenance ("round 3", reviewer names, same-day reversals,
      discovery dates). Not a standalone sweep -- ride along with each refactor.

## Accepted with modification

### 7. Split the `Config` lifecycle states -- staged, after item 2

The review's Layer/ProjectConfig/GlobalConfig/ResolvedConfig split is the
highest-value architectural change *and* the highest-risk one. Don't big-bang
it. Sequence:

- [x] First: kill the `layer bool` validation flags — every validator is now
      a named Layer/Resolved pair over a shared core parameterized by marker
      policy (config.go, nameddecl.go); messages and precedence unchanged.
- [ ] Then: move internal merge state (`EgressClosed`, `MCPClosed`,
      `ClaudeSkillsClosed`, `Sources.From`) out of the persisted struct so
      Merge no longer tolerates its own output as input. NOT cheap — those
      fields are read across status/UI/commands; needs its own session.
- [ ] Only then decide whether the full type split still pays for itself.
      TOML vocabulary is untouched throughout.

### 8. Carve `internal/commands` by extraction, not decree

The "commands is thin" claim is aspirational. Don't split by command name;
extract services as the work above forces them out:

- [ ] Item 2 naturally produces the layer-edit service (the review's
      `internal/layeredit`).
- [ ] Status-model-vs-rendering and review-model extractions only when a second
      consumer (TUI, preset apply) actually needs them -- not speculatively.
- [ ] Reconcile CLAUDE.md/ARCHITECTURE wording about `commands` when the first
      extraction lands (docs sweep rule).

### 9. Removal semantics: normalize internally, keep every spelling

The user-facing vocabulary (`!name`, `remove = true`, `disabled = true`, empty
source, closures, offered entries, stale-marker rows) all stays -- these are
features with real distinctions. What we take:

- [ ] No new spellings (guardrail above).
- [ ] Nice-to-have, only if item 2 makes it cheap: merge/provenance/UI consume a
      normalized internal tombstone model instead of each interpreting every
      syntax. Do NOT drop stale-marker UI rows -- legibility is the product.

## Rejected / deferred

- **Package-manager scale-back (review §7).** Rejected as a code action. The
  system stays. We accept only the passive posture: don't *expand* distribution
  features until there's usage evidence, and note interesting signals
  (third-party installs, forks, preset chauffeur use) when they show up.
- **Removing one-consumer skill rails (review §5).** Rejected -- the review
  itself concedes `containment`/`sock_groups` pass the security/legibility
  test. The justification checklist (guardrail 1) is the whole takeaway.
- **UI feature cuts** (stale-marker rows, offered rows, legacy problem rows
  inside their support window). Rejected -- legibility features, not accretion.
- **The 1,000-2,000-line target as a goal.** Reduction is a byproduct of items
  2-5; we don't chase a number.

## Review-pass residue (2026-07-19)

The post-refactor code review fixed its findings in-tree (lazy row
callbacks, per-vocabulary claim sentinel, retiredConfigKeys table,
fieldInfos growth guard) and consciously deferred three:

- `config.CatalogLoader` nil-fallback is silent (bundled-less catalog for
  any entrypoint that skips builtins) — the acknowledged residual of item
  5; the real fix is item 7's catalog-threading.
- The catalog→ResolveProposed→skills.Resolve orchestration is inlined at
  three sites with deliberately different error handling (declStillEffective
  guarantees a closure, review degrades to best-effort) — consolidate only
  when their policies converge (item 8's rule: extract on the second real
  consumer).
- ADRs 0024/0029 mention `byre skill update` in the present tense — ADRs
  are point-in-time records this repo does not rewrite; CHANGES.md carries
  the live recovery path.

## Suggested order

1 (now) -> 2 -> 3 first bundle (parallel with 2) -> 5 -> 4 -> 7 staged -> rest
opportunistic. Item 2 is the proof the system can absorb its own patterns; if
it goes badly, revisit before attempting 7.
