---
title: Quickstart
weight: 20
description: first run, the picker, and byre status
---

The first `byre develop` in a project asks a few quick questions (template,
agent, and -- for agents that support it -- whether this box shares a
machine-wide login) and remembers your answers: your favourites become the
pre-selected defaults. Log the agent in once; the login persists, per
project, across rebuilds. To skip the questions:

```sh
byre develop --template go --agent claude
```

Ask the box what it can touch, any time:

```text
$ byre status
Project id:   my-project-pjl-069d95
Agent:        byre/claude
Template:     byre/go                 bundled 0.2.0
Engine:       docker
Project:      ~/my-project -> /workspace  (rw)
Network:      open
Ports:        none
Host mounts:  none
Skills:       byre/claude             bundled 0.2.0
State vols:   .claude
Cache vols:   node_modules
Container:    running (0d95f3a2c1b4)
```
