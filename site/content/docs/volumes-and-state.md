---
title: Volumes & state
weight: 70
description: what survives a rebuild, what's disposable, and which hammer resets what
---

The container is throwaway; your state is not. Every `byre develop`
runs a fresh container and removes it on exit -- what carries over is
the project's image and its named volumes:

- **Cache volumes** (`node_modules`, build caches) are disposable.
  Losing one costs a re-download; templates set up the obvious ones.
- **State volumes** (`.claude`, `.codex`) are precious: the agent's
  login, history, and scratch. Anything that would delete one names it
  first and asks.
- **Machine-wide volumes** hold shared agent logins -- one per user per
  machine, used by every project that opts in. That's how
  [shared auth](/docs/how-do-i/configure/#save-my-llm-credentials-so-i-dont-need-to-re-auth-for-each-box)
  makes one login serve every box.

Agents log in once, inside the box, and the login lands in the agent's
state volume -- byre never reads or copies host credentials; nothing
crosses unless you enable it, and what you enable, `byre status` shows.

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
and require confirmation (`--force` skips it). Acting across every engine
means they refuse if one cannot be reached -- "wiped" has to be true of all
of it -- naming `--ignore-podman` / `--ignore-docker` as the way past; what
goes unremoved there is then printed. `byre backup` and `byre rehome` take
the same flags.

**Machine volumes are never touched silently.** `reset` and `forget`
exclude them and say so -- the machine-wide agent login must never die
as a side effect of resetting one project. To delete one deliberately: clear
it from the **Volume data** section of `byre config` (which refuses while
any byre session is running).

**Moved or renamed the project directory?** The identity is derived
from the path, so the old image and volumes would be orphaned --
`byre rehome <old-id>` migrates them onto the new path. Bare
`byre rehome` lists likely candidates: stored projects whose recorded
path no longer exists.

**Moving to another machine?** `byre backup` writes one file holding this
project's config and these volumes, and `byre restore` makes a fresh project
from it on the other side -- the recipe is
[move my box to another machine](/docs/how-do-i/recovery/#move-my-box-to-another-machine).
Machine-wide volumes never travel: you log in once on the new machine.

**Worktree boxes share these volumes, and that is the point** -- two
sessions on one repo, one agent login, one cache. If a volume holds data
that cannot take two writers, declare `sharing = "exclusive"` on it and
`byre develop` will refuse to start the second box rather than corrupt
it, naming the worktree that is holding it.

Declaring your own volumes, engine-side naming, one-time seeds, and the
sharing contract: the
[configuration reference](/docs/configuration-reference/#key-reference).
