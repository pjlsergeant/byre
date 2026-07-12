# deliver: a machine-scoped verb, exec-streamed into /inbox

Decided 2026-07-10 (grilled with Pete driving; both external reviewers
run adversarially against the decisions afterwards). `byre deliver` gets
files from the host into a running box: path arguments, the host
clipboard, or stdin, landing in the box's `/inbox`, with the in-box path
printed and copied back to the host clipboard for pasting into the agent
prompt. This ADR carries the durable rationale; the full working record
(32 numbered decisions, reviewer dispositions, field amendments) lived
in the `docs/deliver/` design workspace, absorbed here and into the
shipped docs on completion and then deleted — git history holds it.

## Machine-scoped discovery

Every other byre verb is cwd/project-scoped. `deliver` is machine-scoped
by design — the Dock/Finder drop and "I'm looking at a screenshot, get
it into the box" flows have no meaningful cwd. Discovery is label-driven
via the engine CLI (ADR 0004), requires no `~/.byre/projects/<id>` dir,
and resolves in cascade order:

0. `--box <id-or-project-prefix>` — explicit selector. A prefix must
   match uniquely (a project prefix legitimately matches several
   worktree sessions, one container per workdir); ambiguity errors,
   listing the candidates.
1. cwd match — walk ancestor directories, matching the `byre.workdir`
   label per level (a naive single-level match would miss invocation
   from a subdirectory, the common case).
2. Exactly one owned session on the machine → it.
3. Interactive picker (TTY: Bubble Tea; graphical launch: osascript /
   zenity / kdialog; neither: a clean ambiguity error listing sessions).
4. Zero sessions → clean error.

The session pool is the union across all installed engines — a machine
can hold live byre boxes under Docker AND Podman, and a machine-scoped
read has no config to consult (`engine =` stays a build/launch concern).
Each entry keeps engine affinity; the exec goes through the engine that
holds it. An UNREACHABLE engine (installed, daemon or machine not up —
podman on a typical Mac) counts as answered-with-zero: one quiet
"isn't reachable; skipping it" line and auto-pick stays alive, because
a daemon you can't reach runs no containers and a stale install must
not break the flagship one-command flow. Any other query failure
degrades loudly and disables the auto-pick (with a partial pool,
"exactly one" is unknowable); `--box` and the picker still operate.

Discovery filters to boxes whose `BYRE_UID` matches the caller — an
**accident filter, not confinement**. `--skip-uid-check` both reveals
and permits foreign boxes; when the filter hides everything, the error
says how many it hid. `BYRE_UID` is runtime env a project's raw
`run_args` can override (ADR 0006 last-wins), and daemon access is
root-equivalent anyway — this exists to stop accidents on shared
daemons, and must not be hardened into an authorization boundary.

Attach is `byre shell`'s model verbatim: exec as the container's
`BYRE_UID:BYRE_GID`, fail-closed when unreadable, `HOME=/home/dev`,
plus the rootless-podman detect-and-warn (ADR 0008: ownership claims
degrade, delivery doesn't block).

## Exec-stream transport, no mount

Files stream into the running container over `exec -i` — no host-side
inbox directory, no mount, no config surface, nothing new for `byre
status` to explain. Ownership is correct for free (exec as the dev
identity, sidestepping `docker cp`'s root-ownership problem), a
concurrent box can't be hit by accident, and the inbox dies with the
throwaway container. Ephemerality is a feature: "survives restarts" is
an explicit non-goal — re-delivering is one command. The rejected
alternative (a persistent host-side inbox mount) could return later as
an opt-in skill; it is not the default.

The write protocol is atomic and no-clobber end to end: stream to a
dotfile temp under `set -C` (noclobber = `O_CREAT|O_EXCL`, refusing to
write through any pre-existing name, planted symlinks included), then
claim the final name with `ln` (link(2) fails `EEXIST` atomically),
uniquifying `report.pdf` → `report-2.pdf` server-side where the
directory is visible. Directory deliveries claim the top-level name by
the same rule with atomic `mkdir`. A died stream leaves at worst an
orphaned dotfile — never a half-file under a real name. Filenames pass
as argv, never spliced into script text.

## /inbox, root-parented

The inbox is `/inbox`: a dev-owned directory whose parent is root-owned
`/`, baked at image build (in `internal/gen`'s build-time set, pinned by
the Dockerfile golden test). Root-parenting structurally prevents the
agent replacing the inbox with a symlink (that needs write on `/`), and
the EEXIST-atomic claims neutralize names planted inside it.
`/home/dev/*` remains user/mount namespace; byre mechanisms live at
root beside `/workspace`. One spelling — absolute `/inbox/...` — on
every surface. Deliveries never land in `/workspace` (it pollutes the
repo the agent gits in). A box whose image predates the bake gets a
clean "rebuild" error: an earlier root-exec backfill ruling was
REVERSED in review because it broke ADR 0008's no-root-after-PID-1
invariant.

## The clipboard round-trip

After delivery the in-box paths land on the host clipboard (one per
line, lazily shell-quoted — built for pasting into an agent prompt),
and always print to stdout (one per line, unquoted; stdout is the
contract, the clipboard is best-effort garnish). Capabilities are
probed per-axis — TTY, GUI session, clipboard tool per direction — and
each degrades independently with a printed claim, never a refusal
(PRINCIPLES: degrade claims).

## The paste beat (clipboard import)

No-arg `byre deliver` on a TTY waits for a paste *gesture* — Ctrl-V or
a bracketed paste — and only then reads the system pasteboard
out-of-band (file references → image → text). Enter is not a gesture
(it isn't semantically paste), and the prompt samples the clipboard's
*types* — never content — to say what's on offer. Field amendments,
all Pete-ratified on a real Mac:

- **The captured paste is evidence, not noise.** A file dragged onto
  the terminal window pastes its PATH — text that was never on the
  pasteboard — so blind discard-and-read-pasteboard delivered stale
  clipboard content (observed). The paste's text is classified:
  mirrors the pasteboard → a real paste, full priority read; parses as
  existing absolute host path(s) → a drag, deliver those files; else
  literal pasted text.
- **Ctrl-V is the headline gesture.** With an image-only clipboard,
  macOS Terminal sends nothing on Cmd-V (no text representation → no
  paste event), so no app can catch it; Cmd-V stays fine for text and
  copied files.
- **Confirmation states kind + size + destination, never content** —
  a preview of a just-delivered password would re-disclose it in
  scrollback.
- **A plain click on the deliver app opens a terminal running this
  beat**: silently shipping the clipboard from a Dock click skipped
  the paste beat exactly where the user can't see what's shipping.
  Drops stay direct — a drag is already the explicit what-to-deliver
  choice.

## The ssh remote shape (follow-on tranche)

`byre deliver ssh://[user@]host` keeps layering honest: local byre owns
local capabilities (clipboard read, staging, GUI), remote byre owns what
it already owns (discovery, picker, exec-stream). Payloads stage via
`scp` to a remote `mktemp -d`, then `ssh -t` runs the remote deliver —
the payload never rides ssh stdin, which must stay free for the remote
picker, and a pty would mangle binary anyway. The ssh-facing surface is
a frozen mini-protocol: `--proto` (handshake before any payload; the
version number pins the WHOLE surface, so capability skew fails at the
handshake), `--porcelain` (`::deliver <path>` sentinel lines, because
`ssh -t` merges stdout/stderr into one pty stream), and `--consume`
(delete-after-deliver, refused outside the `/tmp/byre-deliver-*`
staging pattern — an accident guard, not an authorization boundary; the
agent cannot invoke deliver, so there is no adversary to confine).
There is no remote pre-pick protocol: a non-TTY remote delivery that
finds several owned sessions is a hard error — bake `--box` into a
generated deliver app instead.

## Consequences

- The agent-facing context gains one factual chassis sentence: files
  the user delivers appear in `/inbox`. Mechanism description, not an
  opinion; same class as naming `/workspace`.
- `byre deliver --install-app` (follow-on tranche) generates the
  "deliver app" — a readable generated artifact, display name "Byre
  Deliver" — because macOS Dock drop targets must be `.app` bundles.
- The user guide `docs/deliver.md` owns user-facing behavior including
  the degradation matrix; ARCHITECTURE owns internals; this ADR owns
  the decisions; GLOSSARY owns the words (deliver, inbox, deliver app).
- Consciously accepted: an agent racing symlinks into a freshly created
  delivery tree mid-stream can redirect interior writes — it gains
  nothing over moving files after delivery inside its own box, so no
  fd-relative traversal apparatus. Recorded so reviewers don't re-find
  it.
- Consciously no container-state machinery: no pre-filtering by
  paused/restarting (byre never pauses; it's externally inflicted and
  rare). The exec is the authoritative check — wrap its failure
  legibly, don't pre-guess it.
