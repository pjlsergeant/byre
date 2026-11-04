---
title: Configuring the box
weight: 91
description: shared logins, MCP, ports, env, dotfiles, the firewall, resource caps
---

## Save my LLM credentials so I don't need to re-auth for each box?

tldr: say **y** when the first-run picker offers shared auth for your
agent -- or enable the relevant _x-shared-auth_ skill(s) in
`byre config`.

The picker asks "Use machine-wide credentials to log in to
&lt;agent&gt;?" for Claude, Codex and OpenCode. Yes enables the
companion skill for that project only; saying yes to "Save these as
your default?" pre-selects the answer for the next box. Enabling the
skill in `byre config --global` makes it machine-wide, and the question
stops. The Gemini and Grok companions are enabled by hand, not offered
(Grok's works through a broker:
[ADR 0036](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0036-grok-shared-auth-v2-broker.md)).
The login lives in a machine-scoped volume that `reset` and `forget`
never delete; what that shares across projects is in the
[security model](/docs/security-model/#shared-state).

## Enable a skill in this box?

tldr: the **Skills** section of `byre config`.

Bundled skills toggle on the spot; installed and local ones appear once
they exist on the machine. Everything a skill grants shows up in
`byre status` the moment it's enabled --
[what a skill is](/docs/skills-and-templates/).

## Add an MCP server to my agent's session?

tldr: `byre mcp add <name> <url>` -- or `byre mcp add <name> --
<command...>` for a local server; `--global` for every project.

```sh
byre mcp add tracker https://mcp.example.com/ --bearer TRACKER_TOKEN
```

The config carries the token's name, never its value. Listing,
removing, and getting the token into the box:
[MCP & Claude Skills](/docs/mcp-and-claude-skills/).

## Add a package to this box -- and promote it to my template?

tldr: add it under **Packages** in `byre config`; when it belongs
everywhere, move it into your template (fork the bundled one first:
`byre template fork byre/node my-node`).

After the fork, add the package to `my-node` and set
`template = "my-node"` in the project: every project on that template
gets it at its next develop. A package every project needs, whatever
the stack, goes in `byre config --global`.

## Set my defaults so every new box starts right?

tldr: `byre config --global` -- your personal baseline, the bottom
layer of every project's cascade.

Anything a config can carry can live there: packages, mounts, skills,
env, egress. The first-run picker's favourites (template, agent, shared
auth) are remembered separately, as preferences -- your most-used
answers become the pre-selected defaults.

## Switch agents for one project?

tldr: the **Agent** section of `byre config`, then relaunch.

Each agent keeps its login in its own state volume, so switching to
codex for a week and back to claude costs no re-auth.

## Run a different agent just this once?

tldr: `byre develop -a codex` -- one run, nothing written.

The next plain `byre develop` is back on your config, and `byre status`
names the override while the box runs. A new worktree takes the flag
too -- `byre worktree fix-thing -a codex` -- on a project that is
already configured; a repo that has never been developed refuses it.
What the override enables, and what it leaves out:
[`agent`](/docs/configuration-reference/#key-reference).

## Expose a port to see the box's dev server?

tldr: the **Ports** section of the TUI (`byre config`).

Published ports bind `127.0.0.1` by default -- your browser reaches the
box, your LAN doesn't; opening a wider interface is an explicit,
louder choice. Every published port shows in `byre status`.

## Pass env vars into the box?

tldr: the **Env vars** section of `byre config` -- `[env]` for plain
config values, never secrets, and `[env_from_host]` to pass host values
at runtime.

Each row's **Source** picker says where the value comes from: a literal
(baked into the image, so `docker history` shows it), a host source
read at launch (`ANTHROPIC_API_KEY = "env:ANTHROPIC_API_KEY"`), or an
encrypted **project credential** that lives in the config and unlocks
with a passphrase at launch (`byre credentials set KEY` writes one from
the command line). Switching the source moves the entry rather than
leaving a twin behind. Every source, the default passthroughs and the
reserved `BYRE_` names:
[`[env_from_host]`](/docs/configuration-reference/#key-reference).

## Use my API key instead of an agent login?

tldr: pass it at runtime -- `[env_from_host]` with
`ANTHROPIC_API_KEY = "env:ANTHROPIC_API_KEY"` for claude -- don't use
`[env]`, which bakes it into the image.

The variable is the agent's own:
`ANTHROPIC_API_KEY` for claude, `XAI_API_KEY` for grok,
`ANTHROPIC_API_KEY` or `OPENCODE_API_KEY` for opencode. codex has no
env-key route: its login check ignores a key in the environment, so log
in once with `codex login --device-auth` from `byre shell` (or use
codex-shared-auth to share that login).

The agents' own login flows are still the better default: the
credential lands in the agent's state volume, per project, and the
shared-auth skills make one login serve every box. Where an API key is
the workflow, the passthrough keeps it out of the image, resolves it at
every launch, and shows as a named grant in `byre status`. For a key
that belongs to the project rather than your host environment, use a
project credential instead (`byre credentials set ANTHROPIC_API_KEY`; see
[env vars](#pass-env-vars-into-the-box)).

## Use Podman instead of Docker?

tldr: nothing -- `engine = "auto"` (the default) picks docker if
present, else podman.

To force it, set **Engine** to podman in `byre config` (or
`byre config --global` for every project). Rootful and rootless Podman
both work -- the [platform notes](/docs/install/#platform) have the
details -- and volumes, images and status read identically across
engines.

## Mount other folders from the host?

tldr: the **Mounts** section of the TUI (`byre config`).

Read-only is the default; every mount shows up in `byre status` under
"Host mounts". Fields:
[`[[mounts]]`](/docs/configuration-reference/#key-reference).

## Bring my dotfiles and shell setup into every box?

tldr: don't do this. But if you have to, you can mount them in the TUI as read-only.

A home-relative host path (`~/.config/starship.toml`) suggests the
matching `/home/dev/...` target automatically. Symlinks into a dotfiles
repo work -- byre reads through them.

Mounting is the whole answer here: `[files]` sources are
project-relative, so a config can never name a file in your home
directory. To bake a dotfile in rather than mount it, the file has to
live in the project.

## Give my agent standing instructions in every box?

tldr: in the TUI see the **Instructions** section:

Write the prose, save, quit -- the git-commit shape. The scope is the
layer you write: `--global` reaches every box on the machine, a named
layer (`byre config --layer <name>`, **Instructions**) reaches its
stack, the project config just that project. A later layer's same-name
entry replaces an inherited one; "Remove in this project" (or
`byre context remove <name>`) switches one off, and `byre context list`
shows what's in effect and whether it's delivered.

**How it reaches the agent.** byre bakes your instructions, after its
own box facts and each enabled skill's guidance, into the image at
`/etc/byre/agent-context.md`, and the agent's launch command appends
that file to the session through the agent's own channel; byre never
writes into the agent's own files. The per-agent channels, and what
happens for an agent without one:
[docs/ARCHITECTURE.md](https://github.com/pjlsergeant/byre/blob/main/docs/ARCHITECTURE.md)
(Standing instructions).

Long documents can stay your own file: `byre context add conventions
--file ~/notes/agent-conventions.md` reads it at each rebuild
(machine-local -- inline text is what travels in a preset). Because the
config lives host-side, the boxed agent can't rewrite its own standing
orders, unlike a repo's in-tree `CLAUDE.md`; the deliberate exception
is a `--self-edit` session, which hands the box its own project config.
Prose that should travel with a tool belongs in that skill's own
`[context]` table. The stored form (`[[context]]` blocks) is in the
[configuration reference](/docs/configuration-reference/).

## Stop re-downloading dependencies on every rebuild?

tldr: a `[[volumes]]` entry with `role = "cache"` on the dependency
directory.

Templates ship the obvious ones (`node_modules` for node); add your own
for anything regenerable that's slow to fetch.
[Volumes & state](/docs/volumes-and-state/) has the model.

## Run project setup automatically?

tldr: build-time setup goes in `dockerfile_post`; per-launch setup is a
small skill's hook script.

`dockerfile_post` lines run at image build and cache like any Dockerfile
step -- the home for installs and fetches that shouldn't repeat every
session. For a command that runs at launch, a skill's `[build] files`
places a script in `/etc/byre/firstrun.d/`: those hooks run at every
launch, so a hook guards its own once-only work. `/etc/byre/env.d/` is
for environment only -- its hooks are sourced, and may set variables
but do nothing else
([ADR 0028](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0028-env-d-hooks-are-pure.md)).
There's deliberately no config-level launch hook -- launch behavior is
a skill's job, so it's attributable.

## Cap the box's CPU or RAM?

tldr: `run_args = ["--cpus=2", "--memory=4g"]`.

`run_args` is raw `docker run` passthrough; what that does to
`byre status`'s claims, and the flags it must not carry:
[`run_args`](/docs/configuration-reference/#key-reference).

## Restrict network access?

tldr: enable the _firewall_ skill in `byre config`, then pick what to
open under **Egress**.

<!-- demo-placeholder: firewall-enable -->

By default byre doesn't restrict network access. The _firewall_ skill
makes it deny-by-default, and if the walls can't be set up the box dies
closed rather than running open.

Under "Egress" you choose what to open. The ports your selected agent
needs open automatically, and your other skills may suggest more (eg
GitHub) -- those you open by hand, then relaunch. A granted host that
starts failing mid-session is usually DNS rotation:
[the allowlist is an IP snapshot](/docs/security-model/#the-network).

Just want to block telemetry, not the internet? The _firewall-open_ skill
keeps the network open and drops only the hosts you block:
`egress = ["!statsig.anthropic.com"]`. The same `!host` entries subtract
from the full firewall's allowlist too, skill-declared endpoints included.

## Run other Docker containers from inside the byre environment?

tldr: enable the _docker-host_ skill in `byre config`.

The skill installs the Docker CLI (plus compose and buildx) in the box and
mounts the host daemon's socket. Anything that can run Docker on the host
has effective root on the host, and `byre status` disclaims that hole for
as long as the skill is enabled.
[docs/DOCKER-HOST.md](https://github.com/pjlsergeant/byre/blob/main/docs/DOCKER-HOST.md)
covers what the grant really means and when to prefer something narrower.
Nested Podman (a daemon inside the box, granting nothing on the host)
is unsupported.

## Get the coding agent to edit its own byre config?

tldr: `byre develop --self-edit` -- the box gets its own config mounted,
and changes are shown on exit.

The box's configuration directory is mounted at `/home/dev/.byre-self`,
with documentation telling the agent how to make edits. The agent then
writes the next launch's mounts, run arguments and build -- the config
that decides what it can reach -- so a self-edit session trusts the
agent with your host. Use it for a session you mean, not as your
default;
[the security model](/docs/security-model/#self-edit-sessions) has the
rest.
