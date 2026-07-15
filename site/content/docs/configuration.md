---
title: Configuration
weight: 30
description: the byre config TUI and the three-file cascade
---

**`byre config`** opens an interactive editor in your terminal
(keyboard-driven, works over SSH): grants first (mounts, env), then build
choices, in the same vocabulary `byre status` prints. Adding a package or
mounting another repo read-only takes a couple of seconds. `--self-edit`
(a per-session `develop` flag, announced at launch) lets the agent edit
its own box config; edits apply on the next develop.

Underneath, it's a cascade of three TOML files that are always yours to
edit by hand -- last layer wins (scalars override, lists union, and a
later layer can remove an inherited entry: `!name` for named lists,
`remove = true` for ports):

```text
~/.byre/default.config              your personal baseline
~/.byre/templates/<name>/           template config (+ optional files)
~/.byre/projects/<id>/byre.config   this project's overrides (host-side)
```

The vocabulary covers packages, env, mounts, volumes, skills, and MCP
servers (`[[mcp]]` blocks -- declared once, injected into the agent
session, their network reach and consumed tokens attributed in `byre
status`); raw Dockerfile lines and `docker run` args cover the rest.
Full reference:
[docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md).
One sharp edge to know: `env` values are baked into the image
(`docker history` shows them, and they outlive `byre reset`), so don't put
secrets there -- agent logins belong to the agents' own auth flows.

byre reads config only from its host-side store, never from inside the
project -- the project mount is read-write, so the agent could edit a
config that lived there. A repo can ship a `byre.preset` -- a saved
answer to setup's questions -- but cloning gives you a file, not a
prompt: nothing takes effect until you run `byre preset apply`, which
walks you through any missing package installs, shows the composed
box's grants, and writes the project's config on your confirm.
