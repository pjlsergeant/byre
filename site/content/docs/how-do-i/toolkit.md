---
title: Skills & templates
weight: 93
description: writing and sharing skills, templates, layers, presets
---

## Write my own skill?

tldr: `byre skill init <name>`, edit its `skill.toml`, enable it in a
box.

A skill can install packages, ship files into the image, declare
volumes and network endpoints, and carry agent context --
[the model](/docs/skills-and-templates/). `byre skill validate` checks
your work; `byre skill fork` turns any bundled or installed package
into an editable local copy to start from. The full authoring
reference:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

## Make a template for a stack byre doesn't ship?

tldr: `byre template init <name>`, set its base image and packages,
then `template = "<name>"` in a project.

A template is shape, not behavior: base image, packages, egress offers,
optional files. Fork the nearest bundled one
(`byre template fork byre/python my-elixir`) and edit from there.

## Share a skill or template with someone else?

tldr: `byre skill pack <id> > skill.toml`, host the directory anywhere
https reaches; they run `byre skill install <uri> --digest sha256:...`.

`pack` emits a manifest carrying every file's hash and the package
digest, so `install` verifies byte-for-byte and `--digest` pins to
exactly what was reviewed. `byre skill inspect <uri>` shows the full
trust surface -- contributions, grants, hashes -- without installing,
and installing grants nothing until a box enables the skill.

## Share one config baseline across many projects?

tldr: `byre layer new torn`, put the shared config in it
(`byre config --layer torn`), then `extends = "torn"` in each project
(the **Extends** section of `byre config`).

A **named layer** is a config file at `~/.byre/layers/<name>/layer.config`
that any project (or another layer -- chains work) pulls in with
`extends`. It slots between the template and the project in the cascade
and carries everything a config can except `template` -- skills, egress,
env, mounts, the lot. It's live: edit the layer once and every extending
project picks it up on its next develop. Layers aren't packages -- no
versions, no installing; to share one, send the file. `byre status` shows
the chain, and every inherited setting is attributed to its layer in
`byre config`. See
[ADR 0035](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0035-named-layers-and-extends.md).

## Ship a recommended box config with my project?

tldr: commit a `byre.preset`; whoever clones runs `byre preset apply`.

A preset is a complete proposed config in `byre.config` format --
cloning gives your teammates a file, not a prompt. `apply` walks them
through any missing package installs (each with its own grant review),
shows the composed box's grants, and writes the project config on their
confirm; `[sources]` hints pin where the packages come from and their
digests. [The full flow](/docs/configuration-reference/#presets-byrepreset).

## Version-control my `~/.byre`?

tldr: `git init ~/.byre` and commit the durable layer --
`default.config`, `templates/`, `layers/` -- not `projects/` or
`packages/`.

The store is plain files by design, so git works on it unmodified.
`projects/` is machine-specific (identities derive from paths),
`packages/` is re-fetchable snapshots and already carries its own
`.gitignore`, and `bundled/` is a display mirror byre regenerates.
Your defaults, templates, and layers are the part worth keeping -- and
the part worth carrying to a new machine.
