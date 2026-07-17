---
title: How do I…?
weight: 90
description: the cookbook -- shared logins, dotfiles, review loops, the firewall, the exit, and everything between
---

Every recipe is a question a real user has, a tldr, and a shipped
feature behind it. Grouped by where you are: setting up the box, using
it daily, building your toolkit, or getting out of trouble.

## Configuring the box

### Save my LLM credentials so I don't need to re-auth for each box?

tldr: say **y** when the first-run picker offers shared auth for your
agent -- or `byre config` and enable the relevant _x-shared-auth_ skill(s)
by hand.

By default agents log in once per project, inside the box. The shared-auth
skills (claude-shared-auth, codex-shared-auth, gemini-shared-auth,
opencode-shared-auth) move that to once per machine. For claude, codex,
and opencode every project's first run asks: "Use machine-wide credentials
to log in to &lt;agent&gt;?" -- yes enables the skill for that project
(its `byre.config`), and only for it. Saying yes to "Save these as your
default?" remembers your answer like the template/agent favourites: the
next box's question just defaults to it, one Enter to accept. (Enabling
the skill by hand in `~/.byre/default.config` is the machine-wide route --
then the question stops.) The login lives in a shared volume that
reset/forget deliberately never touch. See the
[security model](/docs/security-model/) for the implications of this.
(Grok's shared auth works differently -- its token rotation can't be
file-shared, so a broker mediates instead, and until its field gate passes
the skill is hand-enabled rather than offered --
[ADR 0036](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0036-grok-shared-auth-v2-broker.md).)

### Enable a skill in this box?

tldr: `byre config` -> Skills.

Bundled skills toggle on the spot; installed and local ones appear once
they exist on the machine. Everything a skill grants shows up in
`byre status` the moment it's enabled --
[what a skill is](/docs/skills-and-templates/).

### Add an MCP server to my agent's session?

tldr: `byre mcp add <name> <url>` -- or `byre mcp add <name> --
<command...>` for a local server; `--global` for every project.

`--bearer TOKEN_NAME` wires static-token auth (the header carries
`${TOKEN_NAME}`, expanded from the box env at launch -- names travel,
values don't). `byre mcp list` shows the effective set with every entry
attributed to the layer or skill that declared it; `byre mcp remove` is
closure-smart, so removing a skill-declared server writes a `!name`
entry instead of failing. Declarations bake into the image and inject
into the agent session; a remote server's host is attributed egress in
`byre status`.

### Add a package to this box -- and promote it to my template?

tldr: `byre config` -> Packages for this box; when it belongs
everywhere, move the line into your template (fork the bundled one
first: `byre template fork byre/node my-node`).

The first time you want a postgres client, it's one `apt` line in one
project's config. When it belongs everywhere you write node, fork the
bundled template into an editable local one, add the package there, and
set `template = "my-node"` -- every project on that template gets it on
its next develop. For literally-everywhere, `byre config --global` puts
it in your personal baseline.

### Set my defaults so every new box starts right?

tldr: `byre config --global` edits `~/.byre/default.config` -- the
bottom layer of every project's cascade.

Anything a config can carry can live there: packages, mounts, skills,
env, egress. The first-run picker's favourites (template, agent, shared
auth) are remembered separately, as preferences -- your most-used
answers become the pre-selected defaults.

### Switch agents for one project?

tldr: `byre config` -> Agent, relaunch.

The `agent` key decides which enabled agent skill's command launches in
the foreground. Each agent keeps its login in its own state volume, so
switching to codex for a week and back to claude costs no re-auth.

### Expose a port to see the box's dev server?

tldr: `byre config` -> Ports (or `[[ports]] container = 3000` in the
config).

Published ports bind `127.0.0.1` by default -- your browser reaches the
box, your LAN doesn't. Binding a different interface is an explicit,
louder choice (`interface = "0.0.0.0"`). Every published port shows in
`byre status`.

### Pass env vars into the box?

tldr: `[env]` for plain config values -- never secrets -- and
`[env_from_host]` to pass host values at runtime.

`[env]` literals are **baked into the image**: `docker history` shows
them and they outlive `byre reset`, so they're for configuration, not
credentials. `[env_from_host]` is the runtime channel and a legible
grant: `KEY = "env:HOST_VAR"` passes a host env var at launch,
`"git:user.email"` reads git config, `"tz:"` passes your timezone --
values resolve at launch and never land in a layer. Git identity,
`TERM`, and `TZ` already pass through by default.

### Use my API key instead of an agent login?

tldr: pass it at runtime -- `[env_from_host]` with
`OPENAI_API_KEY = "env:OPENAI_API_KEY"` -- never `[env]`, which bakes
it into the image.

The agents' own login flows are still the better default: the
credential lands in the agent's state volume, per project, and the
shared-auth skills make one login serve every box. But where an API key
is the workflow, `env_from_host` keeps it out of the image, resolves it
fresh at every launch, and shows as a named grant in `byre status`.

### Use Podman instead of Docker?

tldr: nothing -- `engine = "auto"` (the default) picks docker if
present, else podman.

Force it with `engine = "podman"` per project or globally. Rootful and
rootless both work; rootless Podman 4.3+ runs under `--userns=keep-id`
so files still land correctly owned. Everything else -- volumes,
images, status -- reads identically across engines.

### Mount other folders from the host?

tldr: `byre config` -> Mounts.

Each mount is a host path, an in-box path, and a read-only/read-write
choice (read-only is the default); every mount shows up in `byre
status` under "Host mounts".

### Bring my dotfiles and shell setup into every box?

tldr: mount them read-only -- `byre config --global` -> Mounts -- and
the box's target mirrors your home path, so they land where the agent
looks.

A home-relative host path (`~/.config/starship.toml`) suggests the
matching `/home/dev/...` target automatically. Symlinks into a dotfiles
repo work -- byre reads through them. For dotfiles that should be baked
in rather than live-mounted, a template's `[files]` copies them into
the image (one caveat: files baked under a state volume's mountpoint,
like `~/.claude`, are masked by the volume at runtime).

### Give my agent standing instructions in every box?

tldr: a tiny local skill with a `[context]` block -- every box that
enables it injects the text into the agent's memory file.

`byre skill init my-conventions`, point its `skill.toml` at a
`context.md` (`[context] file = "context.md"`), enable it -- globally
in `~/.byre/default.config` for every box. byre concatenates enabled
skills' contexts into the agent's own memory path (Claude's
`CLAUDE.md`, Codex's `AGENTS.md`), additive with whatever the project
carries. It's how byre's own dev box enforces its diary and review
habits.

### Stop re-downloading dependencies on every rebuild?

tldr: a `[[volumes]]` entry with `role = "cache"` on the dependency
directory.

Templates ship the obvious ones (`node_modules` for node); add your own
for anything regenerable that's slow to fetch. Cache volumes survive
rebuilds and relaunches, and losing one only costs a re-download.
[Volumes & state](/docs/volumes-and-state/) has the model.

### Run project setup automatically?

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

### Cap the box's CPU or RAM?

tldr: `run_args = ["--cpus=2", "--memory=4g"]`.

`run_args` is raw `docker run` passthrough, appended after byre's own
flags so yours win. byre never parses inside it; `byre status` degrades
its posture claims honestly when it's present. (Identity-changing flags
-- `--user`, `--userns` -- are the one documented footgun: they break
the baked-UID ownership model.)

### Restrict network access?

tldr: `byre config` and enable the _firewall_ skill, then pick what to
open under Egress.

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

### Run other Docker containers from inside the byre environment?

tldr: `byre config` and enable the _docker-host_ skill.

The skill installs the Docker CLI (plus compose and buildx) in the box and
mounts the host daemon's socket. It's worth being clear-eyed about what
you're granting: anything that can run Docker on the host also has
effective root on the host, and `byre status` disclaims that hole for as
long as the skill is enabled.
[docs/DOCKER-HOST.md](https://github.com/pjlsergeant/byre/blob/main/docs/DOCKER-HOST.md)
covers what the grant really means and when to prefer something narrower.
Nested Podman (a daemon inside the box, granting nothing on the host) is
possible future work; there's no support for it today.

### Get the coding agent to edit its own byre config?

tldr: `byre develop --self-edit` -- the box gets its own config mounted,
and changes are shown on exit.

`byre develop --self-edit` will mount the box's configuration directory on
`/home/dev/.byre-self` and will also ship contextual documentation to your
box telling your agent how to make edits. There are (of course!) some
security implications to this, so it's probably best not to always run in
this mode. Changes to the configuration will be shown on exit.

## Daily workflow

### Run parallel agents on the same repo?

tldr: `byre worktree <branch>` -- a linked git worktree plus a second
boxed session in it, one command.

<!-- demo-placeholder: worktree-parallel-session -->
> 🎬 *[demo slot: `byre worktree`, a second session opening beside the first -- VM-recorded cast]*

The worktree inherits the repo's config, image, and volumes -- the agent
is already logged in -- but runs in its own container against its own
checkout, so sessions run side by side. Hand-made worktrees
(`git worktree add`) inherit the same way. The mechanics -- where
worktrees live, shared state, blast radius -- are on the
[worktrees page](/docs/worktrees/).

### Set up two agents in a review loop?

tldr: keep one agent as `agent`, enable a second agent's skill as a
ride-along -- byre's own box runs Claude with codex beside it as the
independent reviewer.

More than one agent skill can be enabled in a box; the config's `agent`
key decides which one launches, and the rest install their CLI and keep
their own login. byre develops itself this way: `agent = "claude"`,
`skills = ["codex", ...]`, plus a small skill that ships the review
conventions as [standing instructions](#give-my-agent-standing-instructions-in-every-box)
-- the launched agent runs the reviewer as a fresh-eyes second opinion.
The live example is
[byre.preset](https://github.com/pjlsergeant/byre/blob/main/byre.preset)
in byre's own repo.

### Get a second shell in the box?

tldr: `byre shell`.

A second shell (as the dev user) in the running session -- for logins,
running tests, poking around while the agent works. It sees the same
env the agent launched with.

### Resume my session after a config change?

tldr: exit the agent, `byre develop`, then your agent's own resume verb
(Claude's `/resume`).

Config edits apply on the next develop; the rebuild touches only the
layers after what changed, so relaunches are quick. The agent's history
lives in its state volume, so resuming lands you back in the same
conversation.

### Paste or drag-and-drop images and files into my agent?

tldr: `byre deliver <file>` -- or just `byre deliver` and paste (or
drop a file on the window).

<!-- demo-placeholder: deliver-paste-flow -->
> 🎬 *[demo slot: screenshot, `byre deliver`, Ctrl-V, path lands on the clipboard -- generated cast]*

Anything you deliver lands in the box's `/inbox` and the in-box path
comes back on your clipboard, ready to Cmd-V into the agent prompt.
With no arguments byre reads your *clipboard* -- so screenshot,
`byre deliver`, Ctrl-V, done (Ctrl-V, not Cmd-V, for images -- the
terminal won't paste an image any other way). Dragging a file from
Finder onto the deliver window delivers that file; whole directories
arrive intact; `byre deliver --install-app` adds a Dock-droppable
app and a Finder "Deliver to Byre" Quick Action on macOS. Works from
any directory -- it finds your running box. The full surface, including
piping from stdin:
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md).

### Use byre on a remote machine over SSH?

tldr: byre is terminal-native, so everything works in an SSH session --
and `byre deliver ssh://host` sends files from your laptop into the
remote box.

The config TUI, the pickers, and the agent session all run in a plain
SSH terminal. Delivered paths land on your local clipboard where the
terminal supports OSC 52, and are always printed regardless. The one
thing a terminal can't carry is an image paste -- so deliver runs a
remote mode: `byre deliver ssh://dev@studio shot.png` streams from the
laptop side, with the box picked locally and plain ssh doing transport
and auth. byre must be installed on both ends (a version mismatch fails
loudly before anything moves; `--remote-byre` points at a binary sshd
can't find).

### Get tab completion for byre commands?

tldr: `eval "$(byre completion bash)"` in your shell's startup file.

<!-- demo-placeholder: completion-tab-walk -->
> 🎬 *[demo slot: tab-completing byre commands and flags -- generated cast, short]*

Completions cover every command and flag -- bash, zsh, fish, and
powershell. One line in your rc file regenerates the script at shell
startup (~3ms), so it never goes stale across byre upgrades and needs no
extra packages:

```sh
eval "$(byre completion bash)"        # ~/.bashrc
source <(byre completion zsh)         # ~/.zshrc, after compinit
byre completion fish | source         # ~/.config/fish/config.fish
```

`byre completion --help` has the powershell line and the details.

## Skills & templates

### Write my own skill?

tldr: `byre skill init <name>`, edit its `skill.toml`, enable it in a
box.

A skill can install packages, ship files into the image, declare
volumes and network endpoints, and carry agent context --
[the model](/docs/skills-and-templates/). `byre skill validate` checks
your work; `byre skill fork` turns any bundled or installed package
into an editable local copy to start from. The full authoring
reference:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

### Make a template for a stack byre doesn't ship?

tldr: `byre template init <name>`, set its base image and packages,
then `template = "<name>"` in a project.

A template is shape, not behavior: base image, packages, egress offers,
optional files. Fork the nearest bundled one
(`byre template fork byre/python my-elixir`) and edit from there.

### Share a skill or template with someone else?

tldr: `byre skill pack <id> > skill.toml`, host the directory anywhere
https reaches; they run `byre skill install <uri> --digest sha256:...`.

`pack` emits a manifest carrying every file's hash and the package
digest, so `install` verifies byte-for-byte and `--digest` pins to
exactly what was reviewed. `byre skill inspect <uri>` shows the full
trust surface -- contributions, grants, hashes -- without installing,
and installing grants nothing until a box enables the skill.

### Share one config baseline across many projects?

tldr: `byre layer new torn`, put the shared config in it
(`byre config --layer torn`), then `extends = "torn"` in each project
(`byre config`, EXTENDS section).

A **named layer** is a config file at `~/.byre/layers/<name>/layer.config`
that any project (or another layer -- chains work) pulls in with
`extends`. It slots between the template and the project in the cascade
and carries everything a config can except `template` -- skills, egress,
env, mounts, the lot. It's live: edit the layer once and every extending
project picks it up on its next develop. Layers aren't packages -- no
versions, no installing; to share one, send the file. `byre status` shows
the chain, and every inherited setting is attributed to its layer in
`byre config`. See
[ADR 0035](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0035-named-layers-and-extends.md).

### Ship a recommended box config with my project?

tldr: commit a `byre.preset`; whoever clones runs `byre preset apply`.

A preset is a complete proposed config in `byre.config` format --
cloning gives your teammates a file, not a prompt. `apply` walks them
through any missing package installs (each with its own grant review),
shows the composed box's grants, and writes the project config on their
confirm; `[sources]` hints pin where the packages come from and their
digests. [The full flow](/docs/configuration-reference/#presets-byrepreset).

### Version-control my `~/.byre`?

tldr: `git init ~/.byre` and commit the durable layer --
`default.config`, `templates/`, `layers/` -- not `projects/` or
`packages/`.

The store is plain files by design, so git works on it unmodified.
`projects/` is machine-specific (identities derive from paths),
`packages/` is re-fetchable snapshots and already carries its own
`.gitignore`, and `bundled/` is a display mirror byre regenerates.
Your defaults, templates, and layers are the part worth keeping -- and
the part worth carrying to a new machine.

## Lifecycle & recovery

### Start over -- and which hammer do I reach for?

tldr: `byre reset` wipes this project's volumes, `byre forget` removes
everything byre holds for the directory, `byre rebuild` just refreshes
the image.

`reset` is for wedged box state: project volumes die (agent login and
history included -- it names them first and asks), image and config
stay. `forget` is the full walk-away: volumes, image, and byre's
host-side config for the directory -- your project tree is never
touched. `rebuild` rebuilds the image with the cache off and touches no
volumes. Machine-wide volumes (shared agent logins) survive all three
deliberately. The [comparison table](/docs/volumes-and-state/#which-hammer)
has the details.

### Move or rename a project directory?

tldr: move it, then `byre rehome <old-id>` -- bare `byre rehome` lists
the likely candidates.

A project's identity derives from its path, so a move orphans the old
image and volumes. `rehome` migrates them (and the config) onto the new
path's identity and retires the old one. If the repo has worktrees, run
it from the main worktree.

### Pull fresh tool versions?

tldr: `byre rebuild`.

Rebuilds the image with the build cache disabled, so every install step
re-fetches the current upstream -- the deliberate-staleness valve.
Volumes are untouched; your agent stays logged in.

### Stop using byre?

tldr: `byre dockerfile` and `byre dockerrun` print the whole exit;
`byre ejectfirewall` prints the firewall's step.

`byre dockerfile` prints the image, `byre dockerrun` prints the exact run
command -- that's the whole exit. The firewall is the one thing that
doesn't travel automatically (its rules are applied from outside the box,
by byre); `byre ejectfirewall` prints that step as a standalone script.
See
[docs/EJECTING.md](https://github.com/pjlsergeant/byre/blob/main/docs/EJECTING.md).

### Uninstall byre completely?

tldr: `byre forget` in each project, clear machine volumes via
`byre config` -> Volumes, then delete `~/.byre` and the binary.

`forget` clears a project's volumes, image, and host-side config across
every installed engine. Machine-wide volumes (shared agent logins)
survive it by design -- clear them from `byre config` -> Volumes, or
remove `byre-machine-u*` volumes with your engine directly. Everything
byre ever makes is prefixed `byre-` on the engine and lives under
`~/.byre` on the host; there are no daemons, no background services,
and no networks to clean. If you installed the deliver app, delete
`~/Applications/Byre Deliver.app` and `~/Library/Services/Deliver to
Byre.workflow` (macOS) or the `.desktop` launcher (Linux).

### Report a bug?

tldr: a GitHub issue with `byre version`, `byre status`, and the
generated Dockerfile (`byre dockerfile`).

Those three artifacts exist for this moment -- they say exactly which
byre, what the box could touch, and what was built, with nothing secret
in any of them. Security-sensitive reports go via GitHub security
advisories instead
([docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md)).

## Everything else

### …do something not listed here?

tldr: point your agent at
[github.com/pjlsergeant/byre](https://github.com/pjlsergeant/byre) and
ask.

byre's repo carries unusually thorough documentation of intentions,
design constraints, and decisions -- the architecture, the ADRs, the
glossary, the principles. Your agent can read all of it, which makes it
the long tail of this cookbook: if byre can do the thing, your agent
will find the shipped mechanism; if it can't, you'll hear why.
