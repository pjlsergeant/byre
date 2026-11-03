---
title: Lifecycle & recovery
weight: 94
description: reset vs forget vs rebuild, moving projects, leaving, reporting bugs
---

## Start over -- and which hammer do I reach for?

tldr: `byre reset` wipes this project's volumes, `byre forget` removes
everything byre holds for the directory, `byre rebuild` just refreshes
the image.

Wedged agent state is `reset`; walking away from the directory is
`forget`; stale tools are `rebuild`. None of them touches your project
tree or the machine-wide volumes that hold shared agent logins. What
each one removes, side by side:
[which hammer](/docs/volumes-and-state/#which-hammer).

**"podman isn't reachable"?** `reset`, `forget`, `rehome` and `backup`
check every installed engine, so one that cannot be reached stops them.
Common on a Mac with podman installed and its machine never started.
Start that engine, or leave it out with `byre reset --ignore-podman`
(`--ignore-docker` the other way round); byre then prints what it did
not check or remove there. Why every engine:
[Volumes & state](/docs/volumes-and-state/#which-hammer).

## Move or rename a project directory?

tldr: move it, then `byre rehome <old-id>` -- bare `byre rehome` lists
the likely candidates.

A project's identity derives from its path, so a move orphans the old
image and volumes. `rehome` migrates them (and the config) onto the new
path's identity and retires the old one. The candidates bare `rehome`
lists are stored projects whose recorded path no longer exists. If the
repo has worktrees, run it from the main worktree.

## Move my box to another machine?

tldr: `byre backup`, send the file, `byre restore <file>` in a fresh
checkout.

`byre backup` writes one file holding this project's config and its
state volumes -- the agent's login, memory and history, and your
encrypted credential rows. It leaves out the workspace (git carries
that), the image, and machine-wide volumes, and shows you what it will
carry and what it leaves behind before it starts. It needs the project
completely still: stop any running box first.

Send the file to the other machine, along with anything the backup
NAMES but does not carry: a shared layer (`extends`) is a plain file you
copy across yourself. Then clone or copy the repo there.

**Where restore writes.** Run `byre restore <file>` in the fresh
checkout. Without a DIR it takes the current directory, and it accepts
only an empty directory or the clean root of a git checkout, so a
directory you happened to be in never becomes the project. Name the
directory (`byre restore <file> ~/src/thing`) or pass
`--allow-nonempty` if you mean it anyway.

**What it shows you.** Before writing anything restore reviews the
engine, the volumes it will create, the volumes already there it will
leave alone, the credential state, and every host path this machine
will have to satisfy, with the ones not here yet marked. On your y/n it
writes the config and pours the volumes.

**Missing packages.** Restore offers to install any skill or template
the config names and the file has an install hint for. It builds
nothing: if a package is still missing it stops and prints the install
command; install it and run `byre restore` again. Once it is through,
`byre develop` builds the image and starts the box.

**Credentials.** Credentials travel encrypted and still need the source
config's passphrase at the first launch -- rotate it afterwards with
`byre credentials rekey` if you want the new machine on its own.
Machine-wide volumes (shared agent logins) never travel: log in once on
the new machine, as any box does.

Two things to know before you send the file. It is not encrypted --
encrypt it yourself before it crosses anything you don't trust; what
it exposes, `--no-credentials` included, is in the
[security model](/docs/security-model/#backups). And byre reads its own
format: don't repack it, or restore will refuse it.

## Pull fresh tool versions?

tldr: `byre rebuild`.

The base image is pulled again and every install step re-fetches the
current upstream -- the deliberate-staleness valve. Your agent stays
logged in.

## Stop using byre?

tldr: `byre dockerfile` and `byre dockerrun` print the image and run
command; `byre ejectfirewall` prints the firewall's step; credential
delivery has no eject.

`byre dockerfile` prints the image, `byre dockerrun` prints the exact run
command. Two things byre does from outside the box at launch don't
travel in them: the firewall, whose rules byre applies (`byre
ejectfirewall` prints that step as a standalone script), and the
delivery of encrypted project credentials, which needs the passphrase
and so needs `byre develop`. Both make an ejected box fail closed
rather than run without them. See
[docs/EJECTING.md](https://github.com/pjlsergeant/byre/blob/main/docs/EJECTING.md).

## Uninstall byre completely?

tldr: `byre forget` in each project, clear machine volumes from the
**Volume data** section of `byre config`, then delete `~/.byre` and the
binary.

byre leaves three kinds of thing on a machine, and nothing else -- no
daemons, no background services, no networks:

- **Engine artifacts**, all prefixed `byre-`. `forget` clears a
  project's volumes and image across every installed engine.
  Machine-wide volumes (shared agent logins) survive it by design --
  clear them from the **Volume data** section of `byre config`, or
  remove `byre-machine-u*` volumes with your engine directly.
- **The store**, `~/.byre` on the host.
- **The deliver app**, if you installed it: `~/Applications/Byre
  Deliver*.app` and `~/Library/Services/Deliver to Byre*.workflow`
  (macOS), or the `byre-deliver*` launchers and icons under
  `~/.local/share` (Linux). Named and remote installs add one set per
  target, and each install prints its own paths.

## Report a bug?

tldr: a GitHub issue with `byre version`, `byre status`, and the
generated Dockerfile (`byre dockerfile`).

Those three artifacts say exactly which byre, what the box could touch,
and what was built. Skim them before you paste, because they report
your config faithfully: `[env]` values are baked into the generated
Dockerfile as literals (`byre status` lists only the keys), and
whatever you put in `run_args`, `dockerfile_pre` or `dockerfile_post` is
echoed back word for word in both. Those are the places to redact;
logins and project credentials appear only as paths and key names.
[CONTRIBUTING.md](https://github.com/pjlsergeant/byre/blob/main/CONTRIBUTING.md)
has the report's shape. Security-sensitive reports go via GitHub
security advisories instead
([docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md)).

## …do something not listed here?

tldr: point your agent at
[github.com/pjlsergeant/byre](https://github.com/pjlsergeant/byre) and
ask.

byre's repo carries documentation of intentions, design constraints,
and decisions -- the architecture, the ADRs, the
glossary, the principles. Your agent can read all of it, which makes it
the long tail of this cookbook: if byre can do the thing, your agent
will find the shipped mechanism; if it can't, you'll hear why.
