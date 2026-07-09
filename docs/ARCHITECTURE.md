# byre -- architecture

How byre works, as built. This file is the mechanics reference; it
describes current state only. Its sibling documents own the other lanes:
`GLOSSARY.md` (canonical vocabulary -- this file uses it), `PRINCIPLES.md`
(standing commitments), `adr/` (point-in-time decisions and their
rationale), `TODO.md` at the repo root (open work), `marketing/`
(positioning and launch copy).

byre is a small Go binary that runs an AI coding agent in a throwaway,
project-scoped container. `cd ~/project && byre develop` drops the agent
into a box that sees this project and what you explicitly grant -- not
your home dir, keys, or the rest of your machine.

Name: Scots/Northern-English for a cowshed (pronounced like *buyer*) --
the enclosure you keep the thing in so it doesn't wander off.

## The shape

byre is a **transparent templating layer for running agents in
containers**. It is a convenience for the common case; it never stops you
writing Docker (PRINCIPLES.md #3). The split that defines the project
(PRINCIPLES.md #2):

- **Core** owns the chassis -- the agent-runtime scaffolding everyone
  reinvents (host UID/GID baked into the image, git identity, the
  launcher, credential persistence). Core ships **no opinions**.
- **Skills** own the opinions. The workflow is a skill; the firewall is a
  skill; **the agent itself is a skill** (ADR 0005) -- byre ships agent
  skills for Claude, Codex, and Gemini, and `agent` selects which one
  launches. You compose your baseline from skills.

Image-building is solved -- Docker does it well and everyone knows the
syntax. byre owns the frame around your Docker, not the Docker.

## Security contract

What byre actually guarantees -- stated plainly so it isn't mistaken for
more:

- **Isolated:** the host filesystem, environment, and credentials. The
  agent sees *only* what you explicitly mount or pass -- not your home
  dir, SSH/cloud keys, env, or the rest of the machine. This is byre's
  core promise. (One narrow, named exception: git `user.name`/`user.email`
  are passed through for commit attribution -- see *The chassis*. Nothing
  else from your env or `.gitconfig`.)
- **Not isolated -- by design:** the **network** (open by default, see
  below) and the **mounted project itself** (`/workspace` is read-write so
  the agent can edit and commit your code). An agent with both can
  exfiltrate the project it's working on. byre never promised otherwise;
  the box protects *everything else*, not the code you handed it.
- **Opt-in holes:** anything that widens the boundary is a grant you
  choose, not a default -- a host-socket mount, extra host mounts, ports.
  byre makes every grant legible via `byre status` (PRINCIPLES.md #4); it
  does not block them. Core bundles none of these -- core ships empty.

The doctrine behind every line above -- the threat model is the agent,
never the user; legibility instead of gates; degrade claims, never
refuse -- is PRINCIPLES.md #1 (the footgun doctrine). It is normative and
lives there, not here.

### Network

Open to the world by default. No firewall, no `NET_ADMIN`/`NET_RAW` added
by core; because byre makes no network-containment claim by default, the
container's stock capability set is not a security surface byre reasons
about.

Network restriction is the built-in **firewall skill** (opt-in): it flips
a box's posture to deny-by-default egress with an allowlist. How it works:

1. `develop` starts the box normally; the launcher sees the gate file the
   skill baked into the image and waits at the **launch gate** -- at the
   very top, before first-run hooks -- for a ready signal (ADR 0011).
2. Concurrently, byre (host-side) runs the **netns helper**: a
   run-to-completion container sharing only the box's network namespace,
   root + `NET_ADMIN`, targeted by a per-invocation nonce label. It
   resolves the egress allowlist, installs port-scoped per-IP ACCEPT
   rules plus a default-DROP OUTPUT policy (v4 + v6), and self-verifies
   with a deny probe (ADR 0010).
3. The helper listens once on loopback; the launcher's poll-connect
   succeeds and the agent execs behind the wall. Any failure -- helper
   death, DNS failure, `docker restart` recreating the netns -- means no
   signal, timeout, and the box dies **closed** (ADR 0011).

The allowlist is **derived, and minimal by ruling** (ADR 0020): every
enabled skill declares the `[runtime] egress = ["host[:port]"]` it NEEDS
to function (agents carry their API endpoints -- enabling the agent is
the intent), unioned with the user's `egress` config key (ADR 0012, key
per ADR 0019 -- it cascades like every other list, `!entry` removes).
Nothing else opens: convenience endpoints (git hosting, apt, language
registries) ship as `egress_offered` -- declared-but-closed doors the
config UI opens with one press, writing the entry into the user's own
config. Empty is legal -- a maximally-locked box. `byre status` prints
the posture under honesty rules (skill contributions are trusted and
attributed; project-level raw blocks degrade the claim -- ADR 0010) and
shows the resolved allowlist as an Egress section attributed per source
(each skill, and `config` for the key's entries).

## Image generation

byre's job is to **generate a Dockerfile from config**; Docker's job is
to build and cache it (ADR 0001 -- byre owns no caching layer).

At every launch:

1. Resolve config -> generate Dockerfile *text* + build context.
2. Write it to `~/.byre/projects/<project_id>/Dockerfile.generated`.
3. `docker build --build-arg BYRE_UID=<uid> --build-arg BYRE_GID=<gid> -t byre-<project_id>-u<uid>-g<gid> <context>` (the UID/GID are baked in, so the tag carries them -- ADR 0008).
4. `docker run --rm -it --label byre.project=<project_id> byre-<project_id>-u<uid>-g<gid>` (foreground; see *Box lifecycle*).

On an unchanged config every instruction is a Docker cache hit, so the
build is a near-no-op and launch is effectively instant. A change
rebuilds only the layers from the changed instruction onward. Freshness
is deliberate staleness: `byre rebuild` (`--no-cache`) is the valve for
pulling fresh upstream packages; byre guarantees Dockerfile determinism,
not upstream-artifact reproducibility (ADR 0001).

**Instruction ordering is load-bearing** -- not for byre's own caching
(it has none) but because it determines how well *Docker's* layer cache
shares work across projects. The generator emits blocks in a stable
order, expensive-and-shared first, cheap-and-project-specific last:

```
FROM <base>                 # from template config
<template block>            # shared across all projects on this template ┐ Docker
<core block>                # constant: build-only gosu, baked dev user   │ layer-
<skill blocks>              # each enabled skill, deterministic order     │ cached
<project block>             # this project only                           │ across
USER dev / ENTRYPOINT ...   # constant: drop to the baked user, then exec ┘ projects
```

The **core block precedes skills**: it's constant, so placing it ahead of
the varying skill blocks keeps it cache-shared across all projects on a
base, and it means the `dev` user and `gosu` exist when skills build --
a skill can install as the dev user (e.g. `gosu dev` in a `RUN`) rather
than as root.

Ten projects on one `node` template share the early layers via Docker's
layer store; only the project tail diverges. byre never parses inside a
raw block, so it can't dedupe across them -- keep expensive shared
installs in the *template* block, not per-project blocks. That's the only
caching discipline required.

The generated Dockerfile is printed by `byre dockerfile`. byre shows its
work and you can always read or eject from it.

## Container engine

byre targets **Docker and Podman** through a thin runner abstraction over
the operations it needs -- build, run, volume/image/container ops --
shelling out to the engine CLI, never the SDK (ADR 0002). `engine =
"auto"` (default) picks `docker` if present, else `podman`.

Caveat -- **rootless Podman is not a free win.** The chassis bakes the
host UID/GID assuming a rootful daemon; rootless remaps user namespaces,
so the ownership math differs. The rootless keep-id path is designed but
sequenced later (ADR 0008); until then byre detects rootless Podman and
warns.

## Box lifecycle

The **container is throwaway; the volumes and image are not.** `byre
develop` runs `docker run --rm -it` in the foreground: a fresh container
each session, removed on exit. What persists across sessions is the
project-scoped image and the named volumes (state + cache) -- agent auth,
shell history, and caches survive; no long-lived container accretes
cruft.

A running session is identified by **labels** (`byre.project` +
`byre.workdir`), not an assumed name (ADR 0004); that's how `byre status`
finds "is a session running for this directory?".

**`develop` is single-session per directory** (ADR 0004): if a session is
already running here, it reports that session (and how to stop it, or get
a shell via `byre shell`) rather than spawning a parallel one -- two boxes
on one directory would race the shared state volumes. For two agents on
one codebase, use worktrees: each worktree is its own workdir with its
own session, deliberately sharing the project's config, volumes, and
image (ADR 0009).

## Config

Cascade, config-only (image steps are compiled output, never hand-written
twice):

```
~/.byre/default.config               your personal baseline
~/.byre/templates/<name>/            template.config (+ optional files)
~/.byre/projects/<id>/byre.config    project config (the HOST-SIDE store)
```

Resolution: `default ⊕ template ⊕ project`.

**The project layer lives host-side, NOT in the project tree** (ADR
0003): a config inside the rw-mounted project would let the boxed agent
rewrite its own sandbox. A committed `<project>/byre.config` is a
**proposal** -- shown to the human and **adopted** into the host-side
store only on explicit `[y/N]` (sha256-recorded, re-prompts on change,
non-TTY never adopts). `--self-edit` is the one announced exception: the
session opens with a loud escalation warning and closes by reporting what
changed in the project store -- byre.config as a content diff (it applies
on the next develop), every other file listed as added/changed/deleted.

- **Scalars override** -- last layer wins. Exception: `seed_prefs` is a
  **monotonic opt-in** -- any layer setting it `true` turns it on, and a
  later layer can't set it back to `false` (a plain TOML bool can't
  distinguish unset from false).
- **Lists union** -- `skills`, `mounts`, etc. accumulate across layers.
- **Removal markers** -- a later layer drops something an earlier layer
  added: `!name` where the entry's identity is a string (skills, apt,
  npm_global, volumes, mounts by target), `remove = true` where it's
  structured (ports, keyed by container port alone). ADR 0018. Env has
  no unset (override the value instead); raw blocks are unnamed lines:
  append-only union, no per-line removal.

Vocabulary is deliberately minimal -- the convenient 90%:

```toml
engine      = "auto"                         # auto | docker | podman
template    = "node"                          # which ~/.byre/templates/<name> to layer on (optional)
agent       = "claude"                        # which agent skill launches: claude | codex | gemini | grok
seed_prefs  = true                            # one-time curated prefs seed (ADR 0013); off by default
base        = "node:22"
apt         = ["build-essential"]
npm_global  = ["prettier"]                    # extra global tools (the agent skill installs the agent)
env         = { FOO = "bar" }                 # literals, baked into the image (not a grant)
files       = { "./seed" = "/opt/..." }       # copied into image, read-only
skills      = ["devloop", "firewall"]
mounts      = [ ... ]                         # host-bind mounts (see Mounts & volumes)
ports       = [{ container = 3000 }]          # published ports; binds 127.0.0.1 unless
                                              # interface says otherwise, host defaults to container
volumes     = [ ... ]                         # ad-hoc named volumes; skills usually supply these
dockerfile_pre  = ["RUN ..."]                 # raw BUILD block, before the core block
dockerfile_post = ["RUN ..."]                 # raw BUILD block, project tail
run_args        = ["--cap-add=SYS_PTRACE"]    # raw RUNTIME block: docker-run passthrough
```

Escape hatches are symmetric across both layers byre controls
(PRINCIPLES.md #3):

| layer   | nice primitives                             | raw escape hatch                    |
|---------|---------------------------------------------|-------------------------------------|
| build   | `base`, `apt`, `npm_global`, `files`, `env` | `dockerfile_pre`, `dockerfile_post` |
| runtime | `mounts`, `volumes`, `env`                  | `run_args`                          |

byre never parses inside a raw block -- `byre status` shows raw blocks
verbatim and flags them as not-introspected.

**`run_args` is last-wins** (ADR 0006): byre's own flags first, `run_args`
appended last, so a raw flag can override byre's -- except the identity
labels, re-asserted after it.

There is no full-Dockerfile opt-out: byre either generates the build or
isn't involved (ADR 0014). A whole hand-written Dockerfile means raw
Docker, not byre.

## Skills

A skill is a portable bundle that can contribute to **any layer byre
controls**:

- **build** -- Dockerfile block(s) + files
- **runtime** -- mounts, env, egress declarations, network posture
- **agent context** -- a snippet appended to the agent's instructions
- **state** -- named volume(s) it needs

**Agents are skills** (ADR 0005). An agent skill contributes its CLI
(build), its launch command + autonomy flag, and its auth state volume
(`.claude` / `.codex` / `.gemini` / `.grok`). The chassis ENTRYPOINT is a
constant
launcher that execs the *selected* agent skill's recorded command; the
`agent` scalar picks which, and **implicitly enables** that skill. More
than one agent skill can be enabled; `agent` decides the default command.

A skill declares its contributions in `skill.toml`. Minimal shape (an
agent skill):

```toml
# ~/.byre/skills/claude/skill.toml
[build]
npm_global = ["@anthropic-ai/claude-code"]      # install the CLI

[agent]                                          # marks this as an agent skill
command = "claude --dangerously-skip-permissions"   # what the launcher execs
state   = ".claude"                              # name of its state volume

[runtime]
egress = ["api.anthropic.com"]                   # what the firewall opens for it (ADR 0012)

[[volumes]]
name   = ".claude"
role   = "state"
target = "/home/dev/.claude"
# (no seed: the agent logs in IN THE BOX -- ADR 0007)

[context]
file = "agent-context.md"                        # appended to agent instructions
```

A non-agent skill simply omits `[agent]`. Skills are template-independent:
they drop onto any *supported* base identically ("supported" = the
Debian-derived family of *The chassis*: a skill may assume `apt`, a POSIX
shell, and root at build time, nothing more specific). Each skill gets its
own insertion slot and its own Docker layers. Built-in skills are
**materialized** to `~/.byre/skills/<name>/` as editable copies;
`byre skill update` refreshes stale ones (materialize never silently
overwrites).

Enabling a skill is trusting it (PRINCIPLES.md #2): skill content is
validated for legibility, not as a trust boundary. A skill's grants (a
mounted host socket, a network posture) are named by `byre status`, never
hidden -- and never blocked.

## Mounts & volumes

Two mount species:

1. **host-bind** -- a host path into the box, declared in `mounts`
   (default `ro`). The project itself is the implicit one (`/workspace`,
   read-write by design). `:ro` protects another codebase from
   *modification*, not from being read. Plain Docker binds -- anything
   fancier drops to `run_args`. A mount can be **disabled**
   (`disabled = true`): it stays in the config and in `byre status`
   (marked), but produces no bind -- a switch for long-lived entries,
   distinct from `!target` removal. `mode` survives the off state, and a
   disabled mount's host path may be absent without blocking develop.
2. **named volume** -- Docker-managed, project-scoped
   (`byre-<project_id>-<name>`), survives rebuilds. Usually contributed
   by a skill; a project can declare ad-hoc ones via `volumes`. Carries:
   - **role** -- `cache` (disposable, regenerable) or `state` (precious:
     agent auth + history). Drives lifecycle: `byre reset` warns and
     wipes; rebuild leaves volumes alone.
   - **scope** -- `project` (default) or `machine` (ADR 0017). A
     machine-scoped volume is one per user per machine
     (`byre-machine-u<uid>-<name>`) and mounts identically in every
     project's box -- the shared-auth companion skills' identity
     volumes are the canonical use. `byre status` lists them on their
     own "Shared vols" row; `reset`/`forget` never touch them and say
     so (delete one deliberately via `byre config` -> Volumes ->
     clear, which refuses while ANY byre session runs). `seed` is
     invalid on a machine-scoped volume. (Worktree sharing remains an
     identity question, not a volume one -- ADR 0009.)
   - **seed** (state only, optional) -- initialize a *fresh* volume from
     a host path or config literal (non-secrets only), once. A copy, not
     a shared mount; nothing flows back.

Credentials are **not seeded** (ADR 0007 -- a "not now", not doctrine:
copy-semantics breaks rotating OAuth tokens): agents log in once in the
box and the state volume persists the login per-project. `seed_prefs`
(ADR 0013) is the curated, non-secret exception for agent prefs. The
**shared-auth companion skills** (`claude-shared-auth`,
`codex-shared-auth`, `gemini-shared-auth`, `grok-shared-auth`; ADR 0017)
make one login
serve every project WITHOUT host copying: the credential lives in a
machine-scoped identity volume and byre reads nothing from the host --
Codex, Gemini, and Grok log in once in any box (the credential lands in
the shared volume through symlinks; Gemini's API-key path is verified,
Gemini-OAuth and Grok sharing gate-pending -- see the skills and ADR
0017's verification record); Claude uses a user-minted `claude
setup-token` pasted at a
first-run prompt and exported to the agent process by a **launch env
hook**
(`/etc/byre/env.d/*.sh`, sourced by the launcher after firstrun hooks,
immediately before exec -- the chassis mechanism for skills that must
put env into the agent process).

## The chassis

Core's constant provision to every box. **Supported base (v0):
Debian-derived images** -- the core block assumes `apt`, a POSIX shell,
glibc, and root at build time. Alpine/distroless/non-glibc bases are
unsupported; for those, use Docker directly (ADR 0014).

Build-time (the **core block**, identical everywhere, always cached):

- Bake the host UID/GID into the image (ADR 0008): the `dev` user is
  created at that UID/GID, `/home/dev` + the volume mount points are
  chowned to it, so a fresh volume inherits correct ownership. No runtime
  chown.
- Run unprivileged: `USER dev` after all root build steps; the agent
  never runs as root and there is no runtime `gosu` (it stays installed
  as a *build-only* helper for skill installs).
- Strip inherited `HEALTHCHECK`s (a base's probe could do network I/O
  before a firewall gate lands -- ADR 0011).
- Install the launcher as the constant ENTRYPOINT.

Runtime constants:

- Pass host git identity through -- **narrowly**: only `user.name` /
  `user.email`, injected as `GIT_AUTHOR_*` / `GIT_COMMITTER_*` env. Not
  your `.gitconfig`, not git credentials. The one named exception to
  "host env is isolated", and today's only host-env passthrough grant.
- The launcher: wait at the launch gate if a network-posture skill is
  enabled (ADR 0011), place agent context, run first-run hooks as the
  user (agent login flows live here), then exec the selected agent's
  command in autonomous mode. The box is the safety boundary.

## Project identity

`project_id` is derived from the **canonicalized absolute path** of the
project dir: a readable slug of the last two path components plus a short
hash of the full path, e.g. `/Users/me/dev/byre -> byre-dev-0877d7`. It is
a *naming* device, not a caching one (ADR 0001): it names the image tag
(`byre-<project_id>-u<uid>-g<gid>` -- written `byre-<project_id>`
elsewhere in this doc for brevity), the container, the named volumes
(`byre-<project_id>-<name>`), the labels, and
`~/.byre/projects/<project_id>/`. A collision is not silent: byre records
each id's canonical path and fails loudly if a different path maps to an
existing id.

For a linked git worktree, identity anchors at the **main worktree's**
path (ADR 0009): config, volumes, image, and the setup lock come from the
project; the container name, `byre.workdir` label, and `/workspace` mount
stay per-worktree.

Consequence: moving or renaming the folder yields a new id -> fresh image
and **fresh volumes**; the old volumes persist, orphaned, under the old
id. For `cache` that's a shrug; for state it strands the agent login.
`byre rehome` re-points identity after a move (refusing from a worktree,
which re-resolves automatically once git's own pointers are repaired).

## Commands

Every command is either *lifecycle* (`develop`, `worktree`, `reset`,
`rebuild`, `rehome`, `forget`) or *inspection* (`status`, `dockerfile`,
`shell`, `config`). **No command mutates config behind your back** --
config is edited by you (in files, or explicitly in the `byre config`
editor); `develop` and friends only read it and act.

```
byre develop      Set up if needed (generate, build-on-cache-miss) and run the
                  box in the FOREGROUND. The main entry point.

byre shell        Open a shell (as dev, with the agent's env) in the running
                  session.

byre worktree     Create a git worktree and start a session in it -- a parallel
                  agent inheriting this project's config, volumes, and image.

byre reset        Wipe ALL of this project's named volumes (not the image).
                  Warns and names what dies. Refuses while a session is live.

byre rebuild      Rebuild the image with the cache disabled (--no-cache) -- the
                  deliberate-staleness valve (ADR 0001).

byre status       The legibility surface (PRINCIPLES.md #4): resolved config,
                  every grant and who granted it, network posture + egress,
                  volumes, raw blocks verbatim-but-flagged, session state.

                      Agent:        claude
                      Engine:       docker
                      Project:      /repo -> /workspace  (rw)
                      Network:      open
                      Host mounts:  none
                      Skills:       devloop
                      State vols:   .claude          (per-project)
                      Cache vols:   node_modules     (per-project)
                      Raw run args: --cap-add=SYS_PTRACE   (passed through; not introspected)
                      Dockerfile:   ~/.byre/projects/<project_id>/Dockerfile.generated
                      Container:    not running

byre dockerfile   Print the generated Dockerfile for this directory.

byre config       Interactive editor for this project's host-side config
                  (--global for the baseline).

byre rehome       Re-point a moved directory's identity (migrate volumes) onto
                  its new path-derived id.

byre forget       Remove all of byre's host-side state for this directory --
                  volumes, image, ~/.byre/projects/<id>/. Never touches the
                  project tree.

byre skill update Re-materialize built-in skills into ~/.byre/skills/.
```

## Platform note

Baking the host UID/GID to yield correctly-owned files is a Linux-*host*
concern. On Docker Desktop (macOS/Windows) the file-sharing layer fakes
ownership, so the problem mostly doesn't arise and the baked UID is
harmless. Test the baked-UID ownership on a native Linux host.
