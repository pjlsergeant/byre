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
byre: 🖼  image on the clipboard — press ctrl-v to deliver it (cmd-v won't work for images) · ctrl-c cancels
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

`deliver` is byre's one machine-scoped command: it finds a running box
rather than requiring you to stand in the project directory.

1. `--box <id-or-project-prefix>` picks explicitly (the prefix must
   match exactly one session).
2. Otherwise, if your cwd is inside a running box's workdir (any
   subdirectory), that box wins.
3. Otherwise, if exactly one of your boxes is running on the machine,
   it wins.
4. Otherwise a picker opens -- in the terminal, an interactive list; in
   a graphical launch, a system dialog.

Only boxes started by your user are considered; `--skip-uid-check`
includes everyone's (the error tells you when sessions were hidden).

## What works where

| you are | picker | clipboard out | `byre deliver` (no args) |
|---|---|---|---|
| terminal on your machine | interactive list | yes | paste beat, full clipboard |
| Dock / Finder (no terminal) | system dialog | yes | reads clipboard, OS notification |
| SSH'd into the machine | interactive list | best-effort (OSC 52) + always printed | paste beat, text only |
| script / pipe (no TTY, no GUI) | none -- `--box` or error | printed only | stdin only |

Whenever a nicety is unavailable, byre says so and the path still
prints -- stdout is the contract, the clipboard is garnish. Images over
SSH genuinely can't ride a terminal paste; deliver them from the laptop
side with `pngpaste - | ssh host byre deliver - --name shot.png`.

## The inbox

Delivered files live at `/inbox` inside the box, owned by the dev user;
the agent is told files from you appear there. The inbox dies with the
box -- it's a hand-off point, not storage; if the box is rebuilt,
deliver again (it's one command). Deliveries never land in `/workspace`,
so your repo stays clean. Boxes built before this feature don't have an
`/inbox`; rebuild with `byre develop`.

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
Action delivers from Finder's context menu. On Linux you get a
`.desktop` launcher instead.

The app is a *generated, readable artifact* -- its AppleScript source
ships inside the bundle (`Contents/Resources/droplet.applescript`), it's
assembled on your machine by macOS's own `osacompile` (nothing prebuilt,
so no signing or notarization is ever involved), and re-running
`--install-app` regenerates it -- do that if you move the byre binary.
`--box <id>` bakes a fixed target box in. First use triggers macOS's
one-time permission prompts. To uninstall, delete the printed paths.

## Not here yet

- **Remote delivery** (`byre deliver ssh://host file`) -- routing a
  delivery through another machine running byre. Designed (ADR 0021),
  lands as a follow-on tranche.
