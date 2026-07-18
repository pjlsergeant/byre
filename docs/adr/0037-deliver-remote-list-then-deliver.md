# Remote delivery is list-then-deliver over plain ssh

Decided 2026-07-16. `byre deliver ssh://[user@]host[:port]
...` delivers through another machine running byre as **two headless ssh
invocations**: enumerate the remote boxes, pick locally, then stream one
tar archive into a single targeted remote deliver. **Partially
supersedes ADR 0021**: its "ssh remote shape (follow-on tranche)"
section -- scp staging, `--porcelain` sentinels, `--consume`, and the
remote interactive picker over `ssh -t` -- is reversed in full, before
any of it was built (v1 shipped with no ssh surface, so the paper
protocol dies unshipped and carries no compatibility weight). The
layering principle it stated survives unchanged: local byre owns local
capabilities (clipboard, the paste beat, staging, GUI); remote byre owns
what it already owns -- except that *selection* is now a local
capability too, because the human doing the selecting is local.

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

**Enumerate** (skipped entirely when the caller already passed `--box`):

    ssh <host> byre deliver --boxes --proto 1

`--boxes` runs discovery only and answers headlessly. Channel
discipline does the protocol work: **stdout is the contract** -- one
line per deliverable box in a frozen tab-separated grammar (container
id, engine, project id, workdir id; free-text-ish fields sanitized of
control characters and tabs on emit) -- **stderr is byre's voice**
(unreachable-engine notes, hidden-by-uid-filter counts; it passes
through ssh to the caller's terminal untouched), and the **exit code
says whether the list is trustworthy**: 0 means the pool is complete, a
distinct nonzero code means an engine query failed (the list still
prints, `--box` and the picker still work, but "exactly one" is
unknowable, so the caller must not auto-pick -- ADR 0021's partial-pool
rule, carried across the wire by exit status instead of shared state).
The local side then does a three-way branch -- zero: error; one (and
complete): deliver to it; several: the existing local picker, fed the
remote rows. This is not a second cascade: there is no remote cwd to
walk and the uid filter already ran remotely; the remote never selects
anything.

**Deliver** -- always exactly one exec, however many sources:

    ssh <host> byre deliver --proto 1 --box <id> --no-clip --tar -

The local side packs every source into a single tar stream -- files,
directories (walked with the local delivery rules: structure preserved,
file symlinks followed, directory symlinks skipped), clipboard captures
and stdin spooled to size -- and pipes it up. Remote byre reads the
archive **entry by entry, straight into the existing per-file
exec-stream transport**: top-level names claim atomically exactly as
local delivery claims them (uniquify on collision, never overwrite),
interior paths ride the claimed root. No remote temp files, no staging
directory, no cleanup, no `--consume` -- the archive never touches the
remote host's disk. Landed top-level paths print to remote stdout one
per line, which ssh returns unmerged; the local side prints them (its
stdout contract) and runs the local clipboard round-trip. `--no-clip`
keeps the remote's clipboard garnish off a machine nobody is looking at.

`--proto <n>` pins the whole ssh-facing surface -- the `--boxes`
grammar, the flag set, the tar semantics -- and fails the handshake
before any payload moves. A remote byre too old to know the flag fails
at cobra's unknown-flag parse (exit 2), which the local side translates
into "byre on <host> is too old for remote delivery". Version skew
fails at the first connection, legibly, every time.

## Accepted costs

- **Auth-prompt friction.** Two connections on the interactive path
  (list, then deliver) -- two touches of a confirm-to-auth key. Baked
  `--box` (the generated deliver app, scripts) skips enumeration and
  pays one. Explicitly accepted over ControlMaster socket management.
- **Progress is byre's job now.** scp's progress display is gone; the
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

- `--porcelain` and `--consume` are never built; no sentinel grammar,
  no staging-path accident guards. The frozen surface shrinks to the
  `--boxes` line grammar plus the remote flag set, pinned by `--proto`.
- ADR 0021's "no remote pre-pick protocol" clause is void -- the
  enumeration IS a pre-pick protocol, and the hard-error-on-ambiguity
  rule it justified reduces to the ordinary local degradation (no
  picker available: a listing error naming `--box` candidates).
- Remote delivery reuses the deliver package's transport, discovery,
  and picker verbatim; the new code is the tar walk (both ends), the
  grammar (both ends), and the ssh seam.
- DELIVER.md gains the remote flow and a what-works-where row; the
  agent-facing chassis sentence is unchanged (files still appear in
  `/inbox` -- the box cannot tell local from remote delivery).
