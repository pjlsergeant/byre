# Getting files into the box: `byre deliver`

Your agent is boxed. That's the point -- but sometimes *you* are holding
the thing it needs: a screenshot of the bug, a PDF spec, a CSV someone
mailed you. `byre deliver` gets it in there in one move, and hands you
back the in-box path, already on your clipboard, ready to paste into the
agent prompt.

```
$ byre deliver report.pdf
/inbox/report.pdf
byre: path copied to clipboard
```

Cmd-V into the agent and keep talking.

## The flows

**Drop a file (or several, or a directory):**

```
byre deliver shot.png notes.md
byre deliver ./bugreport/
```

Files land in the box's `/inbox`, keeping their names (`shot.png` →
`/inbox/shot.png`; collisions uniquify to `shot-2.png`, never
overwrite). A directory arrives with its structure preserved and yields
one path (`/inbox/bugreport/`). Every delivered path prints to stdout,
one per line, and the same list lands on your clipboard.

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
Finder onto the window**: the dragged file itself is delivered. The
confirmation tells you what shipped -- kind and size, never the
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

**Exit codes are script-trustworthy:** 0 means bytes landed. Every
nothing-was-delivered outcome is nonzero — cancelling the picker or the
paste prompt, an empty paste, an ambiguous box set with no terminal —
exit 1, alongside ordinary errors (2 stays usage; `--boxes` uses 4 for a
partial pool, see Remote delivery).

## What works where

| you are | picker | clipboard out | `byre deliver` (no args) |
|---|---|---|---|
| terminal on your machine | interactive list | yes | paste beat, full clipboard |
| Dock / Finder (no terminal) | system dialog | yes | reads clipboard, OS notification |
| SSH'd into the machine | interactive list | best-effort (OSC 52) + always printed | paste beat, text only |
| pipe at your terminal (`cmd \| byre deliver`) | interactive list (via the terminal) | yes | stdin is the payload |
| script / detached (no terminal, no GUI) | none -- `--box` or error | printed only | stdin only |
| delivering *to* an `ssh://` remote | interactive list (local) | yes (local) | paste beat, full clipboard |

Whenever a nicety is unavailable, byre says so and the path still
prints -- stdout is the contract, the clipboard is garnish. Images over
SSH genuinely can't ride a terminal paste; deliver them from the laptop
side instead -- that's what remote delivery is for
(`byre deliver ssh://host`, below).

**Platform note.** The terminal flows above are fully supported on macOS
and Linux alike. macOS is the tested platform for the *graphical* extras
(the deliver app, the Finder Quick Action, notification popups). On
Linux the graphical layer -- the `.desktop` launcher, the
`zenity`/`kdialog` picker, `notify-send` -- is **experimental and
unverified across desktop environments** (see "The deliver app"); the
terminal path is the one to rely on.

## The inbox

Delivered files live at `/inbox` inside the box, owned by the dev user;
the agent is told files from you appear there. The inbox dies with the
box -- it's a hand-off point, not storage; if the box is rebuilt,
deliver again (it's one command). Deliveries never land in `/workspace`,
so your repo stays clean. Boxes built before this feature don't have an
`/inbox`; rebuild with `byre develop`.

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

The app is a *generated, readable artifact* -- its AppleScript source
ships inside the bundle (`Contents/Resources/droplet.applescript`), it's
assembled on your machine by macOS's own `osacompile` (nothing prebuilt,
so no signing or notarization is ever involved), and re-running
`--install-app` regenerates it -- do that if you move the byre binary.
`--box <id>` bakes a fixed target box in. First use triggers macOS's
one-time permission prompts. To uninstall, delete the printed paths.

On **Linux**, `--install-app` writes a `.desktop` launcher instead. This
is **experimental and unverified**: whether you can drop files onto a
launcher (and whether a dropped-on launch reaches `/inbox`) depends
heavily on your desktop environment and file manager -- some support it,
some don't, and byre's maintainers haven't been able to test the
graphical path across that spread. The `byre deliver <file>` and
clipboard flows in a **terminal** are the supported Linux path and work
the same as everywhere; the graphical extras (the launcher, the
`zenity`/`kdialog` picker, `notify-send` feedback) are best-effort until
a Linux user confirms them. Reports welcome.

## Remote delivery

Your box runs on another machine? Put the machine in front of the
sources as an `ssh://` target:

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
