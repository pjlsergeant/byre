# byre

**`--dangerously-skip-permissions`, without risking the farm.**

For when you run Claude Code, Codex, or Gemini at full autonomy, in lots of
different folders, and don't entirely trust the agent not to `git push` as
you or go digging around your other repos. byre puts the agent in a
throwaway container that sees the folder you launch it from, plus whatever
you explicitly grant, and nothing else.

```text
$ go install github.com/pjlsergeant/byre/cmd/byre@latest
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

**THIS IS A VERY YOUNG AND FAST-CHANGING PROJECT.** Interfaces and config
will change without warning, and there are sharp edges. Don't point it at
anything you can't afford to break; [the contract](#the-honest-contract)
below says exactly what is and isn't boxed.

No account. No cloud. No control plane. byre is a single MIT-licensed Go
binary that generates a Dockerfile you can read and hands it to your local
Docker or Podman. Free forever -- as in beer and as in speech -- and
structurally so: there's no account to upsell, no service to meter, no
telemetry to monetize. Leaving is as easy as trying it: `byre dockerfile`
prints a plain Dockerfile you keep, and `byre forget` removes every trace.

> *byre* (rhymes with *buyer*) is Scots/Northern-English for a cowshed -- the
> enclosure you keep the thing in so it doesn't wander off.

## Install

byre is a single Go binary. With Go 1.22+ on your machine:

```sh
go install github.com/pjlsergeant/byre/cmd/byre@latest
```

(that puts `byre` in `$(go env GOPATH)/bin` -- make sure it's on your PATH).
Or, no Go toolchain needed, a checksum-verified download of the latest
release binary:

```sh
curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | sh
```

Or build from a checkout:

```sh
go build -o ~/bin/byre ./cmd/byre
```

You need Docker (or Podman) running on the host.

## Quickstart

The first `byre develop` in a project asks its two questions (see above)
and remembers your answers -- your favourites become the pre-selected
defaults. Log the agent in once; the login persists, per project, across
rebuilds. To skip the questions:

```sh
byre develop --template go --agent claude
```

Ask the box what it can touch, any time:

```text
$ byre status
Project id:   my-project-pjl-069d95
Agent:        claude
Engine:       docker
Project:      ~/my-project -> /workspace  (rw)
Network:      open
Ports:        none
Host mounts:  none
Skills:       claude
State vols:   .claude
Cache vols:   node_modules
Container:    running (0d95f3a2c1b4)
```

## Your toolkit, every folder

`~/.byre` fills up with your building blocks: a baseline config, templates
per stack, and skills -- portable bundles that can add packages, mounts,
volumes, and agent context to any box. The result is that `byre develop`
in a brand-new directory lands you somewhere familiar: curl is there, your
Claude skills are there, your usual tools are there, and none of it needed
per-project setup. Want a postgres client next to node for one folder?
That's a one-line config edit or a template, and it's yours everywhere
from then on.

Skills are also how anything *widens* the box: a host socket, say, is a
skill you chose, named by `byre status`, never silent. The agent itself is
a skill -- byre ships agent skills for Claude, Codex, and Gemini.

## The honest contract

byre is young and built in the open. I use it for all my daily
development, but treat it as early software. What it does and doesn't box:

- **Boxed:** your host filesystem, environment, and credentials. The agent
  sees only what you mount or pass.
- **Not boxed, by design:** the network (open by default -- enable the
  default-deny firewall skill to close it) and the project itself (mounted
  read-write -- it's the agent's job to edit it). An agent with an open
  network can exfiltrate the project it's working on.
- **Not a security product:** a container is not a microVM. If you need
  the strongest isolation story, use one.
- **Not your nanny:** the box is locked against the *agent*, not against
  you. Every protection is one config edit away from off, and skills can
  widen the box as far as you like -- you can hang yourself with skills,
  and that's intentional. byre's promise is that `byre status` always
  tells you where the rope is.

The [devlog](https://pjlsergeant.github.io/byre/) shows what's being built.

## Why not…?

byre is a thin layer over the Docker or Podman you already run. The
alternatives:

**…raw Docker?** Nothing -- and byre never takes it away. You'd just be
hand-rolling what it generates: host-matched file ownership, per-project
agent login that survives rebuilds, templates, a clean reset. If you want to
stop using byre, `byre dockerfile` prints your exit.

**…Docker Sandboxes™?** Commercial product with a hosted control plane (you
sign in) and paid tiers. Not open source. *(But it gives you kernel-level
microVM isolation, and we don't.)*

**…your agent's built-in sandbox?** All-or-nothing file isolation, on your
real machine, wearing your identity -- env vars and credentials come along by
default, so a stray `git push` goes out as *you*. byre's box holds nothing
you didn't put in it.

**…nothing -- just keep YOLOing on the host?** The host is the incumbent:
zero setup, and nothing bad has happened yet. But the agent works as you,
in your real home dir -- byre exists because Claude went editing a sibling
repository and did things with an ssh key it shouldn't have. The box costs
one command, so the host's convenience argument is gone. *(If you've never
had the scare, you may not feel the need -- byre is for after your first
one.)*

**…devcontainers?** You hand-write the Dockerfile and JSON per project, and
wire up agent credentials yourself. byre generates the Docker from config --
`byre config` adds a package, mounts another repo read-only, or swaps agents
in seconds. *(But it's the mature industry spec, and we're young.)*

**…container-use?** Explicitly experimental, and MCP-shaped: your agent
manages a fleet of environments; you don't sit inside one. byre does
parallel the git way -- one boxed session per worktree, sharing the repo's
image, volumes, and agent login.

**…a cloud sandbox (e2b, Daytona, your agent's web offering)?** Account,
usage billing, your code in their cloud -- and they're repo-shaped, built
for shipping agent products or driving a GitHub repo. byre is for dropping
into whatever folder you're standing in.

**…a cheap VPS (a Hetzner box)?** A box per project doesn't scale across
many repos -- and half of what you'd point an agent at isn't a repo, just a
folder. byre is a throwaway box per folder, on the machine you're already
sitting at, with your toolkit already inside. *(But a remote box is real
hardware isolation -- if the agent must never share a kernel with your
machine, rent one.)*

## Configuration

**`byre config`** opens an interactive editor in your terminal
(keyboard-driven, works over SSH): grants first -- mounts, env -- then build
choices, in the same vocabulary `byre status` prints. Adding a package or
mounting another repo read-only takes a couple of seconds. `--self-edit`
(a per-session `develop` flag, announced at launch) lets the agent edit
its own box config; edits apply on the next develop.

Underneath, it's a cascade of three TOML files that are always yours to
edit by hand -- last layer wins (scalars override, lists union, `!name`
removes):

```text
~/.byre/default.config              your personal baseline
~/.byre/templates/<name>/           template config (+ optional files)
~/.byre/projects/<id>/byre.config   this project's overrides (host-side)
```

The vocabulary covers packages, env, mounts, volumes, and skills; raw
Dockerfile lines and `docker run` args cover the rest. Full reference:
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

byre reads config only from its host-side store, never from inside the
project -- the project mount is read-write, so the agent could edit a
config that lived there. A `byre.config` committed in a repo is a
proposal: byre shows you its grants and asks before adopting it.

## Commands

| Command | What it does |
|---|---|
| `byre develop` | Generate, build on cache-miss, and run in the foreground. The main entry point. |
| `byre shell` | A second shell in the running session -- for logins, tests, poking around. |
| `byre worktree <name>` | New linked worktree on branch `<name>` + a session in it -- a parallel agent in one step. |
| `byre status` | What can this thing touch? Resolved config, mounts, skills, volumes, session. |
| `byre config [--global]` | Interactive config editor -- packages, mounts, agents, in seconds. |
| `byre dockerfile` | Print the generated Dockerfile. Your exit, whenever you want it. |
| `byre reset [--force]` | Wipe this project's volumes. Names what dies first. |
| `byre forget [--force]` | Remove all of byre's host-side state for this directory. Never touches your project tree. |
| `byre rebuild` | Rebuild with `--no-cache` to pull fresh upstream versions. |
| `byre rehome <old-id>` | Re-point a moved/renamed directory's identity onto its new path. |
| `byre skill update` | Re-materialize built-in skills after upgrading byre. |
| `byre version` | Which byre is this? Release tag, module version, or build info. |

## Worktrees: parallel agents, the git way

```sh
byre worktree fix-flaky-tests
```

creates a linked git worktree on branch `fix-flaky-tests` (existing or
new) and starts a session in it. The worktree inherits the repo's config,
image, and volumes -- the agent is already logged in -- but runs in its own
container against its own checkout, so sessions run side by side.
Worktrees you made yourself with `git worktree add` inherit the same way:
just `byre develop` in them. You pick once where new worktrees live
(`byre config --global`). Commits land in the shared object store,
`byre status` shows every worktree session in the project, and `reset`/`forget` name their
blast radius before touching anything shared.

## Volumes & state

**Cache** volumes (`node_modules`, …) are disposable. **State** volumes
(`.claude`, …) hold the agent's login and history, per project, and
survive rebuilds -- byre never copies host credentials; agents log in once,
inside the box. `byre reset` wipes a project's volumes; `byre rehome`
migrates them after a move.

## Platform

Linux and macOS, over Docker or Podman (rootful; rootless Podman is a
sequenced follow-up). byre bakes your UID/GID into the image so the agent
runs unprivileged as you and files land correctly owned. Debian-derived base
images only.

Design: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md). Build notes:
[devlog](https://pjlsergeant.github.io/byre/).
