# Headless runs: the agent declares a headless command; `byre run` drives it

Decided 2026-11-02. A byre box can run ONE agent task non-interactively,
from any script -- a CI job or anything else -- with an exit status the
script can trust. An agent skill declares a **headless command** in its
manifest (`[agent] headless = "..."`); `byre run` sets up the box exactly
as `byre develop` does, hands the task to that command through a core
chassis script, `byre-headless`, and exits with byre's own verdict. Without
`--data`, stdout carries the agent's final answer and nothing else; with
`--data`, stdout carries one versioned JSON document, byre's outcome. The
agent's files land in a result dir, `.byre-run/`, in the work tree.

The bundled claude, codex and grok skills declare headless commands; an agent
without one is refused by name before anything builds.

Principles: P2 (core learns a CONTRACT -- task bytes in, an answer out, a
closed set of exit codes -- and never an agent's flags or event format;
every agent flag lives in the skill's script); P4 (byre's verdict is the
exit code and the JSON byre itself prints; where byre cannot verify what
ran, the JSON says so instead of asserting; everything the agent could
have written is named agent-trust, and status names each agent's headless
mode, hedged when a skill holds the knobs); P1 (nothing here refuses a
deliberate choice -- the raw `run_args` tier degrades the verdict's
authority rather than being refused, and each refusal `byre run` adds names
its remedy); P5 (a credential a project supplies never becomes
machine-wide, which is why the codex headless command never persists an
env key); P0/P6 (no new config key; the new skill field is rendered
read-only wherever skills render). Related: ADR 0004 (the created
container is the session-ownership token), ADR 0005 (agents are skills),
ADR 0006 (raw `run_args` win), ADR 0040 (anchored, never-overwriting host
writes into an agent-writable tree), ADR 0047 (repo code runs in the
launcher), ADR 0050 (`BYRE_` is reserved vocabulary), ADR 0053 (the launch
record), ADR 0058 (`--agent` is run-scoped).

## Why a core contract, not a wrapper agent skill

The alternative was an agent skill whose command was a bash wrapper that
picked a real backend by env var and exec'd its headless mode. Putting the
opinion in a wrapper cost four things, each structural:

1. Selecting the wrapper with `--agent` REPLACES the configured agent (ADR
   0058), so the real backend had to be hand-listed in `skills` or its
   binary, volume and login hook vanished from the box.
2. The wrapper's injection vouches (`mcp`, `claude_skills`, `context`)
   could be true for only some backends, so status would over-claim.
3. Its in-box variables could not be byre's own names: `BYRE_*` is refused
   in user env channels.
4. Login hooks run before any agent command, so a wrapper cannot stop a
   hook's interactive login from stalling a terminal-less box.

With each real agent skill declaring its own headless command and core
driving it, none of these arise. ADR 0058 is unchanged.

## The skill side: `[agent] headless`

`headless` is a shell command string beside `command`, baked into the
image as `/etc/byre/agent-headless` the way `command` is baked as
`/etc/byre/agent-cmd`. Absent means the agent has no headless mode. A
present value must not be blank and must not equal `command` -- the
interactive command would start a session that waits on a terminal nobody
holds. A typed field rather than a convention because status must
understand it (P4) and more than one agent declares one.

The field is skill-manifest text, so every surface renders it as data: the
`byre status` Agent row appends `headless: <cmd>` or `no headless
command`; `status --data` carries `agent_headless`; the config editor's
Skills screen appends the same suffix to every agent row, after the row's
state and never in the slot that disables it; `byre skill inspect` prints
an `agent headless:` line. When a skill's `[runtime].env` sets any of the
contract's variables below, the suffix reads `-- not warranted: a skill
sets BYRE_RUN_*` (the `headless run` claim, ADR 0050).

## The contract between the chassis and the headless command

The chassis runs the headless command with:

- `BYRE_RUN_TASK_FILE` -- the task text. The command's stdin is
  `/dev/null`.
- `BYRE_RUN_RESULT_FILE` -- `/dev/fd/5`, where the command writes the
  agent's answer.
- `BYRE_RUN_STATUS_FILE` -- `/dev/fd/6`, where the command appends
  `key=value` lines. Two keys are read: `backend_exit=<n>` (owed before an
  exit 11) and `agent_version=<text>`.
- `BYRE_RUN_MODEL` -- a model override, empty for the agent's default.
- `BYRE_RUN_DIR` -- the result dir, for files the command keeps for humans.
- `BYRE_RUN_AGENT`, `BYRE_RUN_ID` -- informational.

The step-by-step rules a command author follows are in the headless-command
section of [`docs/SKILLS.md`](../SKILLS.md); this section records why each
one exists.

**Descriptors.** The two files are descriptors, not paths: the chassis
creates them exclusively, once, and holds them open, and the command keeps
fds 5 and 6, and the variables naming them, to its own writes. A child that
outlived the script while holding one could rewrite the answer or the
status lines after the script exited and before the chassis reads them.
This is a convention of the contract, not a containment: the answer and the
status lines are agent-trust either way, so an agent that writes a
different answer through its own script changes only what it could have
written anyway.

**PATH.** The image's PATH puts the dev user's `~/.local/bin` ahead of the
system directories (agent CLIs install there), and that directory is
agent-writable -- and so writable by repo code, which runs in the launcher
before any headless machinery (a pending worktree checkout's hooks and
filters). A helper resolved through it would run planted code inside the
verdict's or the credentials' own machinery. So byre's own code -- the
launcher from its first line, the chassis, the headless commands, the
launch adapters and the codex-shared-auth reconciler -- runs its helpers on
the system PATH (`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`),
and restores the inherited agent PATH only for the exec of the agent CLI,
whose binaries legitimately live under `~/.local/bin`. The launcher sources
the env.d hooks on the system PATH, re-pinned before each one (ADR 0028).
The components that keep the agent PATH are listed under Accepted
residuals.

The command's stdout is the agent's live event stream and its stderr the
agent's diagnostics. The chassis records each to the result dir
(`transcript.jsonl`, `stderr.log`) and relays both to the box's stderr;
the box's real stdout is not among the command's descriptors.

Credential probes are bounded with `timeout -k`, so a probe that ignores
TERM is still cut off and reads as a 13 rather than a hung run. The agent
gets `/dev/null` or the task file on stdin because an agent handed an open
pipe waits on it forever.

The chassis owns its descriptors, `result.json`, the timeout when one was
asked for, the exit map, signal forwarding and the stream routing. The
command owns every agent flag.

**The exit status is a CLOSED set, never the agent's raw status.** The
command exits one of `0` (success, with a non-empty answer), `10` (the
agent reported failure), `11` (the agent process failed, `backend_exit`
written first), `13` (credential preflight failed) and `15` (the backend
is not in the box). The chassis adds `12` (its own timer fired) and `16`
(its inputs failed validation), and treats anything else -- a shell death,
a signal, a raw status passed up by mistake, `0` with an empty answer -- as
the command breaking the contract: `14`. A raw agent exit of 10 reaches the
host only as 11 with `backend_exit=10`. Passing raw statuses through would
make every number ambiguous -- an agent that happens to exit 13 would read
as "no credential" -- and a script gating on the code could not tell
byre's verdict from the agent's. The numbers are not unique across owners:
byre's own codes are 1, 2, 3 and 70, a box that launched and exited one of
those reaches the host as that number too (develop's 0-127 passthrough),
and byre itself can exit 1 or 3 after the box was created. `--data`'s
`outcome` and `phase` tell these apart. The host's full table is the
generated commands page.

**The inventory.** `BYRE_HEADLESS` and every `BYRE_RUN_*` name join the
reserved-env inventory under two claims, `headless run` and `launch` (a
skill setting one is aiming at a run's inputs, and the chassis is what the
launcher execs). The host re-asserts its `BYRE_RUN_*` set after every
skill's runtime env, the `BYRE_EGRESS` precedent, so no skill's
`[runtime].env` can skew what a run is told; a knob the run was not asked
for (`BYRE_RUN_TIMEOUT`, `BYRE_RUN_QUIET`) is re-asserted empty, which the
chassis reads as unset. Raw `run_args` still follow on the argv and win
(ADR 0006 is not reopened). A test pins that every `BYRE_*` name any
chassis script reads is in the inventory, `BYRE_FETCH_RUN_RETRIES`
included.

## The mode is keyed on argv; `BYRE_HEADLESS`

`byre run` passes the chassis's ABSOLUTE path,
`/usr/local/bin/byre-headless`, as the container command, and the
launcher's existing explicit-argv arm execs it. Before the first-run hooks,
the launcher exports `BYRE_HEADLESS=1` when its first argument is exactly
that string, and UNSETS it otherwise, whatever the environment carried.
Under headless it also saves the box's real stdout on fd 9 and sends its
own stdout, and every hook's, to stderr.

Why argv: a skill's `[runtime].env` can set any `BYRE_*` name (ADR 0050)
and raw `run_args` can override any `-e` (ADR 0006), but neither reaches
the command argv, which the runner places after the image. A stray
positional in `run_args` is taken as the image and breaks the launch
loudly; `--entrypoint` is the raw tier, and `byre run` reports it (below).
Login hooks test `BYRE_HEADLESS` before `[ -t 0 ]`, so a headless launch
never prompts. A raw `run_args = ["-e", "BYRE_RUN_TASK_FILE="]` can still
blank a contract variable; its effect is a chassis that cannot find its
task and exits 16, because the MODE does not depend on the env.

The container command is `gen.HeadlessPath`, the copy the Dockerfile tail
guards (below), never the bare name the agent-writable first PATH entry
could answer; the chassis pins PATH the same way (above).

The chassis has no env seam for which command it runs: it execs
`/etc/byre/agent-headless` unless handed a positional argument, which
production never passes.

## The two output streams, and what is authoritative

`byre run` forces a non-interactive launch whatever the caller's terminal:
no `-t`, and the box's stdin is `/dev/null`. The box's stdout and stderr
are therefore two pipes, and the launcher and chassis move everything
except the answer off stdout. The chassis writes the answer to the saved
fd 9 once, at the end, from its held descriptor -- whatever the exit code,
since a reported failure still carries a message worth reading. fd 9 keeps
ordinary output off stdout; it is not a wall. Any process running as the
box's user can reach it through `/proc`, so the answer on stdout stays
agent-trust content whoever wrote it (Accepted residuals).

- **stdout, without `--data`:** the answer and nothing else. On a terminal
  each line is escaped (external text is data, P4); redirected to a file or
  a pipe it is copied byte for byte, because a program wants the bytes.
- **stdout, with `--data`:** the box's stdout is discarded and byre prints
  ONE JSON document after the session, `RunDataVersion` 1: `version`,
  `run_id`, `outcome`, `authority`, `exit_code`, `agent_exit_code`,
  `phase`, `agent`, `launch_record`, `started_at`, `finished_at`,
  `message`, `exit_report`. byre prints it on the host after the box has
  exited, so nothing in the box can write into it.
- **stderr:** everything else -- the agent's live stream and diagnostics
  (in the result dir only under `--quiet`, below), hook output, the build,
  byre's banners, the exit report, and last, one **summary line**:
  `byre: run <run id>: <outcome>, exit <n> (<authority>)`.

**Only the exit code and the `--data` document are authoritative.** The
summary line is informational and forgeable: stderr belongs to the agent
for the whole run, so an agent can print an identical line before byre's.
Every doc that shows the line says to gate on the exit code or `--data`.

`--quiet` sends the agent's live stream and the headless command's stderr
to the result dir only and drops byre's banner-class notices (the worktree
and preset notes, the run-scoped agent line, the exposure and network
lines). When the run fails -- any exit but 0 and 10 -- the chassis prints
the last lines of the command's stderr before the summary, so a refusal's
remedy is never hidden. It never drops an error, a refusal, a warning, the
exit report or the summary line.

The default is loud: the agent's raw event JSON on stderr is accepted. A
readable per-agent rendering of that progress on stderr is recorded as
future work, not owed.

## Outcome classes and `authority`

`byre run` classifies from develop's own typed account of the launch --
how far it got (`prelaunch`, `created`, `attached`), whether a box was
still live after a failed attach, the box's raw status before develop's
128+ decoding, and whether raw `run_args` were in force -- never by
scraping stderr or scanning launch records:

- `ran`: the session completed and the box returned 0 or a chassis code
  (10-16). `agent_exit_code` is that code.
- `refused`: byre declined before a box of this launch existed (its own
  refusals, a live session), or the attach failed with the box still
  running.
- `box_exited`: a box was created and returned a status outside the
  contract -- it never reached the chassis, or what ran it died.
- `byre_failed`: a plain byre error, before or after create (a credential
  delivery failure, a box status past 127), exit 1.
- `crashed`: byre panicked after staging; the document and the summary
  line are printed best-effort and the process exits 70 as any crash does.

`authority` is `verified` unless the resolved `run_args` were non-empty,
when it is `unverified: raw run_args` on EVERY class, `ran` included. With
raw `run_args` the box may have been handed any mount or a different PID 1
(`--entrypoint`), and byre does not parse the block (ADR 0006). Degrading
the claim rather than refusing the launch is P1's never-refuse-the-raw-tier
and P4's degrade-never-assert.

Usage errors (no task source, `--task -` with `--credentials stdin`, a zero,
malformed, or out-of-range `--timeout`, a bad `--credentials`) are the
command line's and exit 2 before anything runs; they print no document
and no summary line.

## The task

The task comes from `--task <file>`, `--task -` (byre's stdin, read to
EOF), or `BYRE_TASK` in byre's OWN environment -- never `env_from_host`,
which is the box's. The flag wins when both are set, with one note on
stderr; neither is a usage error. `--task -` blocks until the writer
closes, by definition, and the docs say so: a step that cannot guarantee
EOF uses a file or `BYRE_TASK`. The box's own stdin is `/dev/null` in
every case.

The task is bounded at `MaxTaskBytes` = 1 MiB from every source, and a
blank task is refused. There is NO implicit task file in the repo: the
project tree does not get to pick the prompt silently (ADR 0047's spirit).

A task file under any directory the box can write -- the work tree, the
main tree, the common git dir, the project store, or an rw bind source --
is read through `OpenDirRootNoFollow` on that root plus `OpenRegularIn`,
so an interior component the agent swapped for a symlink cannot carry the
read outside the root. A path that passes through such a root and
resolves outside it is refused with "pass the resolved path"; a symlinked
leaf is refused on every route. The bytes are held in memory and hashed
once; the hash rides to the box as `BYRE_RUN_TASK_SHA256`. The under-lock
re-read judges the route again, and a file that changed while byre waited
for the setup lock refuses the run.

## Staging the result dir: after `Create`, under the lock

The work tree is agent-writable, and an earlier session's agent may have
left anything at `.byre-run`. Every host write there therefore rides
ADR 0040's machinery: the work tree is opened with `OpenDirRootNoFollow`;
`.byre-run` is created if absent and refused, named, if it is a symlink or
not a directory; the dir is then opened as its own root, every entry in it
is removed through that root (a symlink is unlinked, never followed), and
`.gitignore` (`*`) and `task.md` are published with
`PublishStreamExclusive` (O_EXCL temp plus link(2), failing on any
existing name). Every run therefore starts from an empty dir; archiving
`.byre-run/` before running again is the documented rule.

Staging runs inside the setup lock, immediately AFTER the session
container is created and before the lock is released. The created
container -- in any state -- is the session-ownership token (ADR 0004), so
a second `byre run` on the same worktree loses the container name at
`Create` before it can reach staging, and can never empty the result dir
of the run that owns the box. A refusal before the lock touches the tree
not at all. A staging failure removes the just-created container and the
run is `refused`. The under-lock re-read must also agree with the pre-lock
one on the agent, or the run refuses.

At the end byre publishes a copy of the `--data` document to
`.byre-run/outcome.json` the same exclusive way, for humans. The agent
had the dir for the whole run, so a name already standing there wins and
the copy is skipped, said so. byre reads NOTHING back from the result dir:
the answer reaches stdout through the chassis's held descriptor, and the
verdict comes from the box's exit status.

## The chassis, `byre-headless`

The chassis is a core script embedded in the binary and installed at
`/usr/local/bin/byre-headless` in every image (a conditional copy would
fork the cache-shared core block). As PID 1 of a headless run it:

1. Validates its inputs or exits 16: the result dir is a directory and not
   a symlink; the task file is a regular, non-symlink file directly inside
   it; the task's sha256 matches `BYRE_RUN_TASK_SHA256` (one bounded read,
   the only pathname READ the chassis makes). The hash check exists because
   repo code -- a pending worktree checkout's hooks and filters -- runs in
   the launcher before the chassis and can rewrite `task.md`; a match means
   the agent gets the bytes the host hashed, a mismatch is a loud 16.
2. Claims its own files once, exclusively, and holds the descriptors:
   `transcript.jsonl`, `stderr.log`, `result.md`, `status`, and
   `result.json` written as `started`. Each name is refused when ANY node
   is already there (`[ -e ] || [ -L ]`), then opened under `set -C`:
   noclobber is O_EXCL only for a name that does not exist, and opens a
   pre-existing FIFO or a symlink to one without O_EXCL. From here the
   streams, the answer and the status lines go through held descriptors,
   and every read is a bounded read of one, so a FIFO or symlink the agent
   plants in place of an unlinked name cannot block or redirect PID 1. The
   final `result.json` publish (step 4) is the one separate path
   operation, best-effort.
3. Runs the headless command in its own process group with stdin
   `/dev/null` and the inherited PATH, relaying its streams as above. When
   `BYRE_RUN_TIMEOUT` is set, the chassis sends TERM to the group at the
   deadline and KILL 15 s later. TERM and INT to the chassis are
   forwarded to the group.
4. Maps the result (timeout -> 12 first, then the closed set), publishes
   the final `result.json` by temp and rename (rename replaces a planted
   symlink rather than following it; a planted directory is probed for and
   reported), writes the answer to fd 9, prints its own line on stderr,
   and exits with the code.

`result.json` (schema 1) is a printf JSON writer with a strict escaper;
the core block carries no jq.

**The chassis is guarded at the Dockerfile tail, unconditionally.** The
launcher (the ENTRYPOINT's content) is re-copied after the project block
so a `files` clobber cannot stub it; `byre-headless` is the same thing one
hop on -- the launcher execs it as PID 1 of a headless run, and it
produces that run's verdict -- so it is re-copied too, and status's
guarded-path note and managed-root shadow disclosure (ADR 0052) cover it.
`/etc/byre/agent-headless` is deliberately NOT guarded, exactly like
`agent-cmd`: both are a skill-declared command the user enabled, so a
`files` clobber there replaces the user's own agent with the user's own
config.

## Timeout: opt-in, with no default

A run has no time limit unless `--timeout` is given, and there is no
default. How long a task may take is the caller's call; a default would
kill legitimate long runs that nothing in the config asked to bound.
`--timeout 0` is a usage error that says to omit the flag, and so is a
duration under 1s or over 3650d: the cap keeps the chassis's centisecond
deadline well inside 64 bits. A `BYRE_RUN_TIMEOUT` a raw `run_args` sets
is refused by the chassis (exit 16) when it is malformed, under 1s or
over 3650d; a raw zero (`0`, `0s`, `0.0m`) reads as unset. The chassis
takes the deadline from the monotonic clock in `/proc/uptime` after the
headless command starts, so a wall-clock step during the run cannot move
it. It checks the deadline between polls of the child at 0.1 s intervals,
so the timeout acts within 0.1 s of the deadline, and a run that ends on
its own inside that window keeps its own verdict. TERM goes to the group
at the deadline, KILL 15 s later. No helper process exists and no signal
produces exit 12, so an agent in the same PID namespace cannot disable
the timeout -- PID 1 cannot be killed or stopped from inside the
namespace. Durations are floored to the centisecond with nine fractional
digits kept, so a value within 1e-10 of a boundary can move one
centisecond -- accepted.

The timeout is the chassis's own, so exit 12 means byre's own timeout
fired -- never an agent that exited 124 or 137 on its own, which
`timeout(1)` would make indistinguishable. The deadline is taken after
the headless command starts; the build, the launcher, the hooks and the
chassis's input checks run before that and do not count against it. Stalls there
are bounded on their own paths (the launch gate, the bounded and
headless-aware login hooks).

The documented case for the flag is an agent that hangs rather than fails.
grok is the known one: with a dead login refresh chain it hangs at use
time, inside the agent, where no login hook can see it, and the bundled
`byre-grok-headless` preflight can check only that a stored credential is
present, not that its refresh chain still works. A present `XAI_API_KEY`
takes the stored login out of the picture (the per-run `GROK_HOME`,
below).

## No persistent codex key

codex reads its credential only from a login written into `CODEX_HOME`; a
key in the environment is never used on its own. With codex-shared-auth
enabled, its reconciler runs at every launch and publishes a valid local
credential to the shared store, preferring an API-key file over a shared
ChatGPT login -- so a project-supplied key persisted into the box's real
`CODEX_HOME` (the state volume) becomes machine-wide one launch later,
with nothing scoping it to the project: billing lands on that key, every
other project's box gains a credential nobody granted it, and a shared
login is displaced, all without a prompt. That is a P5 violation, and ADR
0057 already names a credential copy that outlives its window as a hole.

So codex's headless command, given `OPENAI_API_KEY` or
`CODEX_ACCESS_TOKEN`, logs the key into a `CODEX_HOME` made for that one
run (in the box's temp dir, holding only a copy of the user's
`config.toml`), runs there, and removes it on exit. The key wins over an
existing login: the run uses it even when the real `CODEX_HOME` holds a
working login. Without a key it checks the real `CODEX_HOME`'s login,
bounded, and exits 13 if there is none. codex's own login hook stands down
entirely under `BYRE_HEADLESS`, leaving the bounded preflight to the
headless command. It matters on machines that persist -- a laptop, a
self-hosted runner -- and never on a hosted runner the job discards.

## A present grok key runs in its own GROK_HOME

grok's rule is the reverse of what a caller expects: a stored session
wins over `XAI_API_KEY`, so a login in the state volume -- including one
whose refresh chain has died -- would shadow a key the job supplied. So
grok's headless command, like codex's, isolates a present key: with
`XAI_API_KEY` set it runs grok with a `GROK_HOME` made for that one
invocation (a temp dir, removed on exit), where no stored login exists to
win. Without a key it uses the stored login after its presence check, and
`--timeout` is the remedy for a refresh chain that has died.

## Record and schema versions

`--data` carries its own `RunDataVersion` (1); status's `agent_headless`
field arrives with `StatusDataVersion` 5. A headless run's launch record
adds the run id, the task's hash (never its text) and the timeout. A
record's version is the whole schema, so every record this byre writes is
`LaunchRecordVersion` 2, develop's included, and a byre reading a record
from a newer one renders liveness only: an older byre's `status` on any box
this byre launched shows the box running and describes the config, not the
box.

## Accepted residuals

Each is on the security-model page too.

**Filesystem assumptions**

- Nothing untrusted runs concurrently with the first-run hooks or the
  chassis's pre-checks. The onboarding seeds' `mktemp`-then-reopen and the
  chassis's `[ -e ] || [ -L ]` check before each `set -C` open are not
  atomic; repo code has finished by then and the agent has not started.
- ADR 0040's ancestor-symlink residual applies to the result dir: the work
  tree's and `.byre-run`'s final components are guarded, and an
  agent-swapped symlink in an ANCESTOR of the work tree is followed, as for
  every anchored host write.

**Code that runs before the chassis**

- A pending worktree checkout runs the repository's hooks and filters in
  the launcher before `byre-headless` starts. The task hash turns a
  rewritten `task.md` into a loud exit 16, but that code has run.

**Output authority**

- fd 9 reaches any process running as the box's user: through `/proc`
  (`/proc/<pid>/fd/9` of the launcher, or of the chassis as PID 1), and
  outright in the env.d hooks, which are SOURCED into the launcher's own
  shell (ADR 0028). First-run hooks run under `/bin/bash` by path with fd 9
  closed, which removes the accidental route and the planted-interpreter
  route, not the `/proc` one. The exit code and the `--data` document do
  not pass through fd 9.

**Executable lookup**

- The interactive login hooks run on the agent PATH, because they run the
  agent CLIs for device login, so a helper planted in `~/.local/bin` runs
  there. They stand down under a headless launch before any credential
  probe or store write; in an interactive session a plant gains only what
  the agent, which can already write that directory, already has.
- The agent's command resolves on the agent PATH, by design. It is the
  user's choice, and user-installed tools legitimately live on that PATH
  (`~/.local/bin`, project bins), so the chassis execs
  `/etc/byre/agent-headless` with the inherited PATH restored, exactly as
  the launcher execs `/etc/byre/agent-cmd`. The system-PATH pin protects
  byre's own helpers only; the chassis-owned exits 12, 14 and 16 and the
  host's outcome stay out of the agent command's reach.
- The grok-shared-auth broker (ADR 0036) runs on grok's PATH on every
  credential request, headless runs included, so a plant in `~/.local/bin`
  reaches the shared grok login -- which the agent can already read and
  write through the identity volume.

## Rejected

- **The agent's raw exit status as byre's.** Ambiguous against byre's own
  codes and the chassis's; the closed set plus `backend_exit_code` keeps
  the raw number without letting it impersonate a verdict.
- **An env var as the headless trigger.** Reachable by skill env and raw
  `run_args`; argv is not.
- **The chassis's bare name as the container command.** PATH would resolve
  it, and the first PATH entry is agent-writable before the chassis runs.
- **An in-tree outcome file as the gate.** The result dir is the agent's;
  a file there is a claim the agent can squat or forge. The authoritative
  outcome is printed by the host, on stdout, after the box exits.
- **An implicit task file in the repo.** The tree would choose the prompt.
- **Staging before `Create`, or before the lock.** A second run could
  empty the result dir of a run that already owns a box.

[arm: TestHeadlessExitMap, TestHeadlessHasNoHelperProcesses,
TestHeadlessRefusesInvalidInputs,
TestHeadlessFIFOsPlantedDuringTheRunNeverBlock,
TestHeadlessStreamRouting, TestLauncherHeadlessExportsSignalFromArgv,
TestLauncherNonHeadlessUnsetsSignal, TestLauncherHelpersIgnoreInheritedPath,
TestLauncherFirstrunHookIgnoresBashOnPath,
TestLauncherFirstrunHookDoesNotInheritAnswerFd,
TestReservedEnvKnobsCoverChassisReads,
TestDockerfileGuardReassertsAfterProjectFiles, TestAgentHeadlessValidation,
TestHeadlessStageRunsAfterCreateUnderLock,
TestHeadlessEnvBeatsSkillEnvAndLosesToRunArgs, TestRunUsageErrors,
TestRunTaskPrecedence, TestRunTaskFileContained, TestRunStagesAfterCreate,
TestRunForcesNonInteractive, TestRunOutcomeClassification,
TestRunStdoutIsTheAnswer, TestRunDataDocument,
TestLaunchRecordHeadlessScalarsContract]
