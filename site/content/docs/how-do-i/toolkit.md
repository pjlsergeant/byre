---
title: Skills & templates
weight: 93
description: writing and sharing skills, templates, layers, presets
---

## Write my own skill?

tldr: `byre skill init <name>`, edit its `skill.toml`, enable it in a
box. I'd recommend asking an agent to do it for you

```sh
byre skill init my-tools        # scaffold ~/.byre/skills/my-tools/
byre skill validate my-tools    # check your work
```

`byre skill fork <id> <new-id>` starts you from an editable copy of a
bundled or installed skill instead. What a skill can contribute:
[the model](/docs/skills-and-templates/); the full authoring reference:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

## Make a template for a stack byre doesn't ship?

tldr: `byre template init <name>`, set its base image and packages,
then `template = "<name>"` in a project.

Or fork the nearest bundled one and edit from there:

```sh
byre template fork byre/python my-elixir
```

What a template may carry:
[templates](/docs/packaging-reference/#templates).

## Share a skill or template with someone else?

tldr: `byre skill pack <id> -o skill.toml`, host the directory anywhere
https reaches; they run `byre skill install <uri> --digest sha256:...`.

`pack` prints the ready `install --digest` command, and `byre skill
inspect <uri>` shows the receiver what they would be trusting before
anything lands. Releasing from a fresh checkout of your published repo
starts with `byre skill adopt <dir>`. Manifests, re-publishing and the
adopt round trip:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md#authoring-and-publishing).

## Share one config baseline across many projects?

tldr: `byre layer new torn`, put the shared config in it
(`byre config --layer torn`), then `extends = "torn"` in each project
(the **Extends** section of the config TUI).

Edits to the layer reach every extending project at its next develop.
Full semantics:
[named layers](/docs/configuration-reference/#named-layers).

## Ship a recommended box config with my project?

tldr: commit a `byre.preset`; whoever clones runs `byre preset apply`.

Make it in the editor and copy the saved project config into the repo
as `byre.preset`. Each package that is not bundled needs a `[sources]`
row so `apply` can offer the install. A config that `byre preset apply`
wrote already carries the rows its preset had; for any other package,
add the row to `byre.preset` by hand -- no flow in byre authors
`[sources]` rows, a gap tracked in
[TODO.md](https://github.com/pjlsergeant/byre/blob/main/TODO.md).
[The full flow](/docs/configuration-reference/#presets-byrepreset).

## Version-control my `~/.byre`?

tldr: `git init ~/.byre` and commit the durable layer --
`default.config`, `templates/`, `layers/` -- not `projects/` or
`packages/`.

The store is plain files by design, so git works on it unmodified.
`projects/` is machine-specific (identities derive from paths),
`packages/` is re-fetchable snapshots and already carries its own
`.gitignore`, and `bundled/` is a display mirror byre regenerates.
