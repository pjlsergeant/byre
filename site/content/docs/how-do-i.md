---
title: How do I…?
weight: 90
description: shared logins, delivering files, completions, the exit, the firewall
---

## Save my LLM credentials so I don't need to re-auth for each box?

tldr: say **y** when the first-run picker offers shared auth for your
agent -- or `byre config` and enable the relevant _x-shared-auth_ skill(s)
by hand.

By default agents log in once per project, inside the box. The shared-auth
skills (claude-shared-auth, codex-shared-auth, gemini-shared-auth) move
that to once per machine. For claude and codex every project's first run
asks: "Opt this box into &lt;agent&gt; shared credentials?" -- yes enables the
skill for that project (its `byre.config`), and only for it. Saying yes to
"Save these as your default?" remembers your answer like the
template/agent favourites: the next box's question just defaults to it,
one Enter to accept. (Enabling the skill by hand in
`~/.byre/default.config` is the machine-wide route -- then the question
stops.) On an install that predates the offer, run `byre skill update`
once so the companion skills carry the offer metadata. The login lives in
a shared volume that reset/forget deliberately never touch. See
[docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md)
for the implications of this. (Grok has no shared-auth: its token rotation
can't be file-shared, so it logs in per project --
[ADR 0023](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0023-grok-shared-auth-retired.md).)

## Paste images and files into the box?

tldr: `byre deliver <file>` — or just `byre deliver` and paste.

Anything you deliver lands in the box's `/inbox` and the in-box path comes
back on your clipboard, ready to Cmd-V into the agent prompt. With no
arguments byre reads your *clipboard* — so screenshot, `byre deliver`,
paste, done. Works from any directory (it finds your running box), over
SSH, and with whole directories. See
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md).

## Get tab completion for byre commands?

tldr: `eval "$(byre completion bash)"` in your shell's startup file.

Completions cover every command and flag — bash, zsh, fish, and
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

`byre dockerfile` prints the image, `byre dockerrun` prints the exact run
command -- that's the whole exit. The firewall is the one thing that
doesn't travel automatically (its rules are applied from outside the box,
by byre); `byre ejectfirewall` prints that step as a standalone script.
See
[docs/EJECTING.md](https://github.com/pjlsergeant/byre/blob/main/docs/EJECTING.md).

## Restrict network access?

tldr: `byre config` and enable the _firewall_ skill. Under "Egress" choose
what to open. We automatically open the ports your selected agent needs,
and there may be more suggestions based on your selected skills (eg
Github) but those you'll need to manually open and then relaunch.

By default, we don't restrict network access. The _firewall_ skill flips
that to deny-by-default: your container starts but runs nothing while a
privileged one-shot helper joins its network namespace, installs the
allowlist rules, and verifies them. Only then does the agent launch behind
the wall -- and if any of that fails, the box dies closed rather than
running open.

One honest limit worth knowing: hostname grants are pinned to the IPs they
resolved to at launch, so on DNS that rotates (CDNs, some cloud resolvers)
a granted host can start failing -- closed, never open -- until a relaunch
re-resolves it. Details in
[docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md).

Just want to block telemetry, not the internet? The _firewall-open_ skill
keeps the network open and drops only the hosts you block:
`egress = ["!statsig.anthropic.com"]`. The same `!host` entries subtract
from the full firewall's allowlist too, skill-declared endpoints included.

## Mount other folders from the host?

tldr: `byre config` -> Mounts

## Run other Docker containers from inside the byre environment?

Today this is possible rather than ergonomic. You can mount the host's
Docker daemon socket using `byre config` -> Mounts. It's worth remembering
that anything that can run Docker on the host also has effective root on
the host. I plan to make this even easier and also support nested Podman
in the very near future.

## Get the coding agent to edit its own byre config?

`byre develop --self-edit` will mount the box's configuration directory on
`/home/dev/.byre-self` and will also ship contextual documentation to your
box telling your agent how to make edits. There are (of course!) some
security implications to this, so it's probably best not to always run in
this mode. Changes to the configuration will be shown on exit.
