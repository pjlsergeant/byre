# byre backup / byre restore

Status: v1 design, 2026-10-01, revised the same day after a three-reviewer
sign-off round (Codex, Grok, Z.AI, all NOT READY; findings in the review
log). This replaces the box export/import design (v5,
`wip/box-export-import.md`, deleted in commit a1edcc20; git history keeps
it). The restart came out of a three-reviewer round on v5 (2026-09-21)
and the grill that followed, where Pete cut the feature down to what
people actually want back: the agent's memory and context, the
credentials, and the project's config. Every ruling below is Pete's. Not
yet authorization to implement: TODO.md owns task status. wip/
lifecycle: absorbed into an ADR and the docs on ship, then deleted.

Provenance: [RULING] = Pete's, settled, do not reopen to close a finding.
[RULING 2026-10-01] = Pete's, given while working through the sign-off
findings. [CODE] = verified against the tree on 2026-10-01, after the
reviewers' corrections. [PROPOSED] = this design's mechanism.

## What this is

`byre backup` writes one file holding a project's config and its state
volumes. `byre restore` takes that file and makes a fresh project from it
on this machine or another: config written, volumes filled, ready for
`byre develop`. A backup is your own box coming back to you.

## Rulings

- [RULING] Scope is the project's config file and the project's state
  volumes. Nothing else: no workspace (git carries it), no built image,
  no ephemeral container filesystem, no layers, no skills, no packages,
  no other store files (the applied marker, engine records and launch
  records are this machine's history).
- [RULING 2026-10-01] Every project volume that exists on the source
  engine comes along unless the user says otherwise: `--no-volume NAME`
  drops one. That includes a volume made by a one-off `develop --agent
  grok` run whose agent is not in the config: it is the project's state
  and the user expects it back. The only volumes never carried are the
  ones the resolved config declares with `role = "cache"`.
- [RULING] Machine-scoped volumes are never offered, whether a skill or
  the project config declares them -- the physical name says which they
  are (`byre-machine-u<uid>-...`), not the declarer. Every machine-scoped
  volume byre ships is identity state (a login or a key); the destination
  logs in once (ADR 0017's intended path).
- [RULING] The config travels byte-for-byte. Backup rewrites nothing in
  it, with one exception: `--no-credentials` deletes the `[credentials]`
  block and every credential row from the copy that goes in the backup
  file. Host paths, `engine`, `worktree_base`, `extends`, seeds: all
  carried as they are. Restore lists what this machine must satisfy; the
  verbs that read each one fail or degrade exactly as they do today.
- [RULING 2026-10-01] `--no-credentials` is a statement about the backup
  file and nothing else. Other files on either machine (a shared layer
  the user copies across by hand, say) may hold credentials of their own;
  that is not this tool's business and it says nothing about it.
- [RULING] Credentials travel encrypted under the config file's own
  passphrase. The restorer needs that passphrase and may rotate it after
  restore with `byre credentials rekey`. Backup never decrypts anything.
  "Include credentials" is on by default.
- [RULING] Layers (`extends`), templates, skills and the agent are
  references: they must exist on the destination. A layer is a plain
  file the user sends alongside the backup (ADR 0035's distribution
  story). Backup names each one it saw; restore's review handles a
  missing package exactly as `byre preset apply` does (an install offered
  where the config carries a `[sources]` hint, "install it yourself"
  where it does not) and stops at a missing layer with the path to
  create, as every strict load does.
- [RULING] Backup requires a completely still project: no container
  labelled for this project in any state, on any installed engine, any
  worktree. Backup holds the project setup lock from that check through
  the end of the copy. Refusals print the engine's remedies; byre never
  stops or removes a session on the user's behalf. An engine byre cannot
  account for (unreachable, a declined binary, a failed query) is a
  refusal: backup speaks in totals, like reset and forget, and an engine
  it could not inspect cannot be declared idle. The disclosed residual is
  a hand-run engine command.
- [RULING] Restore does the whole job itself: writes the config, pours
  the volumes, builds nothing. Nothing waits for the first develop.
- [RULING] Restore pours volumes using the config's BASE image, not a
  built image.
- [RULING 2026-10-01] Restore proves the base image before it commits:
  it pulls the image if absent and runs `tar --version` in it, before
  anything is written. A base that cannot be fetched or has no tar
  refuses, naming the base. byre's support boundary is Debian-derived
  bases (config reference, ADR 0014); today that boundary only ever
  showed at build time, and restore is a second place it shows.
- [RULING 2026-10-01] Restore can still fail part way, after the base is
  proven: the user presses Ctrl-C, the engine goes away, the engine's
  disk fills. A failed pour removes the volume it created, restore stops,
  and the summary names the volume and prints the recovery: `byre
  forget`, then `byre restore` again. Forget removes the project's
  volumes and store and leaves the project tree alone, so the second
  restore meets a fresh project. No new mechanism.
- [RULING] Restore refuses to run on a project that already has a
  config: "this project already has a config; restore into a fresh
  checkout". A volume that already exists on the engine is kept and the
  summary says the backed-up copy was dropped.
- [RULING] No archive encryption. The user protects the file with their
  own tools. Volume contents may contain plaintext secrets and the
  credentials switch makes no claim about them.
- [RULING] No login warning. The agent volume carries the box's login
  file; two boxes holding one rotating token is the user's problem, not
  byre's. The new ADR records how this sits beside ADR 0007 (see
  Doctrine); 0007 itself is not reopened.
- [RULING] Symlinks travel with their target stored verbatim. The tar
  validator constrains `Name` only; a symlink's `Linkname` is
  unconstrained bytes, never dereferenced by byre. Hardlinks only to an
  already-seen regular entry of the same payload. Absolute and
  traversing symlink targets are listed by path at backup and again at
  restore.
- [RULING, adjusted 2026-10-01] FIFOs and devices inside a volume reach
  the archive; restore drops them by name and the summary lists them.
  Sockets never reach the archive (GNU tar skips them at the source), so
  backup's summary lists them and restore has nothing to say. No prompt
  at either end.
- [RULING] Verbs: `byre backup [DIR]` and `byre restore FILE [DIR]`. DIR
  is the project directory and defaults to the current one; restore
  creates it when absent. Projects have no names in byre (identity is
  the directory path; a linked worktree is its main tree's project), so
  there is no lookup by name.
- [RULING] Default output is `<folder>-<YYYY-MM-DD>.byre-backup.tar.gz`
  in the current directory; `--output PATH` elsewhere. An existing file
  is never overwritten: a second backup the same day refuses naming
  `--output`.
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
  bundled skill declares a cache-role volume. Claude's memory,
  transcripts, Codex's sessions and history all live in the project-
  scoped volume. Physical names: project `byre-<id>-<name>`, machine
  `byre-machine-u<uid>-<name>` (`scopedVolumeName`, naming.go, branching
  on `Volume.MachineScoped()` in config.go); names carry no engine
  component and volumes carry no labels. Reset and forget enumerate a
  project's volumes by the `byre-<id>-` prefix (`projectVolumes`,
  `Runner.VolumesByPrefix`), on every installed engine, which is how a
  volume outside today's resolved set is found.
- Project identity is `<slug>-<6 hex of sha256(canonical dir)>`
  (project.go idFromCanonical); a linked worktree resolves to the main
  worktree's id (project.go Resolve). The store `~/.byre/projects/<id>/`
  holds `byre.config`, the `applied` marker that `preset apply` writes
  (hash plus source; drift states derive from it, and "unapplied" means
  provably never applied), one `engine.<worktree-id>` record per
  worktree, launch records, the path record and the lock.
- Credentials (ADR 0057): a file-local `[credentials]` block holds a
  scrypt-passphrase-wrapped age identity and its cleartext recipient;
  rows are age-encrypted to that recipient inside the same file.
  `byre credentials rekey` unwraps the identity with the old passphrase
  and rewraps it; every value blob stays byte-identical. Values decrypt
  only on the launch path (credentials.go). A layer file can hold its
  own `[credentials]` block and rows (`layerCredTarget`).
- `byre preset apply` (preset.go): refuses off a TTY, resolves the
  extends chain, checks the proposal's template, agent and skills against
  the catalog (`missingRefs`), offers an install under its own y/n where
  the proposal carries a `[sources]` hint and otherwise says to install
  it yourself, renders the grant review with a diff against the current
  store, and on confirm takes the setup lock, re-reads source and store,
  refuses if either changed, writes the config, then writes the `applied`
  marker. An explicit path or URI is read through
  `packages.Fetcher.FetchManifest`, bounded by `packages.MaxManifestBytes`
  (256 KiB), not the 1 MiB config bound. Restore must hand apply the
  config BYTES it already verified, read under `config.MaxConfigBytes`,
  rather than a path.
- Named layers live at `~/.byre/layers/<name>/layer.config`; a missing one
  fails every strict load with "layer X not found -- create <path>"
  (layers.go:146). The config editor and `status` deliberately degrade
  instead (shortened attribution, no volume screen).
- Host paths at develop: a mount rides `--mount type=bind`, which the
  engine refuses when the source is missing (runparams.go:157 relies on
  it); a `[[context]]` file is read at bake and a missing one fails the
  build naming context and path (build/context.go); a `[[claude_skills]]`
  path is validated as a skill directory at bake (planClaudeSkills). A
  missing seed host is not an error: develop says the volume starts
  empty (seed.go), and an existing volume is never seeded at all.
  `worktree_base` is read by `byre worktree`, not by develop.
- Seeding (seed.go, runner SeedVolume/SeedFiles/SeedLiteral): every seed
  function takes the image as a parameter; the copy runs with the image
  entrypoint bypassed, as `-u 0:0` inside the box's userns mapping
  (`appendUserns(args, id.Userns())`), and ends with a recursive chown to
  the box identity; a failed seed removes the volume it created and a
  failed removal says "remove it manually"; an existing volume is left
  alone. `SeedLiteral` streams content over stdin with no host bind. The
  runner has `ImageExists` and no pull method; `run` pulls implicitly.
- The generated Dockerfile (gen.go): `FROM <base>` (default
  `debian:bookworm`), then the template's raw pre-block, then the
  constant core block whose `apt-get install` adds `gosu`. Nothing
  installs or checks for tar. `base` is validated as an image reference
  only; alpine, scratch and distroless bases get a warning at develop
  (`warnNonDebianBase`), not a refusal.
- The setup lock (internal/lock): one `flock` per project store with an
  inode requeue; exclusive only. Develop holds it from prepare through
  container Create and refuses under the lock if the configured engine
  changed while it waited (`refuseEngineChangedUnderLock`); reset and
  forget take it fail-fast (`TryAcquire`); the editor's volume Clear
  takes it.
- Session discovery. Develop's own check (`refuseCrossEngineSession`,
  develop.go) asks by `workdirLabel`, this worktree only, and only on the
  engines the per-worktree engine record implicates, which after a
  normal session is none. The project-wide check is reset's and
  forget's: `clearSessionMarkers` (reset.go) asks every installed engine
  by project label for a running container (abort) and then for a
  container in any state (reset removes a pre-start one; a failed
  removal aborts); a query failure is fatal. Both enumerate engines with
  `lifecycleEngines`, which refuses on a declined binary because those
  commands speak in totals. `reportRunning` prints attach/shell/stop
  remedies; develop's cross-engine arm separately prints the `rm` line
  for a stopped container.
- `byre config` opens the project's config file directly; a skill the
  catalog cannot resolve shows as a disabled row with a reason, with no
  install command (install hints are printed by `preset apply`, `inspect`
  and `skills.Resolve` at develop); the editor degrades (no volume
  screen, shortened attribution) when the engine or a template cannot be
  resolved, rather than refusing to open.
- Nested-tar hygiene exists in internal/deliver/tar.go (ADR 0037): no
  `..`, no empty components, a leading `/` STRIPPED where this design
  refuses, and only regular files and directories written; links and
  specials are skipped by name. It is not a precedent for materialising
  links. Grab's anchored host writes are ADR 0040.
- GNU tar 1.34 stores a FIFO and exits 0; it omits a socket, prints
  `tar: NAME: socket ignored` on stderr and still exits 0 (reproduced
  2026-10-01 by two reviewers). Archiving `-C /vol .` emits names with a
  `./` prefix.

## The backup file [PROPOSED]

One gzip-compressed tar. Entry order is fixed and restore refuses any
other:

1. `backup.toml`, the index: `format = 1`, the writing byre version and
   the minimum byre version that reads this format (both `1.11.0`
   placeholders until release pins them), the project folder name, the
   source engine (information only), a `config` row (byte count, sha256,
   whether credentials are included), one `[[volumes]]` row per carried
   volume (logical name, byte count, sha256, entry count), and a
   `references` table naming what the destination must provide: the
   `extends` layer if any, the template, the agent, each skill, each
   mount host, each context file, each Claude Skill path, each seed host,
   and `engine` and `worktree_base` when set. Every index field the
   review prints is checked against verified content (below); the index
   is a table of contents, not a source of truth.
2. `byre.config`: the project config, byte-for-byte, or with the
   credentials block and rows deleted through tomldoc under
   `--no-credentials`.
3. `volumes/<name>.tar`: one plain tar per carried volume, the volume
   root as the tar root, written inside a container with
   `--numeric-owner`; restore discards ownership (the pour's chown is the
   ownership step).

Staging. Every payload is produced into a private staging directory
(0700; payload files 0600) and hashed there before the outer tar is
written index-first. The published file is 0600 and is created
exclusively through a streaming variant of hostopen's exclusive publish:
an existing file at the output path is a refusal, never an overwrite.

Reading. Restore reads the index first, bounded by `config.MaxConfigBytes`
(1 MiB), refuses an unknown `format` naming the minimum byre version from
the index, then verifies every payload's length and sha256 into staging
before anything is written to its final place. Bounds, all refusals:
the decompressed outer stream may not exceed the index's declared totals
plus 64 KiB plus 1 KiB per outer entry; outer entries are exactly the
index's (duplicate, extra, missing, out of order, or nonzero trailing
data refused); staged bytes are checked cumulatively against the declared
totals and against free disk on the staging filesystem; a nested payload
may hold at most 1,000,000 entries and must match its declared entry
count. Backup enforces the same bounds on what it writes, so a backup
byre produced is one byre reads. Digests detect corruption, not
authorship, and the restore review says so.

The nested-tar contract. Restore validates every nested payload in
staging, header by header through `archive/tar`, before the config is
written; a payload that fails validation refuses the whole restore,
naming the volume and the entry, with nothing written. Rules:

- `Name`: a leading `./` is accepted and dropped (GNU tar's `-C dir .`
  spelling); after that, a leading `/` is refused, `..` and empty
  components are refused, a trailing `/` on a directory is accepted, and
  a name seen twice is refused. An entry whose parent path includes an
  earlier symlink entry is refused, so no entry is ever written through
  a link.
- Kinds: regular files, directories, symlinks and hardlinks are carried.
  A symlink's `Linkname` is verbatim and never resolved. A hardlink's
  `Linkname` must pass the `Name` grammar and name an earlier regular
  entry of the same payload. FIFOs and devices are dropped by name and
  listed. Sparse entries are refused (the pour would expand them; agent
  state holds none). Modes and mtimes are preserved; ownership is
  discarded; PAX long names are accepted.
- The pour replays the validated stream through `archive/tar`, so the
  container's tar receives a normalised PAX stream with the dropped
  entries absent. The pour container holds the fresh volume and nothing
  else, so a hostile entry can reach only that volume or the throwaway
  rootfs.

Every string the file supplies that a review or error prints goes through
the existing terminal-data funnels (P4). Host writes ride hostopen's
anchored operations, no-follow parents.

Derived, not trusted. The review's symlink-target lists, credential
state, reference list and volume names come from the verified payloads
and config bytes, not from the index: the symlink lists from the
validation pass, the credential state and references from parsing the
verified config, each logical volume name checked against the volume-name
grammar before it is joined into a physical name.

## byre backup [PROPOSED]

`byre backup [DIR] [--output PATH] [--no-volume NAME]... [--no-credentials]
[--yes]`

1. Resolve the project from DIR (default: current directory; a linked
   worktree resolves to its project). Refuse if the project has no
   config.
2. Resolve the config, pick the source engine (the config's `engine` when
   set, else detection), and list the project's volumes on it by the
   `byre-<id>-` prefix. Carried: every one of them except those the
   resolved config declares with `role = "cache"`, minus `--no-volume`
   names. A `--no-volume` name that is not in the list is refused naming
   the list; repeating a name is harmless. A project with no volumes
   backs up its config alone and the preview says so.
3. On a terminal, print the preview and ask y/n unless `--yes`: the
   output path; the config with its credential row count or "credentials
   dropped"; each carried volume with its estimated size and whether the
   current config names it; each dropped volume as "not carried
   (--no-volume)"; each machine-scoped volume the config resolves as "not
   carried; the destination binds its own machine-scoped <name>"; the
   references list; the stillness requirement. Off a terminal, no preview
   and no prompt.
4. Take the project setup lock. Under it, re-read the config and re-list
   the volumes; if the config bytes or the volume list differ from what
   the preview showed, refuse: "changed while you were reviewing; re-run
   byre backup" (apply's rule; the editor's save takes the same lock).
   Then the stillness sweep, reset's shape: every installed engine via
   `lifecycleEngines` (a declined binary refuses), running container by
   project label refuses with attach/shell/stop, a container in any other
   state refuses with the engine's `rm` line; a query failure refuses.
5. Still under the lock: apply `--no-credentials` to a copy if asked,
   write the config to staging and hash it. For each carried volume, run
   a short-lived container from the project's BUILT image when present,
   else the base image (this is the one place the built image is
   preferred, because it is already local), as `-u 0:0` in the box's
   userns like a seed, the volume mounted read-only at one path, no
   network, no host bind, entrypoint overridden to `tar --numeric-owner
   -cf - -C /vol .` on stdout, into staging, hashed there, headers
   inspected on the way through to record each payload's absolute and
   traversing symlink targets and its entry count. tar's stderr is
   captured: `socket ignored` lines become the summary's socket list; a
   nonzero exit is a failed backup, nothing published.
6. Write the index and the outer tar, publish exclusively at the output
   path, release the lock, print the summary: file name, actual bytes,
   volumes carried, the symlink and socket lists.

Cancel at the prompt leaves nothing: staging is created in step 5. A
failure after staging began removes the staging directory.

## byre restore [PROPOSED]

`byre restore FILE [DIR]`, terminal only.

1. Resolve DIR (default: current directory; not yet created). If the
   project store already has a config, refuse: "this project already has
   a config; restore into a fresh checkout". A DIR that is a linked
   worktree of a configured project hits the same refusal, since it
   shares that project's store.
2. Read the index, verify every payload into staging, validate every
   nested payload (the contract above), parse the verified config bytes.
   Any failure refuses here, nothing written.
3. Pick the engine from the verified config (`engine` when set, else
   detection). Unreachable: refuse, "start docker, then run byre restore
   again". Pull the base image if absent (a new `ImagePull` on the
   runner), then run `tar --version` in it with the entrypoint bypassed.
   Either failing refuses naming the base.
4. Hand the verified config bytes to the apply review as the proposal.
   The review is apply's (missing template, agent or skill handled as
   apply handles them; a missing layer in the extends chain stops here
   with the path to create), plus two sections before the grant summary:
   "Names this machine must satisfy", listing every reference with what
   reads it and how it fails (mount host, context file, Claude Skill
   path: develop fails naming it; seed host: nothing, a restored volume
   is never seeded; `engine`: this restore is using it; `worktree_base`:
   `byre worktree` uses it); and "From the backup", listing each volume
   with "will be restored" or "exists here; the backed-up copy is
   dropped", whether the config names it, the credential state, each
   payload's absolute and traversing symlink targets as agent-authored
   content, and the line that the file's authorship is not proven. One
   y/n.
5. On confirm, one critical section: create DIR if absent, bootstrap the
   store, take the project setup lock, re-check under it that the store
   still has no config, write the config bytes through apply's write
   (an entry point callable under a held lock; restore writes no
   `applied` marker, because no preset was applied), then for each
   volume absent on the engine: create it and replay its validated
   stream into a pour container, `SeedLiteral`'s shape taking a reader
   (`-u 0:0`, the box's userns, entrypoint bypassed, `tar -x` into the
   mounted volume, then the recursive chown to this machine's box
   identity). Release the lock after the last pour, so a develop that
   arrived meanwhile sees the config and the poured volumes together.
6. A failed pour removes the volume it created and restore stops; the
   summary names the volume and prints "byre forget, then byre restore
   again". Otherwise print the summary: config written, N volumes
   restored, dropped entries, kept volumes, "next: byre develop".

Declining at the review writes nothing: no DIR, no store, no volume;
staging is removed. A skill installed during the review stays installed,
as apply already has it.

## Doctrine

- New ADR: box backup (this design), citing P1, P4, P5, P7.
- 0004: session discovery is unchanged; backup's stillness sweep is
  reset's project-label, any-state, every-engine check, refusing where
  reset removes.
- 0007 and 0017: byre still reads no host credential file and seeds no
  login; that is what both ADRs ban and it stays banned. Restore moves a
  volume of the user's own box, at the user's instruction, and that
  volume may hold a login. 0007's observed failure (two independent
  holders of one rotating refresh token, the first refresh invalidating
  the other) applies to a backup restored while the source box is still
  in use; the new ADR records this as an accepted consequence of moving
  box state, named once in the security-model page, with no warning at
  the prompt (Pete's ruling). Machine-scoped identity never travels.
- 0008 and 0032: the pour is a root-in-userns runtime chown with no host
  bind, the seeding shape, under rootless Podman's keep-id mapping; the
  ADR names seeding as the precedent.
- 0009: worktrees allowed at backup; restore runs no git.
- 0014: restore enforces the Debian-derived base boundary before it
  writes, by proving tar in the base; a second place the boundary shows.
- 0016: the index names the writing version and the minimum reading
  version; an unknown format names the latter.
- 0017: machine-scoped volumes never travel; no narrowing, no amendment.
- 0030: the project file's own closures travel byte-for-byte. Effective
  egress on the destination also composes its defaults, layers and skill
  manifests, which the apply review shows; the design claims nothing
  stronger.
- 0035: a layer is a plain file the user sends; the backup carries the
  pointer only.
- 0040: file-supplied names never choose a host path; anchored writes.
- 0044: the index is TOML through the one library; the credentials
  deletion rides tomldoc.
- 0047: backup's engine enumeration refuses on a declined binary, as
  every totals command does; restore spawns no host tool over anything
  it wrote.
- 0051: digests are integrity, not authenticity, and the review says so.
- 0054: exclusive-volume checks are unchanged.
- 0057: unchanged. Credentials stay encrypted end to end; byre's
  decrypted plaintext still goes to exactly one place.
- 0058: an override run's volume is carried like any other project
  volume; the override itself writes nothing.
- P0: the backup form is logged as a follow-up; restore's screen is the
  existing apply review.
- P4: every list above (references, dropped and kept volumes, symlink
  targets, dropped entries, sockets) is printed, never silent.
- P7: the base image's tar is the pour tool, and restore proves it is
  there before relying on it. No new dependency.
- Security-model page: a backup is an unencrypted 0600 file of the
  project's volumes; the cooperative stillness residual; file authorship
  unproven; a restored login shares its token with the source box.
- GLOSSARY: "backup" (the file and the verb pair).

## Evidence before done

- Round trip: backup then restore into a fresh checkout yields a config
  byte-identical to the source's, credentials decrypting under the
  source passphrase at the first develop, and `byre credentials rekey`
  working on the restored file. No `applied` marker is written.
- `--no-credentials` leaves no ciphertext, no identity and no
  `[credentials]` block in the backup's config; the restore review says
  credentials are absent.
- Volume selection: every `byre-<id>-` volume on the source engine is
  carried, including one no skill in the config declares; a cache-role
  volume is not; `--no-volume` drops one and an unknown name is refused
  naming the list; machine-scoped volumes are never carried and the
  preview names them; a project with no volumes backs up its config.
- Stillness: a running container refuses with attach/shell/stop; a
  stopped or created container refuses with the `rm` line; a container
  belonging to a sibling worktree refuses; a container on the other
  installed engine refuses; an engine that cannot be queried refuses; a
  declined engine binary refuses; a develop arriving mid-backup waits on
  the lock and runs afterwards.
- Under-lock re-check: a config save or a volume appearing between the
  preview and the lock refuses with the re-run line.
- File: payloads hashed before the index is written, index-first order,
  format refusal naming the minimum version, digest mismatch refusal for
  every payload including the config, read budget (index bound,
  decompressed bound, entry order and count, cumulative bytes, free
  disk, trailing data), 0600 modes, 0700 staging, escaped strings,
  existing output file refused naming `--output`, dated default name, a
  nonzero tar exit publishes nothing, socket lines reach the summary.
- Nested-tar contract, each refused before any write: leading `/`, `..`,
  empty component, duplicate name, an entry under an earlier symlink, a
  hardlink to a missing or non-regular or later target, a sparse entry,
  an entry count over the declared one. Accepted and proven: `./`
  prefix, trailing-slash directory, symlink with an absolute target
  carried verbatim and listed, hardlink to an earlier regular entry,
  FIFO and device dropped and listed, modes and mtimes preserved,
  ownership discarded.
- Restore order: engine down refuses before any write; base image absent
  and unpullable refuses before any write; a base without tar refuses
  before any write naming the base; existing config refuses; a linked
  worktree of a configured project refuses; missing layer stops at the
  review; missing skill offered for install where hinted; declining the
  review leaves no DIR, no store, no volume, no staging.
- Restore commit: config and pours land in one lock hold (a develop
  waiting on the lock sees both); volumes poured from the base image
  with the box identity's ownership; existing volume kept with the line;
  a failed pour removes its volume and the summary prints forget-then-
  restore; `byre forget` then `byre restore` on that project succeeds;
  `byre config` opens cleanly on the restored project; `byre develop` on
  the restored project fails on a missing mount host, context file or
  Claude Skill path with today's messages and starts normally with a
  missing seed host.
- Login posture: restoring a volume that holds an agent login file reads
  nothing from the host and prints no warning.
- Off-terminal: backup runs without a prompt; restore refuses. `--yes`
  skips the backup prompt on a terminal.
- Worktree: backup from a linked worktree backs up the project.
- Gated (byre-inttest, Docker and rootless Podman): a real Claude state
  volume with a memory file streamed out and poured back, then a develop
  on the restored project in which the agent's memory file is present at
  its path, readable, and owned by the box identity.

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
writer and reader with its budget, the nested-tar validator and
normaliser, and the symlink and dropped-entry listings. Command handlers
stay Streams adapters in `internal/commands`; cobra wiring in `cmd/byre`
(ADR 0022).

New seams, each small: the apply review needs an entry point that takes
config bytes, and apply's confirmed write needs a form callable under a
lock the caller holds, without the `applied` marker; the runner needs
`ImagePull`, a seed variant that takes an `io.Reader`, and a capture
variant that runs a container with a read-only volume and returns stdout
as a stream plus stderr; hostopen needs a streaming exclusive publish
(today's `PublishFileExclusive` takes the whole content as a string).
Reused as they are: `lifecycleEngines` and reset's `clearSessionMarkers`
shape (refusing instead of removing), `reportRunning`, the setup lock,
`projectVolumes`, tomldoc deletion, `appendUserns`.
