# Compatibility paths get windows, and this is the inventory

Decided 2026-07-27, absorbing the 2026-07-18 complexity review's
"compatibility sunset" item. Guardrail: no compatibility path ships
without a stated removal release (ADR 0048). This ADR records the
retirement policy and the authoritative inventory of paths still live;
TODO.md carries the removals as open work.

## The policy

A compatibility path (a tolerated legacy spelling, name, or on-disk
shape) **warns and keeps working** -- the warning names the file and the
fix -- and its removal is a per-path operator call once that path's
warnings have gone quiet ("who knows what old repos I have" is the
concern the warning phase serves; a calendar serves nobody). Ruled
2026-08-23 by the maintainer, on one condition: byre has no external
users, so every config a legacy path could live in is on the operator's
own machines.

The first external user ends that condition and restores the windowed
policy for every path not yet removed: a path lives for **two minor
releases or 90 days, whichever is longer**, counted from the release
that shipped its replacement, and the last supported release warns.

Under either policy the removal release ships the refusal with a
recovery remedy in the message and a CHANGES.md recovery path, and the
whole apparatus goes together: parser field, migration machinery,
command surface, catalog/UI rows, tests, docs.

The policy governs *compatibility paths* only: a **live** config key
byre decides to stop supporting gets judgment rather than a window --
removable in a minor, loudly, per the position stated in
`docs/RELEASING.md`.

## The live inventory

Both live paths warn through the compat warning channel
(`internal/config/warning.go`, per-layer collection rendered on develop,
status, the editor row, and `status --data`).

1. **Top-level `shared_auth`** (`internal/config/config.go`'s
   `SharedAuthLegacy` accepts the pre-2026-07-28 spelling beside the
   canonical `[defaults].shared_auth`). Onboarding itself wrote this key
   into users' `default.config`, so refusing it would break upgrades for
   byre's own doing. Every write migrates it -- the editor canonicalizes on
   the presence of the construct, not only on a changed value. Removal
   takes the field and its union in `StoredSharedAuth` together.
2. **Array-shaped `shared_auth`** (`internal/config/sharedauth.go`'s
   parser accepts the legacy array beside the table shape). The write
   side is gone: yes-without-pick is not a saveable state,
   `EncodeTOMLValue` emits picks only, and every writer persists the
   `Saveable` projection, so a save of a carrying file drops legacy
   yes-only entries (presence-triggered, like #1's migration). Removal
   drops the parser's array arm.

## Retired

- `SharedAuthDeclined`: the stale key parses as a tolerated retired key.
- Adoption-record migration and decline-record deletion: old records are
  inert files.
- `skill update` and the `devloop` stub: `RetiredNames` tombstones with
  pinned install remedies.
- Repo-root `byre.config` as a legacy preset name (2026-08-23):
  discovery, the explicit argument, and the passive drift note all
  refuse/flag the name with the rename remedy
  (`commands.retiredPresetName`); detection is permanent so an old repo
  fails loudly, never silently.
- Legacy materialized-package machinery (2026-08-23): the
  `skill archive-legacy` verb, `ArchiveLegacy`, the store-setup notice,
  and the dedicated LEGACY provenance. A local dir wearing a protected
  name ingests as an ordinary INVALID row ("never loaded -- rename or
  remove the directory"); `RetiredNames` and the missing-package
  tombstone remedies are permanent name protection, not a compat path.
