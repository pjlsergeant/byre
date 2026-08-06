---
title: Commands
weight: 40
description: every byre command, generated from the binary
---

<!-- GENERATED FILE — do not edit. Rendered from the cobra command tree:
     go run ./cmd/byre commands-page > site/content/docs/commands.md
     TestCommandsPagePinsSiteFile pins this file to that output. -->

Every command, one line each, straight from the binary. Flags and detail:
`byre <command> --help` -- and
[completions](/docs/how-do-i/workflow/#get-tab-completion-for-byre-commands) cover
every command and flag.

## Daily driving

| Command | What it does |
|---|---|
| `byre develop` | Set up and run the project box in the foreground. |
| `byre shell` | Open a shell (as the dev user) in the running session. |
| `byre worktree <name>` | Create a git worktree and start a parallel session in it. |
| `byre deliver [<path>... \| -]` | Deliver files from the host into a running box's /inbox. |
| `byre grab <box-path> [<host-path> \| -]` | Grab a file or directory out of a running box onto the host. |

## Inspection

| Command | What it does |
|---|---|
| `byre status` | Show resolved config, mounts, skills, session state. |
| `byre dockerfile` | Print the generated Dockerfile for this directory. |
| `byre dockerrun` | Print the docker/podman run command byre would use. |
| `byre ejectfirewall` | Print the firewall netns helper as a standalone script. |
| `byre version` | Print the byre version. |

## Configuration

| Command | What it does |
|---|---|
| `byre config` | Edit this project's config interactively. |
| `byre preset` | Review and apply a config preset (byre.preset, a path, or an https URI). |
| `byre preset apply [<uri>\|<path>]` | Chauffeur missing installs, review the composed box, write byre.config. |
| `byre preset inspect [<uri>\|<path>]` | The apply review without the write (read-only). |
| `byre layer` | Manage named config layers (new, list, validate). |
| `byre layer new <name>` | Scaffold a named layer. |
| `byre layer list` | List named layers, flagging broken ones. |
| `byre layer validate [name]` | Parse a layer and walk its extends chain (or all). |
| `byre mcp` | Manage this project's MCP server declarations ([[mcp]] config blocks). |
| `byre mcp add <name> (<url> \| -- <command>...)` | Declare an MCP server in the project config (or --global defaults). |
| `byre mcp remove <name>` | Remove a declared MCP server (closure-smart). |
| `byre mcp list` | Show the effective MCP set (config + skills, attributed) and its delivery. |
| `byre claude-skill` | Manage this project's Claude Skill declarations ([[claude_skills]] config blocks). |
| `byre claude-skill add <dir>` | Declare a Claude Skill (a directory with a SKILL.md) in the project config (or --global defaults). |
| `byre claude-skill remove <name>` | Remove a declared Claude Skill (closure-smart). |
| `byre claude-skill list` | Show the effective Claude Skill set (config + skills, attributed) and its delivery. |
| `byre context` | Manage standing agent instructions ([[context]] config blocks). |
| `byre context add <name>` | Add or update standing instructions in the project config (or --global defaults). |
| `byre context remove <name>` | Remove standing instructions (closure-smart). |
| `byre context list` | Show the resolved standing instructions (name + source). |
| `byre credentials` | Manage project credentials (an age-encrypted vault; values delivered per launch after unlock). |
| `byre credentials init` | Create this project's vault (masked passphrase, confirmed). |
| `byre credentials declare <name>` | Declare a credential in the project config (name, kind, target — never a value). |
| `byre credentials undeclare <name>` | Remove a credential declaration (closure-smart; the stored value stays). |
| `byre credentials set <name>` | Store a value: masked prompt on a terminal, or piped stdin. Never an argument. |
| `byre credentials unset <name>` | Discard a stored value (the declaration stays). |
| `byre credentials rekey` | Rotate the vault passphrase (the identity is unchanged). |
| `byre credentials list` | Show declared credentials with kind, target, and value-state. Values render nowhere. |

## Skills & templates

| Command | What it does |
|---|---|
| `byre skill` | Manage skill packages (list, inspect, fork, init, validate). |
| `byre skill list` | List skill packages in the catalog. |
| `byre skill inspect <id\|uri>` | Show skill package metadata and grants (URIs fetch without installing). |
| `byre skill install <manifest-uri>` | Fetch, verify, and snapshot a skill package (grants nothing until enabled). |
| `byre skill uninstall <id>` | Remove an installed skill package (referencing boxes are listed first). |
| `byre skill pack <name>` | Emit the distribution manifest for a local skill. |
| `byre skill fork <id> <new-id>` | Fork an immutable skill into a local editable package. |
| `byre skill init <name>` | Scaffold a new local skill package. |
| `byre skill adopt <dir>` | Make a directory the local source for the skill id it declares (symlink into the store). |
| `byre skill validate [name]` | Two-stage parse and resolve-check a skill (or all). |
| `byre skill archive-legacy` | Move LEGACY materialized dirs to skills.legacy/ / templates.legacy/. |
| `byre template` | Manage template packages (list, inspect, fork, init, validate). |
| `byre template list` | List template packages in the catalog. |
| `byre template inspect <id\|uri>` | Show template package metadata (URIs fetch without installing). |
| `byre template install <manifest-uri>` | Fetch, verify, and snapshot a template package (grants nothing until enabled). |
| `byre template uninstall <id>` | Remove an installed template package (referencing boxes are listed first). |
| `byre template pack <name>` | Emit the distribution manifest for a local template. |
| `byre template fork <id> <new-id>` | Fork an immutable template into a local editable package. |
| `byre template init <name>` | Scaffold a new local template package. |
| `byre template adopt <dir>` | Make a directory the local source for the template id it declares (symlink into the store). |
| `byre template validate [name]` | Two-stage parse a template (or all). |

## Lifecycle & recovery

| Command | What it does |
|---|---|
| `byre reset` | Wipe this project's named volumes. |
| `byre rebuild` | Rebuild the image with the cache disabled. |
| `byre rehome [<old-id>]` | Re-point this directory's identity after a move. |
| `byre forget` | Remove all byre host-side state for this directory. |

## Shell integration

| Command | What it does |
|---|---|
| `byre completion <shell>` | Generate a shell completion script. |

## Exit codes

Scriptable, and the same for every command unless a row says otherwise.

| Code | Meaning |
|---|---|
| `0` | Success. For `byre deliver` and `byre grab`, that means bytes landed. |
| `1` | byre failed, with the reason on stderr. Also every nothing-was-delivered outcome -- a cancelled picker, an empty paste, an ambiguous box set with no terminal. |
| `2` | You typed it wrong: an unknown flag, a bad argument count. One known exception, recorded rather than fixed: a panic on a goroutine other than the main one ends the process through Go's runtime, which also exits `2` -- byre cannot recover another goroutine's panic to re-code it. A Go panic trace on stderr, rather than a usage message, is what tells you which happened. |
| `3` | `byre develop` declined to start: a session is already live in this directory, or a live box of this project is holding a volume declared `sharing = "exclusive"` (or byre could not establish that none is). `reset` and `forget` decline the same situation with `1` -- a deliberate asymmetry, since only `develop` has a code to spare. |
| `4` | `byre deliver --boxes` reached part of the pool but not all of it. |
| `70` | byre crashed. That is a bug in byre; the report is on stderr and we would like to see it. |

### What `byre develop` does differently

Once the box has actually run, **whatever status the agent's own process
exits with, `0` through `127`, is passed straight through** -- no byre
banner -- so a script sees what your agent did rather than what byre made
of it.

That band covers the whole table above, and byre does not renumber
around it. `1` is the collision you will actually meet: an agent that
exits `1` and a byre that failed before the box ever launched both
leave you with process status `1`. `3` and `70` collide the same way --
an agent exiting either gives you `develop` exit `3` or `70`,
indistinguishable BY CODE from byre's own refusal or byre's own crash.

What separates them is the shape of byre's own output, and that rule
holds:

- A byre failure carries the `byre: ` error banner.
- A refusal (`3`) carries byre's own report naming what it declined
  over -- the running box and how to reach it, or the exclusive volume
  and who is holding it.
- A crash (`70`) carries the panic report.
- A passed-through agent status carries **no byre error banner at all**.

Note what that does NOT say: quiet output is not the test. The agent's
own stderr is yours to read either way, and byre's exit report (what
changed in the box) prints on a normal session end too. The test is the
absence of a byre ERROR banner, not the absence of output.

Past `127` the passthrough stops: byre exits `1` on its own account,
with the box's real status in the message. It is careful about how much
it claims that status means:

- `137` is read as SIGKILL and said so plainly: the box was killed out
  from under the session -- removed externally, engine shutdown, or the
  kernel's OOM killer. Nothing in a box's normal life ends that way.
- `129`-`192` are decoded **tentatively** ("possibly SIGTERM"), because
  `128+n` is a convention and not a guarantee: a process can exit `130`
  deliberately with no signal anywhere near it.
- `128`, and `193` upward, are left undecoded. Outside the signal range
  there is nothing honest to add.
