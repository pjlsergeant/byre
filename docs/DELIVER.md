# Getting files into the box: `byre deliver`

`byre deliver` copies host files, the clipboard, or stdin into a running
box and prints the in-box paths, ready to paste into the agent prompt.

```
$ byre deliver report.pdf
/inbox/report.pdf
byre: path copied to clipboard
```

## The flows

**Drop a file (or several, or a directory):**

```
byre deliver shot.png notes.md
byre deliver ./bugreport/
```

Files land in the box's `/inbox`, keeping their names (`shot.png` →
`/inbox/shot.png`; collisions uniquify to `shot-2.png`, never
overwrite). A directory arrives with its structure preserved and yields
one path (`/inbox/bugreport/`).

Every delivered path prints to stdout, one per line -- that is the
contract. Where the host has a clipboard byre can write (pbcopy, wl-copy,
xclip, or OSC 52 through SSH), the same list is copied to it too; each
nicety that is unavailable says so on stderr (see "What works where").

**Deliver the clipboard** -- the screenshot flow. Copy anything
(Cmd-Shift-Ctrl-4 a region, Cmd-C a file in Finder, copy some text),
then:

```
$ byre deliver

  byre deliver

  🖼  image on the clipboard

  press ctrl-v to deliver it
    (cmd-v won't work for images)

  ctrl-c cancels
```

The prompt samples your clipboard's *types* (never the content), says
what's on offer, and keeps sampling while it waits -- copy something
else and the line updates in place. **Ctrl-V** reads the clipboard
directly -- file references are delivered as those files, an image
lands as `/inbox/clipboard-<timestamp>.png` (extension matches what
your clipboard actually holds), text as `clipboard-<timestamp>.txt`.
Cmd-V works too for text and copied files (for an image-only clipboard
the terminal sends nothing on Cmd-V -- which is why the prompt steers
you to Ctrl-V when it sees an image). You can also **drag a file from
Finder onto the window**: byre names every host path and asks before opening
and delivering it. The delivery confirmation tells you what shipped -- kind and size, never the
content:

```
byre: delivered clipboard image (1.4 MB) → /inbox/clipboard-20260710-091412.png
```

**Pipe stdin:**

```
pngpaste - | byre deliver - --name shot.png
```

`-` reads stdin into a single file (`--name` names it; otherwise
`stdin-<timestamp>`).

## Which box?

`deliver` (like its mirror, `grab` -- below) is machine-scoped: it finds
a running box rather than requiring you to stand in the project
directory.

1. `--box <id-or-project-prefix>` picks explicitly (the prefix must
   match exactly one session).
2. Otherwise, if your cwd is inside a running box's workdir (any
   subdirectory), that box wins.
3. Otherwise, if exactly one of your boxes is running on the machine,
   it wins.
4. Otherwise a picker opens -- in the terminal, an interactive list
   (even when stdin is busy carrying a piped payload: the picker rides
   the controlling terminal, the way ssh's own prompts do); in a
   graphical launch, a system dialog.

Only boxes started by your user are considered; `--skip-uid-check`
includes everyone's (the error tells you when sessions were hidden).

**Exit codes are script-trustworthy:** 0 means bytes landed, and every
nothing-was-delivered outcome is nonzero -- cancelling the picker or the
paste prompt, an empty paste, an ambiguous box set with no terminal. The
byre-wide table is published at
[/docs/commands/#exit-codes](https://getbyre.com/docs/commands/#exit-codes),
generated from the same constants.

## What works where

| you are | picker | clipboard out | `byre deliver` (no args) |
|---|---|---|---|
| terminal on your machine | interactive list | yes | paste beat, full clipboard |
| Dock / Finder (no terminal) | system dialog | yes | reads clipboard, OS notification |
| SSH'd into the machine | interactive list | best-effort (OSC 52) + always printed | paste beat, text only |
| pipe at your terminal (`cmd \| byre deliver`) | interactive list (via the terminal) | yes | stdin is the payload |
| script / detached (no terminal, no GUI) | none -- `--box` or error | printed only | stdin only |
| delivering *to* an `ssh://` remote | interactive list (local) | yes (local) | paste beat, full clipboard |

Images over SSH can't ride a terminal paste; deliver them from the
laptop side instead -- that's what remote delivery is for (`byre deliver
ssh://host`, below).

**Platform note.** The terminal flows are fully supported on macOS and
Linux alike. macOS is the tested platform for the *graphical* extras
(the deliver app, the Finder Quick Action, notification popups). On
Linux the graphical layer -- the `.desktop` launcher, the
`zenity`/`kdialog` picker, `notify-send` feedback -- is **experimental
and unverified across desktop environments**: whether you can drop files
onto a launcher, and whether a dropped-on launch reaches `/inbox`,
depends on your desktop environment and file manager, and byre's
maintainers haven't tested that spread. In a terminal, `byre deliver
<file>` and the clipboard flows are the supported Linux path. Reports
welcome.

## The inbox

Delivered files live at `/inbox` inside the box, owned by the dev user;
the agent is told files from you appear there. The inbox dies with the
box -- it's a hand-off point, not storage; if the box is rebuilt,
deliver again (it's one command). Deliveries never land in `/workspace`,
so your repo stays clean.

## Getting files back out: `byre grab`

The mirror move. The agent built you a PDF, rendered a chart, produced
a patch -- `byre grab` lands it next to you:

```
$ byre grab out/report.pdf
report.pdf
```

`byre grab <box-path> [<host-path>]` copies a file (or a directory,
recursively) out of the running box. The box path counts from
`/workspace` unless absolute (`/inbox/...`, `/home/dev/...` work too);
the host path defaults to your current directory, and an existing
directory receives the file under its box name. The landed host path
prints to stdout. `-` streams a single file's content to stdout
instead -- `byre grab build.log - | grep error`.

The box is found exactly as deliver finds one (see "Which box?"), and
the same script-trustworthy exit codes apply: 0 means bytes landed.

**Grab never overwrites host files.** Everything a grab writes was
authored inside the box -- by the agent -- so byre won't let it replace
anything of yours, even when you name an existing file exactly:
collisions land uniquified (`report.pdf` → `report-2.pdf`), byre tells
you when that happened, and the printed path is always where the bytes
actually went. Delete the old file first if you want the old name.
Directory grabs skip symlinks and other non-files (with a note), and a
partially unreadable tree grabs what it can -- the exit code and a
summary line carry the truth.

## The deliver app

You can't drag files onto a bare binary in the Dock -- so byre generates
you an app:

```
$ byre deliver --install-app
/Users/you/Applications/Byre Deliver.app
/Users/you/Library/Services/Deliver to Byre.workflow
```

Drag **Byre Deliver** to your Dock and drop files on it -- outcomes
arrive as a small popup ("shot.png → /inbox -- path copied to the
clipboard"). Click it with nothing and it opens a terminal running the
interactive clipboard flow -- the paste beat, so you see what's on your
clipboard before it ships. The right-click **Deliver to Byre** Quick
Action delivers from Finder's context menu.

The app is a generated, readable artifact: its AppleScript source ships
inside the bundle (at `Contents/Resources/droplet.applescript`, the
applet stub's own filename), and it is assembled on your machine by
macOS's own `osacompile` and ad-hoc signed there -- nothing prebuilt
crosses a machine boundary, so no signing certificate or notarization is
involved. Re-running `--install-app` regenerates it; do that if you move
the byre binary. `--box <id>` bakes a fixed target box in. First use
triggers macOS's one-time permission prompts. To uninstall, delete the
printed paths.

Remote and named installs share the same command:

```
$ byre deliver --install-app [ssh://[user@]host[:port]] [--box id] [--name label] [--remote-byre path]
```

An `ssh://` target installs a remote-delivery drag target (distinct
icon; drops ship to that machine). `--name` sets the label so several
installs can coexist as `Byre Deliver (<label>)` (the Quick Action
likewise: "Deliver to Byre (<label>)"); without it a remote install is
named for its target and a local install stays the singleton. `--box`
on a remote install names the box on the *remote* machine.

On **Linux**, `--install-app` writes a `.desktop` launcher instead --
experimental; see the platform note under "What works where".

## Remote delivery

For a box on another machine, put an `ssh://` target in front of the
sources:

```
byre deliver ssh://dev@studio shot.png notes.md
pngpaste - | byre deliver ssh://studio - --name shot.png
byre deliver ssh://studio        # the paste beat, then ship
```

Everything else works exactly as above -- paths, directories, stdin,
the clipboard beat -- and the landed in-box paths print locally and
land on *your* clipboard. byre asks the remote byre which boxes are
running; when yours is the only one it just delivers, when several run
you pick from the usual list (`--box <id-or-prefix>` picks up front and
saves a connection). The files ride a single ssh exec as one stream.
Authentication is your own ssh -- keys, agents, `~/.ssh/config` all
apply -- so expect one auth prompt per connection: two interactively
(list, then deliver), one with `--box` baked.

Wrinkles:

- byre must be installed on the remote and findable by sshd's
  **non-interactive** shell. Stock macOS omits `/usr/local/bin` from
  that PATH -- if delivery says it found no `byre` there, point
  `--remote-byre` at the binary
  (`--remote-byre /usr/local/bin/byre`).
- Both ends speak a versioned protocol; a byre too old on either side
  fails loudly at the first connection, before anything moves. Update
  the older one.
- Large deliveries draw a sending meter locally ("sending", honestly:
  bytes handed to ssh); the box's own confirmation follows when it
  lands.

## How it works

Mechanics in `internal/deliver`; decisions in ADR 0021, ADR 0037
(remote) and ADR 0040 (grab).

**Discovery.** `ps` on every installed engine, filtered on the
`byre.project` label; each hit keeps engine affinity for the later exec.
The target cascade is the one in "Which box?": `--box` (unique prefix),
cwd match walking ancestor directories against the `byre.workdir` label,
the sole owned session, the picker (Bubble Tea on the terminal, through
`/dev/tty` when stdin carries a piped payload; osascript, zenity or
kdialog on a graphical launch), else an error listing the candidates. For
the sole-session step an unreachable engine counts as zero boxes, and
any other failed query disables the step, because a partial pool can't
prove "exactly one". The `BYRE_UID` filter is an accident filter, not
confinement.

**Delivery transport.** One `exec -i` per file, as the container's own
`BYRE_UID:BYRE_GID` (the `byre shell` attach model), running a POSIX-sh
script that streams stdin to a dotfile temp under `set -C` (noclobber)
and claims the final name with `ln`. link(2) fails EEXIST atomically, so
collisions uniquify with no overwrite window, and a stream that dies
leaves no half-file under a real name. Directories claim their top-level
name with an atomic `mkdir` and stream the tree per file. `/inbox` itself
is baked by the chassis: dev-owned under root-owned `/`, so the boxed
agent can't replace it with a symlink.

**Grab transport.** The same exec in reverse, with the judgment moved
host-side: dumb box scripts classify the path, enumerate a directory
(NUL-framed `find` output), and `cat` each file out, while the host
treats everything the box says as agent input. Writes ride an `os.Root`
anchored at the destination, names are claimed with an `O_EXCL` dotfile
temp plus a hardlink (`mkdir` for directories), so nothing ever
overwrites a host file or writes through a planted symlink, and
enumeration records outside the grabbed root are ignored loudly.

**Remote transport.** The local byre asks the remote for its box list
(`byre deliver --boxes --proto N` over ssh: a frozen tab-separated line
grammar on stdout, with exit 4 marking a partial pool, which forbids
auto-picking), picks locally with the ordinary picker, then streams every
source as ONE tar archive up a single plain-ssh exec into the remote's
`byre deliver --tar -`. The archive's entries feed the ordinary per-file
transport, so claiming, uniquifying and `/inbox` confinement are
identical to a local delivery, and nothing is staged on the remote
host's disk. `--box` skips the enumeration connection. The paste beat,
stdin spooling, the sending meter and the clipboard round-trip run on the
near side; the remote's clipboard leg is suppressed with `--no-clip`.

**Host capabilities** are probed per axis and degrade independently. The
landed paths always print to stdout, one per line; the clipboard copy is
best-effort. The no-argument clipboard import waits for a paste gesture
on a TTY and classifies what arrives: text that mirrors the pasteboard is
a real paste (the pasteboard is then read out-of-band: file references,
then image, then text); existing absolute host paths are a possible drag
onto the window (named, and delivered only after confirmation); anything
else is literal pasted text. Graphical launches (no TTY, GUI present)
also report by OS notification.

**The deliver app's artifacts.** The ad-hoc codesign repairs the
signature that writing byre's script and icons into the applet stub
invalidates. An `ssh://` target and/or `--name` produce coexisting
per-target installs. Regeneration replaces only text artifacts carrying
byre's generated marker in their header (plus, for a labeled install,
its identity token -- a colliding label refuses rather than clobbers);
a same-named file byre didn't write is refused. Icons are the one
non-text artifact: on Linux they are judged by content (only byre's own
bytes are ever replaced; anything else is skipped with a note), and the
icns inside a macOS bundle rides the bundle's own marker gate.
