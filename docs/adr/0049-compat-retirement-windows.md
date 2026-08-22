# Compatibility paths get windows, and this is the inventory

Decided 2026-07-27, absorbing the 2026-07-18 complexity review's
"compatibility sunset" item. Guardrail: no compatibility path ships
without a stated removal release (ADR 0048). This ADR records the
window policy and the authoritative inventory of paths still inside
theirs; TODO.md carries the removals as open work.

## The policy

A compatibility path (a tolerated legacy spelling, name, or on-disk
shape) lives for **two minor releases or 90 days, whichever is longer**,
counted from the release that shipped its replacement. The last
supported release warns; the removal release ships the refusal with a
recovery remedy in the message and a CHANGES.md recovery path. Removal
means the whole apparatus goes together: parser field, migration
machinery, command surface, catalog/UI rows, tests, docs.

**Amended 2026-08-23 (Pete's ruling): while byre has no external users,
the calendar and the warning-release step collapse.** The window above
was calibrated to protect an installed base through upgrades; today every
config a legacy path could live in is on the operator's own machines. The
replacement rule: a compatibility path **warns and keeps working** — the
warning names the file and the fix — and its removal is a per-path
operator call once that path's warnings have gone quiet ("who knows what
old repos I have" is the concern the warning phase serves; a calendar
serves nobody). Removal-release requirements are unchanged: loud refusal
with a remedy, CHANGES.md recovery path, the whole apparatus goes
together. This amendment is conditioned on the no-external-users fact
and expires with it: the first external user restores the windowed
policy above for every path not yet removed.

The window governs *compatibility paths* only: a **live** config key
byre decides to stop supporting gets judgment rather than a window --
removable in a minor, loudly, per the position stated in
`docs/RELEASING.md`.

Already retired under this policy: `SharedAuthDeclined` (stale key now
parses as a tolerated retired key), adoption-record migration and
decline-record deletion (old records are inert files), `skill update`
and the `devloop` stub (RetiredNames tombstones with pinned install
remedies).

## The live inventory

1. **Top-level `shared_auth`** (`internal/config/config.go`'s
   `SharedAuthLegacy` accepts the pre-2026-07-28 spelling beside the
   canonical `[defaults].shared_auth`). Onboarding itself wrote this key
   into users' `default.config`, so refusing it would break upgrades for
   byre's own doing. Every write migrates it -- the editor canonicalizes on
   the presence of the construct, not only on a changed value -- so the
   window is about configs never saved since. Removal: after two minors or
   90 days from the 2026-07-28 replacement, whichever is longer; the last
   supported release warns, then the field and its union in
   `StoredSharedAuth` go together.
2. **Array-shaped `shared_auth`** (`internal/config/sharedauth.go`
   accepts both the legacy array and the table shape).
   **Blocker, recorded so it isn't lost: removal needs a warning
   release first, and no parse-time warning channel exists today** --
   the array shape is also round-tripped by `EncodeTOMLValue`. Sequence:
   build the warning channel (or piggyback on develop-time notices),
   warn one release, then drop the array arm of the parser and encoder.
   **Amended 2026-08-23:** the write side is retired -- yes-without-pick
   is no longer a saveable state, `EncodeTOMLValue` emits picks only, and
   every writer persists the `Saveable` projection, so a save of a
   carrying file drops legacy yes-only entries (presence-triggered, like
   #1's migration). The warning channel the blocker asked for now exists
   (`internal/config/warning.go`, per-layer collection rendered on
   develop, status, the editor row, and `status --data`), and both this
   path and #1 warn through it. The array arm of the PARSER stays
   warn-and-works under the amended policy above; dropping it is the
   per-path operator call once its warnings go quiet.
3. **Repo-root `byre.config` as a legacy preset name** (accepted beside
   `byre.preset`). The in-product rename note IS the warning; removal
   is a release-time decision at the end of its window -- refuse with
   the rename remedy. **RETIRED 2026-08-23** under the amended policy:
   discovery, the explicit argument, and the passive drift note all
   refuse/flag the name with the rename remedy
   (`commands.retiredPresetName`); detection is permanent so an old repo
   fails loudly, never silently.
4. **Legacy materialized-package machinery** (`ProvLegacy` rows,
   `skill archive-legacy`, store-setup detection). User-facing recovery
   for pre-ADR-0029 stores; keep until the end of its window, then
   collapse to tombstones only (`RetiredNames` protection stays --
   that is permanent name protection, not a compat path).
   **RETIRED 2026-08-23** under the amended policy: the archive-legacy
   verb, `ArchiveLegacy`, the store-setup notice, and the dedicated
   LEGACY provenance are gone. A local dir wearing a protected name
   ingests as an ordinary INVALID row ("never loaded -- rename or
   remove the directory"); `RetiredNames` and the missing-package
   tombstone remedies are unchanged and permanent.

## Staged work the absorbed review file carried (dispositions)

- **Config lifecycle split (review item 7)**: staged. The `layer bool`
  validation flags are gone (named Layer/Resolved validator pairs);
  moving merge state (`EgressClosed` etc.) out of the persisted struct
  needs its own session; only then decide if the full type split pays.
  Tracked in TODO.md.
  **Staged move SHIPPED 2026-08-23:** merge bookkeeping lives in
  `config.Closures` (all four genera), produced beside the merged Config
  by `MergeStep` — whose `over` is always a raw layer, so the fold never
  eats its own output. `config.Merged{Config; Closures}` is the resolved
  view `Load`/`ResolveProposed` return; skills subtraction, the build
  pipeline, commands, and the editor's live re-merge consume it.
  `SourceHint.From` stays put (layer attribution, not merge state). The
  full-type-split go/no-go is assessed in
  `wip/lifecycle-split-assessment.md` (recommendation: doesn't pay;
  revive trigger recorded there), held for the operator's ruling.
- **Carving `internal/commands` (item 8)**: extraction on the second
  real consumer, never by decree; reconcile CLAUDE/ARCHITECTURE wording
  when the first extraction lands. No open work until a consumer shows.
- **Normalized removal tombstone model (item 9)**: nice-to-have only if
  the genus machinery makes it cheap; every user-facing spelling stays.
  KILLED as an open item -- guardrail 2 already prevents new spellings,
  which was the point.
