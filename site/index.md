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
