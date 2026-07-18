---
title: Worktrees
weight: 60
description: parallel agents, the git way
---

```sh
byre worktree fix-flaky-tests
```

creates a linked git worktree on branch `fix-flaky-tests` (existing or
new) and starts a session in it. The worktree inherits the repo's config,
image, and volumes -- the agent is already logged in -- but runs in its own
container against its own checkout, so sessions run side by side.
Worktrees you made yourself with `git worktree add` inherit the same way:
just `byre develop` in them. You pick once where new worktrees live
(`byre config --global`). Commits land in the shared object store,
`byre status` shows every worktree session in the project, and
`reset`/`forget` name their blast radius before touching anything shared.

The new worktree's files are checked out **inside the box**, at first
launch, rather than on your machine -- so a repository's own git hooks and
checkout filters run boxed, alongside the rest of its code, instead of on
the host. One consequence: `byre worktree` needs Docker or Podman installed
(it says so and stops if neither is there), and a filter's tooling (e.g.
git-lfs) has to be in the box. Made your worktree yourself with `git
worktree add`? Then you already chose to check it out on the host; byre
just develops in it.
