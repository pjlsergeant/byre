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

The two files are DESCRIPTORS, not paths. The chassis creates them
exclusively, once, and holds them open; a command that needs a real path
writes its own temp file and copies it to the descriptor. The descriptors
are the command's OWN: it unsets the `BYRE_RUN_RESULT_FILE` /
`BYRE_RUN_STATUS_FILE` variables before starting anything, and no child
it starts -- the agent, a credential or version probe, or a helper such
as `jq` or `tee` -- ever inherits either descriptor: the scripts close fds
5 and 6 for every command they exec, and every subshell and command
substitution they open closes them as its first act, so no exec'd child ever holds
them. A child that outlived
the script while holding one could rewrite the answer or the status lines
after the script exited and before the chassis reads them. The command
also keeps its own helpers on the image's system PATH
(`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`) and
invokes the agent by its launch adapter's absolute path with the
inherited agent PATH restored for that invocation alone, for the reason
the next section gives for the chassis. The launch adapters it invokes
(`byre-claude-launch`, `byre-codex-mcp-launch`, `byre-grok-launch`), the
interactive-only `byre-opencode-mcp-launch`, and the codex-shared-auth
reconciler follow the same rule: their own helpers
(`cat`, `jq`, `head`, `wc`, `iconv`, `cp`, `flock`, `mktemp`) run on the
system PATH, and only the final exec of the agent CLI gets the inherited
agent PATH. byre's launcher, which runs before all of them, does the same
from its first line: it saves the inherited PATH as the agent PATH and
runs its own helpers (`tr`, `sleep`, `git`, `sed`, `head`, `ls`, `grep`,
`rm`, `cat`) on the system PATH -- the worktree populate's checkout, the
egress and self-edit lines of the session context, the credential wait
-- and sources the env.d hooks there, re-pinned before each one. Only the
first-run hooks (they run the agent CLIs for device login) and the final
exec of the agent command, the chassis included, get the agent PATH back.
A helper planted in `~/.local/bin` by repo code therefore
never runs inside byre's launch or credential machinery; the interactive
login hooks are the remaining exception (Accepted residuals). This is a convention of the contract, not a
containment: the answer and the status lines are agent-trust either way
(below), so an agent that writes a different answer through its own
script changes only what it could have written anyway.

The command's stdout is the agent's live event stream and its stderr the
agent's diagnostics. The chassis records each to the result dir
(`transcript.jsonl`, `stderr.log`) and relays both to the box's stderr. The
command never reaches the box's real stdout.

**The exit status is a CLOSED set, never the agent's raw status.** The
command exits exactly one of: `0` (success, and fd 5 is non-empty), `10`
(the agent REPORTED failure), `11` (the agent process failed --
`backend_exit` written first), `13` (credential preflight failed, with the
remedy on stderr), `15` (the backend is not in the box). The chassis
treats anything else as the command breaking the contract and exits 14: a
shell death, a signal, a raw status passed up by mistake, or `0` with an
empty answer. A raw agent exit of 10 can only reach the host as 11 with
`backend_exit=10`. Passing raw statuses through would make every number
ambiguous -- an agent that happens to exit 13 would read as "no
credential" -- and a script gating on the code could not tell byre's
verdict from the agent's.

The command must not read a terminal, must give the agent `/dev/null` or
the task file on stdin (an agent handed an open pipe waits on it forever),
and must bound every credential probe; a probe that times out is a 13.
The bound is `timeout -k`, so a probe that ignores TERM is still cut off
-- the bundled headless commands and the codex login hook bound every
credential and version probe that way.

The chassis owns its descriptors, `result.json`, the timeout when one was
asked for, the exit map, signal forwarding and the stream routing. The
command owns every agent flag.

**The inventory.** `BYRE_HEADLESS` and every `BYRE_RUN_*` name join the
reserved-env inventory under two claims, `headless run` and `launch` (a
skill setting one is aiming at a run's inputs, and the chassis is what the
launcher execs). The host re-asserts its `BYRE_RUN_*` set after every
skill's runtime env, the `BYRE_EGRESS` precedent, so no skill's
`[runtime].env` can skew what a run is told; a knob the run was not asked
for (`BYRE_RUN_TIMEOUT`, `BYRE_RUN_QUIET`) is re-asserted empty, which the
chassis reads as unset. Raw `run_args` still follow
on the argv and win (ADR 0006 is not reopened). A test pins that every
`BYRE_*` name any chassis script reads is in the inventory, which also
brought in `BYRE_FETCH_RUN_RETRIES`.

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

Why the absolute path, not the bare name: the image's PATH puts the dev
user's `~/.local/bin` ahead of the system directories (agent CLIs install
there), and that directory is agent-writable -- and so writable by repo
code, which runs in the launcher before the chassis (a pending worktree
checkout's hooks and filters). A bare `byre-headless` would resolve
through PATH to whatever such code planted there under that name. The
absolute path names the copy the Dockerfile tail guards (below), so a
planted same-named binary is never what runs. The chassis applies the same
reasoning to itself: it runs with PATH pinned to the system directories
for every helper it calls (`sha256sum`, `tee`, `setsid` and the rest), and
restores the inherited PATH only for the headless command, whose agent
binaries legitimately live under `~/.local/bin`.

The chassis has no env seam for which command it runs: it execs
`/etc/byre/agent-headless` unless handed a positional argument, which
production never passes.

## The two output streams, and what is authoritative

`byre run` forces a non-interactive launch whatever the caller's terminal:
no `-t`, and the box's stdin is `/dev/null`. The box's stdout and stderr
are therefore two pipes, and the launcher and chassis have moved
everything except the answer off stdout. The chassis writes the answer to
the saved fd 9 once, at the end, from its held descriptor -- whatever the
exit code, since a reported failure still carries a message worth reading.

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
  (kept in the result dir only under `--quiet`, except that a failed run
  prints the last lines of the command's stderr), hook output, the build,
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
malformed, or out-of-range `--timeout`, a bad `--credentials`) are the command line's and
exit 2 before anything runs; they print no document and no summary line.

**The exit-code table carries a caveat.** byre's own codes are 1, 2, 3 and
70; the chassis's are 10-16. A box that launched and exited 1, 2, 3 or 70
reaches the host as that number too (develop's 0-127 passthrough), and byre
itself can exit 1 or 3 after the box was created. `--data`'s `outcome` and
`phase` tell these apart, so a script that needs certainty runs with
`--data` and reads `authority`. The table is on the generated commands
page.

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
   pre-existing FIFO or a symlink to one without O_EXCL. After this step
   the chassis never opens a pathname inside the dir again -- every write
   goes to a held descriptor and every read is a bounded read of one -- so
   a FIFO or symlink the agent plants in place of an unlinked name cannot
   block or redirect PID 1.
3. Runs the headless command in its own process group with stdin
   `/dev/null` and its inherited PATH (the chassis's own helpers run on a
   PATH pinned to the system directories), relaying its streams as above.
   When `BYRE_RUN_TIMEOUT` is set, the chassis's own timer sends TERM to
   the group on expiry and KILL 15 s later. TERM and INT to the chassis are forwarded to the group.
4. Maps the result (timeout -> 12 first, then the closed set), publishes
   the final `result.json` best-effort by temp and rename (rename replaces
   a planted symlink rather than following it; a planted directory is
   probed for and reported), writes the answer to fd 9, prints its own
   line on stderr, and exits with the code.

`result.json` (schema 1) is a printf JSON writer with a strict escaper;
the core block carries no jq.

**The chassis is guarded at the Dockerfile tail, unconditionally.** The
launcher (the ENTRYPOINT's content) was already re-copied after the project
block so a `files` clobber cannot stub it; `byre-headless` is the same
thing one hop on -- the launcher execs it as PID 1 of a headless run, and
it produces that run's verdict -- so it is re-copied too, and status's
guarded-path note and managed-root shadow disclosure (ADR 0052) cover it.
`/etc/byre/agent-headless` is deliberately NOT guarded, exactly like
`agent-cmd`: both are a skill-declared command the user enabled, so a
`files` clobber there replaces the user's own agent with the user's own
config.

## Timeout: opt-in, with no default

A run has no time limit unless `--timeout` is given, and there is no
default, ever. How long a task may take is the caller's call; a default
would kill legitimate long runs that nothing in the config asked to bound.
`--timeout 0` is a usage error that says to omit the flag, and so is a
duration under 1s or over 3650d: the cap keeps the chassis's
centisecond deadline (31,536,000,000 at most) well inside 64 bits. The
chassis compares that deadline on the monotonic clock in `/proc/uptime`,
to the centisecond and rounded down, so a wall-clock step during the run
cannot disable the timer, the real timer's signal always passes the
deadline, and a forged `USR1` is ignored until the final 10 ms. The
chassis refuses the same range in a
`BYRE_RUN_TIMEOUT` a raw `run_args` sets (exit 16). When set, the
timer is the chassis's own, so exit 12 means byre's timer fired -- never
an agent that exited 124 or 137 on its own, which `timeout(1)` would make
indistinguishable. The timer starts with the chassis; stalls before it are
bounded on their own paths (the launch gate, the bounded and
headless-aware login hooks).

The documented case for the flag is an agent that hangs rather than fails.
grok is the known one: with a dead login refresh chain it hangs at use
time, inside the agent, where no login hook can see it. (The bundled grok
skill's headless command, `byre-grok-headless`, is where the flag is
owed: without a key it runs on the stored login, and its preflight can
only check that a credential is present, not that its refresh chain
still works. With `XAI_API_KEY` set the stored login is out of the
picture -- next section.)

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
win. Without a key it uses the stored login after its presence check, and the documented `--timeout` is the remedy for a refresh chain
that has died.

## Version bumps

- `RunDataVersion` = 1, the `--data` document's own version, beside
  `StatusDataVersion` with the same comment convention: bumped when a
  consumer would have to change, noted in CHANGES.
- `StatusDataVersion` 4 -> 5: `agent_headless`, always written.
- `LaunchRecordVersion` 1 -> 2: a headless run's record carries `headless
  = true`, `run_id`, `task_sha256` (the hash, never the text) and
  `run_timeout` (empty when none), as scalars before `[image]`. A develop
  record carries none of the four, so its bytes differ from version 1
  only in `record`. The cost: a record's version is the whole schema, and
  a byre reading a record from a newer one renders liveness only -- so an
  older byre's `status` on any box this byre launched, develop's included,
  shows the box running and describes the config, not the box.

## Accepted residuals

Each is on the security-model page too.

- **Nothing untrusted runs concurrently with the first-run hooks or the
  chassis's pre-checks, and two windows rely on it.** The onboarding seeds'
  `mktemp`-then-reopen, and the chassis's `[ -e ] || [ -L ]` check before
  each `set -C` open, are not atomic. Repo code (a pending checkout's hooks
  and filters) runs earlier in the launcher and has finished by then; the
  agent has not started.
- **ADR 0040's ancestor-symlink residual applies to the result dir.** The
  work tree's and `.byre-run`'s final components are guarded; an
  agent-swapped symlink in an ANCESTOR of the work tree is followed, as for
  every anchored host write.
- **Repo code runs before the chassis.** A pending worktree checkout runs
  the repository's hooks and filters in the launcher, before `byre-headless`
  starts; that code can rewrite `.byre-run/task.md`. The chassis verifies
  the task's hash before exec, so the result is a loud exit 16, never a
  silent swap -- but the code has run.
- **fd 9 is reachable by any process in the box.** First-run hooks run
  under `/bin/bash` by path with fd 9 closed, which removes the accidental
  route (a hook inheriting the box's real stdout) and the planted-
  interpreter route (a `bash` on the agent-writable PATH running in place
  of every hook while holding it). It is not containment: any process
  running as the box's user can open a descriptor another one holds
  through `/proc` (`/proc/<pid>/fd/9` of the launcher, or of the chassis
  as PID 1) and write the box's stdout. Launch env hooks (`env.d`) are
  SOURCED into the launcher's own shell (ADR 0028) and hold fd 9 outright.
  So the stdout answer is agent-trust content whoever wrote it (the next
  entry); the exit code and the `--data` document are what a script gates
  on, and neither passes through fd 9.
- **Everything the box can write is agent-trust.** The contents of
  `.byre-run/` (`result.md`, `result.json`, the transcript, the copy of
  `outcome.json`), the answer on stdout, and everything on stderr --
  the summary line included -- can be shaped by the agent. byre reads
  none of it back; a script gates on the exit code or the `--data`
  document. That is also why the descriptor convention above is a
  convention: an agent that writes a different answer or status line
  through its own script changes only what it could have written anyway.
- **The interactive login hooks run on the agent PATH.** The launcher
  hands every first-run hook the inherited PATH, so the codex-login,
  grok-login, opencode-login, claude-shared-auth and grok-shared-auth
  first-run hooks still run the agent CLIs and their own helpers through
  it, and a helper planted in `~/.local/bin` runs there. They are
  interactive-login code that stands down under a headless launch before
  any credential probe or store write, so a headless run executes none of
  their helpers, and in an interactive session a plant there gains only
  what the agent, which can already write that directory, already has.
  The standing-down covers grok-shared-auth's
  first-run hook only, not its broker (next-but-one entry).
- **The agent's command resolves on the agent PATH.** The chassis execs
  `/etc/byre/agent-headless`, which is `exec <the skill's headless
  command>`, with the inherited PATH restored so the agent's own
  subprocesses see the image PATH -- exactly as the launcher execs
  `/etc/byre/agent-cmd` for an interactive session. A same-named script
  planted in `~/.local/bin` by repo code therefore replaces the user's
  agent with repo code's for that session, headless or interactive. It
  gains agent-tier trust only: exits 0, 10, 11, 13 and 15 are what the
  agent's own script reports and are agent-trust already, while the
  chassis-owned 12, 14 and 16 and the host's outcome are not forgeable
  this way. It is the same acceptance this ADR makes for the unguarded
  `agent-headless`. Closing it would mean byre resolving the command on
  the system PATH while handing the agent its PATH through a separate
  variable, a contract change deferred to its own decision.
- **The grok-shared-auth broker runs on grok's PATH, headless runs
  included.** The broker (ADR 0036) is `GROK_AUTH_PROVIDER_COMMAND`,
  which grok runs on every credential request it makes -- in a headless
  run as in an interactive one -- with grok's PATH, the inherited agent
  PATH. The command's `bash` and the broker's helpers (`jq`, `curl`,
  `flock`, `date`, `mv`) resolve there, so a plant in `~/.local/bin` runs
  inside the code that refreshes the machine-wide grok login, and reaches
  that login. It gains nothing the agent lacks: the shared store sits in
  the identity volume mounted in the box, which the agent can already
  read and write, as it can that directory.
- **Exit codes are not unique to their owners.** A box that exits 1, 2, 3
  or 70 reaches the host as that number; `--data` tells them apart.

## Rejected

- **A wrapper agent skill** -- above.
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
- **A default timeout.** Above.
- **Persisting an env key into the codex state volume.** Above.
- **An implicit task file in the repo.** The tree would choose the prompt.
- **Staging before `Create`, or before the lock.** A second run could
  empty the result dir of a run that already owns a box.

[arm: TestHeadlessExitMap, TestHeadlessNoTimeoutMeansNoTimer,
TestHeadlessRefusesInvalidInputs,
TestHeadlessFIFOsPlantedDuringTheRunNeverBlock,
TestHeadlessStreamRouting, TestLauncherHeadlessExportsSignalFromArgv,
TestLauncherNonHeadlessUnsetsSignal,
TestReservedEnvKnobsCoverChassisReads,
TestDockerfileGuardReassertsAfterProjectFiles, TestAgentHeadlessValidation,
TestHeadlessStageRunsAfterCreateUnderLock,
TestHeadlessEnvBeatsSkillEnvAndLosesToRunArgs, TestRunUsageErrors,
TestRunTaskPrecedence, TestRunTaskFileContained, TestRunStagesAfterCreate,
TestRunForcesNonInteractive, TestRunOutcomeClassification,
TestRunStdoutIsTheAnswer, TestRunDataDocument,
TestLaunchRecordHeadlessScalarsContract,
TestLauncherFirstrunHookIgnoresBashOnPath,
TestLauncherFirstrunHookDoesNotInheritAnswerFd]
