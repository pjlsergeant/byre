# byre configuration reference

The complete config vocabulary, as published at
https://getbyre.com/docs/configuration-reference/ -- baked into this box
so it is readable offline.

The configuration editor (https://getbyre.com/docs/configuration/) and your text editor
write the same files: a cascade of TOML that byre resolves at every
develop. This page is the complete contract.

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
  earlier one's -- `seed_prefs` included: an explicit `false` in a later
  layer turns an inherited opt-in off; leaving it unset inherits.
- **Lists union; entries with an identity replace.** `skills`, `apt` and
  `egress` accumulate across layers. Anything with an identity replaces
  the inherited entry of the same identity instead: mounts by target,
  volumes and the `[[mcp]]`/`[[claude_skills]]`/`[[context]]`
  declarations by name, ports by container port.
- **A later layer can remove an inherited entry:** `"!name"` for named
  lists (skills, apt, volumes; mounts by target), and
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
  preset references. Never fetched silently: `preset apply` uses them to
  chauffeur consented installs; everywhere else they're only printed as
  install commands.

**Grants** (each live entry shows in `byre status`)

- `[[mounts]]` -- `host`, `target`, `mode` (`"ro"` default, `"rw"`),
  `disabled` (kept and shown, but not bound). Extra host folders beyond
  the project.
- `[[ports]]` -- `container` (required), `host` (defaults to mirror),
  `interface` (defaults to `127.0.0.1` -- localhost-only unless you
  loudly say otherwise). Publishes a box port to the host.
- `[env_from_host]` -- the deliberate host-to-box value channel:
  `KEY = "env:HOST_VAR"` (a host env var, at runtime),
  `KEY = "git:config.key"` (from `git config`), `KEY = "tz:"` (your
  timezone), `KEY = ""` (disable an inherited entry). Values resolve at
  launch and are never baked into the image. Git identity, `TERM`, and
  `TZ` pass through by default. The `BYRE_` prefix is reserved and
  refused here too -- a passthrough lands in the box's environment
  exactly as an `[env]` literal does, so the same reservation applies;
  the deliberate-override route is the `run_args` one below.
  Two more sources make a row a **project credential** rather than a
  host passthrough -- a value this config carries itself, encrypted:
  `KEY = "encrypted:<blob>"` arrives as an environment variable, and
  `KEY = "encrypted-file:<blob>"` arrives as a file on the session
  tmpfs with `KEY` holding its path. You never type those rows: `byre
  credentials set KEY` writes one; its input is a single-line masked terminal
  prompt or whole piped stdin for multiline values, never a command-line
  argument. The configuration editor's Env screen writes the same encrypted
  row through its own single-line masked field. `KEY = ""` in a nearer layer disables an
  inherited credential like any other row. An env-kind value must be
  NUL-free and at most 64 KiB; the file-kind ceiling is 256 KiB.
- `[credentials]` -- written by `byre credentials`, not by hand: the
  `identity` that opens THIS file's encrypted rows (an age key wrapped
  under your passphrase) and the cleartext `recipient` new values are
  encrypted to. The block belongs to the physical file and never merges
  through the cascade, so a project config can never open a layer's
  rows. `byre credentials rekey` changes the passphrase and leaves every
  stored value byte-identical.
  Delivery is **blocking**: byre asks for one passphrase per config file
  that contributes a credential, root-most first, and any failure to
  unlock or decrypt stops the launch naming the file, the row, and the
  remedy. `byre develop --credentials=skip` launches deliberately
  without them; `--credentials=stdin` reads passphrases from stdin, one
  per line.
- `egress` -- firewall allowlist extensions, `"host[:port]"` (port
  defaults to 443); `"!host[:port]"` closes a door, even a
  skill-declared one. Only meaningful with a network-posture skill
  enabled.
- `egress_offered` -- declared-but-closed convenience doors; always
  inert until the config UI opens one into `egress`.

**Build** (baked into the image)

- `apt` -- packages to install.
- `[env]` -- literal env vars. **Baked into the image**: `docker
  history` shows them and they outlive `byre reset`, so never put
  secrets here -- credentials belong to the agents' own login flows (or
  `env_from_host` for runtime values). The `BYRE_` prefix is reserved
  (those variables parameterize byre's own launch machinery) and
  refused in both user channels, here and in `[env_from_host]`; to
  override one deliberately use
  `run_args = ["-e", "BYRE_X=..."]`, which `byre status` shows verbatim
  while degrading the claims it affects.
- `[files]` -- stages a project file into the build, so a
  `dockerfile_post` line can use it. The build context holds nothing of
  your project otherwise, so this is what makes the standard
  dependency-caching pattern possible:

  ```toml
  files = { "requirements.txt" = "/tmp/requirements.txt" }
  dockerfile_post = ["RUN pip install -r /tmp/requirements.txt"]
  ```

  The install then happens once per build and is cached in the image,
  rather than at every launch. Files are COPY'd in the project block,
  after `dockerfile_pre` and before `dockerfile_post` -- so a
  `dockerfile_post` `RUN` can read them and a `dockerfile_pre` one
  cannot. Sources are **project-relative** (`planFiles`
  refuses an absolute path, a `..` escape, and a symlink out of the
  tree); the destination is an absolute path in the image, and what
  lands there is read-only. It is NOT a way to pull a file in from
  outside the repo -- that is a mount, or a seeded volume. Note that a
  destination under `/workspace` is masked by the project bind mount at
  runtime, and one under a state volume's mountpoint (like `~/.claude`)
  by the volume.
- `dockerfile_pre` / `dockerfile_post` -- raw Dockerfile lines, emitted
  before / after the core block. The build-time raw block, and the
  honest place for project setup that should happen once per build
  rather than at every launch.

**Runtime**

- `[[volumes]]` -- named volumes: `name`, `role` (`"cache"` or
  `"state"`), `target`, optional `scope = "machine"` (per-user
  machine-wide; default is per-project), optional `seed` for state
  volumes (`host` path or `literal` + `path`; never on machine scope --
  a seed populates a fresh state volume exactly once, a copy, never a
  live share). On the engine the names are legible:
  `byre-<project-id>-<name>` for project scope,
  `byre-machine-u<uid>-<name>` for machine scope -- per *user*
  deliberately, so two users on a shared box never silently share
  state. Optional `sharing = "exclusive"` declares the volume
  single-writer: worktree boxes of one project run concurrently and
  mount the same volumes, so `byre develop` refuses to start a second
  box that would mount it while another holds it (exit `3`). The
  default, `"shared"`, is what every byre volume has always been.
  Project scope only -- byre can only see this project's boxes. See
  Volumes & state (https://getbyre.com/docs/volumes-and-state/) for the user-side model.
- `seed_prefs` -- one-time copy of the agent's curated non-secret pref
  files into a fresh state volume. Three states, not two (inherit, on,
  off), so a layer that turns it on can be turned back off downstream.
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
  directory whose root holds `SKILL.md`. (A skill.toml contribution
  declares `from` -- a directory relative to the skill -- instead of
  `path`; `from` is not config vocabulary.)
- `[[context]]` -- standing agent instructions: `name` + inline `text`
  or a host `file` (`~/…` or absolute, read at bake). Injected into the
  agent's instructions at launch, after skill context (see
  how instructions reach the agent (https://getbyre.com/docs/how-do-i/configure/#give-my-agent-standing-instructions-in-every-box));
  layers replace by `name`, `!name` removes.

**Host workflow**

- `worktree_base` -- where `byre worktree` creates worktrees:
  `"sibling"` or a host path; unset refuses with instructions. A live
  cascade value like any other, but it steers a host command rather
  than the box, so `byre config --global` owns it and the project
  editor leaves it out.

**Onboarding state** (picker-owned; never a grant)

- `[defaults]` -- state about how the NEXT onboarding runs, never
  anything a box receives. The one section stripped whole from every
  resolved config, so nothing under it can acquire teeth by accident.
  - `shared_auth` -- the first-run picker's remembered favourite: a
    preference about future *answers*. (Before 2026-07-28 this was a
    top-level key; the old spelling is still read and is migrated here
    on the next write.)
  - `skip_questions` -- configure new projects from your stored answers
    without prompting. Includes the shared-auth pick, which *grants*
    (the companion skill goes into the new project), so this key is the
    standing consent for that; `byre develop` says out loud when it
    acted on it.

  Template and agent "defaults" are deliberately NOT here: they are the
  plain `template` and `agent` keys in `default.config`, real cascade
  values that apply to every project. The picker pre-selects them
  because they *are* the inherited value.

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
into other boxes. Manage with `byre layer new / list / validate`; the
design record is
[ADR 0035](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0035-named-layers-and-extends.md).

## Raw-block symmetry

Build side: `base`, `apt`, `[files]`, `[env]`, then
`dockerfile_pre`/`dockerfile_post` for the rest. Runtime side:
`[[mounts]]`, `[[volumes]]`, `[env_from_host]`, then `run_args` for the
rest. There is deliberately no full-Dockerfile opt-out -- if you want to
own the whole file, `byre dockerfile` prints it and you can
leave (https://getbyre.com/docs/how-do-i/recovery/#stop-using-byre).
