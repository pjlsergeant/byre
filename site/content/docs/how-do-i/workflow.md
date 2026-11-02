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

`byre build` generates and builds the project's image exactly as
`byre develop` does, then stops: no volumes seeded, no container. stdout
is the image tag and nothing else; the build output goes to stderr. Use
it to warm a persistent runner's engine cache before a job, to read the
tag, or to fail a job early on a config that no longer builds.

It does not make a hosted CI runner faster: a hosted runner rebuilds the
image every job, because the engine's build cache lives on the machine
and the machine is new each time. A persistent runner keeps that cache,
so its builds after the first are quick with or without `byre build`.

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

`byre run` sets up the box exactly as `byre develop` does, hands one task
to the agent's headless mode, and exits when the agent does. It works from
any script; nothing in it is specific to a CI vendor. The bundled claude,
codex and grok skills have a headless mode; an agent without one is
refused by name.

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

`--yes` prints the whole review into the job log and applies without
asking, installing the packages the preset's `[sources]` name. It
refuses three things, before fetching or writing anything:

- a preset named by an https URL -- nobody would look at what it
  serves. Commit the preset to the repo and apply the path;
- an https `[sources]` row with no digest -- nobody would look at what
  it fetched;
- anything the review's credential lines would show: an encrypted
  credential row (`byre credentials set`) in the preset or in the config
  it would replace, a credentials identity arriving, leaving or being
  replaced, or a side byre could not read. A swapped value is something
  only your own confirm can catch, so `--yes` never stands in for it,
  on a terminal either. That is why the credential travels as a host
  passthrough here.

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

- **Gate on `$rc` or on `outcome.json`, never on stderr.** The last
  stderr line (`byre: run <id>: ran, exit 0 (verified)`) is for humans:
  the agent writes to stderr for the whole run, so it could print the
  same line. `0` is success; `10`-`16` are the agent's or the headless
  machinery's verdicts (`13` is "no usable credential", with the remedy
  on stderr); `1`-`3` and `70` are byre's. The
  [commands page](/docs/commands/#what-byre-run-exits-with) has the
  table, and the `--data` document's `outcome` and `phase` tell byre's
  codes from a box that happened to exit with the same number. Its
  `authority` reads `unverified: raw run_args` when the config carries
  raw `run_args` -- byre cannot vouch for what such a box was handed.
- **Without `--data`, stdout is the agent's answer** and nothing else,
  ready to post somewhere.
- **The task** can also come from `BYRE_TASK` in byre's own environment,
  or `--task -` from stdin. `--task -` reads until the writer closes its
  end; a step that cannot promise that uses a file or `BYRE_TASK`. Tasks
  are capped at 1 MiB.
- **No time limit unless you set one.** `--timeout 30m` stops the agent
  and exits `12`. Use it for an agent that can hang instead of failing --
  grok, for one, hangs on a dead login refresh, so set `--timeout` for
  grok runs that rely on a saved login.
- **Encrypted credentials** (`byre credentials set`) need
  `--credentials stdin` with the passphrase on stdin, which then can't
  also carry the task.
- `--quiet` sends the agent's live output and stderr to the result dir
  instead of your terminal; errors, the exit report and the summary line
  still print, and a failed run (any exit but 0 and 10) prints the last
  lines of the agent's stderr before the summary, so a refusal's remedy
  is never hidden.

**Keep the output.** The agent's files, its full transcript and the
answer land in `.byre-run/` in the work tree, which ignores itself in git
and is **emptied at the start of every run** -- archive it before running
again. Everything in it is the agent's to write; byre reads none of it
back. Push from the host after `byre run` exits, so push credentials
never enter the box.

**Clean up.** A hosted runner is discarded, so there is nothing to clean
-- and nothing carried over: every job builds the image from scratch. On
a self-hosted runner, `byre reset -y` after the job removes this
project's volumes (the agent's state) and keeps the image for the next
build; `byre forget -y` removes everything byre holds for the project.

**codex on a machine you keep.** codex's headless mode logs an
`OPENAI_API_KEY` (or `CODEX_ACCESS_TOKEN`) into a codex home made for that
one run and deletes it afterwards -- never into the project's saved codex
state. That is deliberate: with codex-shared-auth enabled, any key saved
there would be shared with every project on the machine at the next
launch -- billing their work to this key and replacing a shared login,
without asking. A key in the environment also wins over a codex login the
box already has, for that run. On a discarded hosted runner none of this
can bite; on a laptop or a self-hosted runner it is why the key never
sticks. The mechanics:
[docs/AGENT-CREDENTIAL-MECHANICS.md](https://github.com/pjlsergeant/byre/blob/main/docs/AGENT-CREDENTIAL-MECHANICS.md).
