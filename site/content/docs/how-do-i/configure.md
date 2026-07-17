---
title: Configuring the box
weight: 91
description: shared logins, MCP, ports, env, dotfiles, the firewall, resource caps
---

## Save my LLM credentials so I don't need to re-auth for each box?

tldr: say **y** when the first-run picker offers shared auth for your
agent -- or enable the relevant _x-shared-auth_ skill(s) in
`byre config`.

By default agents log in once per project, inside the box. The shared-auth
skills (claude-shared-auth, codex-shared-auth, gemini-shared-auth,
opencode-shared-auth) move that to once per machine. For claude, codex,
and opencode every project's first run asks: "Use machine-wide credentials
to log in to &lt;agent&gt;?" -- yes enables the skill for that project
(its `byre.config`), and only for it. Saying yes to "Save these as your
default?" remembers your answer like the template/agent favourites: the
next box's question just defaults to it, one Enter to accept. (Enabling the
skill in `byre config --global` is the machine-wide route -- then the
question stops.) The login lives in a shared volume that
reset/forget deliberately never touch. See the
[security model](/docs/security-model/) for the implications of this.
(Grok's shared auth works differently -- its token rotation can't be
file-shared, so a broker mediates instead, and until its field gate passes
the skill is hand-enabled rather than offered --
[ADR 0036](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0036-grok-shared-auth-v2-broker.md).)

## Enable a skill in this box?

tldr: the **Skills** section of `byre config`.

Bundled skills toggle on the spot; installed and local ones appear once
they exist on the machine. Everything a skill grants shows up in
`byre status` the moment it's enabled --
[what a skill is](/docs/skills-and-templates/).

## Add an MCP server to my agent's session?

tldr: `byre mcp add <name> <url>` -- or `byre mcp add <name> --
<command...>` for a local server; `--global` for every project.

`--bearer TOKEN_NAME` wires static-token auth (the header carries
`${TOKEN_NAME}`, expanded from the box env at launch -- names travel,
values don't). `byre mcp list` shows the effective set with every entry
attributed to the layer or skill that declared it; `byre mcp remove` is
closure-smart, so removing a skill-declared server writes a `!name`
entry instead of failing. Declarations bake into the image and inject
into the agent session; a remote server's host is attributed egress in
`byre status`. The full story:
[MCP & Claude Skills](/docs/mcp-and-claude-skills/).

## Add a package to this box -- and promote it to my template?

tldr: add it under **Packages** in `byre config`; when it belongs
everywhere, move it into your template (fork the bundled one first:
`byre template fork byre/node my-node`).

The first time you want a postgres client, it's one entry under
**Packages** in that project's `byre config`. When it belongs everywhere you write node, fork the
bundled template into an editable local one, add the package there, and
set `template = "my-node"` -- every project on that template gets it on
its next develop. For literally-everywhere, `byre config --global` puts
it in your personal baseline.

## Set my defaults so every new box starts right?

tldr: `byre config --global` -- your personal baseline, the bottom
layer of every project's cascade.

Anything a config can carry can live there: packages, mounts, skills,
env, egress. The first-run picker's favourites (template, agent, shared
auth) are remembered separately, as preferences -- your most-used
answers become the pre-selected defaults.

## Switch agents for one project?

tldr: the **Agent** section of `byre config`, then relaunch.

The `agent` key decides which enabled agent skill's command launches in
the foreground. Each agent keeps its login in its own state volume, so
switching to codex for a week and back to claude costs no re-auth.

## Expose a port to see the box's dev server?

tldr: the **Ports** section of `byre config`.

Published ports bind `127.0.0.1` by default -- your browser reaches the
box, your LAN doesn't; opening a wider interface is an explicit,
louder choice. Every published port shows in `byre status`.

## Pass env vars into the box?

tldr: `[env]` for plain config values -- never secrets -- and
`[env_from_host]` to pass host values at runtime.

`[env]` literals are **baked into the image**: `docker history` shows
them and they outlive `byre reset`, so they're for configuration, not
credentials. `[env_from_host]` is the runtime channel and a legible
grant: `KEY = "env:HOST_VAR"` passes a host env var at launch,
`"git:user.email"` reads git config, `"tz:"` passes your timezone --
values resolve at launch and never land in a layer. Git identity,
`TERM`, and `TZ` already pass through by default.

## Use my API key instead of an agent login?

tldr: pass it at runtime -- `[env_from_host]` with
`OPENAI_API_KEY = "env:OPENAI_API_KEY"` -- never `[env]`, which bakes
it into the image.

The agents' own login flows are still the better default: the
credential lands in the agent's state volume, per project, and the
shared-auth skills make one login serve every box. But where an API key
is the workflow, `env_from_host` keeps it out of the image, resolves it
fresh at every launch, and shows as a named grant in `byre status`.

## Use Podman instead of Docker?

tldr: nothing -- `engine = "auto"` (the default) picks docker if
present, else podman.

Force it with `engine = "podman"` per project or globally. Rootful and
rootless both work; rootless Podman 4.3+ runs under `--userns=keep-id`
so files still land correctly owned. Everything else -- volumes,
images, status -- reads identically across engines.

## Mount other folders from the host?

tldr: the **Mounts** section of `byre config`.

Each mount is a host path, an in-box path, and a read-only/read-write
choice (read-only is the default); every mount shows up in `byre
status` under "Host mounts".

## Bring my dotfiles and shell setup into every box?

tldr: mount them read-only under **Mounts** in `byre config --global`
-- the box's target mirrors your home path, so they land where the
agent looks.

A home-relative host path (`~/.config/starship.toml`) suggests the
matching `/home/dev/...` target automatically. Symlinks into a dotfiles
repo work -- byre reads through them. For dotfiles that should be baked
in rather than live-mounted, a template's `[files]` copies them into
the image (one caveat: files baked under a state volume's mountpoint,
like `~/.claude`, are masked by the volume at runtime).

## Give my agent standing instructions in every box?

tldr: a tiny local skill with a `[context]` block -- every box that
enables it injects the text into the agent's memory file.

`byre skill init my-conventions`, point its `skill.toml` at a
`context.md` (`[context] file = "context.md"`), and enable it in
`byre config --global` so every box gets it. byre concatenates enabled
skills' contexts into the agent's own memory path (Claude's
`CLAUDE.md`, Codex's `AGENTS.md`), additive with whatever the project
carries. It's how byre's own dev box enforces its diary and review
habits.

## Stop re-downloading dependencies on every rebuild?

tldr: a `[[volumes]]` entry with `role = "cache"` on the dependency
directory.

Templates ship the obvious ones (`node_modules` for node); add your own
for anything regenerable that's slow to fetch. Cache volumes survive
rebuilds and relaunches, and losing one only costs a re-download.
[Volumes & state](/docs/volumes-and-state/) has the model.

## Run project setup automatically?

tldr: build-time setup goes in `dockerfile_post`; per-launch setup is a
small skill's hook script.

`dockerfile_post` lines run at image build and cache like any Dockerfile
step -- the right home for installs and fetches that shouldn't repeat
every session. For something that must run at every launch (or once per
fresh box), a skill's `[build] files` can place a script into
`/etc/byre/firstrun.d/` (first run) or `/etc/byre/env.d/` (sourced at
every launch, just before the agent starts). There's deliberately no
config-level launch hook -- launch behavior is a skill's job, so it's
attributable.

## Cap the box's CPU or RAM?

tldr: `run_args = ["--cpus=2", "--memory=4g"]`.

`run_args` is raw `docker run` passthrough, appended after byre's own
flags so yours win. byre never parses inside it; `byre status` degrades
its posture claims honestly when it's present. (Identity-changing flags
-- `--user`, `--userns` -- are the one documented footgun: they break
the baked-UID ownership model.)

## Restrict network access?

tldr: enable the _firewall_ skill in `byre config`, then pick what to
open under **Egress**.

<!-- demo-placeholder: firewall-enable -->
> 🎬 *[demo slot: enabling the firewall, opening a door under Egress -- VM-recorded cast]*

By default, we don't restrict network access. The _firewall_ skill flips
that to deny-by-default: your container starts but runs nothing while a
privileged one-shot helper joins its network namespace, installs the
allowlist rules, and verifies them. Only then does the agent launch behind
the wall -- and if any of that fails, the box dies closed rather than
running open.

Under "Egress" you choose what to open. The ports your selected agent
needs open automatically, and your other skills may suggest more (eg
GitHub) -- those you open by hand, then relaunch.

One honest limit worth knowing: hostname grants are pinned to the IPs they
resolved to at launch, so on DNS that rotates (CDNs, some cloud resolvers)
a granted host can start failing -- closed, never open -- until a relaunch
re-resolves it. Details in the [security model](/docs/security-model/).

Just want to block telemetry, not the internet? The _firewall-open_ skill
keeps the network open and drops only the hosts you block:
`egress = ["!statsig.anthropic.com"]`. The same `!host` entries subtract
from the full firewall's allowlist too, skill-declared endpoints included.

## Run other Docker containers from inside the byre environment?

tldr: enable the _docker-host_ skill in `byre config`.

The skill installs the Docker CLI (plus compose and buildx) in the box and
mounts the host daemon's socket. It's worth being clear-eyed about what
you're granting: anything that can run Docker on the host also has
effective root on the host, and `byre status` disclaims that hole for as
long as the skill is enabled.
[docs/DOCKER-HOST.md](https://github.com/pjlsergeant/byre/blob/main/docs/DOCKER-HOST.md)
covers what the grant really means and when to prefer something narrower.
Nested Podman (a daemon inside the box, granting nothing on the host) is
possible future work; there's no support for it today.

## Get the coding agent to edit its own byre config?

tldr: `byre develop --self-edit` -- the box gets its own config mounted,
and changes are shown on exit.

`byre develop --self-edit` will mount the box's configuration directory on
`/home/dev/.byre-self` and will also ship contextual documentation to your
box telling your agent how to make edits. There are (of course!) some
security implications to this, so it's probably best not to always run in
this mode. Changes to the configuration will be shown on exit.
