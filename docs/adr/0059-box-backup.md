# `byre backup` / `byre restore`: one file holding a project's config and its state volumes

Decided 2026-10-02. `byre backup` writes one file holding a project's config
and its state volumes. `byre restore` takes that file and makes a fresh
project from it, on this machine or another: config written, volumes filled,
ready for `byre develop` once every package the config names is installed. A
backup is the user's own box coming back to them -- not an export format, not
a distribution channel, and not a second copy of anything byre can rebuild.

What people want back is the agent's memory and context, the credentials,
and the project's config; everything rebuildable stays out.

Principles: P1 (the threat model is the agent, never the user -- byre does
not warn the user about copying their own login, and every refusal here is
the reset/forget class rather than a gate on the user's choices); P4 (every
list is printed, never silent -- what is carried, what is left behind and
why, every symlink target and dropped entry, and tar's own lines, all through
the terminal-data funnel as data); P5 (one y/n at restore names the engine,
the volumes created and kept, the volumes left to develop, and the
credentials' state -- consent at the scope of its effect); P7 (the base
image's tar is the pour tool, and byre proves it runs the pour command before
using it rather than assuming it). Amended by this decision, each carrying a
one-line pointer: ADR 0007 (the seeding ban is on byre-initiated copies), ADR
0008 (no root at runtime in a session), ADR 0029 (restore is the second entry
to the one acquisition flow), ADR 0046 (the write ban is about context
delivery), ADR 0053 (session containers carry launch records; helpers do not).
Related: ADR 0002/0003 (every engine operation is a runner method), ADR 0004
(session discovery, whose sweep the stillness check is), ADR 0009
(worktrees), ADR 0017 (machine-scoped identity), ADR 0035 (layers are plain
files), ADR 0037 (precedent for tar name hygiene only), ADR 0040 (anchored
host writes),
ADR 0044 (one TOML library), ADR 0051 (integrity, not authenticity),
ADR 0054 (the exclusive-volume scan), ADR 0057 (project credentials).

## What stays out

Every exclusion has its own reason, and none of them is "later":

- **The workspace** -- git carries it. A backup of a tree byre never owned
  would make byre a worse `git clone`.
- **The built image** -- it bakes the host UID/GID and is tagged for this
  machine's identity (ADR 0008). It is derived from the config, which does
  travel, so the destination builds its own at the first develop.
- **The ephemeral container filesystem** -- the container is throwaway by
  construction; nothing in it is state.
- **Layers, templates, skills, packages, the agent** -- references, not
  payloads (below).
- **Every other store file** -- the `applied` marker, the per-worktree engine
  records, the launch records and the build context are this machine's
  history of this project, not the project's state. Restore writes no
  `applied` marker in particular, because no preset was applied.

## Which volumes travel

**Every project volume that exists on the source engine comes along** unless
the user says otherwise. The listing is `projectVolumes`', the same one
`reset` and `forget` use: the `byre-<id>-` prefix, machine-scoped physical
names dropped, and any name a longer known project id claims excluded. That
deliberately includes a volume no skill in the resolved set declares -- the
state a one-off `develop --agent grok` left behind is the project's state and
the user expects it back. The volume an ADR 0058 `--agent` override run
created travels like any other project state volume; the override itself
writes nothing else for a backup to find.

Four reasons a volume stays behind, each named on the preview and in the
summary. The lists name physical volumes, each row with its scope and engine,
because logical names can repeat (a machine-scoped `login` and this project's
`login` on another engine are two rows).

- **`role = "cache"`** in the resolved set. Disposable by declaration; a
  cache volume in a backup is weight with no claim behind it.
- **`--no-volume NAME`**, taking the LOGICAL name (the part after the project
  prefix: `.claude`, `.grok`), matched as a string against the carried
  candidates. An unknown name is refused naming the candidates, and a name
  that is already not carried is refused with the reason it is not carried.
  Where several reasons apply the refusal names the first of cache,
  machine-scoped, owned by another project, other engine. Repeats of a
  carried name are harmless.
- **Machine scope.** A machine-scoped volume is never offered, whether a
  skill or the project config declares it, and the judgement is the PHYSICAL
  name (`byre-machine-u<uid>-...`) rather than the declarer -- a volume byre
  can see is machine-scoped whether or not the current resolution says so. Every
  machine-scoped volume byre ships is identity state, a login or a key, and
  the destination logs in once: ADR 0017's intended path, not a gap.
- **Another engine, or another project.** A project volume found on an
  installed engine the backup is not reading is named with its engine ("this
  backup reads <source engine>"); a name under this project's prefix that a
  longer known project id claims is named with the project that owns it. Both
  are facts about state the user may want and this file does not hold, so
  both are printed rather than silently absent.

The classification reads the set `develop` runs -- the cascade plus the
skills' own declarations unioned in -- not the cascade alone. Every bundled
agent state volume and every machine-scoped identity volume is declared by a
skill, so a cascade-only read would see none of them.

## The config travels byte-for-byte

Backup rewrites nothing in the config, with one exception: `--no-credentials`
deletes the `[credentials]` block and every credential row (an
`env_from_host` value with the `encrypted:` or `encrypted-file:` scheme) from
the COPY that goes in the file, through `internal/tomldoc` so every other
byte survives (ADR 0044). The strip also takes a zero-row `[credentials]`
block and an `env_from_host` table that held nothing but credential rows (a
bare header is residue of the strip, not a byte the user wrote), removed as
tomldoc removes any table; a table the user left empty themselves is
untouched. The source file is never touched. Host paths,
`engine`, `worktree_base`, `extends`, seeds: all carried as they are, and
restore lists what this machine must satisfy, each with the verb that reads
it and how it fails. The file's own ADR 0030 egress closures travel byte for
byte like every other key; effective egress at the destination also composes
that machine's defaults, layers and skill manifests, so the claim is only
that the file travelled.

**Credentials travel encrypted under the config file's own passphrase.**
Backup decrypts nothing -- the ciphertext already lives inline in the config
file, which is ADR 0057's own decision, so it already goes wherever that file
goes. The restorer needs that passphrase and may rotate it afterwards with
`byre credentials rekey`. Including credentials is the default, because the
file is the user's to protect.

`--no-credentials` is a statement about the backup FILE and nothing else.
Other files on either machine -- a shared layer the user copies across by
hand -- may hold credentials of their own; that is not this tool's business
and it says nothing about it.

A config can legally hold encrypted rows with no `[credentials]` identity, or
an identity with no rows, so the file records which of four states it is
(`rows`, `rows-no-identity`, `identity-only`, `none`) and the restore review
says it. That state is DERIVED from the verified config bytes at restore,
never read off the index: a file whose index misstates it is not refused; the
review states the derived state.

## References, not payloads

Layers (`extends`), templates, skills and the agent must exist at the
destination. The backup names them; it carries none of them. A layer is a
plain file the user sends alongside the backup, which is ADR 0035's
distribution story and remains it.

Each reference is discovered, and hinted, by the ordinary loader that reads
it, with that loader's own missing-reference remedy. Restore offers the
install where a hint exists (a `[sources]` entry visible at that step), under
`byre preset apply`'s own digest-verified install consent and nothing else
(ADR 0029); where no hint exists it stops naming the package and its install
command. Backup refuses at each with that loader's own message, because the
volume classification and the references both come from the resolved set.

**Unlike apply, restore does not continue past a package that is still
missing.** Apply can annotate a package "not installed -- grants unknown" and
show a review anyway; restore cannot, because the volume set it must pour and
the engine, base and mounts it must prove ALL come from the resolved set, and
`skills.Resolve` returns nothing at all while any enabled package is missing.
"Install it, then run `byre restore` again." Declining an offered install is
therefore not a clean end -- it is the reason the package is still missing --
so restore then stops as it does for any missing package. A `skills.Resolve`
error that is not a missing package (an invalid name, conflicting
`network_posture` or `netns_init` declarations, an agent with no command)
stops restore before any prompt: the review is never shown with skill grants
omitted (ADR 0050, ADR 0052 preserved by not continuing).

## Stillness

**Backup requires a completely still project**: no container labelled for
this project in any state, on any installed engine, in any worktree. It is
`reset`'s sweep with the removal taken out: backup refuses while any project
container exists on any checked engine and prints the engine's own remedies;
it never stops or removes one.

Backup holds the project setup lock from that check through the end of the
copy, and under the lock it re-runs the collision fence, re-resolves the
project and re-lists the volumes. If the carried set, the not-carried sets,
the references, the configured engine or the config bytes differ from what
the preview showed, it refuses: "changed while you were reviewing; re-run
byre backup". One resolution is what the file records.

An engine byre cannot account for is a refusal, and a **declined binary**
(ADR 0047) is always one: byre will not run that file, and no flag talks it
into doing so. An engine whose daemon byre cannot REACH is a refusal too, but
one that names the two ways past it (PRINCIPLES.md P1):

    byre backup expects to check every installed engine for this project's
    state (listing volumes). podman isn't reachable (<the engine's own first
    line>): start podman, or run with --ignore-podman.

`--ignore-podman` / `--ignore-docker` is the user saying "that engine is
installed but not running -- go ahead without it". The named engine is then not
queried at all, and both surfaces say what that cost: "podman ignored
(--ignore-podman): volumes of this project there, if any, are not in this
backup, and a session there could not be ruled out". The SOURCE engine cannot
be ignored -- every volume in the file comes from it -- so `--ignore-<source>`
is refused outright and an unreachable source engine gets the start remedy
alone. `deliver.IsUnreachable` is the only classifier: a permission or TLS
failure against a daemon that IS running keeps its own refusal and is never
offered as skippable. `reset`, `forget` and `rehome` take the same flags
through the same code, each with its own consequence sentence. The sweep
queries the helper labels too (below).

**One interrupt handler per run, three phases.** It goes on the moment the
verb makes the first thing of its own -- the staging directory, which holds
the volume bytes in plaintext (restore stages before the review; backup
stages under the lock) -- and comes off at the commit point, the file linked
or the last pour done, so a Ctrl-C during the summary kills byre the ordinary
way. A second Ctrl-C always kills byre outright: firing restores the default
disposition before it acts.

1. **Before restore's enrolment** (all of backup's pre-capture stretch): the
   handler clears what the verb made -- staging, this run's helpers, and the
   project directory restore created while it is still empty -- and ends the
   process, 1, saying "byre: <verb> cancelled; nothing written". It ends the
   process directly because prompts and lock waits do not observe context
   cancellation: Go holds SIGINT off the process while a handler is
   installed, so a cancel-only handler would leave a waiting verb unkillable.
   That is safe here and nowhere else: the setup lock is an flock the kernel
   drops with the process, and the clearing is the whole of what the phase
   made.
2. **After restore's enrolment** (the store bootstrap after the review's y,
   before the lock): a cancellation leaves the directory and the enrolled
   store, as every post-bootstrap exit does (below), names both by path, and
   ends "byre: restore cancelled; no config written and no volume created".
   The enrolment and its record are one step the handler cannot land inside,
   so a cancellation is never told the wrong phase. The handler restores the
   default disposition before it waits an enrolment out, so a stalled
   enrolment holds back only the first Ctrl-C's clearing.
3. **The critical section** (restore's config write, backup's first
   capture, onward): the handler cancels the context that stretch watches,
   because a created volume, a written config or a helper mid-archive has to
   be UNDONE, and only the code doing it knows how.

## The helpers

Both verbs run short-lived containers to read and write volume bytes: a
capture per carried volume, a pour per restored volume, and one preflight per
verb. They are the seeding shape, which has run as root in the box's user
namespace since ADR 0008, and they are **not sessions**: they carry
`byre.helper=<project id>` and `byre.helper.run=<random per invocation>`,
never the project label, and they write no launch record because they launch
no session (ADR 0053). The run id belongs to these two verbs: the seed helper
carries
`byre.helper=<project id>` alone; rehome's migrate helper carries
`byre.helper=<new id>` AND `byre.helper.src=<old id>`, because it mounts both
projects' volumes and a label key holds one value, so the old id needs a key of
its own or a sweep under it could not see the helper at all; and the worktree
helper carries neither label.

Helper mechanics are in `docs/ARCHITECTURE.md` (Backup and restore); the constraints and
their reasons are here. Both verbs select the identity through
`resolveIdentity`, so rootless Podman without a keep-id mapping refuses
exactly where develop refuses (ADR 0032). The argv is closed and byre's own
-- no `run_args` reach it (ADR 0006):

- `--rm -i`, entrypoint overridden to `sh`, `-u 0:0` in the identity's
  userns (`appendUserns`, exactly as develop and seeding select it),
  `--network none`, no host bind of any kind.
- The environment is PINNED over the image's: `TAR_OPTIONS=` (empty) on
  capture, pour and preflight. A built image carries config-authored `ENV`
  lines, and `TAR_OPTIONS=--exclude=memory` there would silently omit an
  agent's memory from a backup with no trace in byre's argv.
- Both tar commands name their archive explicitly, `-f -`, because GNU tar
  otherwise reads the `TAPE` variable, which an image can set, and would
  archive or extract something else entirely.
- The volume mounts at a per-run path, `MNT = /.byre-helper-<run id>` -- the
  ONE value used by the mount, the capture's `-C`, the pour's `-C`, the chown
  and both preflights, so no two of them can drift apart.
- Copy-up is disabled on both mounts, as `-v <volume>:<MNT>:nocopy` (the
  capture's reads `ro,nocopy`). Read-only alone does
  not stop the engine populating an empty volume from the helper image before
  mounting it, so without this an empty source volume could capture the
  helper image's files and a poured volume could hold them. That `-v` spelling
  is the one both engines accept: Podman's `--mount` rejects the long
  `volume-nocopy` option ("invalid mount option"), which Docker takes. The
  gated arm proved copy-up disabled on both engines, and the transfer arms
  proved the round trip in both directions. On Podman the
  helper also passes `--image-volume=ignore` (accepted, per the same run): an image-declared `VOLUME`
  becomes an anonymous mount on Docker (which cannot land under a per-run
  path) but an image volume on Podman, and the helper must never see one over
  or under its mount.

**The image is proved before anything runs in it.** The preflight runs
`tar --version` and then the verb's COMPLETE command against a scratch
directory at MNT -- the capture against an empty directory, the pour fed a
valid empty archive (two 512-byte zero blocks, because GNU tar exits 2 on
zero bytes and 0 on this). Both refusals name the IMAGE, which is the thing
the user can change. GNU tar is required and checked by its own banner: every
tar fact this format relies on is GNU tar's, so another tar refuses here
rather than producing a file the format then rejects. This is a
tool-availability preflight and nothing more -- byre's Debian-derived support
boundary stays what it is, a warning at develop and a failure at build
(ADR 0014).

**Leftover helpers are never silent.**

- **Cleanup is bounded.** On any failure or cancellation the verb
  force-removes the helpers carrying ITS run id, never another invocation's.
  EVERY engine call a cleanup path makes -- the label query, the `rm -f`,
  restore's rollback query and removal of the volume it was filling -- rides
  one 30-second deadline, because the path runs holding the setup lock with
  a config removal, a summary and the lock release still to come, and the
  failure that brought the verb there is often the engine itself. A daemon
  that never answers makes cleanup stop and say what may remain.
- **Detection is two label queries.** `byre.helper=<project id>` and
  `byre.helper.src=<project id>`; a hit refuses, naming the leftover. Seed
  and migrate helpers carry these labels too, so a byre killed mid-seed
  leaves a helper the sweeps can see. ADR 0054's exclusive-volume scan reads
  launch records and cannot see a helper at all; these queries cover it.
- **Every path that mounts or mutates a project volume runs both queries**:
  backup; restore (before the review and again under the lock); `develop`
  (under the setup lock, on the engine it is about to use, never on the
  pre-lock fast path -- so a develop that arrived during a backup waits and
  then runs); `reset`, `forget`, `rehome` (both the old and the new id); and
  the config editor's volume Clear.

## The file format

One gzip-compressed tar, one gzip member and nothing after it. Entry order is
fixed and restore refuses any other: `backup.toml` (the index), `byre.config`
(the config member), then `volumes/<name>.tar` per index row, in row order.
Each nested payload is a plain tar whose root is the volume root, captured
with `--format=posix --numeric-owner` so mtimes keep their nanoseconds.

**The index is a table of contents and a note from the source.** It carries
`format`, the writing byre version (`version.String()`, ADR 0016), the
minimum byre version that reads this format (a constant of the format, not
the running version: `v1.12.0`), the project folder name, the source engine,
a `config` row (bytes, sha256, credential state), one `[[volumes]]` row per
carried volume (logical name, bytes, sha256, entry count), and a
`references` table. Every key is present even when empty, so a reader never
guesses, and the shape is pinned by a golden. Reading is two passes: a lenient first pass takes only
`format` and `min_byre_version`, so a newer file's unknown keys still reach
the version sentence; the strict format-1 decode follows, refusing any
unknown key (ADR 0044).

**Derived, not trusted.** Every value a review prints as a fact about THIS
restore comes from content that was length-checked, digest-checked and walked
header by header: the symlink lists and dropped-entry lists from the
validation pass, the credential state from the verified config bytes, the
volume names from the outer member names (which must equal the index's rows
exactly), the destination's requirements from resolving the verified config
on THIS machine. The `references` table has no derived counterpart at all: it
is printed as "what the source saw" and nothing is asserted from it. A
sha256 per payload is INTEGRITY, not authenticity -- it detects corruption,
not authorship -- and the restore review says so in those terms (ADR 0051).

### The read budget

A few compressed kilobytes can ask a reader for terabytes of output, and gzip
is the one layer that does not declare its own decompressed size. The budget
is therefore explicit, in two phases: before the index is read, the index's
own bound (a megabyte, as for any config) plus framing slack; afterwards,
**the index's own measured length, plus the totals it declares, plus 64 KiB,
plus 4 KiB per outer entry**. The bound is HARD -- at most one byte past the
limit ever leaves the underlying reader -- because `archive/tar` reads every
header block with `io.ReadFull`, which discards a read's error whenever the
bytes it got complete the buffer; a reader that merely reported the overrun
would be ignored once per header, and a stream of zero-length local PAX
headers would expand without end. So the read is truncated to the remaining
room and exhaustion is sticky.

The 4 KiB per entry covers long-name PAX framing and member padding: a
255-byte logical volume name needs a PAX path record, which costs the
extended header's block, the block its record is padded into and the
ordinary header block -- 1536 bytes -- plus up to 511 bytes padding the
member's own content. The writer measures its own decompressed output
against the same figure and refuses before publishing, so a backup byre
produced is one byre reads.

The other bounds, all refusals: the index and the config member each within
`config.MaxConfigBytes`; outer entries exactly the index's (duplicate, extra,
missing or out of order); logical names passing the config volume-name
grammar, not `.` or `..`, unique, and within 255 bytes once joined to the
destination's project prefix; the declared payload total fitting the staging
filesystem before any payload is written; at most 1,000,000 entries per
payload, matching its declared count.

**Nothing may follow the gzip member.** Not a second gzip member, not loose
bytes, not another envelope. One consequence is worth stating because it will
be met: a backup repacked with plain GNU tar is REFUSED, because GNU tar pads
its output to a 10 KiB block. The file is byre's to read and byre's to write;
repacking it produces something byre does not accept.

### The nested-tar contract

Restore validates every payload in staging, header by header through
`archive/tar`, before any project state is written; a failure refuses the
whole restore, naming the volume and the entry. The pour then replays a
stream the validator REBUILDS entry by entry from name, type, link name,
mode, mtime and content, in PAX format -- never the file's own bytes. The
rules:

- **The root entry.** GNU tar's `./` directory header is accepted, not
  counted, and replayed as the root so the volume root's own mode and mtime
  land. A payload with no root entry is fine too.
- **`Name`.** A leading `./` is dropped; after that a leading `/` is refused,
  and `..`, `.` and empty components are refused. A trailing `/` comes off
  before comparison, and a name seen twice is refused. Every ancestor that
  appeared earlier must be a DIRECTORY entry -- an ancestor that is a
  symlink, a regular file or a hardlink refuses the payload, so no entry is
  ever written through a link or onto a file. An ancestor that does not
  appear is fine (tar creates it), and is a directory from then on: a later
  entry naming that path as anything else refuses.
- **Kinds.** Regular files, directories, symlinks and hardlinks are carried.
  A symlink's `Linkname` is verbatim and never resolved -- it is
  agent-authored content, and absolute or traversing targets are LISTED by
  path at both ends rather than rewritten (traversing means the relative
  target's lexical join with the link's own directory leaves the volume root;
  lexical only, nothing is resolved). A hardlink's `Linkname` passes the
  `Name` grammar and must name an EARLIER regular entry of the same payload,
  with the refusal naming which rule fired. FIFOs and character and block
  devices are dropped by name and listed. A global PAX header is accepted,
  not counted, its records applied to nothing, and absent from the rebuilt
  stream. Sparse entries refuse the payload, because `archive/tar` expands
  holes on read and the pour would materialise the full size. Every other
  type flag refuses: contiguous files, anything unknown, and sockets -- which
  have no type flag of their own in any tar standard, so the mode's file-type
  bits are the only spelling a hand-made archive has for one. GNU tar omits a
  socket at the source (it prints `socket ignored` and exits 0), so a socket
  header is not output byre wrote. The summary carries tar's own lines, which
  is where that socket list is read: captured under the same 64 KiB cap every
  helper's stderr gets, with one marker line among them naming how many bytes
  the cap dropped when it had to cut the list off -- a partial list says that
  it is partial.
- **GNU long names are accepted, and judged by the one name grammar.** The
  `'L'` and `'K'` vendor headers never reach byre's type switch at all:
  Go's `archive/tar` consumes them inside `Next` and substitutes their
  content into the following header. So a payload carrying them is accepted,
  with the substituted name and link name judged by exactly the grammar and
  ancestor rules above, and the rebuilt stream spelling them as PAX records.
- **One stated transformation, and nothing else.** The restored tree is the
  source tree with ownership replaced by the box identity (the pour's chown
  is the ownership step, and the pour extracts with `--no-same-owner` so an
  archived uid outside this machine's user namespace can never fail an
  extraction) and setuid, setgid and sticky zeroed IN THE REBUILT HEADER,
  which is the contract rather than a promise a chown would break. FIFOs and
  devices are absent. Every other mode bit, every mtime to the nanosecond,
  every size and every link target is preserved; `--delay-directory-restore`
  keeps a directory's archived mtime when the stream revisits it.

### Staging and publishing

Every payload is produced into a private staging directory,
`~/.byre/staging/<run id>/` at 0700 with 0600 files, opened through an
anchored root and named BY POSITION (`0.tar`, `1.tar`): no name the backup
file supplies ever chooses a path, so ADR 0040's rule holds without a
per-call-site judgement. It is deliberately never under the output directory,
which by default is the project tree the next box mounts -- volume bytes,
plaintext secrets included, do not pass through a path an agent can read. The
run id makes two concurrent verbs disjoint. The disk check covers both
filesystems, staging's and the output's. Staging is removed on every exit,
success or failure.

The file is published through a streaming variant of hostopen's exclusive
publish: temp-then-link anchored with `OpenDirRootNoFollow`, 0600, plus the
fsync of the file before the link and of the directory after it that the
existing publisher lacks. **The link is the commit point.** Every failure
before it publishes nothing and the output path never holds a partial file;
the one failure after it -- the directory fsync -- leaves the COMPLETE file in
place and exits 1 saying the file is written but its durability is
unconfirmed. A Ctrl-C while the payloads are streaming is one of the failures
before the link: the publish reads every payload through the run's
cancellation, so the write stops, the staged temp goes with it, and the exit is
a cancelled backup's -- helpers and staging cleared, nothing written. The
cancellation is checked once more immediately before the link, because the last
payload byte and the link are separated by the staged file's fsync and a Ctrl-C
there is past every payload reader. Once the link has happened the handler
comes off, because an interrupt can no longer unmake the file and one arriving
during the summary should kill byre the ordinary way. An existing entry of any
kind at the output path is a refusal, never an overwrite, so a second backup on
the same day refuses naming `--output`. ADR 0040's ancestor residual is
inherited as it stands, neither widened nor narrowed.

## Restore does the whole job

Restore writes the config, pours every carried volume that does not already
exist on the engine, and **builds nothing**. Nothing the backup carries waits
for the first develop.

- **It pours from the effective BASE image**, not a built image -- an unset
  `base` being whatever `gen.DefaultBase` names (ADR 0060), as the generator
  resolves it. The base is pulled if absent and PROVED by running the
  complete pour command in it, fed a valid empty archive, before any project
  state is written. This is new in kind and worth saying out loud: restore
  executes `base` as a byre-driven tool, which no other verb does (ADR 0014).
- **It refuses an existing config** ("this project already has a config;
  restore into a fresh checkout") and **a linked worktree** ("restore in the
  main worktree"): a linked worktree is its main tree's project (ADR 0009),
  and restoring the main tree's state from a side checkout whose main tree
  may be missing or unconfigured has no good answer. Backup FROM a linked
  worktree stays allowed -- it is a backup of the project, named for the main
  directory.
- **The target must be an empty directory or the clean root of a git
  checkout**, and anything else refuses before the file is opened: "byre
  restore expects an empty directory or the clean root of a git checkout.
  <DIR> is neither: <reason>". Restore with no DIR takes the current
  directory, and restricting the target prevents enrolling an unrelated
  nonempty directory (HOME, say) as the project. Empty
  means no entries at all, dotfiles counted (a directory restore itself just
  created is empty by construction). A clean root means DIR IS the top level
  of a working tree (`git -C DIR rev-parse --show-toplevel` resolving to DIR
  by file identity, never by string) and `git -C DIR status --porcelain`
  printing nothing; a subdirectory of a checkout is not a root, and the
  refusal names the root it found. The git probes ride the standing host-git
  shape (pinned resolver, 5s bound, capped output), and no git, a probe that
  fails and a probe that times out all land where a dirty tree does: byre
  cannot prove the tree clean, so it refuses and says that is why.
  **`--allow-nonempty` is the switch** over a refusal that protects byre's own
  guess at where the project is (PRINCIPLES.md P1). With it restore proceeds
  and STATES what it would have refused over, in the review above the "Names
  this machine must satisfy" block and again in the summary ("restoring into
  <DIR>, which is not empty (--allow-nonempty): <reason>").
- **It creates DIR first**, before resolving the project's identity, because
  identity is computed from the real directory and `Canonicalize` falls back
  to the cleaned pathname for a path that does not exist -- an alias path
  would otherwise yield one id now and another after creation. Every exit
  before the store is bootstrapped removes that directory again if restore
  created it and it is still EMPTY (`rmdir` semantics, so nothing a user put
  there is ever touched). An exit after the bootstrap leaves the directory
  and the enrolled store, as apply's same window does by design, and says so,
  naming both by path; the next restore of that path proceeds.
- **One lock hold covers the config write and every pour**, so a develop
  waiting on the lock sees the config and every poured volume together.
  Under that hold, restore revalidates before committing: enrolment (a
  forget that won the lock meanwhile is cancellation, not permission to
  write into an emptied store), then the review text re-rendered from a
  fresh resolution and **compared byte for byte** (so a grant-only change
  in a layer refuses exactly like a volume change), then config absence,
  volume existence for both the create and keep sets, ownership in both
  directions, and helper absence. Any difference is the re-run refusal.
- **A volume that already exists on the engine is kept**, and the summary says
  the backed-up copy was dropped. A carried name THIS machine's resolved set
  declares machine-scoped is not poured -- develop here would mount the
  machine volume and the poured project volume would be an orphan nobody
  reads -- and the review says so by name while the payload stays in the
  file. A carried name this set declares `cache` is poured anyway, with a
  note. A volume the config declares that the backup does not carry is left
  to the first develop, which handles it as any fresh volume: seeded, or
  started empty (ADR 0013). A RESTORED volume that is empty is populated at the first
  develop from the box image's directory at its mount point, exactly as a
  fresh volume is -- the pour keeps helper-image bytes out, the session mount
  behaves as it always has, and the review says so for an empty payload.
- **The destination's ownership check runs in both directions**, computed as
  if this project were not enrolled: a physical name a longer known project
  id claims refuses, and a physical name that already exists and that any
  OTHER known project would list refuses. `projectVolumes`' longest-id rule
  hands a disputed name to the longer id, so once this id has a store
  directory -- on a retry, or after the bootstrap -- the shorter project's
  listing would no longer show the collision, which is why the check takes an
  "ignore this id" parameter.
- **Restore is terminal-only.** Its screen IS the apply review it extends
  (apply's fresh-store credentials annotation reads "this backup brings" on
  this path), plus three sections before the grant summary: "Names this
  machine must satisfy" (from the destination's resolved set, each with what
  reads it and how it fails, a disabled mount omitted because nothing will
  bind it, and each MARKED "(missing on this machine now)" when a degrading
  probe says the path is not there -- a mark, not a gate: restore still
  refuses on none of them, and a probe that could not answer marks nothing), "What the source saw" (the
  index's references table, printed as such), and "From the backup" (every
  volume in play by logical name, the credential state of the carried file,
  each payload's absolute and traversing symlink targets as agent-authored
  content, each payload's dropped entries, and the line that the file's
  authorship is not proven). One y/n. No new config key, so nothing P0 binds;
  an editor-style backup form is a logged follow-up on its own merits.

## Partial failure

Restore can still fail part way after the base is proven: Ctrl-C, the engine
going away, the engine's disk filling, a volume that cannot be created. A
failed restore removes its config and the incomplete volume so the user can
re-run it; `forget` is the fallback, not the first remedy.

The volume CREATE is watched like the pour is: a daemon stalled there would
otherwise take the interrupt with nothing to act on it and sit holding the setup
lock. Nothing byre can do makes that call return -- there is no container to
take away -- so the wait for it rides the same 30-second cleanup deadline, and
the rollback then removes the volume if the create landed after all. If it has
NOT landed, the rollback says so rather than reporting nothing: the engine may
still create that volume once byre has gone, a re-run would then keep whatever
occupies the name and drop the backed-up copy of it, so the `volume rm` line to
run before re-running is printed with the forget fallback.

A failed pour, all under the lock restore still holds: force-remove the
helper, remove the volume being poured, remove the config restore wrote. A
volume counts as poured WHOLE only once its extraction AND its chown have
both succeeded, so a chown failure is a failed pour and the volume goes. What
stays: volumes poured whole before the failure (the next restore meets them
as "exists here; kept", and their content is the backup's), the project
directory, the store's path record, and any pulled image. The summary names
the volume, the cause and what stayed, and says to fix the cause and run
`byre restore` again.

Each of the three removals has its own failure sentence, because each leaves
something different behind. A volume that cannot be removed (the engine is
gone): the summary prints the engine's `volume rm` line for it and says a
re-run will keep whatever occupies that name until it is removed. A config
that cannot be removed: the summary says the project now has a config and the
next restore will refuse until it is gone. A helper that cannot be removed:
the summary names the container and its `rm -f` line, and the sweeps above
refuse until it is gone. `byre forget`, run in the project directory, is
named as the FALLBACK in each case and only as that, with the warning that it
removes every volume and image of the project, kept ones included, prompts,
and refuses while any installed engine cannot be queried.

## Two things byre deliberately does not do

**No login warning.** The agent state volume carries the box's login file,
and two boxes holding one rotating token is exactly ADR 0007's observed
failure: the first refresh on either side logs the other out. byre says
nothing about it at the prompt. This is P1 read straight -- the threat model
is the agent, never the user, and a user moving their own box is making a
choice byre has no standing to second-guess. It is disclosed on the
security-model page, where the residuals live, not as a gate.

**No archive encryption.** The user protects the file with their own tools.
Volume contents may hold plaintext secrets and the credentials switch makes
no claim about them; the file is 0600 and that is the whole of it.

## Exit codes

ADR 0022's contract, unchanged: usage errors exit 2 and never dispatch.
Refusals exit 1, the off-terminal restore refusal included, as `preset
apply`'s does without `--yes`. Declining the backup preview or the restore review exits 0, as
apply's decline does. Declining an offered package install is not a clean end
-- it is the reason the package is still missing -- so restore then stops as
for any missing package, exit 1.

## Accepted residuals

Each of these is also on the user-facing security-model page, where a user
can find it:

- **A backup is an unencrypted 0600 file of the project's volumes.** Anything
  plaintext inside a volume is plaintext in the file; `--no-credentials` is
  about the config file only.
- **Stillness is cooperative.** The sweep covers containers byre can see at
  the moment it asks; a hand-run engine command can start a box during a
  backup, outside the setup lock and outside what byre can serialize.
- **An engine the user ignored is not checked.** `--ignore-<engine>` leaves a
  box of this project there unruled-out -- Docker live-restore or a remote
  Podman can keep one running while the daemon is unreachable -- and its
  project volumes out of the file.
- **A file's authorship is not proven.** The digests detect corruption, not
  authorship, and the restore review says so.
- **A restored agent login shares its rotating token with the source box.**
  ADR 0007's observed failure, and the user's call to make.
- **A layer can change under its own lock during a copy.** Named layers have
  their own lock, which the project's setup lock does not cover, so the one
  resolution the file records can be stale by the time the last payload is
  captured.
- **A crash byre cannot catch runs no cleanup.** SIGKILL or power loss leaves
  what was in flight: the next restore refuses on the config if it was
  written, a surviving helper is caught by the `byre.helper` queries, and a
  staging directory under `~/.byre/staging` may be left holding volume bytes.
  byre cannot tell a crashed verb's leftovers from a concurrent verb's live
  staging, so the next verb NAMES a stale one and leaves it.
- **A same-named project volume on an engine restore did not pick is not
  consulted.** Restore reads its destination engine only; it is not a totals
  command.
- **A project enrolled after restore's ownership re-check is outside it.**
  The under-lock re-check is the cross-project boundary, and it is a moment,
  not a hold on every other project's store.
- **A helper that outlives an engine outage is named, not removed.** The
  summary prints its `rm -f` line and the sweeps refuse until it is gone.

The engine-side half has one gated arm, `TestIntegrationBackupRestoreRoundTrip`
(`BYRE_DOCKER_TESTS=1`, `internal/commands/integration_backup_test.go`): a real
volume of specials captured on a live engine and poured into a fresh project,
the two trees compared under the stated transformation, with the Docker<->Podman
transfer sub-arm skipping aloud when only one engine is installed.

[arm: TestBackupRoundTripsThroughTheReader,
TestBackupRefusesUnlessTheProjectIsCompletelyStill,
TestBackupCaptureHelperSpecIsPinned,
TestBackupNoCredentialsStripsTheCopyAndLeavesTheSource,
TestWriteThenReadRoundTrips,
TestReadRefusals,
TestNestedTarRefusesNameGrammar,
TestNestedTarRefusesEntriesUnderNonDirectories,
TestRebuildReplaysTheContractedTree,
TestNestedTarJudgesAGNULongNameByTheSameGrammar,
TestBudgetSurvivesReadFullAndBoundsTheSource,
TestRestoreCommitWritesTheConfigAndPoursTheVolumes,
TestRestoreProvesTheBaseBeforeWritingAnything,
TestRestoreRefusesWhenTheReviewChangedUnderTheLock,
TestRestorePourFailureRemovesTheVolumeAndTheConfigAndKeepsWhatLanded,
TestRestoreStopsOnAStillMissingPackage,
TestRestoreRefusesABadPayloadBeforeAnyProjectState,
TestRestoreNeverEntersTheCredentialDecryptPath]
