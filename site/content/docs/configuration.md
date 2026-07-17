---
title: Configuration
weight: 30
description: the byre config editor -- widen or narrow the box in seconds
---

<!-- demo-placeholder: config-tui-walk -->
> 🎬 *[demo slot: the `byre config` TUI walk -- the flagship generated cast]*

**`byre config`** opens an interactive editor in your terminal. It's
keyboard-driven (arrows to move, Enter to edit, Esc to back out, `q` to
leave), works over SSH, and edits take effect on your next
`byre develop` -- relaunch and resume where you left off.

The editor shows one screen, organized the way `byre status` reports:
**grants first**, because they're the part worth reading twice.

- **Grants -- what the box can reach.** Extra host mounts (a path, an
  in-box path, read-only or read-write), published ports
  (localhost-only unless you loudly choose otherwise), env vars, and --
  when a firewall skill is enabled -- the egress doors. A summary line
  at the top keeps the running total honest: `exposure: 6 env vars ·
  network open`.

<!-- image-placeholder: config-grants-section -->
> 🖼 *[image slot: the GRANTS section, a mount being added]*

- **Build -- how the box is made.** Base image, engine (docker/podman),
  packages, enabled skills, MCP servers, Claude Skills. Adding a
  postgres client is: arrow to Packages, type the name, done -- it
  installs on the next develop.
- **Onboarding favourites.** The template/agent/shared-login answers
  the first-run picker will pre-select next time. Preferences, never
  grants -- they apply nothing to any box.
- **Volumes.** The box's named volumes across every engine, including
  clearing one deliberately (the only way byre ever deletes a
  machine-wide volume).
- **Extends.** Chain this project onto a shared config layer.

Everything inherited is labeled with where it came from -- a template,
a layer, a skill -- so you always know which file an edit will land in;
the footer names it outright (`Saves to: ~/.byre/projects/<id>/byre.config`).

## Variants

- `byre config --global` -- edit your personal baseline
  (`~/.byre/default.config`): what every box on this machine starts
  from.
- `byre config --layer <name>` -- edit a shared
  [layer](/docs/configuration-reference/#named-layers).
- `byre develop --self-edit` -- let the *agent* edit this box's config
  from inside; announced at launch, diffed at exit
  ([recipe](/docs/how-do-i/configure/#get-the-coding-agent-to-edit-its-own-byre-config)).

## Prefer a text editor?

Underneath, it's a cascade of plain TOML files that are always yours to
edit by hand -- the editor and `vim ~/.byre/projects/<id>/byre.config`
write the same file. The complete vocabulary, the cascade's merge
rules, presets, and layers live in the
[configuration reference](/docs/configuration-reference/).
