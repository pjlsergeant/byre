---
title: Configuration
weight: 30
description: the byre config TUI, the cascade, and the complete key reference
---

<!-- demo-placeholder: config-tui-walk -->
> 🎬 *[demo slot: the `byre config` TUI walk -- the flagship generated cast]*

**`byre config`** opens an interactive editor in your terminal
(keyboard-driven, works over SSH): grants first (mounts, env), then build
choices, in the same vocabulary `byre status` prints. Adding a package or
mounting another repo read-only takes a couple of seconds. `byre config
--global` edits your personal baseline instead; `byre config --layer
<name>` edits a named layer. `--self-edit` (a per-session `develop` flag,
announced at launch) lets the agent edit its own box config; edits apply
on the next develop.

Underneath, it's a cascade of TOML files that are always yours to edit by
hand.

## The cascade

```text
~/.byre/default.config              your personal baseline
~/.byre/templates/<name>/           template config (+ optional files)
~/.byre/layers/<name>/layer.config  named layers, pulled in via `extends`
~/.byre/projects/<id>/byre.config   this project's overrides (host-side)
```

Layers merge in that order -- defaults, template, the `extends` chain
(root first), project -- and the last layer to speak wins:

- **Scalars override.** A later layer's `base` or `engine` replaces an
  earlier one's. (One deliberate exception: `seed_prefs` is a monotonic
  opt-in -- once any layer turns it on, a later layer can't turn it off.)
- **Lists union.** `skills`, `apt`, `mounts` and friends accumulate
  across layers.
- **A later layer can remove an inherited entry:** `"!name"` for named
  lists (skills, apt, npm_global, volumes; mounts by target), and
  `remove = true` for ports (keyed by container port). `!host` entries
  in `egress` are closures: they subtract from the final derived
  allowlist, skill-declared endpoints included.
- **`env` has no unset** -- override the value instead.
- **Raw blocks** (`dockerfile_pre`, `dockerfile_post`, `run_args`) are
  append-only unions: no per-line removal.

byre reads config only from its host-side store (`~/.byre`), never from
inside the project -- the project mount is read-write, so the agent could
edit a config that lived there. The one repo-side artifact is a
**preset** (below), and it is inert until you apply it.

## Key reference

The complete vocabulary. Everything here is legal in any layer, with the
exceptions noted inline.

**Composition**

- `engine` -- `"auto"` (default: docker if present, else podman),
  `"docker"`, or `"podman"`.
- `template` -- which `~/.byre/templates/<name>` to layer in. Project
  config only -- banned in layers and template configs.
- `agent` -- `"claude"`, `"codex"`, `"gemini"`, `"grok"`, or
  `"opencode"`: whose command launches in the foreground. Implicitly
  enables that agent's skill.
- `base` -- the `FROM` image (Debian-derived).
- `extends` -- name this file's one parent layer. Chains are linear and
  walked to the root; cycles fail loudly. Legal in a project config or a
  layer; banned in templates and the default config.
- `skills` -- enabled skills, by name (bundled bare: `"firewall"`;
  installed qualified: `"owner/name"`).
- `sources` -- `id -> { uri, digest }` acquisition hints for packages a
  preset references. Never auto-fetched; only ever printed as install
  commands.

**Grants** (each live entry shows in `byre status`)

- `[[mounts]]` -- `host`, `target`, `mode` (`"ro"` default, `"rw"`),
  `disabled` (kept and shown, but not bound). Extra host folders beyond
  the project.
- `[[ports]]` -- `container` (required), `host` (defaults to mirror),
  `interface` (defaults to `127.0.0.1` -- localhost-only unless you
  loudly say otherwise). Publishes a box port to the host.
- `[env_from_host]` -- the one host-to-box data channel:
  `KEY = "env:HOST_VAR"` (a host env var, at runtime),
  `KEY = "git:config.key"` (from `git config`), `KEY = "tz:"` (your
  timezone), `KEY = ""` (disable an inherited entry). Values resolve at
  launch and are never baked into the image. Git identity, `TERM`, and
  `TZ` pass through by default.
- `egress` -- firewall allowlist extensions, `"host[:port]"` (port
  defaults to 443); `"!host[:port]"` closes a door, even a
  skill-declared one. Only meaningful with a network-posture skill
  enabled.
- `egress_offered` -- declared-but-closed convenience doors; always
  inert until the config UI opens one into `egress`.

**Build** (baked into the image)

- `apt` -- packages to install.
- `npm_global` -- extra global npm tools.
- `[env]` -- literal env vars. **Baked into the image**: `docker
  history` shows them and they outlive `byre reset`, so never put
  secrets here -- credentials belong to the agents' own login flows (or
  `env_from_host` for runtime values).
- `[files]` -- host paths copied into the image, read-only.
- `dockerfile_pre` / `dockerfile_post` -- raw Dockerfile lines, emitted
  before / after the core block. The build-time escape hatch, and the
  honest place for project setup that should happen once per build
  rather than at every launch.

**Runtime**

- `[[volumes]]` -- named volumes: `name`, `role` (`"cache"` or
  `"state"`), `target`, optional `scope = "machine"` (per-user
  machine-wide; default is per-project), optional `seed` for state
  volumes (`host` path or `literal` + `path`; never on machine scope).
  See [Volumes & state](/docs/volumes-and-state/).
- `run_args` -- raw `docker run` flags, appended after byre's own, so
  yours win. Cap resources (`--cpus`, `--memory`), change networking --
  anything the engine accepts. byre never parses inside it; posture
  claims in `byre status` degrade honestly when it's present. One
  documented footgun: identity-changing flags (`--user`, `--userns`)
  break the baked-UID ownership model and are unsupported.

**Agent session wiring** (declarations, not grants -- see them with
`byre mcp list` / `byre claude-skill list`)

- `[[mcp]]` -- an MCP server: `name`, then either `command` (argv, local
  stdio) or `url` (remote); `env` names the variables it may consume
  (names, never values), `headers` templates (`${NAME}`) expand at
  launch, `egress` adds hosts beyond the URL when the firewall is on.
- `[[claude_skills]]` -- a Claude Skill: `name` + `path` to a host
  directory whose root holds `SKILL.md`.

**Preferences** (picker-owned; never a grant)

- `seed_prefs` -- one-time copy of the agent's curated non-secret pref
  files into a fresh state volume.
- `worktree_base` -- where `byre worktree` creates worktrees:
  `"sibling"` or a host path; unset refuses with instructions.
- `shared_auth` -- the first-run picker's remembered favourite. A
  preference about future *answers*, stripped from every resolved
  config.

## Presets: `byre.preset`

A preset is a complete proposed config in `byre.config` format,
conventionally shipped as `byre.preset` in a repo. Cloning gives you a
file, not a prompt: nothing takes effect until you run
`byre preset apply`, which chauffeurs any missing package installs (each
with its own grant summary and confirm), shows the composed box's grants
with a diff against your current config -- applying replaces the whole
file -- and writes the project's config on your confirm.
`byre preset inspect` is the same review without the write.

## Named layers

A **layer** (`~/.byre/layers/<name>/layer.config`) is a config file any
project or other layer pulls in with `extends`. It carries everything a
config can except `template`, and it's live: edit the layer once and
every extending project picks it up on its next develop. Layers aren't
packages -- no versions, no installing; to share one, send the file.
Every inherited setting is attributed to its layer in `byre config`, and
`byre status` shows the chain. Layer files sit outside `--self-edit`'s
writable set, so a boxed agent can never edit a file that propagates
into other boxes. Manage with `byre layer new / list / validate`.

## Escape-hatch symmetry

Build side: `base`, `apt`, `npm_global`, `[files]`, `[env]`, then
`dockerfile_pre`/`dockerfile_post` for the rest. Runtime side:
`[[mounts]]`, `[[volumes]]`, `[env_from_host]`, then `run_args` for the
rest. There is deliberately no full-Dockerfile opt-out -- if you want to
own the whole file, `byre dockerfile` prints it and you can
[leave](/docs/how-do-i/#stop-using-byre).
