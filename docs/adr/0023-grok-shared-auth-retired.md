# Retire grok-shared-auth v1; a symlinked auth.json never counts

> Superseded in part by [ADR 0036](0036-grok-shared-auth-v2-broker.md)
> (2026-07-16): grok-shared-auth is rebuilt as an auth broker. This ADR
> stands as the record of why v1's file-sharing shape can never work.

grok-shared-auth v1 -- the companion skill sharing one Grok login across
all of a user's boxes (the ADR 0017 pattern, codex-shaped: `auth.json`
symlinked into a machine-scoped identity volume) -- is **retired**
(2026-07-12). It shipped 2026-07-09 explicitly to run its empirical gates
(the Grok CLI is closed source, so the two claims file-sharing rests on
could not be source-verified); the field ran them within a day and both
failed. Decided by the maintainer. The field evidence and the later
source corrections are in
[AGENT-CREDENTIAL-MECHANICS](../AGENT-CREDENTIAL-MECHANICS.md) (xAI Grok
CLI section).

## Why it failed

Both assumptions v1 rested on were false. Grok's refresh token is
**single-use**, and a refresh replaces the symlink with a private local
file, so the shared copy freezes and is then permanently rejected; v1's
every-launch symlink heal, built on the assumption the shared copy is the
good one, then replaced working per-box logins with the dead one.

The codex pattern was not transplantable because none of its three
load-bearing facts hold for grok: codex writes the credential in place,
tolerates concurrent refreshes, and rarely refreshes at all; grok
rename-forks, revokes on refresh-token reuse, and refreshes several times
a day. Grok's own `auth.json.lock` cannot serialize refreshes between
boxes either (its staleness probe is meaningless across container PID
namespaces), which forecloses sharing the whole `GROK_HOME` as a repair.
ADR 0017's symlink pattern is therefore agent-specific, not generic --
the gates exist because transplants fail.

## Decision

- The grok skill's login hook drops the ADR 0017 carve-out that kept
  identity-volume symlinks: **a symlinked `auth.json` never counts** and
  is removed. This is also the field heal -- boxes v1 damaged shed the
  dead link at next launch.
- Without the ADR 0036 broker, per-box grok logins (the plain grok
  skill) are the supported shape; they rotate correctly. The orphaned v1
  machine volume (`byre-machine-u<uid>-grok-identity`) is left to manual
  cleanup.
- The env API-key path (`XAI_API_KEY`) is technically clean but **ruled
  out on cost**: xAI's API is a separate pay-per-token billing track (no
  subscription credits), ~50x the flat subscription at coding-agent
  volumes.
