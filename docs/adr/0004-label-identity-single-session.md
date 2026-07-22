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
competing box under the setup lock and refuses if one exists. Two rulings:

- **Unreachable other engine: skip-and-disclose, not fail-closed.**
  Failing closed would brick every develop beside an
  installed-but-stopped podman (the common Mac case). The residual
  (live-restore / a remote daemon can keep a box running while
  unreachable) is real but vanishingly narrow -- disclosed, never gated.
- **The check is scoped by a per-worktree engine record**
  (`~/.byre/projects/<id>/engine.<worktree-id>`, written after
  sole-session is established). Steady state (record == configured
  engine) skips the query -- and the ambient disclosure -- entirely; a
  recorded switch checks just the recorded engine; a missing or invalid
  record widens to every other installed engine. Residuals, disclosed: a
  `--self-edit` agent can forge the record, which only downgrades to the
  pre-record behavior in a box that already authors its own next sandbox;
  and a develop run by an older byre doesn't update the record
  (mixed-version staleness) -- both fail toward the wider check or a
  narrower disclosure, never toward silently launching a second agent...
  except the forged steady-state case, which suppresses the check
  entirely and is accepted for the self-edit grant's reasons.
