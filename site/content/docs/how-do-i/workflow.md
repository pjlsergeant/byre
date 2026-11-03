---
title: Daily workflow
weight: 92
description: parallel agents, review loops, delivering files, remote SSH, completions, headless runs
---

## Run parallel agents on the same repo?

tldr: `byre worktree <branch>` -- a linked git worktree plus a second
boxed session in it.

<!-- demo-placeholder: worktree-parallel-session -->

The worktree inherits the repo's config, image, and volumes -- the agent
is already logged in -- but runs in its own container against its own
checkout, so sessions run side by side. Hand-made worktrees
(`git worktree add`) inherit the same way. The mechanics -- where
worktrees live, shared state, blast radius -- are on the
[worktrees page](/docs/worktrees/).

## Set up two agents in a review loop?

tldr: keep one agent as `agent`, enable a second agent's skill too -- byre's developed in a box that runs Claude with codex beside it as an independent reviewer.

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

Anything you deliver lands in the box's `/inbox` and the in-box path
comes back on your clipboard, ready to Cmd-V into the agent prompt.
With no arguments byre reads your *clipboard* -- so screenshot,
`byre deliver`, Ctrl-V, done (Ctrl-V, not Cmd-V, for images -- the
terminal won't paste an image any other way). Dragging a file from
Finder onto the deliver window names the host path and asks before opening
and delivering it; whole directories
arrive intact; `byre deliver --install-app` adds a Dock-droppable
app and a Finder "Deliver to Byre" Quick Action on macOS -- and takes
an `ssh://` target and/or `--name`, so several drag targets can
coexist, each labeled and delivering to its own (local or remote)
box. Works from any directory -- it finds your running box. The full surface, including
piping from stdin:
[docs/DELIVER.md](https://github.com/pjlsergeant/byre/blob/main/docs/DELIVER.md).

## Get files back out of the box?

tldr: `byre grab <box-path>`.

The agent made you something -- a PDF, a chart, a patch --
`byre grab out/report.pdf` copies it out and prints where it landed.
Box paths count from `/workspace`; directories come out recursively;
a second argument names the destination, and `-` streams to stdout
(`byre grab build.log - | grep error`). Grab never overwrites your
files: name collisions land uniquified (`report-2.pdf`) and byre says
so. Works from any directory -- it finds your running box, exactly like
deliver. The full surface:
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
can't find). The drag targets work for remotes too:
`byre deliver --install-app ssh://dev@studio` installs a
"Byre Deliver (dev@studio)" app (distinct icon) whose drops ship
straight to the remote box.

## Get tab completion for byre commands?

tldr: `eval "$(byre completion bash)"` in your shell's startup file.

<!-- demo-placeholder: completion-tab-walk -->

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

## Build the box without starting a session?

tldr: `tag=$(byre build)` -- the image `byre develop` would build, and
its tag on stdout.

The build output goes to stderr; no volumes are seeded and no container
starts. Use it to warm a persistent runner's engine cache before a job,
to read the tag, or to fail a job early on a config that fails to
build. A hosted runner starts with an empty cache every job, so it gains
nothing there.

- It uses the engine's cache, as develop does. For a build with the
  cache off and the base pulled again, use `byre rebuild`.
- A live session does not stop it; it waits only for another setup of
  the project to finish. The running box keeps the image it started
  from, and the next `byre develop` uses the new one.
- A never-configured checkout needs `--template` and `--agent`, as
  `byre run` does. On a configured one, `-a <agent>` builds with that
  agent for this build only; nothing is written.
- It checks what the build needs -- the config, the skills, the
  Dockerfile -- not what only a launch reads, such as mount paths or
  credentials.

## Run one task headlessly (CI)?

tldr: `byre run --task task.md --data > outcome.json` -- one task, no
terminal, and byre's verdict as the exit code.

`byre run` sets up the box as `byre develop` does, hands one task to the
agent's headless mode, and exits when the agent does. Nothing in it is
specific to a CI vendor. The bundled claude, codex and grok skills have a
headless mode; an agent without one is refused by name.

**Configure once, with a committed preset.** Commit a `byre.preset`
that carries the agent, its skills -- the firewall among them -- the
egress the job needs, and the agent's credential as a host passthrough,
and have the job apply it before running:

```sh
byre preset apply --yes
```

Make the preset on your own machine with the editor: `byre config` in
the project, pick the agent and skills, open the egress doors the job
needs, and in the **Env vars** section add `ANTHROPIC_API_KEY` (or
`OPENAI_API_KEY` for codex) with its source set to the host variable of
the same name. Then copy the file the editor's footer names
(`Saves to: ~/.byre/projects/<id>/byre.config`) into the repo as
`byre.preset`. Your CI secret store sets that variable for the job.
Packages that are not bundled need a `[sources]` row each, with its
digest (`byre skill inspect <url>` prints it) -- the
[configuration reference](/docs/configuration-reference/#presets-byrepreset)
has the shape.

`--yes` prints the review into the job log and applies without asking.
It refuses an https preset address, a digest-less https source and any
credential row
([ADR 0063](https://github.com/pjlsergeant/byre/blob/main/docs/adr/0063-preset-apply-yes.md)),
which is why the credential travels as a host passthrough. A preset
hosted somewhere is downloaded in a step you control and applied as a
file:

```sh
curl -fsSLo byre.preset https://example.com/path/byre.preset
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
- **The task** can also come from `BYRE_TASK` or `--task -` (stdin, read
  until the writer closes it).
- **No time limit unless you set one.** `--timeout 30m` stops the agent
  and exits `12`. Set it for grok runs on a saved login: grok hangs on a
  dead login refresh instead of failing.
- **Encrypted credentials** (`byre credentials set`) need
  `--credentials stdin` with the passphrase on stdin, which then can't
  also carry the task.
- `--quiet` keeps the agent's live output out of your terminal; errors,
  refusals, a failed run's stderr tail and the summary line still print.

**Keep the output.** The agent's files, its transcript and the answer
land in `.byre-run/` in the work tree, which ignores itself in git and is
**emptied at the start of every run** -- archive it before running again.
It is the agent's to write; don't gate on it. Push from the host after
`byre run` exits, so push credentials never enter the box.

**Clean up.** A hosted runner is discarded, so there is nothing to clean.
On a self-hosted runner, `byre reset -y` after the job removes this
project's volumes (the agent's state) and keeps the image for the next
build; `byre forget -y` removes everything byre holds for the project.

**codex on a machine you keep.** A key in the environment is logged into
a codex home that lasts one run, never the project's saved codex state
([docs/AGENT-CREDENTIAL-MECHANICS.md](https://github.com/pjlsergeant/byre/blob/main/docs/AGENT-CREDENTIAL-MECHANICS.md)).
