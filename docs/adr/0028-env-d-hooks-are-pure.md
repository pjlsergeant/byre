# env.d hooks are pure env-setters; login shells source them

byre skills contribute launch-time environment via `env.d` hooks —
`.sh` files sourced by the launcher just before it execs the agent, so
their `export`s land in the agent's process. This ADR pins a contract on
that mechanism and extends it to login shells. Decided 2026-07-13,
surfaced building the `docker-host` skill (ADR 0027) and settled with the
maintainer via /grilling.

## The problem

`env.d` hooks reached only the agent. `byre shell` uses `docker exec`,
which bypasses the launcher, so a shell session never got the env.d
environment — `docker-host`'s `COMPOSE_PROJECT_NAME` (and
`claude-shared-auth`'s token) were absent exactly where a human hand-runs
`docker compose`, reintroducing the cross-worktree collision the compose
name exists to prevent.

The obvious fix — source env.d from `/etc/profile.d` for every login
shell — was unsafe, because `env.d` had quietly become a dumping ground:
`claude-shared-auth`'s hook was *sourced* but did an interactive `read`
prompt and a credential-file `mv`. Blanket-sourcing that into every login
shell would re-fire the prompt and re-run one-shot remediation on every
`byre shell`.

## The decision

**`env.d` hooks are PURE env-setters.** They may only export environment:
no commands, no interactive reads, no file mutations, no output. Anything
that *does* something belongs in `firstrun.d` — which, despite the name,
is executed on *every* launch (each hook self-guards for once-ness), so an
every-launch command has a proper home there. Ordering holds: firstrun.d
runs before env.d in the launcher.

With that contract, three parts:

- **`/etc/profile.d/byre-env.sh`** (baked by the core block) sources
  env.d for every login shell. Pure hooks make this safe and quiet — no
  strict-mode guarding needed (a login shell has no `set -eu`, and pure
  exports cannot fail-exit). So `byre shell` now matches the agent's
  environment.
- **The launcher keeps its own guarded env.d sourcing** (belt-and-
  suspenders; it runs before any profile is read and cannot rely on being
  a login shell). No shared snippet was needed once hooks are pure — the
  two sourcing sites are not duplicated logic, they are the same trivial
  loop with and without a strict-mode belt.
- **`byre shell` passes the container's `BYRE_*` plumbing through the
  `docker exec`** so the shim's hooks have their inputs (e.g.
  `COMPOSE_PROJECT_NAME` reads `BYRE_WORKTREE`).

`claude-shared-auth`'s stale-login remediation (the interactive prompt +
`mv` that warns when a per-project `.credentials.json` shadows the shared
token) moved from its `env.sh` to its `firstrun.sh`, leaving `env.sh` a
pure token export.

## Consequence, accepted

`byre shell` now also inherits `claude-shared-auth`'s token. This is
correct under the threat model: the shell is the *user*, who already has
full box access; exposing the token to their own shell adds no threat
surface (it already sits in the agent's environment beside them).

## For skill authors

If your hook needs to *run* something at launch — a login, a check, a
migration, a prompt — it goes in `firstrun.d`, not `env.d`. `env.d` is for
`export` and nothing else. A reviewer finding a command in an `env.d` hook
should move it, per this ADR.
