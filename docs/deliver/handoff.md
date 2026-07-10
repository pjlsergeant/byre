# Handoff: `byre deliver` — design brief

> Working doc. Original design-consensus handoff, brought in 2026-07-10 from
> brainstorming sessions with three agents (disagreements resolved by Pete,
> noted inline). Nothing implemented yet. Companion docs in this directory:
> `thoughts-fable.md` (in-box review), `grilling-input-grok.md` /
> `grilling-input-codex.md` (external reviewer passes), and
> **`decisions.md` (the grilled design of record — where it disagrees with
> this brief, it wins)**. This directory is a design workspace
> (shared-auth-design.md lifecycle): absorbed into ADRs + shipped docs,
> then deleted.

## What this is

A design-consensus document for a new byre command, `byre deliver`, reached across brainstorming sessions with three agents (all converged; disagreements were resolved by Pete, noted below). Nothing is implemented yet. Your job is to take this from spec to implementation on the byre repo, following the repo's CLAUDE.md workflow (commit frequently, `byre-codereview` after the feature, gofmt/vet/test green before every commit).

## The feature in one paragraph

`byre deliver` gets a file from the host into a running byre box. Sources: path arguments, the host clipboard, or stdin. It streams the file into the selected container's `~/inbox/` via `docker exec` (no mount, no host-side inbox directory), then puts the in-container path on the **host** clipboard so the user can Cmd-V it straight into the agent prompt. It resolves *which* box by label-based session discovery with an interactive picker when ambiguous. A `ssh://host` target routes the whole flow through a remote machine running byre. A generated macOS `.app` droplet / Linux `.desktop` shim makes it a Dock/Finder drag target.

## Command surface (converged spec)

```
byre deliver <path...>                 deliver files to a box on this machine
byre deliver                           no args + TTY: import host clipboard now
byre deliver -                         raw stdin → single file
byre deliver ssh://[user@]host [args]  remote delivery (see below)
byre deliver --install-app             materialize the Dock droplet / .desktop shim
```

Input-source precedence: path args → files; no args + piped stdin → stream; no args + interactive TTY → clipboard. **There is no "wait for a paste" mode locally** — that was explicitly considered and killed, because a terminal paste can't carry binary (screenshots), while reading the clipboard directly covers images, text, AND file references (Cmd-C on files in Finder puts file URLs on the pasteboard — resolve those and treat as path mode). The interactive "paste now, Ctrl-D to finish, text only" mode exists ONLY as the no-arg fallback when there's no local clipboard tool (i.e., SSH'd into a headless box), because terminal paste does traverse SSH.

Clipboard import priority: file references → image data (write as `clipboard-<timestamp>.png`) → plain text (`clipboard-<timestamp>.txt`). Rich text/HTML tier: deferred, not v1.

## Target resolution (decided, precise order)

1. If cwd is inside a running byre workdir → that box. Match on the `byre.workdir` label first, then `byre.project` (worktrees: one project can have several sessions, one per workdir; ADR 0004 is the labels decision).
2. Else if exactly one session is running on the machine → that one.
3. Else → picker (see platform adapter below).
4. Zero sessions → clean "no running boxes" error.

Note: `deliver` is byre's first **machine-scoped** verb (every other command is cwd/project-scoped). This is deliberate — it's what makes the Dock use-case work. Keep discovery purely label-driven via the engine CLI (`docker ps --filter label=byre.project`); do NOT require `~/.byre/projects/<id>` to exist for the discovery path.

## Transport (decided by Pete — this was the one real fork)

**Direct exec-stream into the running container. No host-side inbox directory, no mount.**

```
docker exec -i -u dev <ctr> sh -c 'mkdir -p ~/inbox && cat > ~/inbox/<name>'
```

Rationale: correct ownership for free (exec as `dev`, sidestepping `docker cp`'s root-ownership problem); zero config surface (works on every box, no rebuild, nothing new for `byre status` to explain); can't hit the wrong concurrent box; the inbox dies with the throwaway container so it never accretes cruft — ephemerality is a feature, "survives restarts" is explicitly a non-goal (re-deliver, it's one command). A persistent host-side inbox mount was agent B's proposal and was rejected for v1 (could return later as an opt-in skill for drag-into-a-Finder-folder people).

**Never deliver into `/workspace`** by default — it pollutes the repo the agent gits in. An explicit `--to <path>` flag may allow it later.

Landing path: `~/inbox` (leaning; needs no chassis/build-time support — `mkdir -p` as dev works on any running box). `/inbox` is a nicer address but wants chassis changes. Open question #1 below.

## Naming rules (decided)

- Preserve the original basename and extension (the agent infers file type from the extension; the name carries meaning into the conversation). NOT opaque temp names.
- Uniquify on collision (`shot-2.png`), never silently overwrite.
- Clipboard/stdin captures: `clipboard-<timestamp>.png/.txt`, `stdin-<timestamp>`.
- Write atomically: stream to a temp name in the container, rename into place.
- Multiple files: accept variadic args (a multi-file Dock drag arrives as multiple argv entries); put all resulting paths on the clipboard, shell-quoted, space- or newline-separated.
- Directories: recurse preserving structure under `~/inbox/<dirname>/` (leaning; alternative was dated-subfolder flattening — minor, just pick one).

## Clipboard round-trip (the killer detail — do not drop this)

After delivery, put the shell-quoted in-container path(s) on the **host** clipboard. Flow: drop `report.pdf` → lands at `~/inbox/report.pdf` in the box → that string is on the laptop clipboard → Cmd-V into the agent. **Always print the path too — stdout is the contract, clipboard is best-effort garnish.** When stdout is not a TTY, print only the path (machine-readable, composable).

## Capability detection & degradation (decided; Hetzner-headless case drove this)

Probe three things independently at startup and degrade each feature on its own axis. Never treat "terminal" vs "Dock" as monolithic modes.

1. **TTY?** (`mattn/go-isatty`, already a dep) — decides picker style and feedback channel.
2. **GUI session?** macOS: yes unless `SSH_CONNECTION` set; Linux: `DISPLAY`/`WAYLAND_DISPLAY`. Gates graphical dialogs and notifications — never attempt them without it.
3. **Clipboard tools, each direction separately** — read: `pbpaste`/`pngpaste`-class (macOS uses `osascript`/NSPasteboard for images and file refs), `wl-paste`/`xclip` (Linux); write: `pbcopy`/`wl-copy`/`xclip`. Shell out — that's byre house style (ADR 0002 spirit: CLI, never SDK).

Picker platform adapter (decided in conversation with Pete): interactive TTY → Bubble Tea picker (bubbletea/bubbles/lipgloss are **already in go.mod** — `internal/configui` uses them, so reuse patterns from there); macOS graphical launch → `osascript` dialog; Linux graphical → `zenity`, then `kdialog`; none available → clean ambiguity error listing the sessions. byre stays one Go binary, no GUI toolkit dep. Picker entries: project name, worktree/branch, agent, path, uptime.

Degradation matrix:

| context | picker | clipboard out | clipboard in (no-arg) | feedback |
|---|---|---|---|---|
| laptop terminal | Bubble Tea | pbcopy/wl-copy | read clipboard | print |
| Dock/graphical (no TTY) | osascript/zenity | system clipboard | read clipboard | OS notification |
| SSH'd into headless box | Bubble Tea | OSC 52 best-effort + always print | interactive paste, TEXT ONLY | print |
| no TTY, no GUI (script) | none — ambiguity error | print only | stdin only | stdout, path only |

OSC 52: emit the escape sequence to set the *user's terminal's* clipboard through SSH. Write-only (reads are disabled by terminals for security). Fire-and-forget, hence "always print". Say what was skipped ("clipboard unavailable — path printed above") — degrade-claims-never-refuse, applied to a command.

## Remote delivery: `byre deliver ssh://host` (decided by Pete, shape settled)

Pete explicitly chose this over a `DOCKER_HOST`/docker-context inversion: explicit and self-describing (the `ssh://` names the boundary crossing), engine-agnostic (podman too), no remote-engine protocol invented. Layering: **local byre handles local capabilities (clipboard read, GUI, staging); remote byre handles what it already owns (discovery, picker, exec-stream).**

Flow: resolve payload locally (args/clipboard/stdin) → stage to local temp → `scp` to a remote `mktemp -d` → `ssh -t host byre deliver --porcelain --consume <tmpfile>` → remote runs normal discovery/picker (interactive works because `-t` keeps the channel a real TTY) → remote prints the in-box path as a final machine-readable line (`--porcelain`) → local byre parses it, sets the LOCAL clipboard with real pbcopy (more reliable than OSC 52), cleans up.

Key points:
- **Do NOT pipe the payload over ssh stdin** — tempting (one connection, no temp file) but the pipe occupies stdin so the remote picker can't run, and `ssh -tt` around binary data mangles bytes via pty line discipline. The scp-then-continue shape is correct precisely because it keeps the SSH channel free for interaction.
- `--consume`: remote moves-then-deletes so killed connections don't strand temp files; local also attempts `ssh host rm -rf` cleanup.
- **PATH gotcha** (will be the #1 support issue): `ssh host byre` is a non-login shell; byre in `~/.local/bin` won't be found. Probe with fallbacks and emit a good error ("byre not found on hetzner; is it on PATH for non-interactive shells?"), not a bare 127.
- Honor `~/.ssh/config` aliases naturally (shell out to `ssh`/`scp`).
- Keep the ssh-facing surface tiny and version-stable: positional path, `--porcelain`, `--consume` — a frozen mini-protocol against local/remote version skew.
- Optional, not v1: path component `ssh://host/srv/project` meaning "cd there first" to re-enable cwd-match remotely.

## Dock / Finder shim (decided shape)

You cannot drag files onto a bare binary in the macOS Dock — targets must be `.app` bundles declaring `CFBundleDocumentTypes`. So: `byre deliver --install-app` **materializes** a tiny generated wrapper into `~/Applications` (AppleScript/Automator droplet or minimal shell-stub bundle) whose only jobs are: receive Finder drops → invoke `byre deliver "$@"`; open with no files → `byre deliver` (clipboard mode); show the graphical picker via the adapter; report via macOS notification ("shot.png → ~/inbox — path copied to clipboard"). Generated-artifact-you-can-read is very byre (like `Dockerfile.generated`). A Finder Quick Action (right-click "Deliver to byre") from the same generator arguably beats the Dock ergonomically — ship both. Linux: a `.desktop` file (`Exec=byre deliver %F`, `Terminal=false`). Droplets can be generated with a default `ssh://` target baked in.

## Security posture (assessed, low concern)

`deliver` is human-initiated host→box; the agent cannot invoke it, so the social-engineering surface of automatic path interception (a rejected earlier design) doesn't apply. One residue: clipboard mode ships whatever's on the clipboard, including a password copied two minutes ago — mitigate by printing/notifying *what* was delivered (kind, size, first line for text), maybe a max-size guard. Don't gate harder; byre's threat model is the agent, not the user (PRINCIPLES #1).

## Open questions — settle these (an ADR is the right place)

1. `~/inbox` vs `/inbox` as the landing path (leaning `~/inbox`, zero chassis changes).
2. Agent context line ("files the user delivers appear in `~/inbox`") — chassis constant vs skill? Leaning chassis: tiny and opinion-free.
3. Directory delivery: preserve structure (leaning) vs dated flatten.
4. GLOSSARY.md entries — **required, the glossary is binding for user-facing strings**: "deliver" won the verb (over drop/ingest/airlock); the *place* needs a name ("inbox" vs "airlock" — leaning inbox as the literal term; airlock is evocative but the mechanism is one-way/human-initiated/ephemeral).
5. ADR(s) needed for: machine-scoped verb + exec-stream-not-mount transport; the platform-adapter chooser; `ssh://` remote shape. These are exactly the decisions a future doc would otherwise contradict.

## Deferred by consensus (not v1)

Rich-text clipboard tier · delivery manifest (original path/MIME/time) · Markdown-snippet clipboard variant (agent-specific) · `--url` fetch-into-inbox · size warnings/progress bars · persistent host-inbox mount skill · kitty/iTerm2 terminal-transfer protocols (the eventual answer for *images over SSH*, which the v1 design honestly can't do — degrade with a helpful error suggesting `pngpaste - | ssh host byre deliver -`).

## Repo pointers

- Session discovery by labels: ADR 0004; runner abstraction (engine CLI shell-outs) in `internal/runner` — deliver should only need `ps`-by-label and `exec -i`.
- TUI patterns: `internal/configui` (bubbletea already a dep).
- Commands live in `internal/commands`; CLI is hand-rolled per-command arg loops, no `flag` package — follow that style.
- Ownership math: exec as `dev` works because the host UID/GID is baked (ADR 0008).
- Docs to touch: `docs/GLOSSARY.md` (binding vocabulary), `docs/ARCHITECTURE.md` (Commands section + probably a short Deliver section), new `docs/adr/`, `README.md` user docs, `TODO.md`.

## Suggested build order

1. ADR + glossary entries (settles open questions 1–4 cheaply).
2. Core: discovery cascade + exec-stream transport + naming rules + path-on-stdout. Unit-test via the injected runner fakes (house style).
3. Clipboard out (pbcopy/wl-copy/OSC 52) + capability probes.
4. Clipboard in (macOS file-refs/image/text; Linux equivalents) + stdin mode + headless paste fallback.
5. Bubble Tea picker; then the graphical adapter (osascript/zenity/kdialog).
6. `ssh://` remote flow (`--porcelain`, `--consume`, PATH probing).
7. `--install-app` shim generation.

Each step is independently shippable and committable; `byre-codereview` after each coherent chunk per CLAUDE.md.
