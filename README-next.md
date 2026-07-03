<!--
  Draft replacement for README.md, per docs/positioning.md (2026-07-03).
  LAUNCH BLOCKERS — this file must not replace README.md before:
  1. the default-deny firewall skill ships (the contract block claims it);
  2. `brew install byre` works (the hero and Install claim it — needs at
     least a tap, e.g. `brew install pjlsergeant/tap/byre`, and the copy
     updated to whichever form ships).
-->

# byre

**`--dangerously-skip-permissions`, minus your machine.**

```text
$ brew install byre
$ cd ~/my-project && byre develop
  No byre.config here — let's set one up (press Enter to accept [default]).
  Template — go node python none [node]:
  Agent — claude codex gemini none [claude]:

  byre: wrote ~/.byre/projects/my-project-pjl-069d95/byre.config (template=node, agent=claude)
  byre: ~/my-project -> /workspace (rw) · host mounts: none · network: open

╭──────────────────────────────────╮
│ ✻ Claude Code                    │
│   /workspace                     │
╰──────────────────────────────────╯
```

One command, any folder or repo. Drops you into Claude Code, Codex, or Gemini — running with full autonomy, but in
a throwaway container that sees this project and what you explicitly grant.
Your home dir, your keys, and the rest of your machine stay outside the box.

No account. No cloud. No control plane. byre is a single MIT-licensed Go
binary that generates a Dockerfile you can read and hands it to your local
Docker or Podman. Free forever — as in beer and as in speech — and
structurally so: there's no account to upsell, no service to meter, no
telemetry to monetize. Leaving is as easy as trying it: `byre dockerfile`
prints a plain Dockerfile you keep, and `byre forget` removes every trace.

> *byre* (rhymes with *buyer*) is Scots/Northern-English for a cowshed — the
> enclosure you keep the thing in so it doesn't wander off.

## Status: early, moving fast

byre is young and I'm building it in the open. I use it for all my daily
development, but interfaces and config **will change without warning**, and
there are sharp edges around isolation and agent auth. The honest contract:

- **Boxed:** your host filesystem, environment, and credentials. The agent
  sees only what you mount or pass.
- **Not boxed, by design:** the network (open by default — enable the
  default-deny firewall skill to close it) and the project itself (mounted
  read-write — it's the agent's job to edit it). An agent with an open
  network can exfiltrate the project it's working on.
- **Not a security product:** a container is not a microVM. If you need the
  strongest isolation story, use one.

For now: don't point byre at anything you can't afford to break. The
[devlog](https://pjlsergeant.github.io/byre/) shows what's being built.

## Why not…?

byre is a thin layer over the Docker or Podman you already run. The
alternatives:

**…raw Docker?** Nothing — and byre never takes it away. You'd just be
hand-rolling what it generates: host-matched file ownership, per-project
agent login that survives rebuilds, templates, a clean reset. If you want to
stop using byre, `byre dockerfile` prints your exit.

**…Docker Sandboxes™?** Commercial product with a hosted control plane (you
sign in) and paid tiers. Not open source. *(But it gives you kernel-level
microVM isolation, and we don't.)*

**…your agent's built-in sandbox?** All-or-nothing file isolation, on your
real machine, wearing your identity — env vars and credentials come along by
default, so a stray `git push` goes out as *you*. byre's box holds nothing
you didn't put in it.

**…devcontainers?** You hand-write the Dockerfile and JSON per project, and
wire up agent credentials yourself. byre generates the Docker from config —
`byre config` adds a package, mounts another repo read-only, or swaps agents
in seconds. *(But it's the mature industry spec, and we're young.)*

**…container-use?** Explicitly experimental, and MCP-shaped: your agent
manages a fleet of environments; you don't sit inside one. byre does
parallel the git way — one boxed session per worktree, sharing the repo's
image, volumes, and agent login.

**…a cloud sandbox (e2b, Daytona, …)?** Account, usage billing, your code in
their cloud. Built for shipping agent products, not for `cd ~/project`.

## Install

```sh
brew install byre
```

Or build from source — byre is a single Go binary (Go 1.22+):

```sh
go build -o ~/bin/byre ./cmd/byre   # ensure ~/bin is on PATH
```

You need Docker (or Podman) running on the host.

## Quickstart

The first `byre develop` in a project asks its two questions (see above) and
remembers your answers — your favourites become the pre-selected defaults.
Log the agent in once; the login persists, per-project, across rebuilds.
Skip the questions entirely:

```sh
byre develop --template go --agent claude
```

Ask the box what it can touch, any time:

```text
$ byre status
Agent:        claude
Engine:       docker
Project:      ~/project -> /workspace  (rw)
Network:      open
Host mounts:  none
State vols:   .claude          (per-project)
Cache vols:   node_modules     (per-project)
Dockerfile:   ~/.byre/projects/<id>/Dockerfile.generated
Container:    running
```

## Commands

| Command | What it does |
|---|---|
| `byre develop` | Generate, build on cache-miss, and run in the foreground. The main entry point. |
| `byre shell` | A second shell in the running session — for logins, tests, poking around. |
| `byre worktree <name>` | New linked worktree on branch `<name>` + a session in it — a parallel agent in one step. |
| `byre status` | What can this thing touch? Resolved config, mounts, skills, volumes, session. |
| `byre config [--global]` | Interactive config editor — packages, mounts, agents, in seconds. |
| `byre dockerfile` | Print the generated Dockerfile. Your exit, whenever you want it. |
| `byre reset [--force]` | Wipe this project's volumes. Names what dies first. |
| `byre forget [--force]` | Remove all of byre's host-side state for this directory. Never touches your project tree. |
| `byre rebuild` | Rebuild with `--no-cache` to pull fresh upstream versions. |
| `byre rehome <old-id>` | Re-point a moved/renamed directory's identity onto its new path. |
| `byre skill update` | Re-materialize built-in skills after upgrading byre. |

## Configuration

**`byre config`** opens an interactive editor in your terminal
(keyboard-driven, works over SSH): adding a package or mounting another repo
read-only takes a couple of seconds. Grants first — mounts, env — then build
choices, in the same vocabulary `byre status` prints. And if you really want
to live dangerously, you can let the box edit its own configuration:
`--self-edit` is a per-session flag, announced at launch, shown as a grant
in `byre status`, and its edits do nothing until you next run `develop`.
There's a screencast in the [devlog](https://pjlsergeant.github.io/byre/).

Underneath, it's a cascade of three TOML files that are always yours to edit
by hand — last layer wins (scalars override, lists union, `!name` removes):

```text
~/.byre/default.config              your personal baseline
~/.byre/templates/<name>/           template config (+ optional files)
~/.byre/projects/<id>/byre.config   this project's overrides (host-side)
```

The vocabulary covers the convenient 90%; raw Dockerfile lines and
`docker run` args cover the rest:

```toml
engine   = "auto"                          # auto | docker | podman
template = "go"                            # ~/.byre/templates/<name>
agent    = "claude"                        # claude | codex | gemini
base     = "debian:bookworm"
apt      = ["build-essential"]
npm_global = ["prettier"]
env      = { FOO = "bar" }
files    = { "./seed" = "/opt/seed" }      # copied into the image
skills   = ["devloop"]
mounts   = [ ... ]                         # host-bind mounts
volumes  = [ ... ]                         # named volumes (state/cache)
dockerfile_pre  = ["RUN ..."]              # raw build block, before infra
dockerfile_post = ["RUN ..."]              # raw build block, project tail
run_args        = ["--cap-add=SYS_PTRACE"] # raw docker-run passthrough
# dockerfile = "Dockerfile"                # opt out: bring your own
```

**byre never reads config out of the project tree** — the box could rewrite
it. A committed `byre.config` in a repo is a *proposal*: byre shows you its
grants and asks before adopting it into the host-side store. Adoption is
always an explicit, human, host-side action.

## Skills

A skill is a portable bundle that can contribute to every layer byre
controls: build steps, runtime mounts, named volumes, and agent context.
**The agent itself is a skill** — byre ships agent skills for Claude, Codex,
and Gemini. Anything that widens the box (say, a host socket) is a skill you
chose, named by `byre status`, never silent.

## Volumes & state

- **cache** volumes (`node_modules`, …) are disposable.
- **state** volumes (`.claude`, …) are precious — the agent's login and
  history, per-project, surviving rebuilds. byre never copies host
  credentials; agents log in once, inside the box.

`byre reset` wipes a project's volumes; `byre rehome` migrates them after a
move.

## Worktrees: parallel agents, the git way

Want a second agent on the same repo?

```sh
byre worktree fix-flaky-tests
```

creates a linked git worktree on branch `fix-flaky-tests` (existing or
new) and drops you straight into a session there. You pick once where
worktrees live — beside the repo, or under a base directory — with
`byre config --global`, and byre won't scatter checkouts you didn't ask
for (no location set, and it refuses rather than guessing). A worktree inherits its repo's config, image, and volumes (the agent
is already logged in) but runs in its own container against its own
checkout, so sessions run side by side. Worktrees you've made yourself with
`git worktree add` are detected and inherit exactly the same way — just
`byre develop` in them.

Git works normally in the box (commits land in the shared object store),
`byre status` shows the whole family, and `reset`/`forget` name their blast
radius before touching anything shared.

## Platform

Linux and macOS, over Docker or Podman (rootful; rootless Podman is a
sequenced follow-up). byre bakes your UID/GID into the image so the agent
runs unprivileged as you and files land correctly owned. Debian-derived base
images; anything else via your own Dockerfile.

Design: [`docs/byre-spec-v0.md`](docs/byre-spec-v0.md). Build notes:
[devlog](https://pjlsergeant.github.io/byre/).
