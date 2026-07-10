# `byre deliver` — grilling decisions (design of record)

> Working doc, the OUTPUT of the 2026-07-10 grilling session over
> `handoff.md` + `thoughts-fable.md` + the two grilling-input docs. Where
> this file disagrees with any of those, this file wins — it is the design
> of record until absorbed into the ADR + shipped docs. All rulings Pete's.

## Target discovery & attach

1. **Engine pool: union across installed engines.** Extend `shell.go`'s
   probe (docker, then podman) to collect from every installed engine
   instead of stopping at first match. Each session entry carries engine
   affinity (the later exec goes through the engine that holds it).
   "Exactly one session" means one across the union. A failed engine query
   degrades loudly ("podman query failed; showing docker sessions only"),
   never masked as nothing-running. No config knob; `engine =` stays a
   per-project build/launch concern.
2. **Discovery filters to boxes you own.** Only sessions whose container
   `BYRE_UID` matches the caller's uid are visible/deliverable.
   `--skip-uid-check` both reveals and permits foreign boxes — one flag,
   one uniform rule on every path (auto-pick, picker, `--box`, remote,
   which runs the same check remote-side). When the filter hides sessions
   and zero remain, the error says so: "no running boxes owned by you
   (2 sessions hidden; --skip-uid-check to include them)".
3. **Attach = `shell.go` verbatim.** Exec as the container's
   `BYRE_UID:BYRE_GID` (read from container env), fail-closed if
   unreadable, `HOME=/home/dev`. Not `-u dev`, not `os.Getuid()` — the
   brief's sketch and thoughts-fable's claim are both superseded.
4. **`--box <id-or-project-prefix>` is cascade step 0** — explicit target
   selector, useful for scripts/tests, travels over the ssh protocol,
   and is the answer to Dock×remote×multi-session (see 10).

## Destination

5. **The inbox lives at `/inbox`** — root-parented, dev-owned, baked by
   the chassis into new images. Root-parenting structurally kills the
   agent-plants-a-symlink attack (replacing `/inbox` needs write on
   root-owned `/`); planted symlinks *inside* it are neutralized by
   never-overwrite naming. `/home/dev/*` stays user/mount namespace;
   byre mechanisms live at root beside `/workspace`. One spelling —
   absolute `/inbox/...` — on every surface including prose.
6. **Pre-feature boxes get a printed root-exec backfill.** If `/inbox` is
   missing at deliver time: idempotent `exec -u 0 mkdir + chown`, with a
   line saying so ("created /inbox; a rebuild bakes it in"). No hard
   "rebuild first" error. (No installed base today, so this is cheap
   insurance, not a migration story.)
7. **Fail-closed integrity check, no escape flag.** Before streaming, the
   exec'd script asserts `/inbox` is a real directory (not symlink, file,
   or FIFO) and refuses otherwise. A future `--to <path>` is the pressure
   valve for custom destinations; not v1.

## Transport & naming

8. **Write protocol: tmp + `link()`.** One server-side sh script per file:
   stream stdin → `/inbox/.tmp-<pid>-<rand>` (dotfile, invisible to
   naming); claim the final name by `ln tmp candidate` looping
   `report.pdf`, `report-2.pdf`, ... (link(2) fails EEXIST atomically —
   no clobber, no empty-claim window, concurrency-safe); rm tmp; print
   the won name. `trap` cleans the tmp on any failure — a died stream
   leaves at worst an orphaned dotfile, never a half-file under a real
   name. Uniquify lives in the same script (it can see the directory);
   no host-side guess-then-race. Filenames pass as argv (`sh -c '... "$1"'
   _ "$name"`), never spliced into script text.
9. **Multi-file: independent, no rollback.** Successes stay, per-file
   errors print, exit nonzero. Sources of multi-file: multi-select Dock
   drag (argv), multi-select Finder Cmd-C (file refs), variadic args.
   A directory delivery yields ONE path (the top dir).
10. **Source policy:** regular files + directories. File symlinks are
    followed to content; directory symlinks are skipped with a note
    (also kills cycles); FIFOs/sockets/devices skipped with a note,
    never opened. Unreadable file = per-file error, continue. Directories
    preserve structure under `/inbox/<dirname>/`, uniquify applied to the
    top-level dirname only. No content filtering, no size cap — the
    summary line ("delivered proj/ — 214 files, 38 MB") is the
    legibility. Per-file streams in v1; tar-transport is a deferred
    optimization if directories feel slow.
11. **Type honesty:** a clipboard image's extension follows the format
    actually read from the pasteboard (prefer the PNG representation when
    offered; never transcode, never mislabel). Stdin captures:
    `stdin-<ts>` extensionless, or `--name <basename>`
    (`pngpaste - | ssh host byre deliver - --name shot.png`). No
    magic-byte sniffing.

## I/O contract

12. **stdout carries exactly the delivered in-box paths, one per line,
    unquoted** — same whether TTY or pipe. Everything else (picker
    chrome, notes, degrade claims, summaries) goes to stderr.
13. **Clipboard payload = stdout's lines with lazy quoting.** One path
    per line; names that need it get single-quotes (helps both shells
    and LLM prompts see boundaries), tame names stay bare. Single file =
    the degenerate one-line case, no trailing newline. Designed for its
    one destination: pasting into an agent prompt.
14. **Basenames are line-safe by naming rule, not framing:** control
    chars/newlines in a source basename are sanitized at delivery with a
    printed note. Keeps stdout and porcelain line-parseable.
15. **Exit codes: 0 all delivered / 1 anything failed (partial included)
    / 2 usage** — verify against `cmd/byre/main.go` convention at build
    time rather than inventing a parallel one.
16. **Clipboard-out is always on; `--no-clip` opts out.** No confirm, no
    save/restore cleverness. Feedback always says "path copied to
    clipboard" so the replacement is never silent.

## Clipboard import (no-arg mode)

17. **No-arg + TTY = the paste beat.** Prompt: "paste to deliver the
    clipboard (ctrl-c to cancel)". Trigger is the paste gesture only —
    a Ctrl-V keypress, or a bracketed-paste event (Cmd-V), both of which
    cause ONE system-pasteboard read (Claude Code's own model: the
    gesture is caught, the pasteboard is read out-of-band; image bytes
    never traverse the tty). Priority: file refs → image → text. No
    Enter trigger — Enter isn't semantically paste.
18. **Confirmation states kind + size + destination, never content** —
    "delivered clipboard text (142 bytes) → /inbox/clipboard-….txt". No
    preview, even truncated (printing the first line of a just-delivered
    password re-discloses it). The paste beat itself is the primary
    wrong-thing protection.
19. **Degradations:** SSH'd into a headless box (no pasteboard tool) —
    same prompt, but the bracketed paste's streamed text IS the content,
    text-only, Ctrl-D to finish, honestly labeled. Dock launch (no TTY)
    — read immediately, OS notification as after-the-fact legibility.

## Remote (`ssh://`) protocol

20. **No remote pre-pick protocol.** Non-TTY remote delivery with
    multiple owned sessions = hard error via notification ("3 sessions
    on hetzner — deliver from a terminal, or bake --box into the deliver
    app"). Droplet-era generation can bake `--box` beside the `ssh://`
    target.
21. **`--consume` is confined structurally:** refuses any path not
    matching the staging pattern byre itself creates
    (`/tmp/byre-deliver-*/...` via remote `mktemp -d`). One regex, one
    test; the flag can only eat what the protocol staged.
22. **Probe before payload:** first ssh round-trip runs
    `byre deliver --proto`, which prints exactly `deliver-proto 1`.
    Anything else (127/PATH gotcha, old binary, help text) = clean named
    error before any scp.
23. **`--porcelain` = sentinel-line grammar**, because `ssh -t` merges
    stdout+stderr into one pty stream: result lines are emitted as
    `::deliver <path>`, the local side parses only sentinel lines and
    strips `\r`, all other stream content passes through as human
    chrome. `--porcelain` and `--consume` are marked internal in help.

## Everything else

24. **Container states: consciously no machinery.** No pre-filtering by
    paused/restarting (byre never pauses; it's externally inflicted and
    rare). The exec is the authoritative check; wrap its failure legibly
    ("box is paused — docker unpause it") instead of raw engine stderr.
    Launch-gate boxes are valid targets (filesystem is up). Mid-stream
    death is already clean via the tmp-dotfile protocol.
25. **Agent context line: chassis, one factual sentence** ("the user can
    drop files into /inbox from the host; they appear owned by you").
    It describes a byre mechanism, not an opinion — same class as
    telling the agent where /workspace is. No skew concern (no installed
    base).
26. **Names (GLOSSARY entries required):** *deliver* — the verb (losers:
    drop, ingest, airlock). *Inbox* — the place, `/inbox` (loser:
    airlock). *Deliver app* — generic term for the generated artifact;
    display name **"Byre Deliver"** (app bundle + `.desktop` Name=),
    Quick Action **"Deliver to Byre"**. Rejected: droplet (DigitalOcean
    overload), materialize (glossary-pinned to skill copies), shim
    (jargon), "Byre Paste" (names one non-flagship input mode,
    directionally ambiguous), "Byre Inbox" (second name for the front
    door; naming the app after the command keeps the shim→CLI
    relationship legible).
27. **Picker rows show honestly-available metadata only** (project id,
    engine, uptime — what labels + `ps` provide); no new labels in v1,
    no inspect-per-row wishlist chasing.

## Documentation plan (owner per truth)

28. **`docs/deliver.md` — the user guide, born with the feature**
    (step 2, alongside first working code). Owns the user-facing
    narrative: screenshot→Cmd-V demo, paste beat, what-works-where (the
    degradation matrix in user terms), remote delivery, installing the
    deliver app. Written for its two futures: the README "How do I paste
    images and files into the box?" Q&A entry answers in one line + demo
    and links to it; the website page later lifts it wholesale (site
    plan: docs/marketing/positioning.md).
29. **One ADR, sections not siblings** — machine-scoped verb,
    exec-stream transport, /inbox, uid-filtered discovery, ssh protocol
    shape, picker adapter. GLOSSARY owns words; ARCHITECTURE owns
    internals only (discovery cascade, transport script, probes,
    adapter) and points to deliver.md for behavior; `--help` owns flags
    (internal ones marked); stderr owns in-the-moment truth, worded from
    the glossary.
30. **Lockstep tripwire extends to deliver.md**: any command output it
    shows is re-verified against code when deliver changes (same
    discipline as README/status).

## Scope & sequencing

31. **v1 = build-order steps 1–5** (ADR + glossary + deliver.md
    skeleton; core discovery/transport/naming; clipboard out +
    capability probes; clipboard in + paste beat + stdin; Bubble Tea
    picker + graphical adapter) as ONE reviewed feature. **ssh remote
    and the deliver app are separate follow-on tranches**, each with its
    own review loop, each separately shippable.
32. **TODO.md** gains the deliver item under Now on dispatch; Someday's
    "drag-and-drop into the boxed terminal" gets a cross-reference
    (deliver partially supersedes it). TODO stays Pete's to direct.

## Reviewer-item disposition

Codex (grilling-input-codex.md): #1 engine → D1. #2 atomicity → D8.
#3 consume/protocol → D20–23. #4 source policy → D10. #5 encoding/framing
→ D13–14, D23. #6 labels/scoping → D2, D4, D27. #7 uid/podman → D3.
#8 test seams → build-time concern; adapters for clipboard/TTY/notify
fall out of D12–19 (unit-test via injected fakes, house style). #9
clipboard privacy → D18. #10 doc ownership → D28–30. #11 launcher
lifecycle → deliver-app tranche (D31), generated-readable + uninstall
documented there.

Grok (grilling-input-grok.md): #1 Dock×ssh TTY → D20. #2 inbox integrity
→ D5–7. #3 I/O contract → D12–15. #4 path spelling → D5 (absolute
`/inbox`, dissolves it). #5 cross-principal auto-pick → D2. #6 lifecycle
states → D24. #7 shell-template identity → D3. #8 context line → D25.
#9 GUI/TCC reality → deliver-app tranche; verify on a real Mac at build
time (can't test in-box). #10 clipboard clobber → D16. #11 type honesty
→ D11. #12 version negotiation → D22. #13 materialize collision → D26.
#14 multi-file transaction → D9, D12–13.
