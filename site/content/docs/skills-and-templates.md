---
title: Skills & templates
weight: 50
description: your toolkit, in every folder -- and how to build and share it
---

byre ships templates for go, node, and python, and agent skills for
Claude Code, Codex, Gemini, Grok, and OpenCode; the first `byre develop`
asks which you want, and that's the setup. But the packaging system is
yours: everything the bundled packages do, a package you write can do.

## What a skill is

A **skill** is a portable bundle that can contribute to every layer byre
controls:

- **Build** -- apt packages, raw Dockerfile lines, files baked into the
  image (a binary onto `PATH`, a hook script into the launch sequence).
- **Runtime** -- env, and the network endpoints it needs (`egress`,
  unioned into the firewall allowlist when the firewall is on).
- **Agent context** -- standing instructions appended to the agent's
  memory file in every box that enables the skill.
- **State** -- named volumes, cache or state, project- or
  machine-scoped.

An *agent skill* is a skill with an `[agent]` block: the command byre
launches, plus its state volume. More than one agent skill can be
enabled in a box -- the config's `agent` key decides which one launches;
the rest ride along (byre's own box runs Claude with Codex installed
beside it as an independent reviewer).

**Enabling a skill is trusting it.** Skills ship raw Dockerfile lines
and launch hooks; the typed fields exist so grants read as data, not as
a sandbox. Installing grants nothing -- the grant is the moment a box's
config lists the skill -- and everything an enabled skill reaches is
named by `byre status`. The sharp version:
[security model](/docs/security-model/).

## Where packages come from

`byre skill list` shows three provenances: **bundled** (ship inside the
byre binary, immutable), **local** (`~/.byre/skills/`, plain editable
directories), and **installed** (immutable, content-addressed snapshots
under `~/.byre/packages/`). Templates mirror the same model under
`byre template ...`.

## Authoring

```sh
byre skill init my-tools        # scaffold ~/.byre/skills/my-tools/
byre skill validate my-tools    # strict parse + resolve check
```

Edit the `skill.toml`, enable it in a box (`byre config` -> Skills, or
`skills = ["my-tools"]`), develop. To modify a bundled or installed
package, fork it into a local editable copy:
`byre skill fork byre/go my-go`. The full authoring reference is
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

A **template** is shape, not behavior: base image, packages, egress
offers, optional files copied into the image. It never references other
packages -- composition belongs in a [preset](/docs/configuration/#presets-byrepreset).
Same verbs: `byre template init / fork / validate / pack / install`.

## Sharing

```sh
byre skill pack pete/my-tools > skill.toml    # distribution manifest
byre skill install https://... --digest sha256:...
```

`pack` emits a manifest carrying every payload file's hash and the
package digest. `install` fetches, verifies file by file, and snapshots
-- and grants nothing until a box enables the result. `inspect <uri>`
renders the full trust surface (contributions, grants, hashes) without
installing; `--digest` pins the install to the bytes you reviewed.
Uninstalling lists the boxes still referencing the package first.

## MCP servers and Claude Skills

Both ride the same declare-once model: `[[mcp]]` and `[[claude_skills]]`
blocks in config (or contributed by skills), managed with
`byre mcp add / remove / list` and `byre claude-skill add / remove /
list`. Declarations are wiring, not grants -- the effective set, with
every entry attributed to the layer or skill that declared it, is
`byre mcp list` / `byre claude-skill list`; a `!name` entry drops one
skill-declared server without disabling the skill. MCP declarations bake
into the image and inject into the agent session; an MCP server's
network reach is attributed in `byre status`.
