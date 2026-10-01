# byre backup / byre restore

Status: v1 design, 2026-10-01. This replaces the box export/import design
(v5, `wip/box-export-import.md`, deleted in the same commit; git history
keeps it). The restart came out of a three-reviewer round on v5 (Codex,
Z.AI, Grok, 2026-09-21) and the grill that followed, where Pete cut the
feature down to what people actually want back: the agent's memory and
context, the credentials, and the project's config. Every ruling below is
Pete's. Not yet authorization to implement: TODO.md owns task status.
wip/ lifecycle: absorbed into an ADR and the docs on ship, then deleted.

Provenance: [RULING] = Pete's, settled, do not reopen to close a finding.
[CODE] = verified against the tree on 2026-10-01. [PROPOSED] = this
design's mechanism.

## What this is

`byre backup` writes one file holding a project's config and its agent
state volumes. `byre restore` takes that file and makes a fresh project
from it on this machine or another: config written, volumes filled,
ready for `byre develop`. A backup is your own box coming back to you.

## Rulings

- [RULING] Scope is the project's config file, the agent's state volume,
  and any other PROJECT-scoped state volume named with `--volume NAME`.
  Nothing else: no workspace (git carries it), no built image, no
  ephemeral container filesystem, no layers, no skills, no packages.
- [RULING] Machine-scoped volumes are never offered, whether a skill or
  the project config declares them -- the rule reads the resolved scope,
  not the declarer. Every machine-scoped volume byre ships is identity
  state (a login or a key); the destination logs in once (ADR 0017's
  intended path). Cache-role volumes are never offered either.
- [RULING] The config travels byte-for-byte. Backup rewrites nothing in
  it, with one exception: `--no-credentials` deletes the `[credentials]`
  block and every credential row. Host paths, `engine`, `worktree_base`,
  `extends`, seeds: all carried as they are. Restore lists what this
  machine must satisfy; develop fails loudly where it does not, exactly
  as it does today.
- [RULING] Credentials travel encrypted under the config file's own
  passphrase. The restorer needs that passphrase and may rotate it after
  restore with `byre credentials rekey`. Backup never decrypts anything.
  "Include credentials" is on by default.
- [RULING] Layers (`extends`), templates, skills and the agent are
  references: they must exist on the destination. A layer is a plain
  file the user sends alongside the backup (ADR 0035's distribution
  story). Backup names each one it saw; restore's review shows the
  install command for a missing skill and offers to run it, as
  `byre preset apply` does, and stops at a missing layer with the path
  to create, as every load does.
- [RULING] Backup requires a completely still project: no container
  labelled for this project in any state, on any engine byre can reach,
  any worktree. Backup holds the project setup lock from that check
  through the end of the copy. Refusals print the engine's attach and
  stop commands; byre never stops a session on the user's behalf. The
  source engine unreachable is a refusal (nothing to stream from); a
  cleanly-unreachable other engine is skipped and disclosed; any other
  query failure is fatal, as develop's cross-engine check already has it
  (ADR 0004). The disclosed residual is a hand-run engine command.
- [RULING] Restore does the whole job itself: writes the config, pours
  the volumes, builds nothing. Nothing waits for the first develop.
- [RULING] Restore pours volumes using the config's BASE image, not a
  built image. The generated Dockerfile begins with `apt-get`, so every
  base is Debian-family and carries `tar` and `chown` [CODE: gen.go].
- [RULING] Restore refuses to run on a project that already has a
  config: "this project already has a config; restore into a fresh
  checkout". A volume that already exists on the engine is kept and the
  summary says the backed-up copy was dropped. There is no re-run story
  because nothing can leave restore half-done except the engine being
  unreachable, which stops it before it writes anything.
- [RULING] No archive encryption. The user protects the file with their
  own tools. Volume contents may contain plaintext secrets and the
  credentials switch makes no claim about them.
- [RULING] No login warning and no ADR 0007 change. The agent volume
  carries the box's login file; two boxes holding one rotating token is
  the user's problem, not byre's.
- [RULING] Symlinks travel with their target stored verbatim. The tar
  validator constrains `Name` only (leading `/` refused, no `..`, no
  empty components); `Linkname` is unconstrained bytes written no-follow
  and never dereferenced. Hardlinks only to an already-materialised
  regular entry of the same payload. Absolute and traversing targets are
  listed by path at backup and again at restore.
- [RULING] FIFOs, devices and sockets inside a volume: restore skips them
  by name and the summary lists them. No prompt at either end.
- [RULING] Verbs: `byre backup [DIR]` and `byre restore FILE [DIR]`. DIR
  is the project directory and defaults to the current one; restore
  creates it when absent. Projects have no names in byre (identity is
  the directory path), so there is no lookup by name.
- [RULING] Default output is `<folder>-<YYYY-MM-DD>.byre-backup.tar.gz`
  in the current directory; `--output PATH` elsewhere. An existing file
  is never overwritten.
- [RULING] Flags only in this cut. On a terminal, backup shows a preview
  and asks y/n (`--yes` skips); off a terminal it just runs. Restore is
  terminal-only, like `byre preset apply`, which it extends. An
  editor-style backup form is a logged follow-up, not part of this unit.
- [RULING] Backup from a linked worktree is allowed; it is a backup of
  the project (ADR 0009). The container check covers every worktree's
  box because it asks by project label.
- [RULING] One gzip tar, index first, nested plain tars, SHA-256 per
  payload. Glossary term: "backup". (Not "manifest": the glossary
  reserves that word for a package's `[package]` table.)
- [RULING] One shipping unit, one review loop, one engine-side run.

## What already exists [CODE]

- Volumes: every bundled agent skill declares a project-scoped state
  volume (`.claude`, `.codex`, `.gemini`, `.grok`, `.opencode`); every
  `*-shared-auth` companion and this repo's inttest skill declare a
  machine-scoped identity volume under `/home/dev/.byre-identity/`. No
  skill declares a cache-role volume. Claude's memory, transcripts,
  Codex's sessions and history all live in the project-scoped volume.
  Physical names: project `byre-<id>-<name>`, machine
  `byre-machine-u<uid>-<name>`, chosen by `Volume.MachineScoped()`
  (naming.go); names carry no engine component.
- Project identity is a hash of the canonical directory; a linked
  worktree resolves to the main worktree's id (project.go Resolve). The
  store `~/.byre/projects/<id>/` holds `byre.config` as its only authored
  file; the build context, launch records, path record and lock are
  derived.
- Credentials (ADR 0057): a file-local `[credentials]` block holds an
  scrypt-passphrase-wrapped age identity and its cleartext recipient;
  rows are age-encrypted to that recipient inside the same file.
  `byre credentials rekey` rotates one file's passphrase and leaves every
  value blob byte-identical. Nothing decrypts outside a launch.
- `byre preset apply` (preset.go): refuses off a TTY, resolves the
  extends chain, reports every missing package with the install command
  from its `[sources]` hint and runs each install under its own y/n,
  renders the grant review with a diff against the current store, writes
  the project config on confirm. An explicit path or URI is read through
  `packages.Fetcher.FetchManifest`, bounded by `packages.MaxManifestBytes`
  (256 KiB), not the 1 MiB config bound. Restore must hand apply the
  config BYTES it already verified, read under `config.MaxConfigBytes`,
  rather than a path.
- Named layers live at `~/.byre/layers/<name>/layer.config`; a missing one
  fails every load with "layer X not found -- create <path>"
  (layers.go:146).
- Host paths at develop: a mount rides `--mount type=bind`, which the
  engine refuses when the source is missing (runparams.go:157 relies on
  it); a `[[context]]` file is read at bake and a missing one fails the
  build naming context and path (build/context.go); a `[[claude_skills]]`
  path is validated as a skill directory at bake (planClaudeSkills).
- Seeding (seed.go, runner SeedVolume/SeedFiles/SeedLiteral): every seed
  function takes the image as a parameter; the copy runs as root inside
  the box's userns and ends with a recursive chown to the box identity;
  a failed seed removes the volume it created; an existing volume is
  left alone. `SeedLiteral` streams content over stdin with no host bind.
- The setup lock (internal/lock): one `flock` per project store with an
  inode requeue; exclusive only. Develop holds it from prepare through
  container Create; reset and forget take it fail-fast (`TryAcquire`);
  the editor's volume Clear takes it. Develop's cross-engine check
  (`crossEnginesToCheck`, `refuseCrossEngineSession`, develop.go) asks
  other installed engines for a project container in any state, skips a
  cleanly-unreachable one with a note and fails on any other error.
  `reportRunning` prints attach/shell/stop remedies for the engine in use.
- `byre config` opens the project's config file directly, shows a missing
  skill as a row with its install command, and degrades (no volume
  screen, no inherited rows) when the engine or a template cannot be
  resolved, rather than refusing to open.
- Nested-tar hygiene exists in internal/deliver/tar.go: no `..`, no empty
  components, anchored writes (ADR 0040). Its splitter STRIPS a leading
  `/` where this design refuses; it never validates `Linkname`, which
  the symlink ruling makes the correct behaviour.
- GNU tar stores a FIFO and silently drops a socket, exit 0 both times
  (probed 2026-09-21).

## The backup file [PROPOSED]

One gzip-compressed tar. Entry order is fixed and restore refuses any
other:

1. `backup.toml`, the index: `format = 1`, the writing byre version, the
   project folder name, the source engine (information only), a `config`
   row (byte count, sha256, whether credentials are included), one
   `[[volumes]]` row per included volume (logical name, target, byte
   count, sha256, the list of absolute or traversing symlink targets),
   and a `references` table naming what the destination must provide:
   the `extends` layer if any, the template, the agent, each skill, each
   mount host, each context file, each Claude Skill path, each seed host,
   and `engine` and `worktree_base` when set.
2. `byre.config`: the project config, byte-for-byte, or with the
   credentials block and rows deleted through tomldoc under
   `--no-credentials`.
3. `volumes/<name>.tar`: one plain tar per included volume, the volume
   root as the tar root, written inside a container with
   `--numeric-owner`; restore discards ownership (the seed chown is the
   ownership step).

Every payload is produced into a private 0600 staging directory and
hashed there before the outer tar is written index-first. The published
file is 0600 and is created exclusively: an existing file at the output
path is a refusal, never an overwrite. Restore reads the index first
(bounded like a config file), refuses an unknown `format` naming the
required byre version, then verifies every payload's length and sha256
into staging before anything is written to its final place. One read
budget: the decompressed stream is bounded by the index's declared totals
plus a stated tar-overhead allowance, the entry count is bounded, staged
bytes are checked cumulatively and against free disk, and duplicate,
extra or missing outer entries and nonzero trailing data are refused.
Digests detect corruption, not authorship, and the restore review says
so. One nested-tar validator for the volume payloads: `Name` constrained
as ruled, `Linkname` verbatim, regular files, directories, symlinks and
in-payload hardlinks materialised, every other entry kind skipped by name
and listed. Every string the file supplies that a review or error prints
goes through the existing terminal-data funnels (P4). Host writes ride
hostopen's anchored operations, no-follow parents.

## byre backup [PROPOSED]

`byre backup [DIR] [--output PATH] [--volume NAME]... [--no-credentials]
[--yes]`

1. Resolve the project from DIR (default: current directory; a linked
   worktree resolves to its project). Refuse if the project has no
   config.
2. Resolve the config and the volume set. Offered volumes: every
   project-scoped state-role volume in the resolved set that exists on
   the source engine. The agent's own state volume is always included;
   `--volume NAME` adds another; a name outside the offered set is
   refused naming the offered ones.
3. On a terminal, print the preview and ask y/n unless `--yes`: the
   output path; the config with its credential row count or "credentials
   dropped"; each volume with its estimated size; each machine-scoped
   volume as "not carried; the destination binds its own machine-scoped
   <name> volume"; each unselected project volume as "not carried
   (--volume <name> adds it)"; the references list; the stillness
   requirement. Off a terminal, no preview and no prompt.
4. Take the project setup lock. Ask every reachable installed engine for
   a container labelled for this project in any state; refuse on a hit
   with that engine's remedies; the source engine unreachable refuses;
   a cleanly-unreachable other engine is disclosed and skipped.
5. Under the lock: re-read the config (the editor's save takes the same
   lock, so a save that landed while backup waited is what gets copied),
   apply `--no-credentials` to a copy if asked, write it to staging and
   hash it. For each selected volume, run a short-lived container from
   the project's BUILT image when present, else the base image (either
   has tar; this is the one place the built image is preferred, because
   it is already local), the volume mounted read-only at one path, no
   network, no host bind, entrypoint overridden to `tar --numeric-owner`
   on stdout, into staging, hashed there. Record each payload's absolute
   and traversing symlink targets while streaming.
6. Write the index and the outer tar, publish exclusively at the output
   path, release the lock, print the summary: file name, actual bytes,
   volumes carried, the lists.

Cancel at the prompt leaves no file. A refusal after staging began
removes the staging directory.

## byre restore [PROPOSED]

`byre restore FILE [DIR]`, terminal only.

1. Probe the engine the destination would use (the config's `engine`
   when set, else detection). Unreachable: refuse, nothing written,
   "start docker, then run byre restore again".
2. Resolve DIR (default: current directory; created when absent). If the
   project store already has a config, refuse: "this project already has
   a config; restore into a fresh checkout".
3. Read the index, verify every payload into staging.
4. Hand the verified config bytes to the apply review as the proposal.
   The review is apply's, plus two sections before the grant summary:
   "Names this machine must satisfy", listing every reference from the
   index with what happens when it is missing (a layer: restore stops
   now; a skill: the install command, offered now; a mount host, context
   file, Claude Skill path, seed host: develop fails naming it; engine
   and worktree base: develop fails naming it); and "From the backup",
   listing each volume with "will be restored" or "exists here; the
   backed-up copy is dropped", the credential state, each payload's
   absolute and traversing symlink targets as agent-authored content, and
   the line that the file's authorship is not proven. One y/n.
5. On confirm: write the config through apply's own path. Then, under the
   project setup lock, for each volume absent on the engine: create it
   and stream its tar into a seed container over stdin, the SeedLiteral
   shape taking a reader, image = the base image (pulled if absent),
   skipping FIFO, device and socket entries by name; the existing
   recursive chown finishes it. A failed pour removes the volume it
   created and restore stops naming the volume; the config stays
   written.
6. Print the summary: config written, N volumes restored, skipped
   entries, dropped volumes, "next: byre develop".

Declining writes nothing and stages nothing; a skill installed during the
review stays installed, as apply already has it.

## Doctrine

- New ADR: box backup (this design), citing P1, P4, P5, P7.
- 0004: backup's stillness check is develop's cross-engine query, same
  skip-and-disclose shape; session discovery is unchanged.
- 0007 and 0017: unchanged. Login state inside a project volume travels
  because the user selected the volume; byre still reads no host
  credential and seeds no login.
- 0009: worktrees allowed at backup; restore runs no git.
- 0017: machine-scoped volumes never travel; no narrowing, no amendment.
- 0030: the config travels byte-for-byte, closures included, so the
  restored allowlist equals the source's by construction.
- 0035: a layer is a plain file the user sends; the backup carries the
  pointer only.
- 0040: file-supplied names never choose a host path; anchored writes.
- 0044: the index is TOML through the one library; the credentials
  deletion rides tomldoc.
- 0047: restore spawns no host tool over anything it wrote.
- 0051: digests are integrity, not authenticity, and the review says so.
- 0054: exclusive-volume checks are unchanged.
- 0057: unchanged. Credentials stay encrypted end to end; byre's
  decrypted plaintext still goes to exactly one place.
- 0058: a later `develop --agent` override is irrelevant; the volumes
  already exist.
- P0: the backup form is logged as a follow-up; restore's screen is the
  existing apply review.
- P4: every list above (references, dropped volumes, symlink targets,
  skipped entries) is printed, never silent.
- P7: no new dependency; the base image's tar is the pour tool.
- Security-model page: a backup is an unencrypted 0600 file of whatever
  volumes the user selected; the cooperative stillness residual; file
  authorship unproven.
- GLOSSARY: "backup" (the file and the verb pair).

## Evidence before done

- Round trip: backup then restore into a fresh checkout yields a config
  byte-identical to the source's, credentials decrypting under the
  source passphrase at the first develop, and `byre credentials rekey`
  working on the restored file.
- `--no-credentials` leaves no ciphertext, no identity and no
  `[credentials]` block; the restore review says credentials are absent.
- Volume selection: agent volume always; `--volume` adds; unknown name
  refused naming the offered set; machine-scoped and cache volumes never
  offered and the preview names the machine-scoped ones.
- Stillness: a container in any state on the source engine refuses; a
  container on another reachable engine refuses with that engine's
  remedies; an unreachable non-source engine is disclosed; an unreachable
  source engine refuses; a develop arriving mid-backup waits on the lock
  and runs afterwards; the config copied is the under-lock re-read.
- File: payloads hashed before the index is written, index-first order,
  format refusal, digest mismatch refusal for every payload including
  the config, hostile Name refused, absolute and traversing Linkname
  carried no-follow and listed, special entries skipped and listed,
  ownership discarded, read budget (index bound, decompressed bound,
  entry count, cumulative bytes, trailing data, duplicates), 0600 modes,
  escaped strings, existing output file refused, dated default name.
- Restore: engine down refuses before any write; existing config
  refuses; missing layer stops at the review; missing skill offered for
  install; volumes poured from the base image with the box identity's
  ownership; existing volume kept with the line; failed pour removes its
  volume and leaves the config; `byre config` opens cleanly on the
  restored project; `byre develop` on the restored project fails on a
  missing mount host, context file or Claude Skill path with today's
  messages.
- Off-terminal: backup runs without a prompt; restore refuses.
- Worktree: backup from a linked worktree backs up the project.
- Gated (byre-inttest, Docker and rootless Podman): a real state volume
  streamed out and poured back, then mounted by a develop with the
  content intact and owned by the box identity.

## Hand-off notes for the implementer

Read `CLAUDE.md` for the conventions that bite: plain `os` filesystem
calls are banned outside `internal/hostopen` (every path the file, the
store or the project names is agent-influenced); contracts pin
byte-exact, behaviour asserts the rule that fired; `gofmt` + `go vet` +
`go test ./...` green before every commit; the docs sweep (README,
ARCHITECTURE, GLOSSARY, the commands page pin, the security-model page,
CHANGES) is part of the unit. Two reviewers, independently, each given
the doctrine-index instruction verbatim; findings that touch a ruling go
to Pete. `byre-inttest` before done, never piped.

Suggested new package: `internal/backup` for the index, the file
writer and reader with its budget, the nested-tar validator, and the
symlink and special-entry listings. Command handlers stay Streams
adapters in `internal/commands`; cobra wiring in `cmd/byre` (ADR 0022).
Reuse: the apply review needs an entry point that takes config bytes;
the seed runner needs a variant that takes an `io.Reader`; everything
else (cross-engine check, reportRunning, setup lock, tomldoc deletion,
hostopen exclusive publish) is called as it is.
