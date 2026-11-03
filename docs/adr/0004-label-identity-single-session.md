# Sessions found by label; develop is single-session per project

A running session is identified by the `byre.project` label (plus the
per-worktree `byre.workdir` label -- see ADR 0009), not by assuming a
fixed container name. `develop` is **single-session per directory**:
if a session is already running for the directory it reports that session
(and how to get a shell via `byre shell`) instead of starting a second.

Why: two containers on one directory would share the project's state
volumes (agent auth/history) and race them -- name-safe is not state-safe.
Genuine parallelism is worktrees (ADR 0009): different workdir, isolated
workspace, deliberately shared project identity.

Consequences: the identity label is the one thing `run_args` cannot
override (re-asserted after it; ADR 0006) -- lifecycle and `byre status`
must always be able to find the session.

## Single-session across an engine switch (2026-07-22)

Flipping `engine` mid-session makes a box on the previous engine invisible
to the configured runner, so a second develop would launch a second agent
on the same tree. develop therefore checks OTHER installed engines for a
competing box under the setup lock and refuses if one exists.

**An unreachable other engine is skipped and disclosed, not failed
closed.** Failing closed would brick every develop beside an
installed-but-stopped podman (the common Mac case).

**The check is scoped by a per-worktree engine record**
(`~/.byre/projects/<id>/engine.<worktree-id>`), written only after the
check passed; a refusal never advances it. Its transitions:

- A clean record naming the configured engine: no query and no
  disclosure.
- A recorded switch: check exactly the engines the record implicates.
- A missing or invalid record: check every other installed engine.
- An implicated engine skipped as unreachable: kept as
  `unresolved=<engine>`, re-checked and re-disclosed every develop until
  one finds it reachable and empty -- an inconclusive check never advances
  the record into silence.
- An untracked check (no prior record): its skips are not carried, so a
  fresh project beside a stopped podman is disclosed once and converges.
- An implicated engine gone from PATH: disclosed once and dropped. byre
  cannot query a CLI-less daemon, and re-disclosing after a deliberate
  uninstall polices the user.

Residuals, disclosed:

- An unreachable daemon may still hold a running box (live-restore, a
  remote daemon); the skip discloses it and never gates. The once-only
  disclosure for untracked skips accepts the same residual.
- A `--self-edit` agent can forge the record, suppressing the check in a
  box that already authors its own next sandbox (accepted for the
  self-edit grant's reasons).
- A develop run by an older byre does not update the record (mixed-version
  staleness).
