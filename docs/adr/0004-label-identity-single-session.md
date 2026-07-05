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
