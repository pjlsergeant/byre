# grab: deliver reversed, with the judgment moved host-side

Decided 2026-07-19. `byre grab <box-path> [<host-path> | -]` gets a file
or directory out of a running box onto the host — the mirror of `byre
deliver` (ADR 0021), and byre's second machine-scoped verb. It reuses
deliver's discovery cascade, attach model, and exec-stream transport
shape wholesale; this ADR records only what the reversed direction
decides differently.

## The trust polarity reverses

Deliver's paranoia sits on the host READ side (hostopen, os.Root walks)
because the sources can be agent-shaped; its box-side writes trust
nothing about /inbox's contents. Grab flips that: everything arriving
from the box — whether the path exists, what kind it is, the
enumeration of a directory, every interior name, all content — is
agent-controlled input, and the destination may itself sit inside the
agent-writable project tree (grabbing into the repo is the common
case). So the box side stays dumb — three POSIX-sh scripts (classify,
enumerate, cat) with every variable piece passed as argv, never spliced
into script text — and all judgment lives in the host-side writes:

- **Every write rides an `os.Root`** anchored at the destination
  directory via `hostopen.OpenDirRootNoFollow` (the mandated primitive
  for agent-writable host access): a symlink swapped in for the
  destination's final component is refused and the anchor is pinned by
  fd, so after that interior paths are openat-walked and nothing the box
  supplies can land content outside the directory the user named. This
  is exactly deliver's protection level for a source directory —
  deliver, too, never follows a symlinked final component (a symlinked
  destination dir is refused; pass the resolved path).
- **Grab never overwrites a host file.** The claim protocol is ADR
  0021's, reversed: stream to a dotfile temp created `O_CREAT|O_EXCL`,
  claim the final name with link(2) (mkdir for directories) — both fail
  EEXIST atomically and neither writes through a pre-existing symlink —
  and uniquify `report.pdf` → `report-2.pdf`. This holds even for a
  name the user typed exactly: the CONTENT is agent-chosen, and
  silently replacing host files with agent bytes is precisely the
  accident class byre exists to prevent. The printed path (stdout, one
  per line — the same contract as deliver) is always where the bytes
  actually landed, and a uniquified explicit name says so on stderr.
  `rm` first if you truly want the old name.
- **Enumeration output is input.** Records are NUL-framed (the one byte
  a filename cannot contain); a record outside the grabbed root is
  ignored loudly, `.`/`..`/empty components refuse the entry, control
  characters rewrite with a printed rename — tar.go's `splitEntryName`
  posture, applied to find(1) output.

## Box-side semantics

- Relative box paths resolve against `/workspace` (the box's workdir),
  in Go, before anything execs. One spelling in output: absolute.
- Box-side symlinks are FOLLOWED for the named path (the user asked for
  that path; the whole box filesystem is already the agent's readable
  domain, so following reveals nothing new). A symlinked directory
  classifies to its physical path (`pwd -P`) because find(1) does not
  follow argument symlinks. Interior symlinks/FIFOs/sockets are
  skipped with a note, exactly as deliver skips their inbound cousins.
- Directory enumeration is three find(1) passes (dirs, files, other) —
  sh + find only, matching deliver's no-tooling-assumptions transport;
  content then streams one `exec cat` per file, deliver's per-file exec
  cost mirrored. A pass failing (unreadable subtree) still grabs what
  enumerated; the nonzero exit and a "may be incomplete" line carry the
  truth. Partial semantics throughout are deliverDir's: successes stay,
  the claimed path still prints, the error counts entries.
- `-` as the destination streams a single file's raw content to stdout
  (deliver's `-` reversed); directories refuse it.

## No clipboard leg

Deliver's clipboard round-trip exists to carry an in-box path to the
agent prompt. A grab ends in the user's own shell with the landed path
on stdout — there is nothing to ferry, so no clipboard write and no
`--no-clip`.

## Consequences

- ARCHITECTURE/GLOSSARY/DELIVER.md now say "machine-scoped verbs",
  plural: deliver and grab share the discovery cascade, uid accident
  filter, and picker.
- Consciously accepted, mirroring ADR 0021's race notes: the box can
  swap a path between classify and cat (the user Ctrl-C's a hang; the
  agent gains nothing it couldn't do by changing the file first), and
  an agent writing into a freshly claimed destination *inside the
  project tree* mid-grab can collide names there (claims uniquify,
  never clobber — containment holds either way). Recorded so reviewers
  don't re-find them.
- Consciously accepted, shared with deliver via the same
  `hostopen.OpenDirRootNoFollow` primitive: an agent-swapped symlink in
  an ANCESTOR component of an agent-writable destination path (e.g.
  `a` in `/workspace/a/b`) is followed, so a grab to a destination
  nested inside the project tree can be redirected to land its
  (agent-authored, no-clobber) files elsewhere on the host. The final
  component is guarded; ancestors are not, because refusing every
  ancestor symlink would reject legitimate system symlinks
  (`/tmp`→`/private/tmp`, `/var` on macOS). Closing it fully needs a
  component-by-component nofollow walk that distinguishes system from
  planted symlinks — a cross-cutting change to the shared primitive
  (deliver inherits the identical residual), deferred rather than
  re-architected under grab alone.
- Consciously not built: multiple box paths per invocation (one path,
  one destination — run it twice), grab-over-ssh (deliver's ADR 0037
  shape would carry it if wanted), and a `--force` overwrite (against
  the no-clobber doctrine; rm first).
- Hardlink claims require same-filesystem link support (any normal
  mac/linux filesystem; exotic mounts fail loudly with the claim
  error).
