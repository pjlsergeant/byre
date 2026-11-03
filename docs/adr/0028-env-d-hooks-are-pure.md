# env.d hooks are pure env-setters; login shells source them

> Amended by ADR 0057 (the credential export steps). Amended 2026-11-02:
> the launcher pins PATH ("Why the launcher pins PATH", below).

byre skills contribute launch-time environment via `env.d` hooks -- `.sh`
files sourced by the launcher so their `export`s land in the agent's
process. This ADR pins a contract on that mechanism and extends it to
login shells. Decided 2026-07-13, surfaced building the `docker-host`
skill (ADR 0027).

**Where env.d sits in the launcher.** The order is: launch gate,
credential export, firstrun hooks, env.d hooks, credential re-apply,
agent exec. Credentials are exported before the firstrun hooks, so a hook
sees a `byre credentials` value instead of prompting for a key already
delivered, and re-applied after the env.d loop, so credential exports win
env collisions (ADR 0057); both credential steps keep this ADR's purity
contract. The launcher runs its own helpers on the system directories
(`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`) from its
first line, saving the inherited PATH as the agent PATH for the firstrun
hooks and the agent's exec alone. It sources each env.d hook on that
system PATH, re-pinned before each hook and again after the last, so a
hook's helpers are system tools and a PATH a hook sets reaches neither a
later hook nor the agent (why: below). No bundled hook sets PATH.
Login shells (the profile.d shim below) source env.d on the shell's own
PATH.

**Why the launcher pins PATH.** The image's PATH puts the dev user's
`~/.local/bin` ahead of the system directories (agent CLIs install
there), and that directory is agent-writable -- and so writable by repo
code, which runs in the launcher (a pending worktree checkout's hooks
and filters). A helper resolved through it would run planted code inside
byre's own launch machinery. So the launcher runs its own helpers on the
system PATH and restores the inherited agent PATH only for the firstrun
hooks and the agent's exec, whose binaries legitimately live under
`~/.local/bin`. First-run hooks run under `/bin/bash` by absolute path,
so a planted `bash` is never the interpreter, and with fd 9 closed, so a
descriptor the launcher inherited at that number does not pass into a
hook it did not write. What still resolves on the agent PATH -- the
agent's command, the interactive login hooks, grok-shared-auth's broker
-- is listed on the security-model page.

## The problem

`env.d` hooks reached only the agent. `byre shell` uses `docker exec`,
which bypasses the launcher, so a shell session never got the env.d
environment -- `docker-host`'s `COMPOSE_PROJECT_NAME` (and
`claude-shared-auth`'s token) were absent exactly where a human hand-runs
`docker compose`, reintroducing the cross-worktree collision the compose
name exists to prevent.

The obvious fix -- source env.d from `/etc/profile.d` for every login
shell -- was unsafe, because `env.d` had quietly become a dumping ground:
`claude-shared-auth`'s hook was *sourced* but did an interactive `read`
prompt and a credential-file `mv`. Blanket-sourcing that into every login
shell would re-fire the prompt and re-run one-shot remediation on every
`byre shell`.

## The decision

**`env.d` hooks are PURE: the exported environment they leave behind is
their only lasting effect.** A hook may compute -- conditionals, command
substitution, helpers like `tr`, `unset` of its own temporaries or of
environment it deliberately clears -- but when the `source` returns,
nothing observable may remain beyond the environment delta. That excludes
(examples, not an exhaustive list): output on stdout or stderr, prompts,
filesystem mutation, network activity, subprocesses left running, and
terminating or reconfiguring the sourcing shell -- `exit`, `exec`,
top-level `return`, or persistent changes to options (`set -e`/`+e`,
`pipefail`), traps, aliases, functions, the working directory, umask, or
positional parameters. Anything that *does* something belongs in
`firstrun.d` -- which, despite the name, is executed on *every* launch
(each hook self-guards for once-ness), so an every-launch command
belongs there. firstrun.d runs before env.d in the launcher.

With that contract, three parts:

- **`/etc/profile.d/byre-env.sh`** (baked by the core block) sources
  env.d for every login shell, so `byre shell` gets the same hook
  exports as the agent (on the shell's own PATH). This is safe and quiet ONLY because hooks honor the purity
  contract -- a login shell has no strict-mode belt at all, so an impure
  hook pollutes or kills every shell session. The contract is the
  protection; there is no mechanism behind it.
- **The launcher keeps its own env.d sourcing**, with errexit/nounset
  suspended around each hook. It runs before any profile is read and
  cannot rely on login-shell semantics, so it needs its own loop. The
  suspension keeps strict mode from turning a benign unset-variable
  reference in a pure hook into a dead launcher -- it does NOT contain an
  impure hook: one that re-enables errexit and then fails, or calls
  `exit`/`exec`, still kills the launch. Best-effort is guaranteed only
  for hooks that keep this ADR's contract.
- **`byre shell` passes the container's `BYRE_*` plumbing through the
  `docker exec`** so the shim's hooks have their inputs (e.g.
  `COMPOSE_PROJECT_NAME` reads `BYRE_WORKTREE`).

`claude-shared-auth`'s stale-login remediation (the interactive prompt +
`mv` that warns when a per-project `.credentials.json` shadows the shared
token) lives in its `firstrun.sh`, leaving `env.sh` observably pure:
computation in service of its token export, and nothing else left
behind.

## Consequence, accepted

`byre shell` now also inherits `claude-shared-auth`'s token. This is
correct under the threat model: the shell is the *user*, who already has
full box access; exposing the token to their own shell adds no threat
surface (it already sits in the agent's environment beside them).

## For skill authors

A reviewer finding an impure effect in an `env.d` hook should move it to
`firstrun.d`, per this ADR.

The purity tests (the per-hook behavioral arms and the inventory check)
are partial arms -- they exercise the shipped hooks' known branches and do
not prove the absence of every possible external effect; the contract
itself is the standard reviewers hold hooks to.
