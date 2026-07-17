---
title: Daily workflow
weight: 92
description: parallel agents, review loops, delivering files, remote SSH, completions
---

## Run parallel agents on the same repo?

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

## Set up two agents in a review loop?

tldr: keep one agent as `agent`, enable a second agent's skill as a
ride-along -- byre's own box runs Claude with codex beside it as the
independent reviewer.

More than one agent skill can be enabled in a box; the config's `agent`
key decides which one launches, and the rest install their CLI and keep
their own login. byre develops itself this way: `agent = "claude"`,
`skills = ["codex", ...]`, plus a small skill that ships the review
conventions as [standing instructions](/docs/how-do-i/configure/#give-my-agent-standing-instructions-in-every-box)
-- the launched agent runs the reviewer as a fresh-eyes second opinion.
The live example is
[byre.preset](https://github.com/pjlsergeant/byre/blob/main/byre.preset)
in byre's own repo.

## Get a second shell in the box?

tldr: `byre shell`.

A second shell (as the dev user) in the running session -- for logins,
running tests, poking around while the agent works. It sees the same
env the agent launched with.

## Resume my session after a config change?

tldr: exit the agent, `byre develop`, then your agent's own resume verb
(Claude's `/resume`).

Config edits apply on the next develop; the rebuild touches only the
layers after what changed, so relaunches are quick. The agent's history
lives in its state volume, so resuming lands you back in the same
conversation.

## Paste or drag-and-drop images and files into my agent?

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

## Use byre on a remote machine over SSH?

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

## Get tab completion for byre commands?

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
