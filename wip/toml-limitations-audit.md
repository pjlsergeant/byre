# TOML-binding limitations audit (PRINCIPLES.md #7)

Status: ACTIVE -- inventory feeding the renderer/comments/bool-unset
decisions. Dispatched by Pete 2026-07-25 alongside principle 7
("dependencies don't make design decisions"). Reviewed 2026-07-25 by
BOTH external reviewers (codex, grok); their verified corrections and
missed-instance finds are folded in below, marked [review]. Absorb into
the resulting ADR(s), then delete.

Citations are by SYMBOL with a nearby quote where it matters -- both
reviewers flagged that bare line numbers had already drifted within a
day.

Classes:
- **A owned-around** -- byre already built its contract around the gap.
- **B apologized-for** -- apparatus warns about or works around the gap;
  the decision itself was never made.
- **C leaked-into-semantics** -- the gap became user-facing behavior,
  sometimes documented as if chosen.
- **D near-miss** -- the gap almost dictated architecture; caught.
- **E examined, not an instance** -- judged genuine design, or a
  constraint of TOML-the-format (which is a product choice).

## 0. [review, grok] SharedAuthPref save corruption -- BLOCKER (C)

`SharedAuthPref` has a dual-shape `UnmarshalTOML` and NO marshaler, so
`configui.Save` reflection-encodes its exported fields. A
`default.config` carrying the onboarding-written table form
(`[shared_auth]` / `claude = "..."`) re-emits after any global-editor
save as:

    [shared_auth]
      [shared_auth.Pick]
        claude = "claude-shared-auth"

...which `Parse` then REFUSES (`shared_auth.Pick: want string, got
map[...]`). **Reproduced in-repo 2026-07-25**: parse OK -> Save ->
re-parse fails; the global config is bricked by a normal save until
hand-repaired -- which P6 says users are never expected to do. Worse
than every comment-loss item; also a direct P6 violation ("round-trip
untouched any key it does not yet structure").

Disposition candidates: a `MarshalTOML` on the type emitting the shape
`UnmarshalTOML` reads (stop-gap, same week), and/or the renderer owns
the emission. Open sub-question: a mixed Yes+Pick in-memory state has
no single-shape encoding -- decide canonical form (the onboard surgical
writer already embodies one; single owner). NOT acceptable: stripping
on save (deletes the user's remembered favourite).

## 1. Comment and layout destruction on every re-marshal (B + C)

The encoder cannot round-trip comments or formatting; any re-marshal
normalizes the file and deletes every comment.

Evidence (all reviewer-verified):
- `configui.Save` re-marshals the whole layer; the "Managed by" header
  fronts every save. [review, codex: the header is arguably an
  ownership notice, not an apology -- its class depends on the intended
  file contract.]
- `handComments`/`commentWarn` (configui.go / form.go): warning
  apparatus built solely to disclose the destruction -- and it detects
  FULL-LINE comments only; inline comments (`agent = "claude" # why`)
  are destroyed silently by BOTH paths, acknowledged in the
  `handComments` doc comment. [review, codex]
- `saveDeclLayer` -> `configui.Save`: `byre mcp add|remove` and the
  claude-skill verbs re-marshal with NO warning at all.
- `PresetApply` writes the reviewed raw bytes -- preset comments
  survive apply, until the recipient's first save.
- `LayerNew` writes an instructional comment-rich stub whose prefixes
  are not in `byreBoilerplate`: the first `byre config --layer` cries
  wolf about "hand-written" comments byre itself wrote, then destroys
  the instructions.
- [review, both] **Key/layout ordering is encoder policy, not byre's**:
  top-level key order = Go struct field order, map keys
  ([env], [sources], headers) sorted alphabetically, encoder
  indentation throughout. Human-facing diffs (presets, shared layers,
  self-edit exit diff) are shaped by dependency accident; a dependency
  upgrade could churn every diff with zero semantic change.

Dispositions to decide:
- Style-preserving editing would fully close it. Research spike target
  recorded [review, grok]: pelletier/go-toml v2 documents an
  `unstable/edit` API for comment/whitespace-preserving edits --
  unstable is not mature, but the map is not blank.
- Interim floor regardless: warn on the CLI-verb save path, add the
  layer stub to the boilerplate list. [review, codex: P7 calls warnings
  "the tell" -- if shipped, label as temporary containment with a
  removal condition, or it becomes the apparatus the audit condemns.]
- A byre-owned canonical renderer narrows the loss to comments only:
  layout becomes designed and stable.

## 2. No multiline string emission -- the type specimen (D)

Encoder emits any newline-bearing string as a `\n`-escaped one-liner
(probed against v1.4.0; `writeQuoted` is the only string path -- no
multiline basic or literal emission exists [review, codex verified in
the pinned module source]). Nearly drove `[[context]]` storage to
sidecar files, whose preset story then failed. TOML-the-format has
`'''` literal strings designed for prose; only the binding lacks them.

Disposition (pending renderer ADR): byre owns `[[context]]` emission --
multiline literal with escaped-form fallback, round-trip pinned.
[review, both] Two overclaims corrected:
- "One seam" holds only for the ENCODER: `configui.Save` is the sole
  `toml.NewEncoder` call. But config EMISSION has several seams --
  `onboard.WriteProjectConfig` (hand-rendered), `onboard.SaveDefault`
  and `SaveSharedAuthDefaultPick` (surgical line writers), preset apply
  (raw bytes), `LayerNew` (stub). See item 6a: the surgical writers are
  prior own-the-seam art, not obstacles.
- "Parse is single-owner" is true for CASCADE LAYERS (`config.Parse`);
  skill/package manifests and probes decode TOML separately, by design.

[review, grok] Aspirational-prose sweep when the renderer lands:
`contextdecl.go` ("TOML multi-line strings keep it readable") and ADR
0043 ("inline TOML (multi-line strings)") describe the FORMAT's
capability, which current emission does not deliver -- true again only
once the renderer ships.

## 3. `seed_prefs` is monotonic because a bool decodes zero-valued (C)

The `Merge` comment admits the cause: "A bool can't distinguish unset
from false, so there's no 'turn it back off' in a higher layer"
(`SeedPrefs = base || over`). The docs launder it into intent --
ARCHITECTURE's cascade rules and the site reference
("One deliberate exception: seed_prefs is a monotonic opt-in").

The binding is not the limiter: `md.IsDefined` distinguishes
set-from-unset and byre already uses it (`rejectTemplateComposition`,
`rejectLayerKeys`, `packages.looksLikeAgent`); grok probed
`seed_prefs = false` -> `IsDefined` true. [review, codex] Caveat for
the fix: metadata alone isn't the implementation -- presence must
survive into `Config` through merge (pointer field, or a presence bit
set by `Parse`), and programmatically-built `Config{SeedPrefs: true}`
values need defined semantics.

Disposition to decide: tri-state (explicit `false` wins downward) via
its own ADR -- a user-facing cascade semantics change -- or
accept-on-record. Either is honest ONLY with the docs reworded to stop
claiming deliberateness. [review, grok] Monotonicity might even be good
product for a one-shot seed -- but then accept it BECAUSE of product,
not because "TOML bools can't."

## 4. The `"none"` sentinel for template/agent (E) [reclassed]

[review, codex] Reclassed from "C-lite": empty-scalar-means-inherit is
byre's chosen merge representation (`override()`), not a binding gap --
metadata could distinguish present-empty from absent here too. The
sentinel (`template = "none"`, resolved out via `FromNone`) is a
DESIGN answer, and likely a good one: explicit "none" in a file is more
legible than invisible present-but-empty semantics. Candidate: record
as the worked example of zero-value cascade design done deliberately.

## 5. Raw-block round-trip (E/mixed) [reclassed]

[review, codex] Reclassed from B: `rawSlice` preserving untouched
blocks verbatim is owned-around behavior, and the loss on edit is
primarily the EDITOR's data model (textarea -> `splitLines` -> one
element per line), with original TOML array layout already gone at
decode. A renderer alone cannot reconstruct what the editor model
discarded; the disposition needs an editor data-model decision
alongside the emission contract.

## 6. Additional owned-around instances [review, both -- new]

- **6a. The surgical onboarding writers (A, with residuals).**
  `onboard.SaveDefault` (scalar line replace) and
  `SaveSharedAuthDefaultPick` exist precisely to avoid the destructive
  re-marshal -- prior art for style-preserving edits the audit first
  missed. Residuals recorded: top-level-scalars-before-first-table
  assumption; multiline hand-forms refused (pinned by
  `TestSaveSharedAuthDefaultRefusesMultilineList`); and their error
  remedies tell the user to hand-edit `shared_auth` -- a direct P6
  violation to sweep.
- **6b. Shared-auth `Undecoded` suppression (A).** `config.Parse`
  filters undecoded keys under `shared_auth` because the binding
  reports keys consumed by `UnmarshalTOML` as undecoded -- binding-
  specific apparatus, and it weakens typo detection inside that
  subtree (validation rides the custom unmarshal instead).
- **6c. `omitempty` zero-value collapse, generally (E lean).** On
  re-save, absent vs explicitly-empty distinctions erase across the
  struct. Mostly moot by design -- the cascade's marker grammar exists
  so empties never need to carry "clear" semantics -- but recorded so
  the erasure is a decision, not luck.

## 7. Examined, not instances (E)

- `env` no-unset / `env_from_host` `KEY = ""` disable: marker-grammar
  design. [review, grok] One rationale correction: ADR 0018's "`!KEY`
  doesn't parse as a key" overstates -- TOML quoted keys admit any
  string; the refusal is product grammar, not a format wall.
- `ports` removal via `remove = true`: the entry's identity is an
  integer in the CURRENT schema -- a schema-shaped choice handled
  legibly, not a format wall. [review, codex wording fix]
- Retired-key tolerance; `SharedAuthPref` dual-shape DECODE (migration
  tolerance). Its missing ENCODE is item 0, not an E.

## Suggested decision order [amended per review]

The reviewers split on renderer-first: codex wants the stored-file
CONTRACT defined before any renderer (risk: entrenching full-document
reconstruction before the comments decision); grok endorses
renderer-first with item 0 promoted. These compose:

0. **SharedAuth stop-gap now** (item 0): `MarshalTOML` emitting the
   canonical read shape + round-trip test. Severity does not wait for
   an ADR; the renderer later absorbs it.
1. **Stored-file contract + preservation research, one spike**: what
   must survive a save (comments? ordering? quoting style?), semantic
   vs textual stability distinguished; pelletier `unstable/edit`
   assessed; the surgical writers (6a) assessed as the prototype seam.
2. **Renderer/emission ADR** off that contract -- includes the minimal
   `[[context]]` prose emitter (unblocks the CONTEXT screen; need not
   wait for the universal renderer).
3. **Comments disposition** falls out of 1+2 (build style-preserving vs
   accept-on-record + interim floor with a removal condition).
4. **Bool-unset ADR** (item 3) -- independent semantics change.
5. **Record accepts** (items 4, 6c, 7) so this file can die.

## Doc lines to sweep when decisions land

- ARCHITECTURE cascade rules + site configuration-reference: the
  "deliberate/monotonic" seed_prefs claims.
- `contextdecl.go` + ADR 0043: multiline-readability prose (item 2).
- Onboard shared-auth error remedies ("edit by hand") -- P6 sweep.
- PRINCIPLES.md P6 "written only through the one validated save path"
  -- true-ish for structured saves, overclaims across the surgical/raw
  writers; reconcile when the renderer ADR names the seams.
- configui Save/managed-by/`handComments` prose per the comments
  decision.
