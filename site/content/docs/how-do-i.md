---
title: How do I…?
weight: 90
description: shared logins, config layers, delivering files, completions, the exit, the firewall
---

## Save my LLM credentials so I don't need to re-auth for each box?

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

## Share one config baseline across many projects?

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

## Paste images and files into the box?

tldr: `byre deliver <file>` -- or just `byre deliver` and paste.

Anything you deliver lands in the box's `/inbox` and the in-box path comes
back on your clipboard, ready to Cmd-V into the agent prompt. With no
arguments byre reads your *clipboard* -- so screenshot, `byre deliver`,
paste, done. Works from any directory (it finds your running box), over
SSH, and with whole directories. See
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md).

## Get tab completion for byre commands?

tldr: `eval "$(byre completion bash)"` in your shell's startup file.

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

## Stop using byre?

tldr: `byre dockerfile` and `byre dockerrun` print the whole exit;
`byre ejectfirewall` prints the firewall's step.

`byre dockerfile` prints the image, `byre dockerrun` prints the exact run
command -- that's the whole exit. The firewall is the one thing that
doesn't travel automatically (its rules are applied from outside the box,
by byre); `byre ejectfirewall` prints that step as a standalone script.
See
[docs/EJECTING.md](https://github.com/pjlsergeant/byre/blob/main/docs/EJECTING.md).

## Restrict network access?

tldr: `byre config` and enable the _firewall_ skill, then pick what to
open under Egress.

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

## Mount other folders from the host?

tldr: `byre config` -> Mounts.

Each mount is a host path, an in-box path, and a read-only/read-write
choice; every mount shows up in `byre status` under "Host mounts".

## Run other Docker containers from inside the byre environment?

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

## Get the coding agent to edit its own byre config?

tldr: `byre develop --self-edit` -- the box gets its own config mounted,
and changes are shown on exit.

`byre develop --self-edit` will mount the box's configuration directory on
`/home/dev/.byre-self` and will also ship contextual documentation to your
box telling your agent how to make edits. There are (of course!) some
security implications to this, so it's probably best not to always run in
this mode. Changes to the configuration will be shown on exit.
