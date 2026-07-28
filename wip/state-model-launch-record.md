# The missing "box that was actually launched" -- external review, verified

**Status: OPEN, undesigned.** Four findings from an external review (relayed by
Pete, 2026-07-28), each verified CONFIRMED against HEAD the same day with the
evidence below. Nothing here is decided; remedy shapes are the reviewer's
proposals plus session notes. TODO.md carries the item; delete this file when
the work ships or the decisions land in ADRs.

The through-line, in the reviewer's words: byre models *current configuration*
very well and does not model *the immutable resolved configuration that created
a particular running box*. That one gap produces findings 1 and 2; findings 3
and 4 are independent but were verified in the same pass.

## 1. `byre status` reports intent, not reality -- CONFIRMED

Status resolves the CURRENT config/skills/host-env and renders those; its only
engine queries are liveness/labels (`RunningContainersByLabel`,
`ContainerLabels` for orphan detection -- status.go). It never inspects the
running container's mounts, ports, env, or network mode, and no surface
distinguishes running-container state from next-launch config: `Container:
running` renders directly beneath rows resolved from the file as it is NOW.
Edit the config while a box runs and status shows the safer next-launch values
beside the live container. Labels carry identity only (runparams.go) -- no
resolved-config data survives the launch anywhere queryable. The develop
banner IS an accurate launch-time record (rendered from the same `rv` that fed
runParams) but it is transient stderr.

Reviewer's line: "a desired-state renderer wearing the clothes of an
observed-state inspector."

## 2. Saves and launches are not transactionally ordered -- CONFIRMED

`develop` resolves config/skills and detects the engine BEFORE taking the
setup lock (resolve at develop.go:86; lock at :213 -- the code's own comment
names the ordering); the only post-lock revalidation is `requireRecorded`, a
forget-fence. The editor's save takes the same lock -- which serializes the
write against the LATER build/create, not the EARLIER read. So: develop
resolves a config carrying a sensitive mount; the editor saves the removal
under the lock and reports success; develop acquires the lock and launches
the stale resolution. Same shape in rebuild and worktree. The editor's drift
check protects the editor from the file, not develop from a stale read.

Reviewer's fix shape: not another mutex -- resolve under the lock, or
revalidate a configuration generation after acquiring it and retry on drift.

## 3. Host executable lookup is a confused-deputy route -- CONFIRMED

`runner.Detect` calls LookPath and DISCARDS the absolute result, keeping the
bare name; all ~20 engine call sites and the post-session git probes
(`gitprobe.go:27`, bare `"git"`) re-resolve through PATH on every exec.
Nothing pins a resolved path or checks it against agent-writable roots
(hostopen governs file I/O, not exec resolution). On a host whose PATH
carries an ABSOLUTE agent-writable entry (direnv `.venv/bin`, a project
`.bin`) ahead of the real binary, the agent plants `git`/`docker` and BYRE
executes it host-side automatically -- the exit report's own probes fire at
every session end. Go's ErrDot refusal covers relative entries only.

The doctrine gap is precise: ADR 0047 covers the project tree as an execution
channel with THE HUMAN as the trigger; byre as the AUTOMATIC trigger is
uncovered -- and the exit report added by 0047 is itself such a trigger.

Reviewer's fix (and the session's read: treat as the urgent, independent
piece): resolve absolutes before the box starts, refuse binaries beneath
agent-writable roots, reuse the pinned paths for the process lifetime.

## 4. Worktree sharing assumes every volume is concurrency-safe -- CONFIRMED

Volume names are project-scoped (`byre-<id>-<name>`, naming.go); concurrent
worktree boxes mount the identical set, and ARCHITECTURE presents that as the
point. ADR 0009's safety rationale is agent-STATE-specific ("agents already
handle concurrent access to one state dir") -- but volumes are now generic:
skills contribute arbitrary `[[volumes]]`, config declares them, the editor
writes them (2026-07-28). The `Volume` grammar has Role/Scope/Seed and no
exclusivity or concurrency-safety vocabulary; Scope only WIDENS sharing.
"Enabling a skill means trusting it" does not imply its SQLite database
supports concurrent mutation.

## The reviewer's priority remedy

A content-addressed **launch record** attached to every container: resolved
config, skill/package identities, effective grants, image digest, generation.
Then `status` shows **Running** and **Next launch** separately whenever they
differ; finding 2's revalidation compares generations under the lock; finding
4 gains a place to record per-volume expectations. Session note: the develop
banner already computes most of the record's content -- the work is making it
durable and addressable (a label-carried digest pointing at a store file is
the obvious shape), not computing it.

## Relationship to shipped work (so this isn't re-litigated)

ADR 0052 (runtime mount shadowing) discloses that byre CANNOT vouch for
covered paths -- an honesty patch over exactly this gap, not a closure. The
2026-07-28 reserved-env one-degradation-set work makes banner and status agree
about NEXT launch; neither speaks for the running box. The per-worktree engine
records (ADR 0004) are the nearest existing thing to a launch record and carry
one fact (the engine). None of today's ~75 commits changes any verdict above.
