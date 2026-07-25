# TOML-binding limitations audit (PRINCIPLES.md #7)

Status: ACTIVE -- inventory feeding the renderer/comments/bool-unset
decisions. Dispatched by Pete 2026-07-25 alongside principle 7
("dependencies don't make design decisions"): a laundry list of places
the TOML binding (BurntSushi/toml v1.4.0) has shaped byre's design,
each awaiting one of P7's three honest moves -- own the seam, replace
the dependency, or accept on the record. Absorb into the resulting
ADR(s), then delete.

Classes used below:
- **A owned-around** -- byre already built its contract around the gap.
- **B apologized-for** -- apparatus exists to warn about or work around
  the gap; the decision itself was never made.
- **C leaked-into-semantics** -- the gap became user-facing behavior,
  sometimes documented as if chosen.
- **D near-miss** -- the gap almost dictated architecture; caught.
- **E examined, not an instance** -- looked at, judged genuine design
  (or a constraint of TOML-the-format, which is a product choice).

## 1. Comment and layout destruction on every re-marshal (B)

The encoder cannot round-trip comments or formatting, so any save that
re-marshals normalizes the file and deletes every comment.

Evidence:
- `internal/configui/configui.go:21-32` -- Save re-marshals the whole
  layer; the "Managed by" header is the standing apology.
- `internal/configui/configui.go:34-66` -- `handComments`/`commentWarn`:
  a user-facing warning built solely to disclose the destruction.
- `internal/commands/nameddecl.go:77` -- `byre mcp add|remove` and
  `byre claude-skill ...` save through `configui.Save` with NO warning:
  a silent-destruction path the TUI's apparatus does not cover.
- `internal/commands/preset.go:168` -- preset apply writes the raw
  bytes, so a preset author's comments survive apply... until the
  recipient's first save.
- `internal/commands/layer.go:53-63` -- `byre layer new` writes an
  instructional, comment-rich stub (including commented-out `# apt = []`
  suggestions). Those comments are byre's OWN, are not in
  `byreBoilerplate`'s expendable list (`configui.go:55+`), so the very
  first `byre config --layer` both cries wolf about "hand-written
  comments" byre itself wrote and then destroys the instructions.

Cost: shared layers and presets are the surfaces where humans write
comments for other humans; byre deletes them on first touch, warns
inconsistently (TUI yes, CLI verbs no), and false-positives on its own
stubs.

Dispositions to decide:
- Style-preserving editing would fully close it, but needs a research
  spike first: no mature Go equivalent of toml-edit is known (verify
  before asserting). P7's coda says the build being big is not the
  argument against it; whether comments matter enough post-P6 (the
  editor is the interface; who writes comments in a file nobody is
  expected to open? -- layer/preset AUTHORS do) is the real question.
- Interim floor regardless: extend the warning to the CLI-verb save
  path, add the layer stub to the boilerplate list.
- A byre-owned canonical renderer (item 2) narrows the loss to comments
  only -- layout becomes designed and stable instead of encoder-luck.

## 2. No multiline string emission -- the type specimen (D)

The encoder emits any newline-bearing string as a `\n`-escaped
one-liner (probed 2026-07-25 against v1.4.0; the encoder has no
multiline support at all -- zero hits in `encode.go`). This nearly
drove the `[[context]]` storage architecture to per-target sidecar
files, whose preset story then failed (machine-local paths cannot ride
a single-file preset). TOML-the-format has `'''` literal strings
designed for exactly this prose; only the binding lacks them.

Disposition (pending the renderer ADR): byre owns emission of
`[[context]]` blocks -- multiline literal strings with a fallback to
escaped form for edge cases (text containing `'''`, control chars),
round-trip pinned by test. Generalize: config emission joins gen's
Dockerfile as a designed, pinned surface; the encoder is used where its
output meets the contract, bypassed where it doesn't. Positive finding
easing this: emission already has exactly ONE seam --
`configui.Save` is the only `toml.NewEncoder` call in the tree, and
parse is likewise single-owner (`config.Parse`).

## 3. `seed_prefs` is monotonic because a bool decodes zero-valued (C)

`internal/config/config.go:760-763` admits the cause outright: "A bool
can't distinguish unset from false, so there's no 'turn it back off' in
a higher layer." The docs then launder it into intent --
`docs/ARCHITECTURE.md:288` and
`site/content/docs/configuration-reference.md:26` call it a
"deliberate exception: a monotonic opt-in."

The binding is not even the limiter: `md.IsDefined` distinguishes
set-from-unset and byre already uses it (`config.go:687-696`,
`layers.go:116`, `packages/catalog.go:473`). We inherited the
convenience of plain struct decode and documented the result as design.

Disposition to decide: tri-state the field (metadata or pointer decode)
so a later layer's explicit `false` wins -- a user-facing cascade
semantics change (ADR + docs sweep + the "deliberate" wording
retracted), or accept-on-record and reword the docs to stop claiming
deliberateness. Note ARCHITECTURE.md:290-291 and PRINCIPLES.md P5's
implication text also lean on "a plain TOML bool can't distinguish" --
sweep all of it with whichever decision lands.

## 4. The `"none"` sentinel for template/agent (C-lite, lean accept)

Same root gap as item 3, scalar edition: an empty scalar means
"inherit" in the cascade (`override()`), so "explicitly nothing" needs
a stored sentinel value -- `template = "none"`, resolved out at load
(`config.go:534-539`, `FromNone`). Judgment lean: the sentinel is
arguably GOOD file UX -- `template = "none"` reads as exactly what it
means, where a tri-state "present-but-empty beats absent" rule would be
invisible in the file. Candidate: accept on the record in the same ADR,
as the worked example of the gap handled well.

## 5. Raw-block verbatim round-trip apparatus (B, mixed cause)

`internal/configui/complete.go:208-213` (`rawSlice`): raw blocks
round-trip verbatim only while untouched; an edited block is
re-normalized line-by-line, losing hand formatting (indented Dockerfile
continuations, blank lines). Cause is mixed -- partly the textarea edit
path, partly emission normalization. Folded into the renderer decision
(item 2): a designed emission contract states what raw-block formatting
survives, instead of "whatever survived this path".

## 6. Examined, not instances (E)

- **`env` has no unset; `env_from_host` disables via `KEY = ""`**
  (ARCHITECTURE.md:296-298, config.go:363-369). TOML maps could carry
  `!KEY` marker keys; byre chose value-override/empty-disable as marker
  grammar (ADR 0018's scope). Design, not binding.
- **`ports` removal is `remove = true`, not `!name`** -- the entry's
  identity is an integer, and a string marker has no slot to live in.
  A constraint of TOML-the-format's typing, i.e. a product-choice
  input, handled legibly. (GLOSSARY "Removal marker" already records
  the two spellings.)
- **Retired-key tolerance and `SharedAuthPref`'s dual-shape decode**
  (config.go:737-741) -- migration tolerance for byre's own past
  formats, not a binding gap.

## Suggested decision order

1. **Renderer ADR** (items 2 + 5, narrows 1): config emission becomes a
   designed surface with one owner and pinned round-trip/golden tests.
   Unblocks the `[[context]]` CONTEXT screen build (inline prose needs
   clean emission).
2. **Comments disposition** (item 1 remainder): research spike on
   style-preserving TOML editing in Go; decide build vs
   accept-on-record. Interim floor ships regardless (verb-path warning,
   stub boilerplate fix).
3. **Bool-unset ADR** (item 3): semantics change wants its own decision
   and migration note.
4. **Accepts recorded** (item 4 + the item 6 non-instances) in whichever
   ADR fits, so the inventory dies with this file.

## Doc lines to sweep when decisions land

- `docs/ARCHITECTURE.md:288-291` (monotonic bool as deliberate),
  `:296-298` (env unset).
- `site/content/docs/configuration-reference.md:26` (same claim).
- `internal/configui/configui.go` Save/managed-by comments and
  `handComments` prose (if the comments decision changes them).
- `docs/adr/0018-*` if marker grammar wording needs the bool story.
