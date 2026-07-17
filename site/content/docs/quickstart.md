---
title: Quickstart
weight: 20
description: first run, the picker, and byre status
---

<!-- demo-placeholder: quickstart-picker-status -->
> 🎬 *[demo slot: the first-run picker, ending on `byre status` -- generated cast]*

```sh
cd ~/my-project
byre develop
```

The first `byre develop` in a project asks a few quick questions and
remembers your answers:

- **Template** -- the stack the box is built for (go, node, python, or
  one of your own). It decides the base tooling and cache volumes.
- **Agent** -- which coding agent launches in the box (Claude Code,
  Codex, Gemini, Grok, OpenCode).
- **Shared login** -- for agents that support it, whether this box uses
  your machine-wide credentials instead of a per-project login (see
  [How do I…?](/docs/how-do-i/configure/#save-my-llm-credentials-so-i-dont-need-to-re-auth-for-each-box)).

Your favourites become the pre-selected defaults, so the second project's
picker is a couple of Enters. Log the agent in once; the login persists,
per project, across rebuilds. To skip the questions entirely:

```sh
byre develop --template go --agent claude
```

byre generates a Dockerfile from your config, builds it (only on a
cache-miss -- relaunches are fast), and drops you into the agent in the
foreground. Exit the agent and the session ends; run `byre develop` again
and `/resume` to pick up where you left off.

Ask the box what it can touch, any time:

```text
$ byre status
Project id:   my-project-pjl-069d95
Agent:        byre/claude
Template:     byre/go                 bundled 0.2.0
Engine:       docker
Project:      ~/my-project -> /workspace  (rw)
Network:      open
Ports:        none
Host mounts:  none
Skills:       byre/claude             bundled 0.2.0
State vols:   .claude
Cache vols:   none
Container:    running (0d95f3a2c1b4)
```

From here: [configuration](/docs/configuration/) to widen or narrow the
box, [worktrees](/docs/worktrees/) for parallel sessions, and
[what's boxed, what isn't](/docs/whats-boxed/) for the contract you just
signed.
