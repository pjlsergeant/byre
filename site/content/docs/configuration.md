---
title: Configuration
weight: 30
description: the byre config editor -- widen or narrow the box in seconds
---

<!-- demo-placeholder: config-tui-walk -->

**`byre config`** opens an interactive editor in your terminal. It's
keyboard-driven (arrows to move, Enter to edit, Esc to back out; Esc from
the form leaves), works over SSH, and edits take effect on your next
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

- **Build -- how the box is made.** Base image, engine (docker/podman),
  packages, enabled skills, MCP servers, Claude Skills, and your
  agent's standing
  [instructions](/docs/how-do-i/configure/#give-my-agent-standing-instructions-in-every-box)
  (prose opens in your `$EDITOR`; inherited snippets are readable in
  full, attributed to their layer). Adding a postgres client is: arrow
  to Packages, type the name, done -- it installs on the next develop.
  Also here: whether to seed the agent's curated prefs into a state
  volume being created (three-state -- inherit, on, off), and a
  read-only list of the `[sources]` install hints `byre preset apply`
  recorded for the packages this config names.
- **Onboarding favourites.** The template and agent answers the
  first-run picker will pre-select next time. Preferences, never
  grants -- they apply nothing to any box. `--global` also shows your
  stored shared-credentials answer read-only, beside the checkbox that
  decides whether new projects take it without asking -- and flags it if
  the companion skill it names is no longer installed.
- **Volumes.** The named volumes this config declares -- add, edit, and
  turn an inherited one off, with every skill's contribution shown
  read-only beside them.
- **Volume data.** The same volumes as they exist on every engine right
  now, including clearing one deliberately (the only way byre ever
  deletes a machine-wide volume).
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

Underneath, it's a cascade of plain TOML files, and they're always
yours to edit by hand -- the editor and
`vim ~/.byre/projects/<id>/byre.config` write the same file, held to
the same validation. The two coexist safely: byre's saves edit only
what you changed, so your comments, formatting, and hand-arranged
layout survive every save byte-for-byte. That's a right byre defends, never a requirement
it imposes: `byre config` is the interface, every config feature is
reachable there -- editable, or shown read-only with the flow that
writes it named (the `[sources]` install hints `byre preset apply`
records; your stored shared-credentials answer) -- and no recipe or
error message will ever send you into the files.

One exception, and it only arrives if you went into the files first: a
config file hand-edited into something the TOML parser refuses. The
editor will not reconcile against a document it cannot read -- it would
have to guess what to preserve, and guess wrong about the rest of your
file -- so it hands you the repair instead: the file, and the line,
column and key wherever it can pin them down. Where it can't, it says
so by staying quiet about the position rather than guessing at one.
byre's own saves cannot turn a loadable file into an unloadable one;
only a hand edit gets you here.

The complete vocabulary, the cascade's merge rules, presets,
and layers live in the
[configuration reference](/docs/configuration-reference/).
