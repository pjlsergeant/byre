---
title: Daily workflow
weight: 92
description: parallel agents, review loops, delivering files, remote SSH, completions, headless runs
---

## Run parallel agents on the same repo?

tldr: `byre worktree <branch>` -- a linked git worktree plus a second
boxed session in it.

<!-- demo-placeholder: worktree-parallel-session -->

```sh
byre worktree fix-flaky-tests
```

The new session shares the repo's config, image, volumes and agent
login, in its own container and checkout. Where worktrees live, what
they share, and hand-made ones: the [worktrees page](/docs/worktrees/).

## Set up two agents in a review loop?

tldr: keep one agent as `agent`, enable a second agent's skill too -- byre's developed in a box that runs Claude with codex beside it as an independent reviewer.

More than one agent skill can be enabled in a box; the config's `agent`
key decides which one launches, and the rest install their CLI and keep
their own login. The configuration is `agent = "claude"`,
`skills = ["codex", ...]`, plus a small skill that ships the review
conventions as [standing instructions](/docs/how-do-i/configure/#give-my-agent-standing-instructions-in-every-box)
-- the launched agent runs the reviewer as a fresh-eyes second opinion.
The live example is
[byre.preset](https://github.com/pjlsergeant/byre/blob/main/byre.preset)
in byre's own repo.

## Get a second shell in the box?

tldr: `byre shell`.

For logins, running tests, poking around while the agent works. It sees
the same env the agent launched with.

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

```sh
byre deliver design.png   # lands in the box's /inbox; the path is on your clipboard
byre deliver              # then Ctrl-V a screenshot, or drop a file on the window
```

Images paste with Ctrl-V, not Cmd-V: the terminal won't paste an image
any other way. Directories, stdin, and the Dock-droppable deliver app
(`byre deliver --install-app`):
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md).

## Get files back out of the box?

tldr: `byre grab <box-path>`.

```sh
byre grab out/report.pdf        # copies it here and prints where it landed
```

Box paths count from `/workspace`. Grab never overwrites your files: a name collision lands uniquified
(`report-2.pdf`). Destinations, directories, and streaming to stdout:
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md#getting-files-back-out-byre-grab).

## Use byre on a remote machine over SSH?

tldr: byre is terminal-native, so everything works in an SSH session --
and `byre deliver ssh://host` sends files from your laptop into the
remote box.

The config TUI, the pickers, and the agent session all run in a plain
SSH terminal. A terminal can't carry an image paste, so send images
from the laptop side, with byre installed on both ends:

```sh
byre deliver ssh://dev@studio shot.png
```

ssh does the transport and authentication. Remote drag targets, version
checks, and a remote byre sshd can't find:
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md#remote-delivery).

## Get tab completion for byre commands?

tldr: `eval "$(byre completion bash)"` in your shell's startup file.

<!-- demo-placeholder: completion-tab-walk -->

Completions cover every command and flag, for bash, zsh, fish, and
powershell. The line regenerates the script at shell startup (~3ms), so
it never goes stale across byre upgrades and needs no extra packages.
For the other shells:

```sh
source <(byre completion zsh)         # ~/.zshrc, after compinit
byre completion fish | source         # ~/.config/fish/config.fish
```

`byre completion --help` has the powershell line and the details.

## Build the box without starting a session?

tldr: `tag=$(byre build)` -- the image `byre develop` would build, and
its tag on stdout.

The build output goes to stderr; no volumes are seeded and no container
starts. Use it to warm a persistent runner's engine cache before a job,
to read the tag, or to fail a job early on a config that fails to
build; a hosted runner starts with an empty cache every job, so it
gains nothing there.

- It uses the engine's cache, as develop does; `byre rebuild` builds
  with the cache off and the base pulled again.
- It waits only for another setup of the project, not for a live
  session; the running box keeps the image it started from.
- A never-configured checkout needs `--template` and `--agent`; on a
  configured one, `-a <agent>` swaps the agent for this build only.
- It checks only what the build needs, not mount paths or credentials.

## Run one task headlessly (CI)?

tldr: `byre run --task task.md --data > outcome.json` -- one task, no
terminal, and byre's verdict as the exit code.

`byre run` sets up the box as `byre develop` does, hands one task to the
agent's headless mode, and exits when the agent does. Nothing in it is
specific to a CI vendor. The bundled claude, codex and grok skills have a
headless mode; an agent without one is refused by name.

**Configure once, with a committed preset.** Make the preset on your
own machine with the editor (`byre config` in the project):

1. Pick the agent and its skills -- the firewall among them.
2. Open the egress doors the job needs.
3. In the **Env vars** section, add `ANTHROPIC_API_KEY` (or
   `OPENAI_API_KEY` for codex) with its source set to the host variable
   of the same name. Your CI secret store sets that variable for the
   job. A codex key passed this way is logged into a codex home that
   lasts one run, never the project's saved codex state
   ([docs/AGENT-CREDENTIAL-MECHANICS.md](https://github.com/pjlsergeant/byre/blob/main/docs/AGENT-CREDENTIAL-MECHANICS.md)).

Then copy the file the editor's footer names
(`Saves to: ~/.byre/projects/<id>/byre.config`) into the repo as
`byre.preset`. Packages that are not bundled need a `[sources]` row
each, with its digest (`byre skill inspect <url>` prints it) -- the
[configuration reference](/docs/configuration-reference/#presets-byrepreset)
has the shape. The job applies it before running:

```sh
byre preset apply --yes
```

`--yes` prints the review into the job log and applies without asking.
It refuses an https preset address, a digest-less https source and any
credential row
([ADR 0063](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0063-preset-apply-yes.md)),
which is why the credential travels as a host passthrough. A preset
hosted somewhere is downloaded in a step you control and applied as a
file:

```sh
curl -fsSLg -o byre.preset https://example.com/path/byre.preset
byre preset apply --yes
```

**Or, for the minimal box, two flags.** On a checkout byre has never
configured, `byre run --template <t> --agent <a> ...` (`none` for no
template) writes the project's config from them and proceeds. That
config carries no credential passthrough, so the agent's credential has
to come from a machine you keep: your baseline (`byre config --global`,
the same **Env vars** row). On a fresh hosted runner, use the preset.

Either way, packages the preset does not install for you go in first
with `byre skill install <url> --digest sha256:... --yes`.

**Run it.**

```sh
byre run --task path/to/task.md --data > outcome.json
rc=$?
```

- **Gate on `$rc` or on `outcome.json`, never on stderr** -- the agent
  shares stderr. The
  [commands page](/docs/commands/#what-byre-run-exits-with) has the exit
  table.
- **Without `--data`, stdout is the agent's answer**, ready to post
  somewhere.
- **Set a `--timeout`** (exit `12` when it expires); there is none by
  default. Grok on a saved login needs one: it hangs on a dead login
  refresh instead of failing.
- `byre run --help` has the other task sources (`--task -`,
  `BYRE_TASK`), `--credentials stdin` for encrypted credentials, and
  `--quiet`.

**Keep the result dir.** The agent's files, its transcript and the
answer land in `.byre-run/` in the work tree, which ignores itself in
git and is **emptied at the start of every run** -- archive it before
running again. It is the agent's to write; don't gate on it. Push from
the host after `byre run` exits, so push credentials never enter the
box.

**Clean up.** A hosted runner is discarded, so there is nothing to
clean. On a self-hosted runner, `byre reset -y` after the job removes
the agent's state and keeps the image for the next build
([which hammer](/docs/volumes-and-state/#which-hammer)).
