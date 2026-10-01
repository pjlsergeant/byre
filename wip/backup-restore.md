# byre backup / byre restore

Status: v1 design, 2026-10-01, revised 2026-10-02 after four reviewer
sign-off rounds (Codex, Grok, Z.AI; every verdict NOT READY so far, each
round narrower than the last; findings in the review log). This replaces the box export/import design
(v5, `wip/box-export-import.md`, deleted in commit a1edcc20; git history
keeps it). The restart came out of a three-reviewer round on v5
(2026-09-21) and the grill that followed, where Pete cut the feature down
to what people actually want back: the agent's memory and context, the
credentials, and the project's config. Every ruling below is Pete's. Not
yet authorization to implement: TODO.md owns task status. wip/
lifecycle: absorbed into an ADR and the docs on ship, then deleted.

Provenance: [RULING] = Pete's, settled, do not reopen to close a finding.
[RULING 2026-10-01] = Pete's, given while working through the sign-off
findings. [DESIGN CALL] = chosen by the designer to close a finding,
flagged to Pete, overridable. [CODE] = verified against the tree on
2026-10-02, after four rounds of reviewer corrections. [PROPOSED] = this
design's mechanism. [VERIFY] = a claim about an engine the box cannot
run; the gated run proves it or the design changes.

## What this is

`byre backup` writes one file holding a project's config and its state
volumes. `byre restore` takes that file and makes a fresh project from it
on this machine or another: config written, volumes filled, ready for
`byre develop` once every package the config names is installed. A
backup is your own box coming back to you.

## Rulings

- [RULING] Scope is the project's config file and the project's state
  volumes. Nothing else: no workspace (git carries it), no built image,
  no ephemeral container filesystem, no layers, no skills, no packages,
  no other store files (the applied marker, engine records, launch
  records and the build context are this machine's history).
- [RULING 2026-10-01] Every project volume that exists on the source
  engine comes along unless the user says otherwise: `--no-volume NAME`
  drops one. That includes a volume made by a one-off `develop --agent
  grok` run whose agent is not in the config: it is the project's state
  and the user expects it back. The only volumes never carried are the
  ones the resolved set declares with `role = "cache"`. NAME is the
  logical name, the part after the project prefix (`.claude`, `.grok`),
  matched as a string against the names the preview lists.
- [RULING] Machine-scoped volumes are never offered, whether a skill or
  the project config declares them -- the physical name says which they
  are (`byre-machine-u<uid>-...`), not the declarer. Every machine-scoped
  volume byre ships is identity state (a login or a key); the destination
  logs in once (ADR 0017's intended path).
- [RULING] The config travels byte-for-byte. Backup rewrites nothing in
  it, with one exception: `--no-credentials` deletes the `[credentials]`
  block and every credential row (an `env_from_host` value with the
  `encrypted:` or `encrypted-file:` scheme) from the copy that goes in
  the backup file. The source file is untouched. Host paths, `engine`,
  `worktree_base`, `extends`, seeds: all carried as they are. Restore
  lists what this machine must satisfy; the verbs that read each one
  fail or degrade exactly as they do today.
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
  story). Backup names every layer in the chain and every package it
  saw. Restore offers a missing package's install exactly as `byre
  preset apply` does where the config carries a `[sources]` hint, and
  otherwise stops naming the package and its install command. Unlike
  apply, restore does not continue past a package that is still missing:
  the volume set it must pour and the engine, base and mounts it must
  prove all come from the resolved set, and `skills.Resolve` returns
  nothing at all while a package is missing [CODE]. "Install it, then
  run byre restore again." A missing layer stops the same way with the
  path to create, as every strict load does.
- [RULING] Backup requires a completely still project: no container
  labelled for this project in any state, on any installed engine, any
  worktree. Backup holds the project setup lock from that check through
  the end of the copy. Refusals print the engine's remedies; byre never
  stops or removes a session on the user's behalf. An engine byre cannot
  account for (a declined binary, a daemon that does not answer the
  query) is a refusal: backup speaks in totals, like reset and forget,
  and an engine it could not inspect cannot be declared idle. The
  disclosed residual is a hand-run engine command.
- [RULING] Restore does the whole job itself: writes the config, pours
  every carried volume that does not already exist on the engine (the
  existing-volume rule below), builds nothing. Nothing the backup
  carries waits for the first develop. A volume the config declares that
  the backup does not carry is handled by the first develop exactly as
  today (seeded or started empty).
- [RULING] Restore pours volumes using the effective config's BASE image,
  not a built image. An unset `base` is `gen.DefaultBase`
  (`debian:bookworm`), as the generator resolves it.
- [RULING 2026-10-01] Restore proves the base image before it commits:
  it pulls the image if absent and runs the pour's own tar command in
  it, entrypoint overridden, flags included, fed a valid empty archive
  (two 512-byte zero blocks; GNU tar exits 2 on zero bytes), before any
  project state is written. A base that cannot be fetched or
  whose tar cannot run that command refuses, naming the base and the
  engine's error. This is a tool-availability preflight for the pour and
  nothing more: byre's Debian-derived support boundary stays what it is
  today, a warning at develop and a failure at build.
- [RULING 2026-10-01, mechanism revised 2026-10-02 as a DESIGN CALL]
  Restore can still fail part way after the base is proven: the user
  presses Ctrl-C, the engine goes away, the engine's disk fills, a
  volume cannot be created. Pete's ruling was "forget, then restore
  again", chosen for needing no new mechanism. The design call refines
  the mechanism and keeps the intent: a failed pour force-removes the
  helper container, removes the volume being poured, and removes the
  config restore wrote, all under the lock restore still holds. A volume
  counts as poured whole only once its extraction AND its chown have
  both succeeded; a chown failure is a failed pour and the volume goes.
  What stays: volumes poured whole before the failure (the next restore
  meets them as "exists here; kept", and their content is the backup's),
  the project directory, the store's path record, and any pulled image. The
  summary names the volume, the cause and what stayed, and says to fix
  the cause and run `byre restore` again. When the volume being poured
  cannot be removed (the engine is gone), the summary says so, prints
  the engine's `volume rm` command for it, and says a re-run will keep
  whatever occupies that name until it is removed; `byre forget`, run in
  the project directory, is named as the fallback, with the warning that
  forget removes every volume and image of the project, kept ones
  included, prompts, and refuses while any installed engine cannot be
  queried. When the helper itself cannot be removed (the engine is
  gone), the summary names the container and prints the engine's
  `rm -f` line for it; the next backup's or reset's stillness sweep also
  lists leftover helpers by their label and refuses until they are gone,
  so a surviving helper is never silent. Reason for the call: forget is
  a poor first remedy for the common case; one file removal makes a
  plain re-run work.
- [RULING] Restore refuses to run on a project that already has a
  config: "this project already has a config; restore into a fresh
  checkout". A volume that already exists on the engine is kept and the
  summary says the backed-up copy was dropped.
- [DESIGN CALL] Restore refuses to run from a linked worktree: "restore
  in the main worktree". A linked worktree is its main tree's project
  (ADR 0009), and restoring the main tree's state from a side checkout
  whose main tree may be missing or unconfigured has no good answer.
  Backup from a linked worktree stays allowed.
- [DESIGN CALL] Restore creates DIR first, before it resolves the
  project's identity, because identity is computed from the real
  directory and a not-yet-created path cannot be canonicalised
  (`Canonicalize` falls back to the cleaned pathname, so an alias path
  would yield one id now and another after creation). Every exit before
  the config write (a bad file, a missing layer or package, a refusal, a
  decline) removes that directory again if restore created it and it is
  still empty (`rmdir` semantics, so nothing a user put there is ever
  touched); if it cannot, the summary says the directory stays.
- [RULING] No archive encryption. The user protects the file with their
  own tools. Volume contents may contain plaintext secrets and the
  credentials switch makes no claim about them.
- [RULING] No login warning. The agent volume carries the box's login
  file; two boxes holding one rotating token is the user's problem, not
  byre's. The new ADR records this against ADR 0007 as an amendment
  note on 0007, the way ADR 0057 added one (see Doctrine).
- [RULING] Symlinks travel with their target stored verbatim. The tar
  validator constrains `Name` only; a symlink's `Linkname` is
  unconstrained bytes, never dereferenced by byre. Hardlinks only to an
  already-seen regular entry of the same payload. Absolute and
  traversing symlink targets are listed by path at backup and again at
  restore. Traversing means: the target is relative and its lexical join
  with the link's own directory leaves the volume root (the normalised
  path begins with `..`). Lexical only; nothing is resolved.
- [RULING, adjusted 2026-10-01] FIFOs and devices inside a volume reach
  the archive; restore drops them by name and the summary lists them.
  Sockets never reach the archive (GNU tar skips them at the source), so
  backup's summary lists them and restore has nothing to say. No prompt
  at either end.
- [RULING] Verbs: `byre backup [DIR]` and `byre restore FILE [DIR]`. DIR
  is the project directory and defaults to the current one; restore
  creates it when absent, and refuses when it exists as something other
  than a directory. Projects have no names in byre (identity is the
  directory path; a linked worktree is its main tree's project), so
  there is no lookup by name. Usage errors exit 2 (ADR 0022); refusals,
  the off-terminal restore refusal included, exit 1 as `preset apply`'s
  does.
- [RULING] Default output is `<folder>-<YYYY-MM-DD>.byre-backup.tar.gz`
  in the current directory, `<folder>` being the project directory's
  base name and the date local; `--output PATH` elsewhere. An existing
  entry of any kind at the output path is a refusal, never an overwrite,
  so a second backup the same day refuses naming `--output`. A missing
  parent directory of the output path refuses naming it.
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
- [RULING] One shipping unit, one review loop, one gated engine-side
  test run (`byre-inttest`) before done. The phrase counts review and
  test cycles, not containers: the verbs run one helper container per
  volume.

## What already exists [CODE]

- Volumes: every bundled agent skill declares a project-scoped state
  volume (`.claude`, `.codex`, `.gemini`, `.grok`, `.opencode`); every
  `*-shared-auth` companion and this repo's inttest skill declare a
  machine-scoped identity volume under `/home/dev/.byre-identity/`. No
  bundled skill declares a cache-role volume (the node template does;
  a skill may). Claude's memory, transcripts, Codex's sessions and
  history all live in the project-scoped volume. Physical names: project
  `byre-<id>-<name>`, machine `byre-machine-u<uid>-<name>`
  (`scopedVolumeName`, naming.go, branching on `Volume.MachineScoped()`
  in config.go); names carry no engine component and volumes carry no
  labels (`VolumeCreate` sets none). `volumeNameRe` (config.go:141) is Docker's
  character set and admits `.` and `..`. Reset and forget enumerate a
  project's volumes with `projectVolumes` (naming.go), which lists by
  the `byre-<id>-` prefix, drops machine-scoped names, and excludes any
  name a longer known project id claims (both the id and the name use
  `-`, so `app-123456` plus `other-abcdef-state` spells the same physical
  name as `app-123456-other-abcdef` plus `state`); they call it on every
  installed engine because state can live in an engine the config no
  longer names.
- The volume set develop runs is not the cascade's alone: `resolve`
  (resolve.go) loads the cascade, resolves the skills, and `combine`
  unions the skills' declared volumes and mounts in. Every bundled agent
  state volume and every machine-scoped identity volume is declared by a
  skill, so a cascade-only read sees none of them. `skills.Resolve`
  returns an empty result and an error when any enabled package is
  missing; `effectiveReview` (review.go) returns the cascade config
  without that union, omits every skill grant on that error, and falls
  back to the raw proposal when the cascade fails.
- Project identity is `<slug>-<6 hex of sha256(canonical dir)>`
  (project.go idFromCanonical, slug capped at 40 characters); a linked
  worktree resolves to the main worktree's id and store (project.go
  Resolve). `Canonicalize` resolves symlinks and falls back to the
  cleaned absolute pathname on any resolution error, a missing path
  included. `checkRecord`/`requireRecorded` (lock.go) are the
  collision fence: every setup writer that bootstraps before taking the
  lock calls `requireRecorded` as its first action inside the lock, so a
  concurrent forget (which empties the store under the lock,
  `clearStoreContents`) cancels the writer instead of being resurrected. The
  store `~/.byre/projects/<id>/` holds `byre.config`, the `applied`
  marker that `preset apply` writes (hash plus source; the drift states
  derive from it, and `presetState` reports nothing at all when no
  preset file exists), one `engine.<worktree-id>` record per worktree,
  launch records, the build context, the path record and the lock.
- The cascade (config.go Load): the core layer (`env_from_host` only),
  `default.config`, the template, the extends chain root-first, then the
  project file. `engine`, `base`, volumes, mounts, seeds, context files,
  Claude Skill paths and `worktree_base` can all be inherited; the
  project file alone does not determine any of them. A disabled mount
  is skipped before its host path is expanded (runparams.go, ADR 0015).
- Credentials (ADR 0057): a file-local `[credentials]` block holds a
  scrypt-passphrase-wrapped age identity and its cleartext recipient;
  rows are age-encrypted to that recipient inside the same file.
  `byre credentials rekey` unwraps the identity with the old passphrase
  and rewraps it; every value blob stays byte-identical. Values decrypt
  only on the launch path (credentials.go), where develop prompts for
  the passphrase. A layer file can hold its own `[credentials]` block
  and rows (`layerCredTarget`).
- `byre preset apply` (preset.go): refuses off a TTY (exit 1), resolves
  the extends chain, checks the proposal's direct template, agent and
  skills against the catalog (`missingRefs`; inherited references are
  not checked there), offers an install under its own y/n where the
  proposal carries a `[sources]` hint and otherwise says to install it
  yourself and continues with the package marked "not installed --
  grants unknown", renders the grant review with a diff against the
  current store, and on confirm bootstraps the store, takes the setup
  lock, re-reads the store and refuses if it changed (appeared, vanished
  or differs), re-reads the source and refuses only if that re-read
  succeeds with different bytes, writes the config, then writes the
  `applied` marker. An explicit path or URI is read through
  `packages.Fetcher.FetchManifest`, bounded by `packages.MaxManifestBytes`
  (256 KiB), not the 1 MiB config bound. Restore must hand apply the
  config BYTES it already verified, read under `config.MaxConfigBytes`,
  rather than a path. Config decode is strict (`decodeStrict`, unknown
  fields refused); go-toml is lenient unless asked.
- Named layers live at `~/.byre/layers/<name>/layer.config`, under their
  own lock (layers.go), so a layer can change while a project's setup
  lock is held; a missing one fails every strict load with "layer X not
  found -- create <path>" (layers.go:146), `status` included. The config
  editor degrades instead: the volume screen is hidden when resolution
  or `lifecycleEngines` fails (a declined engine is printed as the
  reason), inheritance marks are dropped for a broken template or skill.
- Host paths at develop: a mount rides `--mount type=bind`, which the
  engine refuses when the source is missing (runparams.go:157 relies on
  it); a `[[context]]` file is read at bake and a missing one fails the
  build naming context and path (build/context.go); a `[[claude_skills]]`
  path is validated as a skill directory at bake (planClaudeSkills).
  Seeding (seedVolumes, seed.go) runs only for a volume that does not
  yet exist: a literal seed is written, a host seed is copied in, a
  missing host prints "byre: seed source <host> not found; <name> starts
  empty", and `seed_prefs` (ADR 0013) copies the curated allowlist into
  a fresh agent state volume. `worktree_base` is read by `byre
  worktree`, not by develop.
- Seeding (runner SeedVolume/SeedFiles/SeedLiteral): every seed function
  takes the image as a parameter; the copy runs `--rm` with the image
  entrypoint bypassed, as `-u 0:0` inside the box's userns mapping
  (`appendUserns(args, id.Userns())`), with no label of any kind, and
  ends with a recursive chown to the box identity; a failed seed removes
  the volume it created and a failed removal says "remove it manually";
  an existing volume is left alone. `SeedLiteral` streams content over
  stdin with no host bind. The runner has `ImageExists` and no pull
  method; the engine CLIs pull implicitly on `run` and on a `build`
  whose base is missing. `ContainersByLabel` and `ContainerRemove`
  exist; the latter is deliberately non-forcing and there is no stop. A fresh named volume is populated by
  the engine from the image's directory at the mount point (copy-up);
  the generator pre-creates mount points to rely on exactly that
  (gen.go). Nothing in the tree passes `volume-nocopy`.
- Identity (identity.go): develop uses `resolveIdentity`, which refuses
  rootless Podman without a keep-id mapping unless
  `BYRE_ALLOW_ROOTLESS_PODMAN=1`; `engineIdentity` is the lifecycle
  commands' never-refusing fallback (keep-id 1000 where supported, host
  numeric ids without keep-id otherwise). `imageTagCandidates` returns
  the identity's tag, the generic rootless tag on a rootless engine, the
  host uid/gid tag always, and the legacy tag.
- The generated Dockerfile (gen.go): `FROM <base>` (default
  `debian:bookworm`), then the template's raw pre-block, then the
  constant core block whose `apt-get install` adds `gosu`. Nothing
  installs or checks for tar. `base` is validated as an image reference
  only; alpine, scratch and distroless bases get a warning at develop
  (`warnNonDebianBase`), not a refusal.
- The setup lock (internal/lock): one `flock` per project store with an
  inode requeue; exclusive only. Develop holds it from prepare through
  container Create (its container may still be running long after the
  lock is released) and refuses under the lock if the configured engine
  changed while it waited (`refuseEngineChangedUnderLock`); reset and
  forget take it fail-fast (`TryAcquire`); the editor's volume Clear
  takes it.
- Session discovery. Develop's own check (`refuseCrossEngineSession`,
  develop.go) asks by `workdirLabel`, this worktree only, on the engines
  the per-worktree engine record implicates: none after a normal
  session, every other installed engine when the record is missing or
  invalid; an unreachable one is skipped with "<engine> isn't
  reachable", and a declined binary is named on every develop. The
  project-wide check is reset's and forget's: `clearSessionMarkers`
  (reset.go) asks every installed engine by project label for a running
  container (abort) and then for a container in any state (reset removes
  a pre-start one; a failed removal aborts); a query failure is fatal
  ("checking for a running session (<engine>): ..."), which is how a
  daemon that is installed but down refuses those commands. Both
  enumerate engines with `lifecycleEngines`, which itself refuses on a
  declined binary and when no engine is installed at all. `reportRunning` prints attach/shell/stop remedies;
  develop's cross-engine arm separately prints the `rm` line for a
  stopped container.
- `byre forget` (forget.go): current directory only, no DIR argument;
  prompts y/N unless forced; refuses while a session is live; removes
  every project volume and every image-tag candidate on every installed
  engine, then the store; if any removal fails it leaves the store in
  place and says so. A declined binary or an unanswering daemon makes it
  refuse.
- `byre config` opens the project's config file directly; a configured
  skill the catalog does not know is kept as an ordinary enabled row
  with no reason and no install command (install hints are printed by
  `preset apply`, `inspect` and `skills.Resolve` at develop); only
  catalog problem rows (`ListProblemRows`: invalid, conflict) show
  disabled with a reason.
- hostopen: `PublishFileExclusive` (publish.go) takes the whole content
  as a string, writes a temp file and links it into place so the final
  name appears only once the bytes are durable, and anchors on
  `os.OpenRoot(dir)`, which follows a symlink at the final directory;
  `OpenDirRootNoFollow` (hostopen.go) is the stronger anchor that
  refuses one, and is what forget uses to remove the store. Neither
  guards every ancestor; ADR 0040 accepts that ancestor residual on the
  record. The publisher does not fsync the file or the directory.
  `PlainRemove` and `PlainRemoveAll` exist in hostopen; forget's
  `removeIn(dir, name)` (forget.go) is an anchored single-entry removal
  local to that command.
- Containers byre launches carry `byre.launch=<sha256 of the launch
  record>` and the record lives in the store (ADR 0053); seed containers
  carry nothing and write no record. ADR 0054's exclusive-volume scan
  reads live boxes' launch records.
- Nested-tar hygiene exists in internal/deliver/tar.go (ADR 0037): no
  `..`, no empty components, a leading `/` STRIPPED where this design
  refuses, and only regular files and directories written; links and
  specials are skipped by name. It is not a precedent for materialising
  links.
- `version.String()` (internal/version) is the one exported version:
  stamped tag, else build info, else `(devel)` with the VCS revision when
  known; never a literal.
- GNU tar 1.34 stores a FIFO and exits 0; it omits a socket, prints
  `tar: ./NAME: socket ignored` on stderr and still exits 0 (reproduced
  2026-10-01 by two reviewers). Archiving `-C /vol .` emits a root
  directory header named exactly `./`, then `./` prefixed names, and
  does so for an empty volume too. As root, `tar -x` restores archived
  numeric ownership unless told `--no-same-owner`. A recursive chown
  clears setuid and setgid on regular files and leaves setgid on
  directories. GNU tar's default (gnu) format truncates mtimes to whole
  seconds; `--format=posix` keeps nanoseconds. Zero-byte input to
  `tar -x` exits 2 ("does not look like a tar archive"); a valid empty
  archive (1024 zero bytes) exits 0. (All reproduced by reviewers.)

## The backup file [PROPOSED]

One gzip-compressed tar. Entry order is fixed and restore refuses any
other:

1. `backup.toml`, the index, decoded strictly (an unknown key at
   `format = 1` is a refusal; any new field bumps the format): `format
   = 1`, the writing byre version (`version.String()`) and the minimum
   byre version that reads this format (a constant of the format, set
   when the unit ships), the project folder name, the source engine, a
   `config` row (byte count, sha256, whether credentials are included),
   one `[[volumes]]` row per carried volume (logical name, byte count,
   sha256, entry count), and a `references` table: what backup saw in
   the SOURCE's resolved set, named for the human at restore (every
   layer in the chain, the template, the agent, each skill, each enabled
   mount host, each context file, each Claude Skill path, each seed
   host, and `engine` and `worktree_base` when set). The index is a
   table of contents and a note from the source. Every value the review
   prints as a fact about this restore is derived from verified content
   (below); the references table is printed as what the source saw,
   through the funnel, and asserted as nothing more.
2. `byre.config`: the project config, byte-for-byte, or with the
   credentials block and rows deleted through tomldoc under
   `--no-credentials`.
3. `volumes/<name>.tar`: one plain tar per carried volume, the volume
   root as the tar root, written inside a container with
   `--format=posix --numeric-owner` so mtimes keep their nanoseconds;
   restore discards ownership (the pour's chown is the ownership step).

Staging. Every payload is produced into a private staging directory
(0700; payload files 0600) opened through an anchored root so no
file-supplied name can choose a path: for backup under the output file's
directory, for restore under `~/.byre/` (created if absent, the way the
store's bootstrap creates it). The disk check is on that filesystem and
counts the payloads plus the outer file. Everything is hashed in staging
before the outer tar is written index-first. The published file is 0600
and is created exclusively through a streaming variant of hostopen's
exclusive publish that keeps the temp-then-link shape and adds what the
existing publisher lacks: fsync of the file before the link and of the
directory after it, so the final name appears only once the bytes are
durable and a failed backup never leaves a partial file at the output
path. It anchors with
`OpenDirRootNoFollow`, inheriting ADR 0040's ancestor residual as it
stands: an existing entry at the output path is a refusal, never an
overwrite. Staging is removed on every exit, success or failure.

Reading. Restore reads the index first, bounded by `config.MaxConfigBytes`
(1 MiB), refuses an unknown `format` naming the minimum byre version from
the index, then verifies every payload's length and sha256 into staging
before anything is written to its final place. Bounds, all refusals:
the config member may not exceed `config.MaxConfigBytes`; the
decompressed outer stream may not exceed the index's declared totals
plus 64 KiB plus 1 KiB per outer entry; outer entries are exactly the
index's (duplicate, extra, missing, out of order, or nonzero trailing
data refused); logical volume names must pass `volumeNameRe`, must not
be `.` or `..`, must be unique, and joined to the destination's project
prefix must not exceed 255 bytes (a design bound, not an engine fact in
the tree); staged bytes are
checked cumulatively against the declared totals and against free disk;
a nested payload may hold at most 1,000,000 entries and must match its
declared entry count. Backup enforces the same bounds on what it writes
and refuses before publishing, so a backup byre produced is one byre
reads. Digests detect corruption, not authorship, and the restore review
says so.

The nested-tar contract. Restore validates every nested payload in
staging, header by header through `archive/tar`, before any project
state is written; a payload that fails validation refuses the whole
restore, naming the volume and the entry. Rules:

- The root entry: GNU tar's `./` directory header is accepted, not
  counted, and replayed as the root so the volume root's mode and mtime
  land. A payload with no root entry is fine too.
- `Name`: a leading `./` is dropped; after that, a leading `/` is
  refused, `..`, `.` and empty components are refused, a trailing `/` is
  removed before comparison, and a name seen twice is refused. Every
  ancestor of an entry that appears earlier in the payload must be a
  directory entry; an ancestor that is a symlink, a regular file or a
  hardlink refuses the payload, so no entry is ever written through a
  link or onto a file. An ancestor that does not appear at all is fine
  (tar creates it).
- Kinds: regular files, directories, symlinks and hardlinks are carried.
  A symlink's `Linkname` is verbatim and never resolved. A hardlink's
  `Linkname` passes the `Name` grammar (the `./` prefix included) and
  must name an earlier regular entry of the same payload. FIFOs, and
  character and block devices, are dropped by name and listed. Every
  other type flag (sockets that a hand-made archive could carry,
  contiguous files, GNU vendor types, anything unknown) refuses the
  payload. Sparse entries refuse the payload (the pour would expand
  them; agent state holds none). PAX long names are read. The restored
  tree is the source tree under one stated transformation and nothing
  else: ownership replaced by the box identity; setuid, setgid and
  sticky bits zeroed (in the rebuilt header, which is the contract
  rather than a promise the chown would break); FIFOs and devices
  absent; every other mode bit, every mtime to the nanosecond, every
  size, every link target preserved.
- The pour replays a stream the validator REBUILDS entry by entry from
  name, type, link name, mode, mtime and content, in PAX format; global
  headers, vendor records and the dropped entries are not carried into
  it, ownership fields are zero, special mode bits are zero. The
  container's tar extracts it with `--no-same-owner`, so an archived uid
  outside this machine's user namespace can never fail an extraction.
  The pour container holds the fresh volume and nothing else, so a
  hostile entry can reach only that volume or the throwaway rootfs.

Every string the file or a helper supplies that a review, a summary or
an error prints goes through the existing terminal-data funnels (P4):
index fields, names, link targets, and the helper's tar stderr lines
alike. Host writes ride hostopen's anchored operations.

Derived, not trusted. The review's symlink-target lists, dropped-entry
lists and credential state come from the verified payloads and config
bytes, not from the index: the lists from the validation pass, the
credential state from parsing the verified config. Volume names are the
outer member names, which must equal the index's rows exactly (a
mismatch is a refusal), each checked as above before it is joined into a
physical name. The destination's requirements come from resolving the
verified config on THIS machine. The index's references table has no
derived counterpart: it is printed as "what the source saw" and nothing
is asserted from it.

Helpers. Both verbs run short-lived containers: `--rm`, labelled
`byre.helper=<project id>` and `byre.helper.run=<random per
invocation>` (not the project label, so they never read as a session;
not a launch record, ADR 0053, because they launch no session),
entrypoint overridden, `-u 0:0` in the identity's userns, no network, no
host bind. On any failure or cancellation the verb force-removes the
helpers carrying ITS run id (stop then remove; a new force-remove on the
runner) before anything else, never another invocation's. If that
removal fails, the summary names the container and prints the engine's
`rm -f` line. Backup's stillness sweep and reset's and forget's session
checks also query `byre.helper=<project id>` and refuse on a hit,
naming it a leftover helper with that line, so a helper that survived
an engine outage is never invisible to the commands that must not run
beside it. The pour mounts the fresh
volume with copy-up disabled (`volume-nocopy` [VERIFY on both engines
in the gated run]) at a path no byre image populates, so the restored
volume holds the backup's bytes and nothing from the helper image; the
gated arm with a base that has files at the mount path proves it.

## byre backup [PROPOSED]

`byre backup [DIR] [--output PATH] [--no-volume NAME]... [--no-credentials]
[--yes]`

1. Resolve the project from DIR (default: current directory; a linked
   worktree resolves to its project). Refuse if the project has no
   config. Resolve the set develop runs (`resolve` plus `combine`:
   cascade, skills, their volumes and mounts); a missing layer, template
   or package refuses here with the load's own message, because the
   volume classification and the references come from that set; a
   missing package is named with its install command.
2. Pick the source engine (the resolved `engine` when set, else
   detection, named in the preview either way) and the identity as
   develop does (`resolveIdentity`, so rootless Podman without keep-id
   refuses as develop refuses). List the project's volumes on the source
   engine with `projectVolumes`, and on every other installed engine too
   (`lifecycleEngines`; a declined binary refuses; an unanswering daemon
   refuses at the query). Carried: every source-engine volume except
   those the resolved set declares with `role = "cache"`, minus
   `--no-volume` names (matched as strings against the carried
   candidates; an unknown one is refused naming them; repeats are
   harmless). A project with no volumes backs up its config alone and
   the preview says so. Pick the helper image: the first
   `imageTagCandidates` tag that exists on the source engine; otherwise
   the resolved base, pulled if absent and proven by running the capture
   command itself on an empty directory, refusing on failure naming the
   base. An image-exists query failure refuses. A pulled image stays, as
   any pull does, and the preview says a pull happened.
3. On a terminal, print the preview and ask y/n unless `--yes`: the
   output path; the source engine; the config with its credential row
   count or "credentials dropped"; each carried volume by logical name,
   marked when the resolved set does not declare it; each `--no-volume`
   volume as "not carried"; each cache-role volume as "not carried
   (cache)"; each project volume found on another installed engine as
   "not carried (on <engine>; this backup reads <source engine>)"; each
   machine-scoped volume the set declares as "not carried; the
   destination binds its own machine-scoped <name>"; the references
   list; the stillness requirement. Off a terminal, no prompt; the same
   lists print in the summary instead, so nothing left out is silent.
4. Take the project setup lock. Under it, re-resolve the set and re-list
   the volumes; if the carried set, the not-carried sets, the
   references, the engine or the config bytes differ from step 2's
   resolution (the baseline whether or not a preview was shown), refuse:
   "changed while you were reviewing; re-run byre backup". That is the
   one resolution the file records; a layer edited during the copy
   itself is not re-read (the project lock does not cover layers, a
   stated residual). Then the stillness sweep, reset's shape minus the
   removal: every installed engine, a running container by project label
   refuses with attach/shell/stop, a container in any other state
   refuses with the engine's `rm` line, a leftover `byre.helper`
   container refuses with its `rm -f` line, a query failure refuses.
   Nothing is removed; the container is still there after the refusal.
5. Still under the lock: apply `--no-credentials` to a copy if asked,
   write the copy to staging and hash it. For each carried volume, run a
   helper from the helper image, the volume mounted read-only at one
   path, `tar --format=posix --numeric-owner -cf - -C /vol .` on stdout, into staging,
   hashed there, headers inspected on the way through to record each
   payload's absolute and traversing symlink targets and its entry
   count. tar's stderr is captured and printed through the funnel in the
   summary (its `socket ignored` lines are the socket list; byre parses
   nothing out of them); a nonzero exit is a failed backup, nothing
   published.
6. Write the index and the outer tar, publish exclusively at the output
   path, release the lock, print the summary: file name, actual bytes,
   volumes carried, the not-carried lists, the symlink list, tar's
   stderr lines.

Cancel at the prompt leaves nothing but a pulled image, when step 2
pulled one: staging is created in step 5. A failure or cancellation at
any later step force-removes this invocation's helper if one is running
(naming it and its `rm -f` line if that fails), removes staging, and
publishes nothing; the output path never holds a partial file.

## byre restore [PROPOSED]

`byre restore FILE [DIR]`, terminal only.

1. Resolve DIR (default: current directory; an existing non-directory
   refuses; created now when absent, see the design call). Resolve the
   project from the real directory. A linked worktree refuses ("restore
   in the main worktree"). Run the same collision fence every verb
   runs (`checkRecord`): an id whose record names another path refuses
   as it does elsewhere. If the project store already has a config,
   refuse: "this project already has a config; restore into a fresh
   checkout".
2. Read the index, verify every payload into staging, validate every
   nested payload (the contract above), parse the verified config bytes.
   Any failure refuses here.
3. Hand the verified config bytes to the apply review as the proposal.
   Apply resolves the extends chain on this machine (a missing layer
   stops here with the path to create) and offers each hinted missing
   package's install as apply does. If any package is still missing
   after that, restore stops naming it and its install command ("install
   it, then run byre restore again"): the set below cannot be resolved
   without it. Restore then resolves the set develop would run from the
   proposal (`resolve` plus `combine`, the same union as backup). From
   that set: the
   engine (`engine` when set, else detection; the review names the
   engine either way; unreachable refuses naming it; a declined binary
   for that engine refuses as develop refuses; other installed engines
   are not consulted, restore is not a totals command), the identity
   (`resolveIdentity`), the base image, the declared volumes, the
   enabled mounts. Join each carried logical name to this project's
   prefix and check ownership as `projectVolumes` would: a physical name
   a longer known project id claims refuses, naming both projects; a
   name over the length bound refuses. Record which carried volumes
   exist on the engine (keep set) and which do not (create set).
4. The review is apply's, plus three sections before the grant summary:
   "Names this machine must satisfy", from the destination's resolved
   set, each with what reads it and how it fails (enabled mount host,
   context file, `[files]` source, Claude Skill path: develop fails
   naming it at launch or at bake; a host seed for a volume left to
   develop: develop seeds from it if present, else starts the volume
   empty; `engine`: this restore uses it, named; `worktree_base`: `byre
   worktree` uses it); "What the source saw", the index's references table printed as
   such; and "From the backup", listing every volume in play by logical
   name: the create set as "will be restored", the keep set as "exists
   here; the backed-up copy is dropped", each marked when this set does
   not declare it; each volume this set declares that the backup does
   not carry as "not in the backup; the first develop handles it as
   today (seeded, or started empty)"; each machine-scoped volume as
   "binds this machine's own"; then the credential state (present:
   "encrypted under the source's passphrase; develop asks for it";
   absent: "none"), each payload's absolute and traversing symlink
   targets as agent-authored content, each payload's dropped entries,
   and the line that the file's authorship is not proven. One y/n.
5. On confirm, in order, one critical section: pull the base image if
   absent and prove it by running the pour command itself (entrypoint
   overridden, `tar -x --no-same-owner` into a scratch directory, fed a
   valid empty archive; failure refuses with no project state written
   and the pulled image left as any pull leaves it); bootstrap the
   store; take the project setup lock; under it, first `requireRecorded`
   (a forget that won the lock meanwhile is cancellation, not permission
   to write into an emptied store; no second bootstrap), then re-resolve
   the set and refuse on any change to the engine, the identity, the
   base or the declared volumes ("changed while you were reviewing;
   re-run byre restore"), re-check that the store still has no config,
   that every create-set volume is still absent and every keep-set
   volume still present, and that no create-set physical name has since
   been claimed by a longer known project id (any difference: the same
   refusal; the cross-project boundary is this re-check, a project
   enrolled after it is the stated residual); write the config bytes
   through apply's write (an
   entry point callable under a held lock; restore writes no `applied`
   marker, because no preset was applied); then for each create-set
   volume: create it and replay its rebuilt stream into a pour helper
   (`SeedLiteral`'s shape taking a reader, copy-up disabled, `tar -x
   --no-same-owner`, then the recursive chown to this machine's box
   identity). Release the lock after the last pour, so a develop that
   arrived meanwhile sees the config and every poured volume together.
6. Failure inside the critical section (a pour's extraction or chown
   fails, a volume cannot be created, the config write fails, Ctrl-C,
   the engine stops answering): force-remove this invocation's helper if
   one is running, remove the volume being poured, remove the config
   restore wrote (forget's anchored `removeIn`, moved where both can
   call it), release the lock, remove staging. Volumes poured whole
   before the failure stay. The summary names the volume,
   the cause, what stayed, and says "fix the cause and run byre restore
   again". If the volume cannot be removed, the summary says so, prints
   the engine's `volume rm` line for it, says a re-run keeps whatever
   occupies that name until it is gone, and names `byre forget` in the
   project directory as the fallback with its warning. If the config
   cannot be removed, the summary says the project now has a config and
   the next restore will refuse until it is removed, and names forget
   the same way. If the helper cannot be removed, the summary names the
   container and its `rm -f` line, and the sweeps above refuse until it
   is gone.
7. Otherwise print the summary: config written, N volumes restored,
   volumes kept, dropped entries, "next: byre develop" (or, when a
   package was left uninstalled, "next: install <package>, then byre
   develop").

Declining at the review writes no config, no store, no volume, and pulls
nothing; staging is removed; the directory restore created in step 1 is
removed again if still empty, as on every pre-write exit. A skill
installed during the review stays installed, as apply already has it.

## Doctrine

- New ADR: box backup (this design), citing P1, P4, P5, P7, with its
  index line in `docs/adr/README.md` in the same commit and its accepted
  residuals on the security-model page.
- 0004: session discovery is unchanged; backup's stillness sweep is
  reset's project-label, any-state, every-engine check, refusing where
  reset removes, plus a `byre.helper` query. Helpers carry `byre.helper`,
  never the project label, so they are never mistaken for a session;
  they are force-removed on failure, and one that survives an engine
  outage is named with its removal line and refuses every sweep until it
  is gone.
- 0007 and 0017: byre still reads no host credential file and seeds no
  login; that stays banned. Restore moves a volume of the user's own
  box, at the user's instruction, and that volume may hold a login; if
  the source box is still in use, the two copies are exactly 0007's
  observed failure, and the first refresh on either side logs the other
  out. Because the record must not hold "this copy is dead" and "this
  copy is accepted" side by side, the new ADR adds an amendment note to
  0007 (the shape 0057's note already has) scoping 0007's copy ban to
  byre-initiated seeding, and the index line changes with it. No
  warning at the prompt (Pete's ruling). Machine-scoped identity never
  travels.
- 0008 and 0032: the pour and the capture are one-shot root-in-userns
  helpers with no host bind, the seeding shape, under `resolveIdentity`
  and `appendUserns` exactly as develop and seeding select them; the ADR
  names seeding as the precedent and distinguishes this from 0008's ban
  on chown at session launch.
- 0009: worktrees allowed at backup; restore refuses a linked worktree
  and runs no git.
- 0013: a declared volume the backup does not carry is handled by the
  first develop as today; the review says so per volume.
- 0014: the base preflight proves the pour command runs, not Debian
  ancestry; the Debian boundary stays a develop warning and a build
  failure. New in kind: restore executes `base` as a byre-driven tool,
  and the ADR says so.
- 0015: disabled mounts are inert and are not listed as requirements.
- 0016: the index names the writing version from `version.String()`
  and the minimum reading version; an unknown format names the latter.
- 0017: machine-scoped volumes never travel; the classification reads
  the same resolved set develop runs, skill declarations included.
- 0022: cobra wiring; usage errors exit 2.
- 0029: restore acquires packages through apply's digest-verified
  install flow and nothing else; a package that flow does not supply
  stops restore.
- 0030: the project file's own closures travel byte-for-byte. Effective
  egress on the destination also composes its defaults, layers and skill
  manifests, which the apply review shows; the design claims nothing
  stronger.
- 0035: every layer in the chain is named; the backup carries pointers
  only.
- 0037: tar-handling precedent for name hygiene only; this format's
  link materialisation is its own contract above.
- 0040: file-supplied names never choose a host path; staging and
  publish are anchored with `OpenDirRootNoFollow`; the ancestor
  residual is inherited, not widened and not narrowed.
- 0044: the index is TOML through the one library, decoded strictly;
  the credentials deletion rides tomldoc.
- 0047: backup's engine enumeration refuses on a declined binary, as
  every totals command does; restore consults only its destination
  engine and refuses a declined one as develop does; neither spawns a
  host tool over anything it wrote.
- 0051: digests are integrity, not authenticity, and the review says so.
- 0053: helpers are not sessions and carry no launch record; the ADR
  records the distinction beside seeding's.
- 0054: exclusive-volume checks are unchanged; a helper that outlives
  the lock is caught by the stillness sweeps' `byre.helper` query and
  named, since the launch-record scan cannot see it.
- 0057: unchanged. Credentials stay encrypted end to end; byre's
  decrypted plaintext still goes to exactly one place.
- 0058: an override run's volume is carried like any other project
  volume; the override itself writes nothing.
- P0: the backup form is logged as a follow-up; restore's screen is the
  existing apply review.
- P4: every list above (requirements, what the source saw, carried,
  kept, not-carried for every reason, other-engine volumes, symlink
  targets, dropped entries, tar's own lines) is printed on a terminal
  and off it, through the funnel, never silent.
- P5: one y/n at restore names the engine, the volumes created and kept,
  the volumes left to develop, and the credentials' state; package
  installs keep their own prompts; nothing is restored with a package
  whose grants the review could not name (0050, 0052 preserved by not
  continuing past a missing package).
- P7: the base image's tar is the pour tool, proved by running the pour
  command before use. No new dependency.
- Security-model page: a backup is an unencrypted 0600 file of the
  project's volumes; the cooperative stillness residual; file authorship
  unproven; a restored login shares its token with the source box; a
  layer can change under its own lock during a copy.
- GLOSSARY: "backup" (the file and the verb pair).

## Evidence before done

Each arm asserts contents and surviving resources, not only the message.

- Round trip: backup then restore into a fresh checkout yields a config
  byte-identical to the source's, credentials decrypting under the
  source passphrase at the first develop (nothing decrypts during
  backup or restore), and `byre credentials rekey` working on the
  restored file. No `applied` marker is written. The outer archive's
  entry set is exactly index, config, one tar per carried volume; no
  image build runs during restore.
- `--no-credentials` leaves no ciphertext, no identity and no
  `[credentials]` block in the backup's config under each legal TOML
  spelling of a row, and the source file is byte-identical afterwards;
  the restore review says credentials are absent.
- Volume selection: every source-engine project volume is carried,
  including one no skill in the set declares; a cache-role volume
  declared by the config and one declared by a skill are not, and the
  preview and the off-terminal summary name them; a volume whose name
  is claimed by a longer project id is not; `--no-volume` takes the
  logical name, drops one, and an unknown name is refused naming the
  candidates; machine-scoped volumes declared by skills are never
  carried and are named; a volume on the other installed engine is not
  carried and is named with its engine, on a terminal and off it; a
  project with no volumes backs up its config.
- Resolved set: an inherited `engine`, `base`, volume, mount, seed,
  context file and `worktree_base` (from a template and from a
  two-layer chain) and a skill-declared volume appear in the
  classification and the references; every layer in the chain is named;
  a disabled mount is not listed; a missing layer, template or package
  refuses backup with the load's message; the source engine is named in
  the preview when `engine` is unset.
- Helper image: a built image present on the source engine is used; with
  none, the base is pulled and the capture command proven; an unpullable
  base, or one whose tar rejects the capture flags, refuses backup
  naming it; an image-exists query failure refuses; cancel after a pull
  leaves the image and says so.
- Stillness: a running container refuses with attach/shell/stop and is
  still running afterwards; a stopped or created container refuses with
  the `rm` line and still exists afterwards; a sibling worktree's
  container refuses; a container on the other installed engine refuses;
  a daemon that does not answer refuses; a declined engine binary
  refuses; backup arriving while develop holds the lock waits, then
  refuses because the session container exists; backup arriving after a
  session has ended waits for nothing and runs; develop arriving
  mid-backup waits and runs afterwards.
- Under-lock re-check: a config save, a layer edit that changes the
  carried set or the engine, or a volume appearing between step 2 and
  the lock refuses with the re-run line, on a terminal and off it.
- Helper lifecycle: a cancelled or failed backup leaves no helper
  container, no staging, and no entry at the output path; the helper
  carries `byre.helper` plus a run id and not the project label; a
  cancellation removes only its own run's helper while another
  invocation's preflight helper is running (fake runner); a helper whose
  removal fails is named with its `rm -f` line and the next backup,
  reset and forget refuse on it; the exclusive scan ignores it.
- File: payloads hashed before the index is written, index-first order,
  format refusal naming the minimum version, an unknown index key
  refused, the writing version is `version.String()`'s, digest mismatch
  refusal for every payload including the config, read budget (index
  bound, config bound, decompressed bound, entry order and count,
  cumulative bytes, free disk, trailing data, duplicate or dotted or
  malformed or over-long logical names), backup-side refusal before
  publish on the same bounds, 0600 modes, 0700 staging, staging removed
  on success and on failure, escaped strings including tar's stderr,
  existing output entry refused naming `--output`, missing output parent
  refused naming it, dated default name from the project directory's
  base name, a nonzero tar exit publishes nothing.
- Forged index: a self-consistent archive whose index misstates a volume
  name or an entry count is refused; one whose index misstates the
  credential state shows the derived state, not the index's; one whose
  references table is fiction prints it under "what the source saw" and
  asserts nothing from it.
- Nested-tar contract, each refused before any project state is written:
  leading `/`, `..` or `.` component, empty component, duplicate name
  (with and without trailing slash), an entry under an earlier symlink,
  an entry under an earlier regular file, a hardlink to a missing or
  non-regular or later target, a sparse entry, a socket header, a
  contiguous-file header, an unknown type flag, an entry count over the
  declared one. Accepted and proven: the `./` root header replayed (an
  empty volume round-trips and the root's mode and mtime land), `./`
  prefixes, trailing-slash directory, PAX long names, a global PAX
  header present in the input and absent from the rebuilt stream, a
  symlink with an absolute target and one with a traversing target
  carried verbatim and listed at both ends, a hardlink to an earlier
  regular entry restored as a hardlink, FIFO and device dropped and
  listed, modes and nanosecond mtimes preserved, setuid, setgid and
  sticky zero on files and directories, the 1,000,000 entry ceiling
  refused, ownership discarded.
- Restore order and refusals: a linked worktree refuses; an id
  collision refuses as elsewhere; an existing config refuses; an
  existing non-directory DIR refuses; a destination physical name a
  longer project id claims refuses naming both; a missing layer stops at
  the review; a missing skill is offered for install where hinted, and
  one still missing afterwards stops restore naming it and its command;
  the engine down refuses naming the engine; a declined destination
  engine refuses; rootless Podman without keep-id refuses as develop
  does; the review names the engine when `engine` is unset and states
  the credentials' passphrase line; every pre-write exit, declining
  included, removes the directory restore created when it is still
  empty and leaves one the user had; an unpullable base, or one whose
  tar rejects the pour command, after confirm refuses with no config
  written; the empty-archive preflight passes on a working base.
- Restore commit: config and pours land in one lock hold (a develop
  waiting on the lock sees both); a forget that won the lock first
  cancels restore with nothing written (`requireRecorded`); a volume
  appearing or vanishing between review and lock, a layer edit that
  changes the engine or the base, or a longer project id claiming a
  create-set name, refuses with the re-run line; volumes poured from the base
  image with this machine's box identity's ownership; a keep-set volume
  kept with the line and its bytes unchanged; the review lists a
  declared volume the backup lacks and a machine-scoped volume.
- Restore failure: a pour failing after one volume poured whole removes
  the failing volume and the config, keeps the first volume, prints the
  re-run line, and a plain `byre restore` then succeeds with the first
  volume reported kept; a chown failure after a successful extraction
  counts as a failed pour and the volume goes; a volume-create failure
  before any pour and a config-write failure each clean up the same way;
  a cancelled pour leaves no helper container and does the same; a
  volume removal failure (fake runner) prints what is left, the `volume
  rm` line and the forget fallback with its warning; a config removal
  failure prints its own line and the fallback; a helper removal failure
  names the container and its `rm -f` line.
- Login posture: restoring a volume that holds an agent login file reads
  nothing from the host and prints no warning while the volume is
  listed.
- Follow-on verbs: `byre config` opens cleanly on the restored project;
  `byre develop` fails on a missing mount host, context file or Claude
  Skill path with today's messages, starts normally with a missing seed
  host for a restored volume, and handles a declared volume the backup
  did not carry as today (host seed, literal seed, prefs seed, empty).
- Off-terminal: backup runs without a prompt and prints the not-carried
  lists in its summary; restore refuses with exit 1. `--yes` skips the
  backup prompt on a terminal. A usage error on either verb exits 2
  without dispatching.
- Worktree: backup from a linked worktree backs up the project.
- Gated (byre-inttest): Docker to rootless Podman and Podman to Docker,
  with a payload whose archived numeric owner is outside the
  destination's user namespace, a base image that has files at the
  helper's mount path, an empty volume, a setgid directory, a file with
  a sub-second mtime, a symlink and a hardlink: the restored tree's
  listing (names, types, sizes, modes, nanosecond mtimes, link targets)
  equals the source's under the stated transformation (special bits
  zero, ownership the box identity's) with nothing extra;
  a real SIGINT delivered to restore mid-pour leaves no helper, no
  config and no partial volume; then a develop on the restored project
  in which the agent's memory file is present at its path, readable,
  with its mode and mtime, and owned by the box identity; `volume-nocopy`
  accepted by both engines [VERIFY].

## Hand-off notes for the implementer

Read `CLAUDE.md` for the conventions that bite: plain `os` filesystem
calls are banned outside `internal/hostopen` (every path the file, the
store or the project names is agent-influenced); contracts pin
byte-exact, behaviour asserts the rule that fired; `gofmt` + `go vet` +
`go test ./...` green before every commit; the docs sweep (README,
ARCHITECTURE, GLOSSARY, the commands page pin, the security-model page,
CHANGES, the ADR index line, the 0007 amendment note) is part of the
unit. Two reviewers, independently, each given the doctrine-index
instruction verbatim; findings that touch a ruling go to Pete.
`byre-inttest` before done, never piped.

Suggested new package: `internal/backup` for the index, the file
writer and reader with its budget, the nested-tar validator and
rebuilder, and the symlink and dropped-entry listings. Command handlers
stay Streams adapters in `internal/commands`; cobra wiring in `cmd/byre`
(ADR 0022).

New seams, each small: the apply review needs an entry point that takes
config bytes; `resolve`/`combine` need a form that resolves a proposal
rather than the store's config; apply's confirmed write needs a form
callable under a lock the caller holds, without the `applied` marker;
the runner needs `ImagePull`, a helper runner (labelled with the
project id and a run id, `--rm`, entrypoint overridden, optional
read-only volume, optional nocopy volume, stdin reader in, stdout stream
and stderr out), and a force-remove (`ContainerRemove` is non-forcing
and there is no stop; `ContainersByLabel` exists for the lookup);
hostopen needs a streaming exclusive publish with the temp-then-link
shape plus fsync of file and directory, and an anchored staging root;
forget's `removeIn` moves to a shared home for the config rollback.
`skills.Resolve` needs no partial mode: restore stops on a missing
package instead. Reused as they are:
`lifecycleEngines`, `projectVolumes` and its ownership rule,
`resolveIdentity`, `imageTagCandidates`, `appendUserns`, reset's
`clearSessionMarkers` shape (refusing instead of removing),
`reportRunning`, the setup lock, `Canonicalize`, tomldoc deletion,
`version.String()`.
