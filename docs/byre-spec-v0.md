# byre — spec (v0.4)

A small Go binary that runs an AI coding agent in a throwaway, project-scoped
container. `cd ~/project && byre develop` drops you into a sandbox that sees
this project and what you explicitly grant it — not your home dir, keys, or
the rest of your machine.

Name: Scots/Northern-English for a cowshed (pronounced like *buyer*) — the
enclosure you keep the thing in so it doesn't wander off.

> **v0.4 changes** (second mechanics review): **single-session per project** —
> `develop` reports an existing session (and how to act on it) rather than
> spawning a parallel container that would race shared volumes (use git worktrees
> for genuine parallelism, or `byre shell` for a second terminal); this corrects
> v0.3's overstated "concurrency-safe". `agent`
> **implicitly enables** its skill (don't list it twice). **`run_args` is
> last-wins** and may override core flags except the identity label — contract
> stated. **Git identity passthrough narrowed** to `user.name`/`user.email`
> only. **Full-Dockerfile opt-out** now has a contract. Added a minimal
> `skill.toml` sketch and volume-naming rule. Fixed the `rebuild` wording that
> contradicted build-at-every-launch.
>
> **v0.3 changes** (after mechanics review): **the agent is a skill** — byre
> ships agent skills for Claude, Codex, and Gemini, and the `agent` scalar
> selects which one the constant entrypoint launches. **Container lifecycle**
> made explicit: ephemeral `--rm` container, persistent project-scoped
> volumes/image, container identified by **label** (see v0.4). **Mounts
> vs volumes** split: `mounts` are plain host-binds, **skills own named
> volumes** (plus an ad-hoc `volumes` config key). `byre mount` is **removed**
> (no command mutates config). Added a **`run_args`** raw runtime escape hatch,
> giving build/runtime symmetry; `byre status` shows raw args verbatim-but-
> flagged. Fixed the stale "content-hash-deterministic" wording.
>
> **v0.2 changes** (after positioning review): added *Positioning* and
> *Non-goals* so byre is pitched as a local-first inspectable harness, not a
> security product. Container engine is pluggable (`engine = auto|docker|podman`)
> behind a thin runner abstraction. One blessed removal mechanism (`!name`);
> dropped `skills_remove`. `byre status` has a concrete output shape. Rootless
> Podman vs the UID/GID plumbing is logged as an open question.
>
> **v0.1 changes** (after design review): byre no longer owns image caching —
> `docker build` already does the "did anything change?" detection, so the
> content-hash/skip-rebuild machinery is gone. `project_id = hash(path)` is now
> a *naming* device only. Core ships empty (no bundled moarcode). The security
> contract is stated explicitly. v0 targets Debian-derived bases.

## Thesis

byre is a **transparent templating layer for running agents in containers**.
It is a convenience for the common case; it never stops you writing Docker.
Writing a raw Dockerfile block is a first-class, expected path, not an escape
hatch.

The split that defines the project:

- **Core** owns the *plumbing* — the agent-runtime scaffolding everyone
  reinvents (UID/GID passthrough, drop to non-root, git identity, credential
  persistence, the autonomous-agent default that's only safe because it's
  boxed). Core ships **no opinions**.
- **Skills** own the *opinions*. moarcode (the diary / milestone / codex-review
  workflow) is just a skill. So is shem (host command execution via a mounted
  socket). **So is the agent itself** — byre ships agent skills for Claude,
  Codex, and Gemini, and `agent` selects which one launches. Core ships empty;
  you compose your baseline from skills.

Image-building is solved — Docker does it well and everyone knows the syntax.
byre owns the *frame* around your Docker, not the Docker.

## Positioning

byre is **the local-first, inspectable, Docker-native project harness for
autonomous coding agents.** It is *not* pitched as a stronger security boundary
than Docker — products like Docker Sandboxes run agents in microVMs and own that
story. byre competes on a different axis:

> Docker Sandboxes *isolates* the agent. byre *explains, builds, and manages*
> the agent's project environment.

The wedge is **no account, no cloud identity, no control plane** — just local
Docker or Podman and files you can read. The concrete pain byre removes ("agent
runtime slop"):

- unclear mounts — *what can this thing actually touch?*
- agent credentials smeared across projects
- one-off, unreadable per-project Dockerfiles
- no clean reset story
- no portable way to package a workflow (moarcode, shem) and carry it between
  projects

Everything byre does should serve one moment — `cd repo && byre develop` — and
make what just happened legible (see `byre status`).

## Non-goals

byre is **not**:

- **an agent** — it runs one; it isn't one.
- **a Docker replacement** — it generates Docker you can read and hands it to
  your engine. Raw Docker stays first-class.
- **a devcontainer implementation** — no editor protocol, no `devcontainer.json`
  compatibility.
- **a policy engine** — it makes grants *legible*, it doesn't gate them.
- **a secret manager** — it can *seed* a credential volume from a source you
  point it at; it doesn't store, rotate, or broker secrets.
- **a cloud sandbox service** — no hosted runtime, no sign-in, no fleet/control
  plane. Local only.

## Security contract

What byre actually guarantees — stated plainly so it isn't mistaken for more:

- **Isolated:** the host filesystem, environment, and credentials. The agent
  sees *only* what you explicitly mount or pass — not your home dir, SSH/cloud
  keys, env, or the rest of the machine. This is byre's core promise. (One
  narrow, named exception: git `user.name`/`user.email` are passed through for
  commit attribution — see *Plumbing*. Nothing else from your env or
  `.gitconfig`.)
- **Not isolated — by design:** the **network** (open to the world by default,
  see below) and the **mounted project itself** (`/workspace` is read-write so
  the agent can edit and commit your code). An agent with both can exfiltrate
  the project it's working on. byre never promised otherwise; the box protects
  *everything else*, not the code you handed it.
- **Opt-in holes:** anything that widens the boundary is a skill you choose, not
  a default — e.g. **shem** (mounts a host socket so the agent runs host
  commands) or a **firewall** skill (network restriction). byre makes such
  grants **legible** via `byre status`; it does not block them. Core bundles
  none of these — core ships empty.

### Network

Open to the world by default. No firewall, no `NET_ADMIN`/`NET_RAW` added by
core. Because byre makes **no** network-containment claim, the container's
default Docker capability set is not a security surface byre reasons about.
Network restriction, if anyone wants it, is a skill — not a core concern.

## Image generation

byre's job is to **generate a Dockerfile from config**; Docker's job is to build
and cache it. byre does not own a caching layer — `docker build` already detects
what changed.

At every launch:

1. Resolve config → generate Dockerfile *text* + build context.
2. Write it to `~/.byre/projects/<project_id>/Dockerfile.generated`.
3. `docker build -t byre-<project_id> <context>`.
4. `docker run --rm -it --label byre.project=<project_id> byre-<project_id>`
   (foreground; see *Container lifecycle*).

On an unchanged config every instruction is a Docker cache hit, so the build is
a near-no-op (no work, no network — a locally-present base isn't re-pulled) and
launch is effectively instant. A change rebuilds only the layers from the
changed instruction onward. This is exactly the "skip if nothing moved, rebuild
only what did" behavior — supplied by Docker, not reimplemented by byre.

*(Optional later micro-optimization, not v0: skip even the `docker build`
invocation when the freshly-generated Dockerfile text is byte-identical to the
last `Dockerfile.generated` — a plain string compare, no hashing — and go
straight to `docker run`.)*

**Instruction ordering is still load-bearing** — not for byre's own caching
(it has none) but because it determines how well *Docker's* layer cache shares
work across projects. The generator emits instructions in a stable order,
expensive-and-shared first, cheap-and-project-specific last:

```
FROM <base>                 # from template config
<template raw block>        # shared across all projects on this template ─┐ Docker
<byre infra layer>          # constant: gosu, non-root dev user, launcher  │ layer-
<skill blocks>              # each enabled skill, deterministic order       │ cached
<project raw block>         # this project only                             │ across
ENTRYPOINT ...              # constant                                      ┘ projects
```

The **infra layer comes before skills**: it's constant, so placing it ahead of
the varying skill layers lets it be cache-shared across *all* projects on a base
(not rebuilt per skill-set); and it means the `dev` user and `gosu` exist when
skills build, so a skill can install **as the dev user** (e.g. the claude skill
drops to `gosu dev` so its CLI lands in `/home/dev/.local/bin`, owned by the
runtime user) rather than as root.

Ten projects on one `node` template share the early layers via Docker's layer
store → node is built once, ever; only the project tail diverges. (Each project
gets its own `byre-<project_id>` *tag*, but identical leading instructions reuse
the same underlying layers regardless of tag.) byre never parses inside a raw
block, so it can't dedupe across them — keep expensive shared installs in the
*template* block, not in per-project blocks. That's the only caching discipline
required.

The generated Dockerfile is printed by `byre dockerfile`. byre shows its work
and you can always read or eject from it.

> **Freshness, honestly:** because byre tags by `project_id` and lets Docker
> own the cache, an unchanged config keeps using the existing cached image even
> if upstream moved (`node:22` republished, apt/npm indexes advanced). That
> stability is a feature, not a bug; `byre rebuild` (`--no-cache`) is the valve
> when you want to pull fresh. byre guarantees **Dockerfile determinism**, not
> upstream-artifact reproducibility. Digest-pinned bases / lockfiles, for anyone
> who wants true reproducibility, are a later optional skill — not a core
> concern.

## Container engine

byre targets **Docker and Podman**. Podman builds Containerfiles and overlaps
the Docker CLI heavily, so supporting both is mostly a matter of not binding
tightly to one. `engine = "auto"` (default) picks `docker` if present, else
`podman`; set it explicitly to force one.

Internally byre talks to the engine through a **thin runner abstraction** over
the operations it actually needs — `build`, `run`, `volume` (create/inspect/rm),
`image` (inspect/rm), `container` (ps/inspect/rm) — rather than depending deeply
on the Docker SDK. byre shells out to the engine CLI behind this interface (it
also answers the "CLI vs SDK" question: **CLI**, for parity and zero SDK
coupling). Docker and Podman are two implementations of the same small runner.

Caveat — **rootless Podman is not a free win.** byre's core plumbing maps the
host UID/GID and drops to a non-root user via `gosu`, which assumes a rootful
daemon on a Linux host. Rootless Podman remaps user namespaces, so "the UID you
see inside" is no longer the host UID and the ownership math differs. v0 should
get the runner abstraction and rootful-Docker/rootful-Podman paths right; the
rootless-Podman ownership model is an open question (below), not a v0 promise.

## Container lifecycle

The **container is throwaway; the volumes and image are not.** `byre develop`
runs `docker run --rm -it` in the foreground: a fresh container each session,
removed on exit. What persists across sessions is the project-scoped image
(`byre-<project_id>`) and the named volumes (state + cache) — so your agent auth,
shell history, and caches survive, but no long-lived container accretes cruft.

The running container is identified by a **label** (`byre.project=<project_id>`),
not a fixed `--name`, and that label is how `byre status` finds "is a container
running for this directory?".

**`develop` is single-session per project.** Before starting, it checks the
label; if a container is already running for this directory it **reports that
session** (and how to stop it, or get a shell via `byre shell`) rather than
spawning a parallel one. This is deliberate: two containers on one project would
share the same per-project state volumes
(`.claude` history, caches) and race or corrupt them — name-safe is not
state-safe. If you genuinely want two agents on one codebase, use two **git
worktrees**: different paths → different `project_id` → isolated image and
volumes. The agent runs autonomously; the box is the boundary.

## Config

Cascade, config-only (image steps are compiled output, never hand-written
twice):

```
~/.byre/default.config              your personal baseline
~/.byre/templates/<name>/            template.config (+ optional files)
~/.byre/projects/<id>/byre.config    project config (the HOST-SIDE store)
```

Resolution: `default ⊕ template ⊕ project`.

**The project layer lives host-side, NOT in the project tree.** The config that
defines the sandbox must live *outside* the sandbox: a `byre.config` inside the
rw-mounted project would let the contained agent rewrite its own sandbox (caps,
mounts, `--privileged`, base) and have it applied on the next host-run `develop`.
So byre reads/writes the project config under `~/.byre/projects/<id>/`, never the
project tree.

A **committed `<project>/byre.config` is a proposal, not live config** (so a repo
*can* ship its dev env, like byre's own repo). On `develop`, byre shows the
human its grants and asks `[y/N]` before copying it into the host-side store
(`direnv allow` / devcontainer-trust style; a sha256 record re-prompts on
change; non-TTY never adopts). The contained agent can edit the proposal, but it
stays inert until a human reviews the diff and adopts it. `--self-edit` (mounting
`~/.byre/projects/<id>/` rw) is the one deliberate, announced exception that lets
an agent edit its own `byre.config`.
- **Scalars override** — last layer wins (`agent = "codex"` in a project beats
  the default).
- **Lists union** — `skills`, `mounts`, etc. accumulate across layers.
- **Removal escape hatch** — a `!name` entry in the same list drops something an
  earlier layer added (e.g. `skills = ["!moarcode"]` in a project removes a
  default-layer skill). One blessed mechanism (no separate `skills_remove`),
  applied to **named** lists — `skills`, `mounts`, `volumes` (keyed by
  name/target). Raw blocks (`dockerfile_pre`, `dockerfile_post`, `run_args`) are
  unnamed lines and are **append-only/union in v0** — no per-line removal; to not
  inherit a raw block, don't add it in the parent layer.

Vocabulary is deliberately minimal — the convenient 90%:

```toml
engine      = "auto"                         # auto | docker | podman
template    = "node"                          # which ~/.byre/templates/<name> to layer on (optional)
agent       = "claude"                        # which agent skill launches: claude | codex | gemini
base        = "node:22"
apt         = ["build-essential"]
npm_global  = ["prettier"]                    # extra global tools (the agent skill installs the agent)
env         = { FOO = "bar" }
files       = { "./seed" = "/opt/..." }      # copied into image, read-only
skills      = ["moarcode", "shem"]
mounts      = [ ... ]                         # host-bind mounts (see Mounts)
volumes     = [ ... ]                         # ad-hoc named volumes (see Mounts); skills usually supply these
dockerfile_pre  = ["RUN ..."]                 # raw BUILD block, before infra layer
dockerfile_post = ["RUN ..."]                 # raw BUILD block, project tail
run_args        = ["--cap-add=SYS_PTRACE"]    # raw RUNTIME block: docker-run passthrough
```

Escape hatches are symmetric across both layers byre controls — nice primitives
for the 90%, raw passthrough for the rest:

| layer   | nice primitives                          | raw escape hatch                 |
|---------|------------------------------------------|----------------------------------|
| build   | `base`, `apt`, `npm_global`, `files`, `env` | `dockerfile_pre`, `dockerfile_post` |
| runtime | `mounts`, `volumes`, `env`               | `run_args`                       |

Anything the primitives can't express goes in the matching raw block, or a
project/template supplies a full hand-written Dockerfile and opts out of
generation entirely. byre stays small *because* raw Docker is first-class.
byre never parses inside a raw block — `byre status` shows raw blocks verbatim
and flags them as not-introspected (see *Commands*).

**`run_args` is last-wins.** byre builds its own `docker run` flags first and
appends `run_args` last, so a raw flag can override byre's (e.g. `--user`,
`--network`, even `--rm`) — that's the point of an escape hatch, and the risk is
yours. The *one* exception: byre re-asserts the `byre.project` identity label
after `run_args`, so lifecycle and `status` can always find the container.

**Full-Dockerfile opt-out contract.** If a project supplies a complete
hand-written Dockerfile, byre stops generating the build entirely — which means
*you* own the infra layer too (host UID/GID mapping, `gosu` drop-to-non-root,
and the launcher ENTRYPOINT that execs the agent). byre still owns *runtime*
(mounts, volumes, the identity label, `docker run`), but it assumes your image
provides the user model and entrypoint. Opt out of generation, opt into owning
what generation gave you.

## Skills

A skill is a portable bundle that can contribute to **any layer byre
controls**:

- **build** — Dockerfile block(s) + files
- **runtime** — mounts, caps, env passed to `docker run`
- **agent context** — a snippet appended to the agent's instructions (this half
  is essentially a SKILL.md)
- **state** — named volume(s) it needs

**Agents are skills.** byre ships agent skills for Claude, Codex, and Gemini. An
agent skill contributes its CLI (build), its launch command + autonomy flag, and
its auth state volume (`.claude` / `.codex` / `.gemini`). The infra-layer
ENTRYPOINT is a constant launcher that execs the *selected* agent skill's
recorded command, and the `agent` config scalar picks which one — so "a constant
entrypoint launches a variable agent" is not a contradiction. Setting
`agent = "claude"` **implicitly enables** the claude agent skill — you do not
also list it in `skills`. You can enable more than one agent skill (e.g. to have
both CLIs installed); `agent` decides which is the default command.

A skill declares its contributions in `skill.toml`. Minimal shape (an agent
skill):

```toml
# ~/.byre/skills/claude/skill.toml
[build]
npm_global = ["@anthropic-ai/claude-code"]      # install the CLI

[agent]                                          # marks this as an agent skill
command = "claude --dangerously-skip-permissions"   # what the launcher execs
state   = ".claude"                              # name of its state volume (see below)

[[volumes]]
name   = ".claude"
role   = "state"
target = "/home/dev/.claude"                     # where it mounts in the container
# (no seed: the agent logs in IN THE BOX; the volume persists it per-project.
#  `seed = { host = "..." }` exists for initializing a fresh volume with
#  NON-credential data — never a rotating login.)

[context]
file = "agent-context.md"                        # appended to agent instructions
```

A non-agent skill simply omits `[agent]` and contributes whatever
build/runtime/volume/context pieces it needs. (Ordering, dependencies, and
conflict rules are pinned in the implementation plan; this is the v0 shape.)

Skills are first-class and **template-independent** — they drop onto any
*supported* base identically, so they can be published and shared. "Supported"
means the Debian-derived family of *Plumbing* (v0): a skill may assume `apt`, a
POSIX shell, and root at build time, and nothing more specific. Each skill gets
its own insertion slot and its own Docker layers.

```
~/.byre/skills/<name>/
  skill.toml          # build/runtime/context/state contributions
  *.sh, prompt.md, agent-context.md, ...
```

Examples:
- **moarcode** — soft skill: a script, a review prompt, a context snippet, a
  diary state file. The moarcode workflow shipped as a skill pack on byre.
- **shem** — hard skill: a separate project; contributes a **runtime mount** of
  a host socket so the agent can run commands on the host. Proves a skill must
  reach `docker run`, not just the Dockerfile. Deliberately punches a hole in
  the sandbox — so byre should make such runtime grants **legible** (`byre
  status` names what a skill mounts/grants), without blocking them.

## Mounts & volumes

Two mount species:

1. **host-bind** — a host path into the container, declared in the `mounts`
   config key (default `ro`). The project itself is the implicit one
   (`/workspace`, read-write by design — the agent can rewrite and commit your
   code); shem's socket is another, contributed by its skill. `:ro` protects
   another codebase from *modification*, not from being read. (Plain Docker
   binds — for anything fancier, drop to `run_args`.)
2. **named volume** — Docker-managed, project-scoped, survives rebuilds.
   Usually contributed by a **skill** (the agent skill's `.claude`, a build
   skill's cache); a project can also declare an ad-hoc one inline via the
   `volumes` config key, using the same schema. Carries:
   - **role** — `cache` (disposable; regenerable; e.g. node_modules, venvs,
     target dirs) or `state` (precious; e.g. `.claude` — agent auth + history).
     Drives lifecycle: `byre reset` warns and wipes, rebuild leaves volumes
     alone.
   - **scope** — volumes are **per-project**: isolated identity, small blast
     radius. There is no scope knob. (Cross-project sharing across *unrelated*
     projects was considered and dropped — no natural boundary. The one case with
     a boundary — a repo's **worktrees** sharing the agent setup — is handled not
     by a volume scope but by resolving the worktree's *identity* from the main
     worktree's path; see `docs/agent-volume-sharing.md`.)
   - **seed** (state only, optional) — initialize a *fresh* volume from a
     source, once, if it doesn't already exist. Source preference: a **host
     path** (secret stays in one place, never in tracked config) > config
     literal (**non-secrets only** — documented loudly) > resolved reference
     (secret manager; later). Seeding is a copy *into* a fresh volume, not a
     shared mount — the volume diverges immediately and nothing flows back.
   - **naming + target** — a volume is the Docker named volume
     `byre-<project_id>-<name>`, mounted at the `target` the declaration
     specifies. `byre reset` filters by the `byre-<project_id>-` prefix.

Credentials are **not seeded**: an agent's login is a rotating OAuth token, so
copying it invites a refresh collision (two holders of one single-use token).
Agents log in once **in the box**; the `state` volume persists that login
per-project. The `seed` above is for initializing a fresh state volume with
NON-credential data from a host path or literal.

Examples: `node_modules` = cache / per-project / no seed. `.claude` = state /
per-project / no seed (log in in the box).

## Plumbing (core, constant infra layer)

**Supported base (v0): Debian-derived images** (`debian`, `ubuntu`,
`node:*-bookworm`, etc.). The infra layer below assumes `apt`, a POSIX shell,
glibc, and root available at build time. Alpine/distroless/scratch/non-glibc
bases are *unsupported by core* in v0 — use them only via a full hand-written
Dockerfile that opts out of generation. The same assumption bounds what a skill
may rely on (see *Skills*).

Appended on top of whatever Debian-derived `base` the config names, identical
everywhere (always cached):

- Map host UID/GID to a non-root user; fix ownership on volumes + home.
- Drop to the unprivileged user via `gosu`; the agent never runs as root.
- Pass host git identity through so commits are attributed to the developer —
  **narrowly**: only `user.name` / `user.email`, injected as `GIT_AUTHOR_*` /
  `GIT_COMMITTER_*` env. Not your `.gitconfig`, not git credentials. This is the
  one named, deliberate exception to "host env is isolated" (see *Security
  contract*).
- First-run credential detection in the entrypoint, where agent skills drop
  login hooks (e.g. codex's device-auth) that run before the agent launches.
- Default command is a constant launcher that execs the *selected* agent skill's
  command in autonomous mode (`agent` picks which); the box is the safety
  boundary.

## Project ↔ container identity

`project_id` is derived from the **canonicalized absolute path** of the project
dir (resolve symlinks, normalize trailing slash): a **readable slug** of the last
two path components plus a short hash of the full path for uniqueness, e.g.
`/Users/me/dev/byre → byre-dev-0877d7`. It is a *naming* device, not a caching
one: it names the image tag (`byre-<project_id>`), the container, the named
volumes (`byre-<project_id>-<name>`), the label (`byre.project=<project_id>`),
and `~/.byre/projects/<project_id>/`. The slug is sanitized to Docker-safe
characters and the `byre-` prefix keeps the final name valid regardless of slug
content; the hash suffix carries uniqueness (so two same-named dirs in different
locations don't collide). Caching/freshness is Docker's concern, not this id's —
see *Image generation*.

The hash suffix is short (readability over headroom); a collision is not silent —
byre records each id's canonical path under `~/.byre/projects/<id>/path` and
fails loudly if a different path maps to an existing id.

Consequence: moving or renaming the folder yields a new id → fresh image and
**fresh volumes**; the old volumes persist, orphaned, under the old id. For
`cache` that's a shrug; for `.claude` it strands the agent identity (re-auth on
next develop). Accepted for v0. `byre rehome` re-points identity after a move.

## Commands

Every command is either *lifecycle* (`develop`, `reset`, `rebuild`, `rehome`) or
*inspection* (`status`, `dockerfile`). **No command mutates config** — config is
edited by you, in files; byre only reads it and acts. (This is why there's no
`byre mount`: a mount is configuration, so you configure it.)

```
byre develop      Set up if needed (generate, build-on-cache-miss) and run the
                  container in the FOREGROUND. The main entry point.

byre reset        Wipe ALL of this project's named volumes (volumes only — not
                  the image). Warns and names what dies before proceeding
                  (e.g. "wipes node_modules and your agent login; you'll log in
                  again on next develop"). --force / -y to skip the prompt.

byre rebuild      Rebuild the image with the cache disabled (--no-cache), to
                  pick up new tool/package versions. The deliberate-staleness
                  valve: develop runs `docker build` every launch, but on an
                  unchanged config that's a cache-hit no-op that yields no new
                  image — so rebuild is the one place you force fresh upstream
                  packages.

byre status       Show the resolved config cascade (default ⊕ template ⊕
                  project), active mounts and what skills grant, and whether a
                  container is currently running for this directory.

                  Legibility is the whole pitch, so the default output is a
                  flat, scannable "what can this thing touch?" block:

                      Agent:        claude
                      Engine:       docker
                      Project:      /repo -> /workspace  (rw)
                      Network:      open
                      Host mounts:  none
                      Skills:       moarcode
                      State vols:   .claude          (per-project)
                      Cache vols:   node_modules     (per-project)
                      Raw run args: --cap-add=SYS_PTRACE   (passed through; not introspected)
                      Dockerfile:   ~/.byre/projects/<project_id>/Dockerfile.generated
                      Container:    not running

                  Skill-granted runtime holes (e.g. shem's host socket) are
                  named under Host mounts / Skills, never hidden. Raw blocks
                  (`run_args`, `dockerfile_*`) are shown verbatim and flagged as
                  not-introspected — byre never parses inside them, but never
                  hides them either.

byre dockerfile   Print the generated Dockerfile for this directory.

byre rehome       Re-point this directory's identity (image/volumes/state) onto
                  the current path after a move or rename.
```

## Platform note

The UID/GID passthrough that yields correctly-owned files is a Linux-*host*
nicety. On Docker Desktop (macOS/Windows) the file-sharing layer handles
ownership differently and the problem mostly doesn't arise. Don't over-engineer
that path for those platforms; test the `id -u` / `gosu` mapping on a native
Linux host.

## Open questions

- **Image distribution** — embed a base Dockerfile and build on first run, or
  ship a prebuilt base byre layers onto?
- **Skill packaging & distribution** — `skill.toml` shape (build/runtime/context/
  state contributions, ordering, dependencies, conflicts) is deferred to the
  skills milestone. v0 likely ships built-in skills (agents, moarcode) and
  hand-dropped `~/.byre/skills/<name>/`; there is no `byre skill add` yet, so the
  "publishable/shareable" pitch is aspirational until a fetch/install path exists.
- **Skill trust surface** — how loudly to surface a skill's runtime grants
  (shem mounting a host socket). v0: legible via `byre status`; no permission
  framework yet.
- **Seed source kinds** — host-path and config-literal in v0; reserve room for
  a resolved-reference (secret manager / `pass`) kind without hardcoding to
  "path".
- **Worktree volume inheritance** — volumes are per-project; machine-wide sharing
  was considered and dropped (no natural boundary across unrelated projects). The
  one case with a boundary — a repo's **worktrees** sharing the agent setup — is
  a future feature handled by resolving a worktree's *identity* (config + volumes
  + image) from the main worktree's path, with the container/workspace staying
  per-worktree. Designed in `docs/agent-volume-sharing.md`; not built.
- **Rootless Podman ownership model** — the UID/GID + `gosu` plumbing assumes a
  rootful daemon. Rootless Podman remaps user namespaces, changing how host file
  ownership lands. v0 supports rootful Docker/Podman; how (and whether) to make
  the ownership math correct under rootless Podman is unresolved. Don't market
  "rootless" until this is designed.
