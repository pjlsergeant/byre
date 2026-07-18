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

`byre worktree` runs the repository's git **inside the box**, never on
your machine: the worktree is registered by a short-lived container from
the project image, and its files are checked out at first launch -- so a
repository's own git hooks and checkout filters run boxed, alongside the
rest of its code, instead of on the host. One consequence: `byre worktree`
needs Docker or Podman installed (it says so and stops if neither is
there), and git -- plus any filter tooling like git-lfs -- has to be in
the box. Made your worktree yourself with `git worktree add`? Then you
already chose to run its git on the host; byre just develops in it.
