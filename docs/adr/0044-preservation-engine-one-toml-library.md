# Config files are shared custody: the preservation engine, one TOML library

> **Amended by ADR 0057** (2026-08-08): [[credentials]] declaration saves
> ride tomldoc like every user-config write; the vault's index.toml is
> MACHINE-authored whole-file (temp+rename) and outside this ADR's
> style-preserving scope.

Decided 2026-07-25, grilled with the maintainer end-to-end. Three decisions
that stand together:

1. **Preservation doctrine.** Config files are shared-custody documents --
   byre writes them and users may hand-edit them (a defended right, P1,
   never a requirement, P6). A save must not clobber what it didn't touch:
   hand-written comments, formatting, and exotic-but-legal spellings
   survive every structured save. byre owns an emitted artifact's shape
   only where it is SOLE author (gen's Dockerfile; a file byre creates
   from nothing).
2. **One engine, byre-owned.** Every surface that edits a stored config
   file rides `internal/tomldoc`: an expression index over the original
   bytes (pelletier/go-toml's unstable parser supplies the byte-ranged
   AST) with every mutation a byte-range splice -- bytes outside the
   edited span survive IDENTICALLY by construction, which is a stronger
   guarantee than any reformat-based approach can make.
3. **One TOML library.** BurntSushi/toml is gone from the module graph
   (P7's "replace the dependency", executed same-day). pelletier v2
   carries decode (strict unknown-key detection, the presence probes, the
   dual-shape shared_auth unmarshaler) and the parse layer under tomldoc.
   The unstable-API risk is priced: the version is pinned, the golden
   corpus fronts any upgrade, and an upgrade is a chosen event.

Principles: P6 (the editor is the interface -- and it must not punish the
right it isn't); P7 (this ADR is the type specimen's resolution: the old
encoder's missing multiline emission nearly drove `[[context]]` prose to a
sidecar-file architecture whose preset story then failed).

## The problem

The audit behind this ADR (wip/toml-limitations-audit.md, reviewed by both
external reviewers, now absorbed here and deleted) inventoried what
delegating file emission to a library default had cost:

- Every structured save re-marshaled the whole file: all comments
  destroyed (with a warning in the TUI, silently via the `byre mcp add`
  CLI path, and silently for inline comments everywhere), layout and key
  order imposed by encoder policy.
- `byre layer new`'s own instructional stub comments tripped the
  hand-comments warning and were then eaten by the first save.
- SharedAuthPref had a custom decoder and no encoder, so a global-editor
  save re-emitted `[shared_auth.Pick]` -- a shape byre's own parser
  refuses. A normal save bricked default.config (reviewer find,
  reproduced same day, stop-gap shipped and later absorbed here).
- Missing multiline string emission (the near-miss above).
- Compensating apparatus throughout: the destroys-comments warning, the
  surgical top-level-scalar line writers in onboarding (own-the-seam
  prior art, but line-anchored and refusing multiline hand-forms), the
  Undecoded-suppression filter around shared_auth.

Canonical-file doctrine (byre owns the layout, comments byre didn't write
die, disclosed) was considered and rejected by maintainer ruling: the
maintainer's own preference for never hand-editing does not make the
population's, and gen's Dockerfile analogy fails on custody -- the
Dockerfile is pure output nobody edits back; config files have two
authors.

## The engine

`internal/tomldoc`: Load -> expression index (tables, array-tables,
key-values with flattened dotted keys, comments) -> mutations as single
splices -> re-index after every splice (documents are small; re-parsing
beats offset bookkeeping). House rendering (render.go) applies to content
byre writes: new files, appended blocks, edited entries -- including
multiline literal (`'''`) prose for `[[context]]` text, with an escaped
fallback pinned to round-trip exactly.

Behavioral contract (grilled, each default approved):

- An in-place value edit keeps the line's trailing inline comment.
- New entries land after the last entry of the same kind; new root keys
  before the first table header (TOML's own rule); an absent table is
  appended.
- A removal takes the entry's lines, its trailing inline comment, and
  full-line comments glued immediately above (no blank line between).
- Exotic-but-valid constructs are untouched until an edit TARGETS them;
  then that construct alone is rewritten in house shape (an interior
  comment belongs to the construct). An inline `defaults = { ... }` an
  edit reaches INSIDE is such a construct: it becomes a `[defaults]`
  block carrying its members, and emptying it removes it whole -- as
  does removing the table itself, which takes the construct's line the
  way any removal takes a line. Inside
  an `[[array]]` element the house shape is the INLINE one, re-emitted
  where it stands -- an element is identified by its position, so a
  promoted `[mcp.headers]` block would join whichever element was
  declared last.
- A path-addressed edit resolves an existing target first-match, in
  document order; CREATING one on a path that runs through an array of
  tables is refused, because a key path cannot name an element -- there,
  position is identity, and it belongs to the position/match APIs.
- Text TOML cannot carry -- a key or a value that isn't valid UTF-8 --
  is refused at the mutation, naming the rule, with the document left as
  it was. The renderers stay total (they take a value and return text,
  with no channel to refuse through) and pass such bytes along verbatim
  rather than substituting U+FFFD, so the refusal has something to see.
- No-op saves leave the document byte-identical.
- Every edit is re-read with the STRICT decoder before it is handed
  back, and a failure restores the pre-edit bytes: the expression parser
  accepts a key defined twice, so syntax alone would let a save write a
  file no later command can load. The check is schema-agnostic -- a
  document byre doesn't understand stays editable -- and it holds the
  edit to account for what IT broke: a document that already failed the
  strict decoder before the edit still accepts edits, since refusing
  would strand the file at the moment the user is repairing it.

Above the engine, `configui.Save` reconciles the desired Config against
the file's parsed content -- an untouched field produces no edit at all.
`TestReconcileCoversEveryField` (the Merge guard's sibling) forces every
toml-visible field to reconcile and round-trip. The onboarding writers
(SaveDefault, SaveSharedAuthDefaultPick) ride the same engine; shared_auth
emission has one owner (`EncodeTOMLValue`: picks win, yes-without-pick
re-asks, empty removes the key).

Deleted, per the no-accretion ruling: the whole-file encoder path, the
handComments/commentWarn warning apparatus (nothing left to warn about),
the line-based surgical primitives and their residual refusals, the
Undecoded-suppression filter (the byte-based unmarshaler consumes the
whole shared_auth subtree), and the same-day SharedAuth stop-gap
marshaler.

## Recorded accepts (the audit's non-instances, so it can die)

- **The `"none"` sentinel** (template/agent): designed vocabulary, kept.
  Empty-scalar-means-inherit is byre's merge representation; an explicit
  `template = "none"` in a file is more legible than invisible
  present-but-empty semantics. Not a binding limitation (presence
  detection existed); a design choice made deliberately.
- **`env` has no unset; `env_from_host` disables via `KEY = ""`**:
  marker-grammar design (ADR 0018's scope), not a binding gap. ADR
  0018's "`!KEY` doesn't parse as a key" rationale overstated -- quoted
  keys admit any string; the refusal is product grammar.
- **`ports` removal is `remove = true`**: the entry's identity is an
  integer in the current schema -- schema-shaped, handled legibly.
- **`omitempty` zero-value collapse**: absent-vs-explicitly-empty erases
  on re-save for most fields; moot by design, because the cascade's
  marker grammar means empties never carry "clear" semantics. The one
  place it wasn't moot -- `seed_prefs` -- is fixed by ADR 0045.
- **Retired-key tolerance**: migration behavior for byre's own past
  formats, kept (a filtered lenient re-decode under strict parse).

`seed_prefs` tri-state is its own decision: ADR 0045.
