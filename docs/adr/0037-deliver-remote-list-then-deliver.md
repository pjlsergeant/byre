# Remote delivery is list-then-deliver over plain ssh

Decided 2026-07-16. `byre deliver ssh://[user@]host[:port]
...` delivers through another machine running byre as **two headless ssh
invocations**: enumerate the remote boxes, pick locally, then stream one
tar archive into a single targeted remote deliver. **Partially
supersedes ADR 0021**: its "ssh remote shape (follow-on tranche)"
section -- scp staging, `--porcelain` sentinels, `--consume`, and the
remote interactive picker over `ssh -t` -- is reversed in full, before
any of it was built (v1 shipped with no ssh surface, so the paper
protocol carries no compatibility weight). The layering principle it
stated holds: local byre owns local capabilities (clipboard, the paste
beat, staging, GUI); remote byre owns what it already owns -- and
*selection* is a local capability too, because the human doing the
selecting is local.

## Why the reversal

The superseded shape hung everything off one decision -- the picker runs
remotely, interactively, over `ssh -t` -- and every complication in that
design was that decision's bill: a pty merges stdout into stderr, so
landed paths needed `--porcelain` sentinel framing; the picker needed
stdin free, so payloads couldn't ride it and had to stage via scp, which
needed `--consume` to clean up; and the local side had to relay a raw
pty stream while scanning it. Moving the pick local dissolves the whole
chain. The "payload never rides ssh stdin" rule had two recorded
justifications -- stdin busy with the picker, and pty binary-mangling --
and neither survives: there is no remote picker, and without `-t` a
plain ssh exec is binary-safe (it was already DELIVER.md's documented
workaround: `pngpaste - | ssh host byre deliver - --name shot.png`).
This design is that workaround, promoted: byre does the discovery and
the framing for you.

## The two invocations

The wire mechanics -- the `--boxes` enumeration, the single
`--tar -` exec feeding the per-file transport, `--box`, `--remote-byre`
and `--no-clip` -- are described in `docs/DELIVER.md` ("How it works").
What this decision fixes about them:

- **Channel discipline does the protocol work.** On
  `byre deliver --boxes --proto 1`, **stdout is the contract** (one line
  per deliverable box in a frozen tab-separated grammar: container id,
  engine, project id, workdir id, with control characters and tabs
  sanitized on emit), **stderr is byre's voice** (unreachable-engine
  notes, hidden-by-uid-filter counts, passed through ssh untouched), and
  the **exit code says whether the list is trustworthy**: a distinct
  nonzero code marks a partial pool, so the caller must not auto-pick --
  ADR 0021's partial-pool rule, carried across the wire by exit status
  instead of shared state.
- **Selection is local and is not a second cascade.** Zero rows is an
  error, one complete row is delivered to, several feed the existing
  local picker. There is no remote cwd to walk and the uid filter
  already ran remotely; the remote never selects anything.
- **One exec, however many sources.** The local side packs files,
  directories (walked with the local delivery rules: structure
  preserved, file symlinks followed, directory symlinks skipped),
  clipboard captures and spooled stdin into one tar stream; remote byre
  reads it entry by entry straight into the existing exec-stream
  transport, so claiming and uniquify are identical to local delivery
  and the archive never touches the remote host's disk. Landed paths
  return on remote stdout, unmerged without `-t`.
- **`--proto <n>` pins the whole ssh-facing surface** -- the `--boxes`
  grammar, the flag set, the tar semantics -- and fails the handshake
  before any payload moves. A remote byre too old to know the flag fails
  at cobra's unknown-flag parse (exit 2), which the local side
  translates into "byre on <host> is too old for remote delivery".

## Accepted costs

- **Auth-prompt friction.** Two connections on the interactive path
  (list, then deliver) -- two touches of a confirm-to-auth key. Baked
  `--box` (the generated deliver app, scripts) skips enumeration and
  pays one. Explicitly accepted over ControlMaster socket management.
- **Progress is byre's job.** There is no scp progress display; the
  local side wraps the tar stream in a counting writer against a
  pre-statted total (TTY-only). The bar's claim is honest about
  buffering: "sending" while bytes move, "waiting for the box" after --
  it measures bytes handed to ssh, not bytes claimed in `/inbox`.
- **The list is a snapshot.** A box can die between pick and deliver;
  the delivery fails legibly and re-running is one command. No
  freshness machinery.
- **Non-interactive ssh PATH is sparse** (stock macOS sshd omits
  /usr/local/bin), so "byre: command not found" on a machine that
  plainly has byre is the common first failure; the error says so, and
  `--remote-byre <path>` names the remote binary explicitly.

## Consequences

- The frozen surface is the `--boxes` line grammar plus the remote flag
  set, pinned by `--proto`; there is no sentinel grammar and no staging
  path to guard.
- With no picker available, ambiguity degrades the ordinary local way: a
  listing error naming `--box` candidates.
- Remote delivery reuses the deliver package's transport, discovery,
  and picker verbatim; the new code is the tar walk (both ends), the
  grammar (both ends), and the ssh seam. The box cannot tell local from
  remote delivery: files still appear in `/inbox`.
