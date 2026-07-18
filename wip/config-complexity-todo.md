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

- [ ] Config layer: shared split/replace/merge/reopen/close/stale-marker ops over
      named closable declarations (`internal/config/mcp.go:280` vs
      `internal/config/claudeskills.go:110`).
- [ ] Effective-set construction incl. skill-contribution union and post-union
      closures.
- [ ] UI: one effective-row state machine feeding both (`internal/configui/effective.go`
      -- `mcpRows` / `claudeSkillRows`, ~170 parallel lines).
- [ ] Commands: one layer-edit lifecycle (choose layer -> parse -> replace/close ->
      validate -> save atomically -> report) shared by `internal/commands/mcp.go`
      and `internal/commands/claudeskill.go`; only parsing/validation/rendering/
      delivery stay per-kind.
- [ ] Acceptance test: a third named-declaration class would touch only its
      kind-specific hooks.

### 3. Compatibility sunset (the one removal we endorse)

Adopt a window (proposal: two minor releases or 90 days, warning in the last
supported release; removal = parser field + migration + command + catalog/UI
state + tests + docs together, with release-note recovery path). Then execute
the first bundle:

- [ ] Remove `SharedAuthDeclined` (parsed, read by nothing -- `internal/config/config.go`).
- [ ] Drop the legacy shared-auth array shape (`internal/config/sharedauth.go`);
      warn one release first.
- [ ] Retire adoption-record migration / decline-record deletion
      (`sweepAdoptionRecords` in `internal/packages/store.go` runs on every
      ordinary store setup).
- [ ] Stop accepting repo-root `byre.config` as a legacy preset name (after warning).
- [ ] Retire `skill update` (transitional no-op) and description-only compat
      skill stubs.
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

- [ ] Centralize field metadata + target placement first (cheap, kills the
      independent declarations in `form.go:279` and the `fieldID`/`isListField`/
      `fieldLabel` switches).
- [ ] Then fold the `listitem.go` per-field switches (`removeHere`, `startOverride`,
      `deleteItem`, `startItem`, `commitItem`, `itemTitle`, `itemLabel`,
      `itemNotes`) into descriptor hooks.
- [ ] Acceptance test: adding a simple list field is one descriptor + tests, not
      ~20 touch points.

### 5. Explicit dependencies instead of callback globals

- [ ] Replace `config.BundledFS` / `ByreVersion` / `ByreCompat` and the
      `packages.Stage2Skill` init-wiring (`internal/skills/skills.go`) with a
      constructed loader/parser registry passed into resolution and acquisition.
      Small, mechanical, makes ownership visible; good to pair with item 2 since
      both touch the config/skills/packages seams.

### 6. Comment hygiene (opportunistic)

- [ ] In files touched by the work above, keep invariant comments, delete
      process provenance ("round 3", reviewer names, same-day reversals,
      discovery dates). Not a standalone sweep -- ride along with each refactor.

## Accepted with modification

### 7. Split the `Config` lifecycle states -- staged, after item 2

The review's Layer/ProjectConfig/GlobalConfig/ResolvedConfig split is the
highest-value architectural change *and* the highest-risk one. Don't big-bang
it. Sequence:

- [ ] First: kill the `layer bool` validation flags by splitting validation
      entry points, not yet types.
- [ ] Then: move internal merge state (`EgressClosed`, `MCPClosed`,
      `ClaudeSkillsClosed`, `Sources.From`) out of the persisted struct so
      Merge no longer tolerates its own output as input.
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

## Suggested order

1 (now) -> 2 -> 3 first bundle (parallel with 2) -> 5 -> 4 -> 7 staged -> rest
opportunistic. Item 2 is the proof the system can absorb its own patterns; if
it goes badly, revisit before attempting 7.
