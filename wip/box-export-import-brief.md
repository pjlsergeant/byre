# Box export / import: implementation brief

Hand-off for the implementer. The design is `wip/box-export-import.md`
(v4, commit 61a21ed9); this page says how to read it and what the repo
expects of you. Delete both files when the feature ships (wip/README.md).

## Read, in this order

1. `wip/box-export-import.md`. The whole spec. Line tags:
   - `[RULING]`: Pete's, settled, closed. Do not reopen one to close a
     reviewer finding; two review rounds tried and each reason is in the
     doc.
   - `[CODE]`: verified against the tree on 2026-09-08. Re-verify before
     relying on a line; the tree moves.
   - `[PROPOSED]`: what you build. The "Evidence before done" section is
     the test plan; treat every bullet as a test to write.
2. `TODO.md`, the "Box export / import" entry. Status lives there and
   nowhere else; do not restructure the file.
3. `CLAUDE.md` at the repo root. The conventions that bite: plain `os`
   filesystem calls are banned outside `internal/hostopen` (every path
   the archive, the store or the workspace names is agent-influenced, so
   it rides hostopen's real functions, or says `hostopen.PlainStat(p,
   hostopen.StoreOwned)`-style why not); contracts pin byte-exact,
   behaviour asserts the rule that fired; `gofmt` + `go vet` +
   `go test ./...` green before every commit; the docs sweep is part of
   the unit, not a follow-up.
4. `docs/adr/README.md`, then the ADRs the design's Doctrine section
   names: 0004, 0007, 0009, 0017, 0029, 0030, 0040, 0051, 0054, 0055,
   0057, and principles P0 to P6. You write one new ADR (box archive) and
   amend 0007, 0017 and 0057 as the design states; each amendment changes
   its README one-liner in the same commit
   (`TestDoctrineIndexCoversCorpus`).
5. `docs/GLOSSARY.md`: binding vocabulary. Add "box archive" and
   "narrowing" (under volumes) there before using the words in
   user-facing strings.
6. `docs/BYRE-DEVELOPMENT.md` for `byre-inttest`, the sacrificial engine
   runner. The volume stream, the narrowing of a companion skill's
   machine volume, the receipt-driven seed and the export form all need
   the gated run before "done".

## The code the design reuses

All named in the design's "What already exists" section:

- `internal/commands/preset.go` (apply, review, missingRefs, the
  256 KiB reader that moves to `config.MaxConfigBytes` for apply,
  inspect AND the passive drift probe together)
- `internal/config/config.go` (mergeStep, mergeStrings, mergeMap,
  Volume/Seed, validateVolumeShape) and `mergestate.go` (Closures: the
  flattener re-emits them as `!name` entries)
- `internal/commands/resolve.go` (combine is the narrowing dedupe point;
  attributedCollisions exempts an exact narrowing)
- `internal/commands/review.go` (skillGrantSummary walks skill
  declarations today; it must read the resolved set)
- `internal/commands/status.go` (managedPathShadows, same conversion)
- `internal/configui/effective.go`, `listitem.go`, `volumes.go` (skill
  volume rows; the "Narrow to this project" action)
- `internal/commands/seed.go` and `internal/runner/runner.go`
  (SeedLiteral streams over stdin: the shape for the receipt-driven
  restore; no bind of `~/.byre`, ever)
- `internal/lock/lock.go` (add LOCK_SH; keep the inode requeue)
- `internal/commands/develop.go` (reportRunning remedies; the second
  setup-lock span is where the shared barrier goes, after all prompts)
- `internal/commands/credentials.go` (unlock; export needs a variant
  that retains the single successful passphrase)
- `internal/deliver/tar.go` (nested-tar name rules; note its splitter
  strips a leading `/` where you must refuse, and it never validates
  Linkname)
- `internal/hostopen` (anchored writes, PublishFile with an explicit
  0600)

Suggested new package: `internal/transfer` for the manifest, archive
read/write budget, nested-tar validator and the stage receipt. Command
handlers stay Streams adapters in `internal/commands`; cobra wiring in
`cmd/byre` (ADR 0022).

## Rules of the road

- **One unit, one branch.** `byre preset export`, `byre export`,
  `byre import`, the export form, the seed source kind, the barrier, the
  narrowing rule, the new ADR and the three amendments, the GLOSSARY and
  security-model page entries all land together. No "preset export
  first" split; Pete ruled it.
- **Commit per coherent piece** on that branch (lock mode + tests;
  flattener + tests; ...). No commit trailers of any kind.
- **Two reviewers, independently.** After the feature, run
  `byre-codereview` and a second reviewer (`--reviewer grok` or `zai`)
  without briefing either on the other. Give each this focus verbatim:
  "Check the change against the index in docs/adr/README.md: report
  which entries apply and whether the change complies, or state
  'Doctrine: none apply'." A review with no Doctrine line has not done
  the check. Fix or consciously defer every finding; findings that touch
  a ruling or the security model go to Pete, not into a silent fix.
- **Engine-side run before done:** `byre-inttest` over the gated suite;
  never pipe it through `tail` or `head` (the exit status is lost).
- **Docs sweep in the same unit:** README, ARCHITECTURE, GLOSSARY,
  the commands page pin, `site/content/docs/security-model.md`, CHANGES.

## What the reviewers already settled (so you need not re-derive it)

Two fresh three-reviewer rounds (Grok, Z.AI, Claude) ran on v2 and v3.
Every finding is folded into v4; the raw logs are box-local and not in
git. The recurring classes, in case a reviewer raises them again:

- Closures are not in the merged Config; a flattener that forgets them
  silently widens the imported allowlist. Re-emit them.
- A removal marker acts only from the layer that carries it. A
  destination-default `!skill` cannot remove a preset-named skill; the
  import delta must not claim otherwise.
- Narrowing touches every reader of skill volumes, not just validation;
  otherwise the grant review and status lie about scope.
- A stage without a receipt cannot tell "never staged" from "vanished".
  The receipt is the source of truth; never retarget a stage.
- Export must re-read under the project lock after the barrier, as
  develop does, or it archives bytes nobody reviewed.
- Every host-path key needs the remap line: mounts, seed.host,
  context.file, claude_skills.path.
- Sibling bounds move together: the preset size cap has three readers.
