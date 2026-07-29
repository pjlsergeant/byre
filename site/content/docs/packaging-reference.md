---
title: Packaging reference
weight: 55
description: provenance, manifests, digest pinning, and the MCP / Claude Skills wiring contract
params:
  reference: true
---

The precise contract behind
[skills & templates](/docs/skills-and-templates/). For authoring:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md)
covers installing, publishing, and the packaging workflow; the per-field
`skill.toml` vocabulary -- including `[runtime]`, `[agent]`,
`companion_for` and `[[context]]` -- lives in
[docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md).

## Provenance

`byre skill list` / `byre template list` show three provenances:

- **bundled** -- ship inside the byre binary, immutable, `byre/*` ids;
  a display mirror sits at `~/.byre/bundled/` but is never loaded from.
- **local** -- plain editable directories under `~/.byre/skills/` and
  `~/.byre/templates/`, bare or `owner/name`-nested.
- **installed** -- immutable, content-addressed snapshots under
  `~/.byre/packages/<digest>/`.

Forking (`byre skill fork <id> <new-id>`) copies bundled or installed
content into a local editable package under a NEW id. To edit and
publish under the SAME id -- the author's own round trip from a
distribution-repo checkout -- `byre skill adopt <dir>` makes a
directory the local source for the id its manifest declares (a
validated symlink into the store). A local package with the same id and
kind as an installed one shadows the snapshot; `list` labels it
`local (shadows installed <version>)`, and removing the local entry
falls back to the snapshot.

## Distribution

`byre skill pack <owner>/<name> -o skill.toml` enumerates every file in
the package, computes per-file sha256s and exec bits, and writes the
distribution manifest -- a `skill.toml` carrying the payload list plus
the package digest -- and prints a ready `install --digest` command.
`-o` writes after all reads, so the manifest inside the packed
directory is a safe target (a shell redirect would truncate it before
byre reads it).

`byre skill install <uri> --digest sha256:...` fetches, verifies file
by file, and snapshots. `--digest` pins the install to the reviewed
bytes -- a git tag can move; the digest can't. `inspect <uri>` renders
the full trust surface (contributions, grants, payload hashes, digest)
without installing. Reinstalling an identical digest is a no-op;
anything else is a replacement review: version and digest before/after,
payload adds/changes/removals, grant deltas, and the boxes referencing
the package.

Installing grants nothing. The grant is the config line that enables
the package in a box.

Uninstalling warns about configs still referencing the package -- and a
box that slips through fails loudly at its next develop (naming the
exact reinstall command when a `[sources]` hint carries it). Sources
are `https://` or local paths; private-https auth isn't supported yet
-- clone and install from a path.

## Templates

A template is shape, never composition: base image, packages, egress
offers, optional files copied read-only into the image. Its config bans
`skills`, `agent`, and `[sources]` -- composition belongs in a
[preset](/docs/configuration-reference/#presets-byrepreset). Same verb
set: `byre template init / fork / validate / pack / install /
uninstall / inspect`.

## MCP and Claude Skills wiring

Both are declarations -- wiring, not grants -- contributed by config
layers and by skills, merged by name (a later config layer replaces; a
`!name` closure drops one entry, skill-declared included).

**MCP.** A block is `name` plus either `command` (argv, local stdio) or
`url` (remote, self-discriminating). `env` lists variable *names* the
server consumes, never values; `headers` values are `${NAME}` templates
expanded from the box env at launch (`--bearer NAME` is sugar for a
Bearer header); a block's own `egress` opens extra hosts, and a remote
url's host becomes attributed egress automatically. The effective set
bakes into every image and is injected into the agent session; the
agent skill vouches for the injection path. `byre mcp remove` is
closure-smart: declared in this layer means delete, still effective
from below means also write a `!name` closure.

**Claude Skills.** A block is `name` plus a directory whose root holds
`SKILL.md` (`path` host-side in config; `from` package-relative in a
skill.toml). Validated at bake: frontmatter name must match, bounded
size, no symlinks. The merged set bakes into the image in Claude's
native discovery layout, so skills load bare as `/name`; an in-box
skill of the same name shadows byre's delivery -- box state wins. Same
closure-smart remove.
