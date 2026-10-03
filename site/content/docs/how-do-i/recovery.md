---
title: Lifecycle & recovery
weight: 94
description: reset vs forget vs rebuild, moving projects, leaving, reporting bugs
---

## Start over -- and which hammer do I reach for?

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

## Move or rename a project directory?

tldr: move it, then `byre rehome <old-id>` -- bare `byre rehome` lists
the likely candidates.

A project's identity derives from its path, so a move orphans the old
image and volumes. `rehome` migrates them (and the config) onto the new
path's identity and retires the old one. If the repo has worktrees, run
it from the main worktree.

## Move my box to another machine?

tldr: `byre backup`, send the file, `byre restore <file>` in a fresh
checkout.

`byre backup` writes one file holding this project's config and its state
volumes -- the agent's login, memory and history, your encrypted credential
rows, everything byre keeps for the directory. It shows you what it will
carry and what it leaves behind first, and it needs the project completely
still: stop any running box before you run it. The workspace is not in there,
because git carries that.

Send the file to the other machine, along with anything the backup NAMES but
does not carry: a shared layer (`extends`) is a plain file you copy across
yourself. Then clone or copy the repo there, and in that fresh checkout run
`byre restore <file>`. It reviews what it is about to do -- the engine, the
volumes it will create, the volumes already there it will leave alone, the
credential state, and every host path this machine will have to satisfy --
offers to install any skill or template the config names and the file has an
install hint for, and on your y/n writes the config and pours the volumes.

Restore does not build anything. If a package it names is still missing it
stops and prints the install command; install it and run `byre restore`
again. Once it is through, `byre develop` builds the image and starts the box.
Credentials travel encrypted and still need the source config's passphrase at
the first launch -- rotate it afterwards with `byre credentials rekey` if you
want the new machine on its own. Machine-wide volumes (shared agent logins)
never travel: log in once on the new machine, as any box does.

Two things to know before you send the file. It is an ordinary file at mode
0600 with no encryption of its own, and whatever is plaintext inside a volume
is plaintext in it -- `--no-credentials` drops the encrypted rows from the
CONFIG copy and says nothing about volume contents, so encrypt the file
yourself if it is crossing anything you don't trust. And byre reads its own
format: don't repack it, or restore will refuse it.

## Pull fresh tool versions?

tldr: `byre rebuild`.

Rebuilds the image with the build cache disabled, so every install step
re-fetches the current upstream -- the deliberate-staleness valve.
Volumes are untouched; your agent stays logged in.

## Stop using byre?

tldr: `byre dockerfile` and `byre dockerrun` print the whole exit;
`byre ejectfirewall` prints the firewall's step.

`byre dockerfile` prints the image, `byre dockerrun` prints the exact run
command -- that's the whole exit. The firewall is the one thing that
doesn't travel automatically (its rules are applied from outside the box,
by byre); `byre ejectfirewall` prints that step as a standalone script.
See
[docs/EJECTING.md](https://github.com/pjlsergeant/byre/blob/main/docs/EJECTING.md).

## Uninstall byre completely?

tldr: `byre forget` in each project, clear machine volumes from the
**Volume data** section of `byre config`, then delete `~/.byre` and the
binary.

`forget` clears a project's volumes, image, and host-side config across
every installed engine. Machine-wide volumes (shared agent logins)
survive it by design -- clear them from the **Volume data** section of
`byre config`, or remove `byre-machine-u*` volumes with your engine
directly. Everything
byre ever makes is prefixed `byre-` on the engine and lives under
`~/.byre` on the host; there are no daemons, no background services,
and no networks to clean. If you installed the deliver app, delete
`~/Applications/Byre Deliver*.app` and `~/Library/Services/Deliver to
Byre*.workflow` (macOS) or the `byre-deliver*` launchers and icons
under `~/.local/share` (Linux) -- named/remote installs add one
artifact set per target, and each install prints its own paths.

## Report a bug?

tldr: a GitHub issue with `byre version`, `byre status`, and the
generated Dockerfile (`byre dockerfile`).

Those three artifacts exist for this moment -- they say exactly which
byre, what the box could touch, and what was built. Skim them before
you paste, because they report your config faithfully: `[env]` values
are baked into the generated Dockerfile as literals (`byre status`
lists only the keys), and whatever you put in `run_args`,
`dockerfile_pre` or `dockerfile_post` is echoed back word for word in
both. byre never puts a credential in a box itself -- logins arrive
through mounts and volumes, which show up as paths -- so usually there
is nothing to redact; the two places to look are the ones you wrote.
Better still, ask an agent or two to confirm the bug
against the source first -- the repo is built to be read, and an
agent-verified report usually gets fixed fast
([CONTRIBUTING.md](https://github.com/pjlsergeant/byre/blob/main/CONTRIBUTING.md)
has the shape). Security-sensitive reports go via GitHub security
advisories instead
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
