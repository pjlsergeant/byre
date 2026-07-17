---
title: How it works
weight: 25
description: config in, Dockerfile out, container up -- every step a file you can read
params:
  reference: true
---

byre is a transparent layer over the Docker or Podman you already run.
There is no daemon, no service, no hidden state: every launch is the
same four steps, and every step's output is inspectable.

## The pipeline

1. **Resolve config.** The [cascade](/docs/configuration-reference/) -- your
   defaults, the template, any layers, the project -- merges into one
   resolved config. `byre status` shows the result and attributes every
   setting to the layer or skill that contributed it.
2. **Generate a Dockerfile.** Deterministically: the same config always
   produces byte-identical output, written to
   `~/.byre/projects/<id>/context/Dockerfile.generated` and printed any time by
   `byre dockerfile`. Blocks emit in a stable, cache-friendly order --
   base, template, byre's core, each enabled skill, your project's tail
   -- so ten projects on one template share the expensive layers in
   Docker's own cache.
3. **Build.** Plain `docker build`. byre owns no caching layer of its
   own: an unchanged config is a full cache hit; a change rebuilds
   only from the changed instruction onward. `byre rebuild` (`--no-cache`) is the
   deliberate valve for pulling fresh upstream versions.
4. **Run.** `docker run --rm -it`, in the foreground -- see the exact
   command with `byre dockerrun`. The container is throwaway; what
   persists is the image and the [named volumes](/docs/volumes-and-state/).
   Sessions are identified by labels, not guessed names, and `develop`
   is single-session per directory ([worktrees](/docs/worktrees/) are
   the parallel story).

## The dev identity

The image bakes a `dev` user matching your UID/GID, so the agent runs
unprivileged and every file it writes into the mounted project lands
owned by *you* -- no root-owned droppings, no chown pass. (Rootless
Podman gets the same result through a generic id plus a userns
mapping.) The build uses root only where builds must; the session drops
to `dev` before anything runs.

## Nothing is hidden

- `byre status` -- what the box can reach, attributed.
- `byre dockerfile` -- the exact image definition.
- `byre dockerrun` -- the exact run command.
- `byre ejectfirewall` -- the firewall's outside-the-box step, as a
  standalone script.

Those four commands are also the exit: byre either generates the build
or isn't involved, so the day you stop wanting it, the files are yours
and [you leave](/docs/how-do-i/recovery/#stop-using-byre) with a working
Dockerfile. The deeper design -- block ordering, the chassis, engine
abstraction, project identity -- is
[docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md).
