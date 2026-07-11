# `byre deliver` v1 — human test script (real machine)

> Working doc. The in-box suite can't touch the macOS surface, real
> terminals, or a live engine — this checklist is that coverage. Run on
> your Mac; the Linux and SSH sections are optional extras. Delete or
> absorb with the rest of the design workspace when v1 ships.

## Setup (once)

- [ ] Build + install the branch binary on the host:
      `go build -o ~/.local/bin/byre . ` (from the `copypaste` checkout)
      — `byre version` should NOT be a released tag.
- [ ] In a test project: `byre develop`. **The box must be built from this
      binary** (the image bakes `/inbox`) — if the project had an image,
      expect the changed-Dockerfile rebuild; let it run.
- [ ] In the box: `ls -ld /inbox` → owned by `dev`, mode `drwxr-xr-x`,
      parent `/` owned by root.

## 1. Basic delivery + the round-trip

- [ ] Host, from the project dir: `byre deliver README.md`
      → stdout: `/inbox/README.md`
      → stderr: `byre: delivering to <project-id> (docker, <id>)` then
        `byre: path copied to the clipboard (pbcopy)`
- [ ] Cmd-V into the agent prompt → pastes `/inbox/README.md`; ask the
      agent to read it → works, file owned by dev.
- [ ] Same command again → `/inbox/README-2.md` (uniquified, no overwrite).
- [ ] `byre deliver README.md | cat` → **stdout is only the path** (target
      line and clipboard chatter stay on stderr).
- [ ] `byre deliver README.md --no-clip` → no clipboard line, clipboard
      contents untouched.
- [ ] `echo $?` after a success → `0`.
- [ ] `byre deliver /no/such/file; echo $?` → error on stderr, exit `1`.
- [ ] `byre deliver --nonsense; echo $?` → usage error, exit `2`.

## 2. Names

- [ ] Deliver a file with spaces (a real screenshot file from ~/Desktop):
      stdout shows the bare path; **the clipboard holds the quoted form**
      (`'/inbox/Screenshot ... .png'`) — paste it into a shell `ls` to
      prove the quoting works.
- [ ] Two files at once: `byre deliver a.txt b.txt` → two stdout lines;
      clipboard holds both, one per line.
- [ ] A directory: `byre deliver ./somedir/` → ONE path (`/inbox/somedir`),
      stderr summary `delivered /inbox/somedir — N files, SIZE`; in the
      box, structure preserved.

## 3. Discovery

- [ ] From a SUBDIRECTORY of the project (`cd src && byre deliver ../README.md`)
      → still finds the box (ancestor walk).
- [ ] From an unrelated dir (e.g. `cd /tmp`), one box running →
      auto-picks it; target line names it.
- [ ] Start a second session (`byre worktree` or a second project) →
      `byre deliver <file>` from /tmp → **Bubble Tea picker** appears on
      stderr; arrows + enter deliver; run again and press `q` → exit 0,
      `byre: cancelled — nothing delivered`, empty stdout.
- [ ] `byre deliver --box <project-prefix> <file>` → picks it without the
      picker. A prefix matching both sessions → error listing candidates.
- [ ] Stop all boxes → `byre deliver x` → `no running byre boxes; start
      one with 'byre develop'`.
- [ ] With podman installed but its machine NOT started: any deliver →
      one quiet `byre: podman isn't reachable; skipping it` line (no
      multi-line engine essay), and single-box auto-pick still works.

## 4. The paste beat (no args, terminal)

- [ ] Copy some TEXT (Cmd-C in any app), then `byre deliver` →
      prompt says `your clipboard holds text — ...`; **Cmd-V** →
      `paste received (N bytes)` then `reading the clipboard…` then
      `delivered clipboard text (N bytes) → /inbox/clipboard-<ts>.txt`
      — content NOT echoed anywhere.
- [ ] Screenshot to clipboard (Cmd-Ctrl-Shift-4), `byre deliver` →
      prompt says `your clipboard holds an image — ctrl-v to deliver it
      (cmd-v won't register for images)`; **Ctrl-V** →
      `reading the clipboard…` then `/inbox/clipboard-<ts>.png`; open it
      in the box (`file` it) → PNG.
- [ ] Same screenshot, but press **Cmd-V** instead → expected: NOTHING
      happens (the terminal sends no event for an image-only clipboard —
      the prompt warned you); Ctrl-V still works from the same prompt.
- [ ] Multi-select files in Finder, Cmd-C, `byre deliver` → prompt says
      `your clipboard holds copied files — ...`.
- [ ] **Drag a file from Finder onto the terminal window** during the beat
      → `paste received…` then `delivering the dragged file` → the FILE
      lands in /inbox (its real content — not stale clipboard text).
- [ ] Copy an image FROM A BROWSER (right-click → copy image, typically
      JPEG), deliver via the beat → extension matches the actual format
      (`.jpeg`, not `.png`) — check with `file` in the box.
- [ ] Multi-select 2-3 files in Finder, Cmd-C, `byre deliver`, paste →
      all of them land as files (paths, not a text blob).
- [ ] `byre deliver` then **ctrl-c** → exit 0, cancelled, nothing delivered.
- [ ] `byre deliver` then press Enter a few times → nothing fires (Enter
      is not a gesture); ctrl-c to leave.
- [ ] After any beat: terminal is NOT left in raw mode (typing echoes,
      Enter works). Also try `byre deliver 2>/dev/null` + Cmd-V — the
      paste must still register (arm sequence goes to the TTY, not stderr).

## 5. stdin

- [ ] `echo hello | byre deliver` → `/inbox/stdin-<ts>` (piped no-arg
      streams).
- [ ] `pbpaste | byre deliver - --name note.txt` → `/inbox/note.txt`.
- [ ] `byre deliver - --name '../escape.txt' < README.md` → lands at
      `/inbox/escape.txt` with a loud `renamed` note (never outside /inbox).

## 6. SSH / headless (optional — needs a remote with a running box)

- [ ] SSH in, `byre deliver <file>` → path prints; clipboard line says
      OSC 52 best-effort (or unavailable); if your terminal supports
      OSC 52, the LOCAL clipboard now holds the path.
- [ ] SSH in, `byre deliver` (no args, no clipboard tool remote-side) →
      degraded prompt (`no clipboard access here — paste text...`);
      paste text, Ctrl-D → delivered as text. **Images over SSH are
      expected to NOT work** — that's the documented degradation.

## 7. The deliver app (macOS)

- [ ] `byre deliver --install-app` → prints the .app and .workflow paths;
      no errors; `Byre Deliver.app` appears in ~/Applications with the
      byre icon (Finder may need a moment / a relaunch to show it).
- [ ] Open the bundle's `Contents/Resources/droplet.applescript` → the
      readable generated source, with your byre path baked in.
- [ ] Drag the app to the Dock; drop a file on it → notification
      ("… → /inbox… — path copied to the clipboard"); Cmd-V into the
      agent works. FIRST use: expect macOS permission prompts once.
- [ ] Click the app with nothing selected (or open it) → a Terminal
      window opens running the paste beat (sampled prompt: "your
      clipboard holds …"); deliver via ctrl-v → window closes itself on
      success (default Terminal profile). First click may prompt for
      Automation permission (controlling Terminal) once.
- [ ] Right-click a file in Finder → Quick Actions → "Deliver to Byre" →
      same flow. (May need enabling once under System Settings →
      Extensions → Finder if macOS doesn't show it immediately.)
- [ ] With two boxes running, drop a file on the app → the graphical
      picker dialog appears (osascript choose-from-list).
- [ ] Re-run `--install-app` → regenerates in place, no complaints.
- [ ] Gatekeeper check: the app launches without any "unidentified
      developer" dialog (it's locally generated, never quarantined) —
      if macOS complains here, note the exact wording.

## 8. Linux GUI (optional)

- [ ] With two boxes and no TTY (e.g. a `.desktop` launcher or
      `setsid byre deliver <file> < /dev/null`) on X11/Wayland →
      zenity/kdialog picker dialog; Cancel → exit 0.

## Wrap-up

- [ ] `byre status` in the project still renders normally (no deliver
      side effects).
- [ ] Box restart (`byre develop` again after exit): `/inbox` is empty —
      ephemerality is the documented behavior, not a bug.

Anything that fails: note the step number + verbatim output. The likely
suspects by design: TCC prompts on first osascript use (approve once),
and terminals that don't do OSC 52 (fine — the path still prints).
