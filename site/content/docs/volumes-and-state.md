---
title: Volumes & state
weight: 70
description: what survives a rebuild, what's disposable, and which hammer resets what
---

The container is throwaway; the volumes and image are not. Every
`byre develop` runs a fresh container and removes it on exit -- what
persists between sessions is the project's image and its **named
volumes**. Two axes describe a volume:

- **Role.** A **cache** volume (`node_modules`, build caches) is
  disposable -- wiping it is a shrug, it regenerates. A **state**
  volume (`.claude`, `.codex`) is precious: the agent's login, history,
  and scratch live there, so anything that would delete one warns and
  names it first.
- **Scope.** A **project** volume (the default) exists once per
  project. A **machine** volume exists once per user per machine and is
  mounted identically by every project that declares it -- that's how
  the shared-auth skills make one agent login serve every box. The
  per-user qualifier is deliberate: two users on a shared machine never
  silently share state.

On the engine the names are legible: `byre-<project-id>-<name>` for
project volumes, `byre-machine-u<uid>-<name>` for machine volumes. A
state volume can carry a one-time `seed` (a host path, or inline
non-secret content) that populates it on first creation -- a copy,
never a live share, and never on machine scope.

Agents log in once, inside the box, and the login lands in the agent's
state volume -- byre never reads or copies host credentials; nothing
crosses unless you enable it, and what you enable, `byre status` shows.
See the cookbook for
[sharing one login across projects](/docs/how-do-i/#save-my-llm-credentials-so-i-dont-need-to-re-auth-for-each-box).

## Which hammer

Four verbs touch built state, from lightest to heaviest:

| Verb | Volumes | Image | Host-side config | Use it when |
|---|---|---|---|---|
| `byre develop` | untouched | rebuilt only on config change (cached) | untouched | every day |
| `byre rebuild` | untouched | rebuilt with `--no-cache` | untouched | pull fresh upstream tool versions |
| `byre reset` | **project volumes wiped** (named first; confirm) | untouched | untouched | agent state is wedged; start the box's contents over |
| `byre forget` | **project volumes wiped** | **removed** | **`~/.byre/projects/<id>/` removed** | remove all trace of byre for this directory |

All four leave your project tree alone -- `forget` removes byre's
*host-side* state, never files in the project. `reset` and `forget` act
across every installed engine, name everything they're about to delete,
and require confirmation (`--force` skips it).

**Machine volumes are never touched silently.** `reset` and `forget`
exclude them and say so -- the machine-wide agent login must never die
as a side effect of resetting one project. To delete one deliberately:
`byre config` -> Volumes -> clear (which refuses while any byre session
is running).

**Moved or renamed the project directory?** The identity is derived
from the path, so the old image and volumes would be orphaned --
`byre rehome <old-id>` migrates them onto the new path. Bare
`byre rehome` lists likely candidates: stored projects whose recorded
path no longer exists.
