# Retire grok-shared-auth; park the rebuild designs

grok-shared-auth -- the companion skill sharing one Grok login across all
of a user's boxes (the ADR 0017 pattern, codex-shaped: `auth.json`
symlinked into a machine-scoped identity volume) -- is **retired**
(2026-07-12). It shipped 2026-07-09 explicitly to run its empirical gates
(the Grok CLI is closed source, so the two claims file-sharing rests on
could not be source-verified); the field ran them within a day and both
failed. Decided by the maintainer; two rebuild designs are parked in
`docs/grok-shared-auth-v2-designs.md`, mechanics recorded in
`docs/agent-credential-mechanics.md`.

## Why it failed (field evidence, 2026-07-10, twice in one day)

Grok's credential is an OAuth pair: ~6h access tokens plus a **single-use**
refresh token (the "~7 days" in vendor docs is not the working lifetime).
On refresh the CLI evidently writes via temp+rename, which replaces the
symlink with a private local file -- the rotated pair lands in that box's
own volume and the shared copy freezes (shared file mtime stuck at login
time against a same-day expiry). Because rotation is single-use, the
frozen shared pair is not merely stale but permanently rejected
("ServerRejected", `refresh_chain short-circuit on permanent failure` in
grok's event log). The skill's every-launch symlink heal -- designed for
logout-forks on the assumption the shared copy is the good one -- then
deleted working per-box logins and replaced them with the corpse. Net
effect: "grok randomly breaks," and headless runs HANG on an interactive
device prompt (grok's auth-failure fallback).

The codex pattern was not transplantable because none of its three
load-bearing facts hold for grok: codex writes the credential in place
(source-verified), tolerates concurrent refreshes, and rarely refreshes at
all; grok (evidently) rename-forks, revokes on refresh-token reuse, and
refreshes several times a day. A further finding forecloses the obvious
repair: grok's own `auth.json.lock` is a `PID:timestamp` lockfile, and PID
liveness is meaningless across container PID namespaces, so **grok's lock
cannot serialize refreshes between boxes no matter how it is shared**.

## What retirement means

- The skill remains a resolvable **stub** (configs naming it must not
  break a launch) that contributes nothing: no hooks, no volumes. Its
  description carries the retirement notice into the config picker.
- The grok skill's login hook drops the ADR 0017 carve-out that kept
  identity-volume symlinks: a symlinked `auth.json` never counts and is
  removed. This is also the field heal -- boxes v1 damaged shed the dead
  link at next launch and log in per box.
- Per-box grok logins (the plain grok skill) are the supported shape; they
  rotate correctly. The orphaned machine volume
  (`byre-machine-u<uid>-grok-identity`) holds only the dead credential and
  is left to manual cleanup.
- ADR 0017 is unchanged for claude/codex/gemini; this ADR records that its
  symlink pattern is agent-specific, not generic -- the gates exist
  because transplants fail.

## Why not rebuild now

The env API-key path (`XAI_API_KEY`) is technically clean but **ruled out
on cost**: xAI's API is a separate pay-per-token billing track (no
subscription credits), ~50x the flat subscription at coding-agent volumes.
The two designs that keep subscription billing -- an auth broker riding
grok's `GROK_AUTH_PROVIDER_COMMAND` seam (no resident process, closes the
refresh race) and a fork-shipping watcher + refresh jitter (accepts a rare
race) -- both rest on unverified closed-source behaviors and need their
gates run before any build. That is deliberate future work, parked with
the designs, not a blocker worth carrying as an open wound: the retired
state is strictly better than v1 (which actively broke boxes) and only
per-box re-login worse than success.
