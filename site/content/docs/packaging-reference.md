---
title: Packaging reference
weight: 55
description: user operations on packages -- provenance, inspect, install with --digest, replacement review, uninstall -- plus required skills
params:
  reference: true
---

The user operations behind
[skills & templates](/docs/skills-and-templates/): seeing what you have,
reviewing and installing a package, and removing one. Authoring --
provenance, fork and adopt, packing, validation, the per-field
`skill.toml` vocabulary -- is in
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

## Provenance

`byre skill list` / `byre template list` show where each package came
from: **bundled** (inside the byre binary, `byre/*` ids), **local** (an
editable directory under `~/.byre/`), or **installed** (an immutable
snapshot). A local package that shadows an installed one of the same id
says so: `local (shadows installed <version>)`. What each provenance
means, and how a package becomes local:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

## Inspecting and installing

```sh
byre skill inspect <uri>
byre skill install <uri> --digest sha256:...
```

`inspect <uri>` renders the full trust surface -- contributions, grants,
payload hashes, digest -- without installing. `install` fetches,
verifies file by file, and snapshots. `--digest` pins the install to the
reviewed bytes: a git tag can move; the digest can't.

Installing grants nothing. The grant is the config line that enables
the package in a box.

**Replacing a package.** Reinstalling an identical digest is a no-op;
anything else is a replacement review: version and digest before/after,
payload adds/changes/removals, grant deltas, and the boxes referencing
the package.

**Uninstalling.** `byre skill uninstall <id>` warns about configs still
referencing the package -- and a box that slips through fails loudly at
its next develop (naming the exact reinstall command when a `[sources]`
hint carries it).

**Sources** are `https://` URLs or local paths. Private https with
authentication is not supported: clone the package and install it from
the path.

## Templates

A template is shape, never composition: base image, packages, egress
offers, optional files copied read-only into the image. Its config bans
`skills`, `agent`, and `[sources]` -- composition belongs in a
[preset](/docs/configuration-reference/#presets-byrepreset). The
`byre template` commands mirror `byre skill`'s: the
[commands page](/docs/commands/) lists them.

## MCP and Claude Skills wiring

Both are declarations -- wiring, not grants -- contributed by config
layers and by skills. Using them:
[MCP & Claude Skills](/docs/mcp-and-claude-skills/). How they merge,
bake and reach each agent:
[docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md)
(MCP provisioning, Claude Skills delivery).

## Required skills

`requires = ["<skill>", ...]` at the top level of a skill.toml names
skills this one does not work without; each must already be enabled, and
byre never enables one itself. `byre skill inspect` and the install
summary print `requires: <name>`. Resolution:
[ADR 0062](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0062-skill-requires.md).
