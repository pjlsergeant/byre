---
title: byre -- devlog
---

# byre

**byre** runs an AI coding agent in a throwaway, project-scoped container.
`cd ~/project && byre develop` drops you into a Docker/Podman sandbox that sees
only that project and what you explicitly grant -- not your home dir, keys, or
the rest of your machine.

The code lives on [GitHub](https://github.com/pjlsergeant/byre). This is the
devlog -- notes written as I build it in the open.

> ⚠️ **byre is early, unfinished, and not production-ready.** It's vibe-coded Go
> that I use daily but am not yet proud of. Expect sharp edges and breaking
> changes. See the posts below for exactly what's done and what isn't.

## Posts

- [Day 1 -- byre is working](day-01.md) -- what's done, what's in progress, and
  where it came from (the `moarcode` Claude/Codex review loop).
- [Day 2 -- the file-ownership rabbit hole](day-02.md) -- why host/container file
  ownership was so hard, and how baking the UID in at build time let us delete the
  whole runtime chown machinery.
- [Day 3 -- mounting extra folders with byre config](day-03.md) -- the interactive
  config editor learns to mount more context fast (with a screencast).
- [Day 4 -- where do the worktrees go?](day-04.md) -- `byre worktree` lands, and a
  small question about default directory placement turns into a lesson about byre
  refusing to guess.
- [Day 5 -- the agent reviews its own harness](day-05.md) -- one Saturday: a full
  pre-release review and six phases of fixes from inside the box. The rot was
  accretion, not slop, and the review loop wouldn't let go.
- [Day 6 -- the wall goes up, and we agree what to call things](day-06.md) --
  the default-deny firewall lands and is verified live (rules applied from
  outside the box, fail-closed launch gate, derived port-scoped allowlist),
  then new skills drive a docs taxonomy: glossary, principles, ADRs, and the
  code reconciled to the ratified vocabulary.
