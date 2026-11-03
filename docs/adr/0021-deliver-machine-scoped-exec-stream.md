# deliver: a machine-scoped verb, exec-streamed into /inbox

Decided 2026-07-10. `byre deliver` gets
files from the host into a running box: path arguments, the host
clipboard, or stdin, landing in the box's `/inbox`, with the in-box path
printed and copied back to the host clipboard for pasting into the agent
prompt. This ADR carries the durable rationale; the user-facing behavior
is [DELIVER.md](../DELIVER.md).

## Machine-scoped discovery

Byre's project verbs are cwd-scoped; `deliver` is machine-scoped by
design -- the Dock/Finder drop and "I'm looking at a screenshot, get it
into the box" flows have no meaningful cwd. Discovery is label-driven via
the engine CLI (ADR 0004), requires no `~/.byre/projects/<id>` dir, and
resolves in a fixed cascade (explicit `--box`, cwd match, the one owned
session, an interactive picker, else an error; the procedure is in
DELIVER.md). Two choices in it are deliberate: a `--box` project prefix
may match several worktree sessions (one container per workdir), so
ambiguity errors listing the candidates; and the cwd match walks ancestor
directories per level, because invocation from a subdirectory is the
common case.

The session pool is the union across all installed engines -- a machine
can hold live byre boxes under Docker AND Podman, and a machine-scoped
read has no config to consult (`engine =` stays a build/launch concern).
Each entry keeps engine affinity; the exec goes through the engine that
holds it. An UNREACHABLE engine (installed, daemon or machine not up --
podman on a typical Mac) counts as answered-with-zero: one quiet
"isn't reachable; skipping it" line and auto-pick stays alive, because a
stale install must not break the flagship one-command flow. An
unreachable daemon can still hold a running box (live-restore, a remote
daemon); that box is not offered, and the skip line says why. Any other
query failure degrades loudly and disables the auto-pick (with a partial
pool, "exactly one" is unknowable); `--box` and the picker still operate.

Discovery filters to boxes whose `BYRE_UID` matches the caller -- an
**accident filter, not confinement**. `--skip-uid-check` both reveals
and permits foreign boxes; when the filter hides everything, the error
says how many it hid. `BYRE_UID` is runtime env a project's raw
`run_args` can override (ADR 0006 last-wins), and daemon access is
root-equivalent anyway -- this exists to stop accidents on shared
daemons, and must not be hardened into an authorization boundary.

Attach is `byre shell`'s model verbatim: exec as the container's
`BYRE_UID:BYRE_GID`, fail-closed when unreadable, `HOME=/home/dev`.
Under rootless Podman those are the box's in-container identity, and the
uid filter skips caller-scoped engines (ADR 0032).

## Exec-stream transport, no mount

Files stream into the running container over `exec -i` -- no host-side
inbox directory, no mount, no config surface, nothing new for `byre
status` to explain. Ownership is correct for free (exec as the dev
identity, sidestepping `docker cp`'s root-ownership problem), a
concurrent box can't be hit by accident, and the inbox dies with the
throwaway container. Ephemerality is a feature: "survives restarts" is
an explicit non-goal -- re-delivering is one command. The rejected
alternative (a persistent host-side inbox mount) could return later as
an opt-in skill; it is not the default.

The write is atomic and no-clobber end to end: a stream never writes
through a pre-existing name, planted symlinks included, a collision
uniquifies (`report.pdf` -> `report-2.pdf`) rather than overwriting, and
a died stream never leaves a half-file under a real name. Filenames pass
as argv, never spliced into script text. The temp-then-link protocol is
in ARCHITECTURE.md.

## /inbox, root-parented

The inbox is `/inbox`: a dev-owned directory whose parent is root-owned
`/`, baked at image build (in `internal/gen`'s build-time set, pinned by
the Dockerfile golden test). Root-parenting structurally prevents the
agent replacing the inbox with a symlink (that needs write on `/`), and
the EEXIST-atomic claims neutralize names planted inside it.
`/home/dev/*` remains user/mount namespace; byre mechanisms live at
root beside `/workspace`. One spelling -- absolute `/inbox/...` -- on
every surface. Deliveries never land in `/workspace` (it pollutes the
repo the agent gits in). A box whose image predates the bake gets a
clean "rebuild" error, never a root-exec backfill, which would break
ADR 0008's no-root-after-PID-1 invariant.

## The clipboard round-trip

After delivery the in-box paths land on the host clipboard (one per
line, lazily shell-quoted -- built for pasting into an agent prompt),
and always print to stdout (one per line, unquoted; stdout is the
contract, the clipboard is best-effort garnish). Capabilities are
probed per-axis -- TTY, GUI session, clipboard tool per direction -- and
each degrades independently with a printed claim, never a refusal
(PRINCIPLES: degrade claims).

## The paste beat (clipboard import)

No-arg `byre deliver` on a TTY waits for a paste *gesture* -- Ctrl-V or
a bracketed paste -- and only then reads the system pasteboard
out-of-band (file references -> image -> text). Enter is not a gesture
(it isn't semantically paste), and the prompt samples the clipboard's
*types* -- never content -- to say what's on offer. The interaction
contract (each rule verified on a real Mac):

- **The captured paste is evidence, not noise.** A file dragged onto
  the terminal window pastes its PATH -- text that was never on the
  pasteboard -- so discarding it and reading the pasteboard would
  deliver stale clipboard content. The paste's text is classified:
  mirrors the pasteboard -> a real paste, full priority read; parses as
  existing absolute host path(s) -> a possible drag, name the paths and
  ask before opening and delivering those files; else literal pasted
  text.
- **Ctrl-V is the headline gesture.** With an image-only clipboard,
  macOS Terminal sends nothing on Cmd-V (no text representation -> no
  paste event), so no app can catch it; Cmd-V stays fine for text and
  copied files.
- **Confirmation states kind + size + destination, never content** --
  a preview of a just-delivered password would re-disclose it in
  scrollback.
- **A plain click on the deliver app opens a terminal running this
  beat**: silently shipping the clipboard from a Dock click would skip
  the paste beat exactly where the user can't see what's shipping.
  Drops stay direct -- a drag is already the explicit what-to-deliver
  choice.

## Remote delivery

Delivery to a box on another machine is ADR 0037. The layering holds:
local byre owns local capabilities, remote byre owns what it already
owns.

## Consequences

- The agent-facing context gains one factual chassis sentence: files
  the user delivers appear in `/inbox`. Mechanism description, not an
  opinion; same class as naming `/workspace`.
- `byre deliver --install-app` generates the "deliver app" -- a
  readable generated artifact -- because macOS Dock drop targets must be
  `.app` bundles. It takes an optional `ssh://` target (baking the
  ADR 0037 remote-deliver invocation) and a `--name` label, so
  per-target installs coexist; a colliding label refuses rather than
  clobbers. Naming is in DELIVER.md.
- The user guide `docs/DELIVER.md` owns user-facing behavior including
  the degradation matrix; ARCHITECTURE owns internals; this ADR owns
  the decisions; GLOSSARY owns the words (deliver, inbox, deliver app).
- Consciously accepted: an agent racing symlinks into a freshly created
  delivery tree mid-stream can redirect interior writes -- it gains
  nothing over moving files after delivery inside its own box, so no
  fd-relative traversal apparatus. Recorded so reviewers don't re-find
  it.
- Consciously no container-state machinery: no pre-filtering by
  paused/restarting (byre never pauses; it's externally inflicted and
  rare). The exec is the authoritative check -- wrap its failure
  legibly, don't pre-guess it.
