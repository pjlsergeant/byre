# byre

> # ⚠️ NOT READY FOR PRODUCTION
>
> **byre is early, unfinished, and experimental.** It's vibe-coded Go that the
> author uses daily but is *not yet happy with*. The architecture is deliberate,
> but parts were written by an agent and haven't been fully audited. Interfaces,
> config format, and behaviour **will change without warning**. There are sharp
> edges around isolation, agent auth, and permissions. **Do not rely on it for
> anything you can't afford to have break.** Use it to kick the tyres, not to
> guard anything precious. See the [devlog](site/index.md) for an honest
> rundown of what works and what doesn't.

Run an AI coding agent in a throwaway, project-scoped container.

```sh
cd ~/project
byre develop
```

drops you into a sandbox that sees this project and what you explicitly grant it
— not your home dir, keys, or the rest of your machine.

> *byre* (rhymes with *buyer*) is Scots/Northern-English for a cowshed — the
> enclosure you keep the thing in so it doesn't wander off.

byre is the **local-first, inspectable, Docker-native project harness for
autonomous coding agents.** It generates a Dockerfile you can read, runs it
locally (Docker or Podman — no account, no control plane), scopes state and cache
to the project, and makes every grant legible. Raw Docker stays first-class.

See [`docs/byre-spec-v0.md`](docs/byre-spec-v0.md) for the full design, and the
[devlog](site/index.md) for build notes and current status.

## Install

byre is a single Go binary. Build it (Go 1.22+):

```sh
go build -o ~/bin/byre ./cmd/byre   # ensure ~/bin is on PATH
```

You need Docker (or Podman) running on the host.

## Quickstart

```sh
cd ~/some/project
byre develop
```

On a project with no `byre.config`, byre asks you to pick a **template** (go /
node / python / none) × an **agent** (claude / codex / gemini / none), with your
favourites pre-selected (Enter accepts). It writes a `byre.config`, builds the
image, and launches the agent in the container with your repo mounted at
`/workspace`.

Non-interactively:

```sh
byre develop --template go --agent claude
```

## Commands

| Command | What it does |
|---|---|
| `byre develop` | Generate (if needed), build on cache-miss, and run the container in the foreground. The main entry point. If a session is already running for the dir, it tells you (and how to stop it) rather than starting a second. |
| `byre shell` | Open an interactive shell (as the `dev` user, with the agent's env) in this project's running session — for `codex login`, running tests, poking around. |
| `byre status` | Show the resolved config, mounts, skills + what they grant, volumes, and whether a container is running for this directory. |
| `byre dockerfile` | Print the generated Dockerfile for this directory. |
| `byre reset [--force]` | Wipe this project's named volumes (not the image). Names what dies; refuses while a session is live. |
| `byre rebuild` | Rebuild the image with the cache disabled (`--no-cache`) to pick up new upstream versions. |
| `byre rehome <old-id>` | Re-point a moved/renamed directory's identity (migrate volumes) onto its new path-derived id. |

## Configuration

A cascade of TOML files, last layer wins (scalars override, lists union, `!name`
removes):

```
~/.byre/default.config         your personal baseline (your "favourites")
~/.byre/templates/<name>/       template.config (+ optional files)
<project>/byre.config           project overrides
```

Vocabulary (the convenient 90%; anything else goes in a raw block):

```toml
engine   = "auto"                          # auto | docker | podman
template = "go"                            # ~/.byre/templates/<name>
agent    = "claude"                        # enables the claude/codex/gemini skill
seed_prefs = true                          # one-time copy of the agent's curated,
                                           # non-secret prefs (theme/keybindings)
                                           # into a FRESH state volume; off by default
base     = "debian:bookworm"
apt      = ["build-essential"]
npm_global = ["prettier"]
env      = { FOO = "bar" }                  # baked into the image
files    = { "./seed" = "/opt/seed" }       # copy project files into the image
skills   = ["moarcode", "shem"]
mounts   = [ ... ]                          # host-bind mounts
volumes  = [ ... ]                          # named volumes (role/target/seed)
dockerfile_pre  = ["RUN ..."]               # raw build block, before infra
dockerfile_post = ["RUN ..."]               # raw build block, project tail
run_args        = ["--cap-add=SYS_PTRACE"]  # raw docker-run passthrough
# dockerfile = "Dockerfile"                 # opt out: bring your own Dockerfile
```

## Skills

A skill is a portable bundle that contributes to any layer byre controls —
build (Dockerfile block + files), runtime (mounts/caps/env), state (named
volumes), and agent context. **The agent itself is a skill** — byre ships agent
skills for Claude, Codex, and Gemini. Skills live in `~/.byre/skills/<name>/`
(built-ins are materialized there and are editable).

## Security contract

byre isolates the **host filesystem, environment, and credentials** — the agent
sees only what you explicitly mount or pass. It does **not** restrict the network
(open by default) and the project mount is read-write by design (the agent edits
and commits your code). Skill-granted runtime holes (e.g. a host socket) are
opt-in and named by `byre status`, never silent.

## Volumes & state

- **cache** volumes (e.g. `node_modules`) are disposable.
- **state** volumes (e.g. `.claude`) are precious — per-project agent auth that
  persists across rebuilds. byre never copies your host credentials — agents log
  in inside the box (the volume persists the login). A `seed` can initialize a
  fresh volume with non-credential data from a host path.
- **prefs seeding** (`seed_prefs = true`) opts into a one-time copy of the
  selected agent's curated, non-secret prefs (theme, keybindings — the skill's
  `[agent.prefs]`) into a fresh state volume. Only files the skill vouches are
  secret-free are copied (e.g. for Claude, `keybindings.json` + `themes/`, never
  `settings.json` or `~/.claude.json`). Acts only when the volume is fresh.

`byre reset` wipes a project's volumes; `byre rehome` migrates them after a move.

## Platform

The host UID/GID passthrough that yields correctly-owned files is a Linux-host
nicety; on Docker Desktop (macOS/Windows) the file-sharing layer handles
ownership differently. byre targets Debian-derived base images (the core infra
layer assumes apt/glibc); use other bases via a full hand-written Dockerfile.
