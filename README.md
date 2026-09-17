# byre

<img src="site/static/logo.png" align="right" width="160" alt="The byre logo: a highland cow peeking out of a boxed terminal">

**A comfortable, constrained agent environment around any folder**

Run `byre develop` in a project, worktree, or scratch directory, and byre creates a local container -- the box -- around it. Your favourite tools and creature comforts come with you, but the rest of the host stays out of reach.

MIT licensed | open source | local | single binary | no lock-in | for Linux and macOS

**📖 Full documentation: [getbyre.com/docs](https://getbyre.com/docs/)** -- [quickstart](https://getbyre.com/docs/quickstart/) · [configuration](https://getbyre.com/docs/configuration/) · [cookbook](https://getbyre.com/docs/how-do-i/) · [security model](https://getbyre.com/docs/security-model/)

```console
$ brew install --cask pjlsergeant/tap/byre   # (see below for Linux)
$ cd ~/my-project
$ byre develop

  byre: exposure: /workspace rw · 10 env vars
  byre: network open
  ╭──────────────────────────────────╮
  │ ✻ Claude Code                    │
  │   /workspace                     │
  ╰──────────────────────────────────╯
```

It's **`--dangerously-skip-permissions`, without risking the farm.**

Every box opens familiar: your tools installed, your defaults applied, your agent's login persisting, per project, across rebuilds.

Ask your agent if byre is right for you:

```text
Take a good look at https://github.com/pjlsergeant/byre. Is it a good project
or just vibe-coded trash? Is it right for me? Would you be happy there?
```

## Comfortable: bring your environment

Bring your familiar tools, reusable skills, caches, and stack-specific packages. Agents [stay logged in, per project, across rebuilds](https://getbyre.com/docs/volumes-and-state/), and your defaults follow you everywhere. Templates handle different stacks, and project configuration handles the exceptions.

byre ships templates for Go, Node, and Python, and agent skills for Claude Code, Codex, Gemini, Grok, and OpenCode. It's easy to fork the ones that are bundled to make changes to them, or you and your agent can easily write your own based on the ones supplied. Skills can provide mounts, volumes, installed packages, and agent context.

Adding a postgres client -- or anything else -- is as simple as adding a line in the project's config, which the bundled TUI makes easy. Pretty soon you'll be at a point where `byre develop` in a brand-new directory starts you up with everything you personally need to be productive: your tooling, your favourite agents, and no further setup.

## Change the box in seconds

Most byre config is done very quickly in the TUI: `byre config` will open an editor for that folder's config, including over SSH:

```text
byre project config  (client-api-pjl-3bbe8c)
exposure: 1 host mount · 11 env vars · network deny-by-default · egress 7 hosts

─ GRANTS — what this box can reach ─────────────────────────
▸ Extra mounts      : 1 mount  (enter to edit)
  Ports             : (none)
  Egress            : 7 hosts  (7 from skills)  — 11 offered
  Env vars          : 11 vars  (6 inherited, 5 from skills)

─ BUILD — how the box is made ──────────────────────────────
  Template          : [go] [node] [python] [none]
  Agent             : [claude] [codex] [gemini] [grok] [opencode] [none]
  Packages          : 2 packages
  Skills            : 3 enabled
  MCP servers       : (none)
  Instructions      : 1 snippet
··· (more below)

↑↓ move · ←→ change · ↵ open · ^s save · ^e $EDITOR · ^q quit
```

Configs are hierarchial, so if a specific client or set of projects need their own standing instructions, that's easy to add to a shared layer. Adding ripgrep (or any package), mounting a sibling directory ... it's all a few key taps in `byre config` and relaunch and `/resume`.

And if you really want to live dangerously: `byre develop --self-edit` will the agent its own box config (but we'll show you what it changed on exit).

## Install

byre is a young project -- read the [maturity note](https://getbyre.com/docs/install/) before you lean on it.

byre is a single Go binary:

```sh
brew install --cask pjlsergeant/tap/byre
```

You need Docker (or Podman) running on the host. For Linux, installation via go, and build-from-source, please see the [install page](https://getbyre.com/docs/install/).

## Quickstart

When you first start byre (`byre develop`) it'll ask you to choose a language template, an agent, and ask whether you want to share the agent credentials themselves between byre boxes: you can read more in the [quickstart](https://getbyre.com/docs/quickstart/).

To skip the questions:

```sh
byre develop --template go --agent claude
```

You can always see what the box can see with `byre status`:

```text
$ byre status
Project id:   my-project-pjl-069d95
Agent:        byre/claude
Template:     byre/go      bundled v1.3.1
Engine:       docker
Project:      ~/my-project -> /workspace  (rw)
Image:        byre-my-project-pjl-069d95-u501-g20  (sha256:1f0b7c9d4e2a…
              (--full to show); base golang:1.26-bookworm)
Network:      open
Ports:        none
Host mounts:  none
Skills:       byre/claude  bundled v1.3.1
State vols:   .claude
Cache vols:   none
Box env:      10 keys the box received  (values never recorded; --full to show)
Container:    running (0d95f3a2c1b4)
              ↳ the grant rows above describe THIS box (launch record
              4c1e8a7b2d90). Other rows describe the current config.
```

(you can use `byre status --full` for more comprehensive output, or `byre status --json` if you need machine-parseable)

Everything from here on has a page on the docs site:
**[getbyre.com/docs](https://getbyre.com/docs/)**.

## What's available to the agent, and what isn't

By default the agent can't access your host filesystem, environment, or credentials you have kicking about in dot files (eg ssh keys). The network and the folder you've run `byre develop` in are open by design. byre is not intended to be a security product, it's intended to be guardrails, but with the ability to switch almost all of those guardrails off. The contract in full:
[what's boxed, what isn't](https://getbyre.com/docs/whats-boxed/); security model:
[security model](https://getbyre.com/docs/security-model/).

## Commands

`byre develop`, `byre config`, and `byre deliver` are the ones you'll use frequently. There's a full table at
[getbyre.com/docs/commands/](https://getbyre.com/docs/commands/).

## Why not...?

A list of comparisons against other sandboxing solutions: [getbyre.com/why-not](https://getbyre.com/why-not/).

## How do I...?

Every answer's full recipe lives in the
[cookbook](https://getbyre.com/docs/how-do-i/); the tldrs:

**Save my LLM credentials so I don't need to re-auth for each box?**
tldr: say **y** when the first-run picker offers shared auth for your
agent -- or enable the relevant _x-shared-auth_ skill(s) in
`byre config`.
([recipe](https://getbyre.com/docs/how-do-i/configure/#save-my-llm-credentials-so-i-dont-need-to-re-auth-for-each-box))

**Use my API key instead of an agent login?**
tldr: pass it at runtime -- `[env_from_host]` with
`OPENAI_API_KEY = "env:OPENAI_API_KEY"` -- don't use `[env]`, which bakes
it into the image.
([recipe](https://getbyre.com/docs/how-do-i/configure/#use-my-api-key-instead-of-an-agent-login))

**Run parallel agents on the same repo?**
tldr: `byre worktree <branch>` -- a linked git worktree plus a second
boxed session in it.
([recipe](https://getbyre.com/docs/how-do-i/workflow/#run-parallel-agents-on-the-same-repo))

**Set up two agents in a review loop?**
tldr: keep one agent as `agent`, enable a second agent's skill too -- byre's developed in a box that runs Claude with codex beside it as an independent reviewer.
([recipe](https://getbyre.com/docs/how-do-i/workflow/#set-up-two-agents-in-a-review-loop))

**Give my agent standing instructions in every box?**
tldr: in the TUI see the **Instructions** section:
([recipe](https://getbyre.com/docs/how-do-i/configure/#give-my-agent-standing-instructions-in-every-box))

**Add an MCP server to my agent's session?**
tldr: `byre mcp add <name> <url>` -- or `byre mcp add <name> --
<command...>` for a local server; `--global` for every project.
([recipe](https://getbyre.com/docs/how-do-i/configure/#add-an-mcp-server-to-my-agents-session))

**Bring my dotfiles and shell setup into every box?**
tldr: don't do this. But if you have to, you can mount them in the TUI as read-only.
([recipe](https://getbyre.com/docs/how-do-i/configure/#bring-my-dotfiles-and-shell-setup-into-every-box))

**Share one config baseline across many projects?**
tldr: `byre layer new torn`, put the shared config in it
(`byre config --layer torn`), then `extends = "torn"` in each project
(the **Extends** section of the config TUI).
([recipe](https://getbyre.com/docs/how-do-i/toolkit/#share-one-config-baseline-across-many-projects))

**Ship a recommended box config with my project?**
tldr: commit a `byre.preset`; whoever clones runs `byre preset apply`.
([recipe](https://getbyre.com/docs/how-do-i/toolkit/#ship-a-recommended-box-config-with-my-project))

**Paste or drag-and-drop images and files into my agent?**
tldr: `byre deliver <file>` -- or just `byre deliver` and paste (or
drop a file on the window).
([recipe](https://getbyre.com/docs/how-do-i/workflow/#paste-or-drag-and-drop-images-and-files-into-my-agent))

**Get files back out of the box?**
tldr: `byre grab <box-path>`.
([recipe](https://getbyre.com/docs/how-do-i/workflow/#get-files-back-out-of-the-box))

**Use byre on a remote machine over SSH?**
tldr: byre is terminal-native, so everything works in an SSH session --
and `byre deliver ssh://host` sends files from your laptop into the
remote box.
([recipe](https://getbyre.com/docs/how-do-i/workflow/#use-byre-on-a-remote-machine-over-ssh))

**Get tab completion for byre commands?**
tldr: `eval "$(byre completion bash)"` in your shell's startup file.
([recipe](https://getbyre.com/docs/how-do-i/workflow/#get-tab-completion-for-byre-commands))

**Restrict network access?**
tldr: enable the _firewall_ skill in `byre config`, then pick what to
open under **Egress**.
([recipe](https://getbyre.com/docs/how-do-i/configure/#restrict-network-access))

**Cap the box's CPU or RAM?**
tldr: `run_args = ["--cpus=2", "--memory=4g"]`.
([recipe](https://getbyre.com/docs/how-do-i/configure/#cap-the-boxs-cpu-or-ram))

**Mount other folders from the host?**
tldr: the **Mounts** section of the TUI (`byre config`).
([recipe](https://getbyre.com/docs/how-do-i/configure/#mount-other-folders-from-the-host))

**Expose a port to see the box's dev server?**
tldr: the **Ports** section of the TUI (`byre config`).
([recipe](https://getbyre.com/docs/how-do-i/configure/#expose-a-port-to-see-the-boxs-dev-server))

**Run other Docker containers from inside the byre environment?**
tldr: enable the _docker-host_ skill in `byre config`.
([recipe](https://getbyre.com/docs/how-do-i/configure/#run-other-docker-containers-from-inside-the-byre-environment))

**Use Podman instead of Docker?**
[see here](https://getbyre.com/docs/how-do-i/configure/#use-podman-instead-of-docker)

**Get the coding agent to edit its own byre config?**
tldr: `byre develop --self-edit` -- the box gets its own config mounted,
and changes are shown on exit.
([recipe](https://getbyre.com/docs/how-do-i/configure/#get-the-coding-agent-to-edit-its-own-byre-config))

**Write my own skill?**
tldr: `byre skill init <name>`, edit its `skill.toml`, enable it in a
box. I'd recommend asking an agent to do it for you
([recipe](https://getbyre.com/docs/how-do-i/toolkit/#write-my-own-skill))

**Stop using byre?**
tldr: `byre dockerfile` and `byre dockerrun` print the whole exit;
`byre ejectfirewall` prints the firewall's step.
([recipe](https://getbyre.com/docs/how-do-i/recovery/#stop-using-byre))

**…do something not listed here?**
tldr: point your agent at
[github.com/pjlsergeant/byre](https://github.com/pjlsergeant/byre) and
ask.
([recipe](https://getbyre.com/docs/how-do-i/recovery/#do-something-not-listed-here))

**📖 Docs: [getbyre.com/docs](https://getbyre.com/docs/)** · design:
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) · contributions:
[`CONTRIBUTING.md`](CONTRIBUTING.md).
