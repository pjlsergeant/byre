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
   `~/.byre/projects/<id>/context/Dockerfile.generated` and printed any
   time by `byre dockerfile`. Shared blocks come before project-specific
   ones, so projects on one template share the expensive layers in
   Docker's cache, and a short guard at the end re-copies byre's own
   launcher and enforcement files, so a `files` entry aimed at one of
   those paths can't leave its content there. A runtime mount over the
   same path is not guarded: byre
   [discloses it](/docs/security-model/#the-engine-and-the-host)
   instead. byre never parses a raw build line, so `byre status` flags
   raw lines as not introspected and `byre status --full` shows them
   verbatim. Block order and the guard:
   [docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md)
   (Image generation).
3. **Build.** Plain `docker build`, with no caching layer of byre's own:
   an unchanged config is a cache hit, and `byre rebuild` is the
   deliberate valve for a republished base image and fresh upstream
   versions.
4. **Run.** `docker run --rm -it`, in the foreground -- see the exact
   command with `byre dockerrun`. The container is throwaway; what
   persists is the image and the [named volumes](/docs/volumes-and-state/).
   Sessions are identified by labels, not guessed names, and a directory
   has one session at a time ([worktrees](/docs/worktrees/) are the
   parallel story). `byre run` starts the other kind of session: the
   same box, no terminal, one task handed to the agent's headless mode,
   and byre's verdict as the exit code
   ([how](/docs/how-do-i/workflow/#run-one-task-headlessly-ci)).

## The dev identity

The image bakes a `dev` user matching your UID/GID, so the agent runs
unprivileged and every file it writes into the mounted project lands
owned by *you* -- no root-owned droppings, no chown pass. (Rootless
Podman gets the same result through a generic id plus a userns
mapping.) The build uses root only where builds must; the session drops
to `dev` before anything runs.

## Inspection commands

- `byre status` -- what the box can reach, attributed.
- `byre dockerfile` -- the image definition.
- `byre dockerrun` -- the run command.
- `byre ejectfirewall` -- the firewall's outside-the-box step.

The [commands page](/docs/commands/#inspection) lists them with the rest.

Those commands are also the exit: byre either generates the build
or isn't involved, so the day you stop wanting it, the files are yours
and [you leave](/docs/how-do-i/recovery/#stop-using-byre) with a working
Dockerfile. The deeper design -- block ordering, the chassis, engine
abstraction, project identity -- is
[docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md).
