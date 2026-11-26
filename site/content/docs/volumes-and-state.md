---
title: Volumes & state
weight: 70
description: what survives a rebuild, what's disposable, and which hammer resets what
---

The container is throwaway; your state is not. Every `byre develop`
runs a fresh container and removes it on exit -- what carries over is
the project's image and its named volumes.

Every volume has a **role** -- cache or state -- and a **scope** --
project or machine:

- **Cache** volumes (`node_modules`, build caches) are disposable.
  Losing one costs a re-download; templates set up the obvious ones.
- **State** volumes (`.claude`, `.codex`) are precious: the agent's
  login, history, and scratch. Anything that would delete one names it
  first and asks (`--force` skips the question on `reset` and `forget`).
- **Project-scoped** volumes belong to one project. **Machine-scoped**
  volumes are one per user per machine, used by every project that opts
  in -- that is how
  [shared auth](/docs/how-do-i/configure/#save-my-llm-credentials-so-i-dont-need-to-re-auth-for-each-box)
  makes one login serve every box.

Agents log in once, inside the box, and the login lands in the agent's
state volume -- byre never reads or copies host credentials; nothing
crosses unless you enable it, and what you enable, `byre status` shows.

## Which hammer

Five verbs touch built state, from lightest to heaviest:

| Verb | Volumes | Image | Host-side config | Use it when |
|---|---|---|---|---|
| `byre develop` | untouched | rebuilt only on config change (cached) | untouched | every day |
| `byre build` | untouched | built as develop builds it (cached); tag printed | untouched | warm the cache, check the config builds, read the tag -- no session |
| `byre rebuild` | untouched | rebuilt with `--no-cache --pull=true` | untouched | pull a fresh base image and upstream tool versions |
| `byre reset` | **project volumes wiped** (named first; confirm) | untouched | untouched | agent state is wedged; start the box's contents over |
| `byre forget` | **project volumes wiped** | **removed** | **`~/.byre/projects/<id>/` removed** | remove all trace of byre for this directory |

All five leave your project tree alone -- `forget` removes byre's
*host-side* state, never files in the project. `reset` and `forget` act
across every installed engine, because a project's volumes can live in
one the config no longer names, and name everything they're about to
delete before asking. An engine that cannot be reached stops them, so
"wiped" is true of all of it; `--ignore-podman` / `--ignore-docker`
leaves that engine out, and byre prints what went unchecked or
unremoved there. `byre backup` and `byre rehome` take the same flags.

**Machine-scoped volumes are never touched silently.** `reset` and
`forget` exclude them and say so -- the machine-wide agent login must never die
as a side effect of resetting one project. To get rid of one deliberately,
open `byre config --global` and press enter on its row under **Volumes**
(or use the **Volume data** section, which lists every machine volume on
disk). **Delete** removes the volume, and refuses while a box is using
it, naming those boxes. **Clear** empties it and keeps it, and works
while boxes are running -- the way to force a fresh shared login without
closing every window. Running boxes see the volume empty at once, but
an agent already running keeps what it read in memory and may write it
back -- a login refreshing its token, say. So after a Clear, restart the
agent in each box still using the volume, then log in once.

**Moved the project, or moving it to another machine?** `byre rehome`
and `byre backup` / `byre restore` carry these volumes across:
[the recipes](/docs/how-do-i/recovery/#move-or-rename-a-project-directory).
Machine-scoped volumes never travel: you log in once on the new machine.

**Worktree boxes share these volumes, and that is the point** -- two
sessions on one repo, one agent login, one cache. A volume that cannot
take two writers can be declared
[exclusive](/docs/configuration-reference/#key-reference).

Declaring your own volumes, engine-side naming, one-time seeds, and the
sharing contract: the
[configuration reference](/docs/configuration-reference/#key-reference).
