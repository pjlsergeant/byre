# byre backup / byre restore

Status: v1 design, 2026-10-01, revised 2026-10-02 after two three-reviewer
sign-off rounds (Codex, Grok, Z.AI; both rounds NOT READY; findings in the
review log). This replaces the box export/import design (v5,
`wip/box-export-import.md`, deleted in commit a1edcc20; git history keeps
it). The restart came out of a three-reviewer round on v5 (2026-09-21)
and the grill that followed, where Pete cut the feature down to what
people actually want back: the agent's memory and context, the
credentials, and the project's config. Every ruling below is Pete's. Not
yet authorization to implement: TODO.md owns task status. wip/
lifecycle: absorbed into an ADR and the docs on ship, then deleted.

Provenance: [RULING] = Pete's, settled, do not reopen to close a finding.
[RULING 2026-10-01] = Pete's, given while working through the sign-off
findings. [DESIGN CALL] = chosen by the designer to close a finding,
flagged to Pete, overridable. [CODE] = verified against the tree on
2026-10-02, after two rounds of reviewer corrections. [PROPOSED] = this
design's mechanism.

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
  no other store files (the applied marker, engine records and launch
  records are this machine's history).
- [RULING 2026-10-01] Every project volume that exists on the source
  engine comes along unless the user says otherwise: `--no-volume NAME`
  drops one. That includes a volume made by a one-off `develop --agent
  grok` run whose agent is not in the config: it is the project's state
  and the user expects it back. The only volumes never carried are the
  ones the resolved config declares with `role = "cache"`. NAME is the
  logical name, the part after the project prefix (`.claude`, `.grok`),
  which is how the preview and the summary print volumes.
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
  saw; restore's review handles a missing package exactly as `byre
  preset apply` does (an install offered where the config carries a
  `[sources]` hint, "install it yourself" where it does not, and the
  review continues with that package marked unknown) and stops at a
  missing layer with the path to create, as every strict load does.
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
- [RULING] Restore pours volumes using the effective config's BASE image,
  not a built image.
- [RULING 2026-10-01] Restore proves the base image before it commits:
  it pulls the image if absent and runs `tar --version` in it, before
  any project state is written. A base that cannot be fetched or has no
  tar refuses, naming the base and the engine's error. This is a
  tool-availability preflight for the pour and nothing more: byre's
  Debian-derived support boundary stays what it is today, a warning at
  develop and a failure at build.
- [RULING 2026-10-01, mechanism revised 2026-10-02 as a DESIGN CALL]
  Restore can still fail part way after the base is proven: the user
  presses Ctrl-C, the engine goes away, the engine's disk fills, a
  volume cannot be created. Pete's ruling was "forget, then restore
  again", chosen for needing no new mechanism. The design call refines
  the mechanism and keeps the intent: a failed pour stops the pour
  container, removes the volume it created, and removes the config
  restore wrote, all under the lock restore still holds. Nothing restore
  made remains except volumes already poured whole, which the next
  restore meets as "exists here; kept" and whose content is the backup's
  anyway. The summary names the volume and the cause and says to fix the
  cause and run `byre restore` again. Only when that cleanup itself fails
  (the engine is gone, so the volume cannot be removed) does the summary
  say what is left behind and name `byre forget`, run in the project
  directory, as the fallback, with the warning that forget removes every
  volume and image of the project, kept ones included, and needs every
  installed engine reachable. Reason for the call: forget takes no
  directory argument, prompts, deletes kept volumes and images, and
  refuses while any engine is unreachable, so it is a poor first remedy
  for the common case; one file removal makes a plain re-run work.
- [RULING] Restore refuses to run on a project that already has a
  config: "this project already has a config; restore into a fresh
  checkout". A volume that already exists on the engine is kept and the
  summary says the backed-up copy was dropped.
- [DESIGN CALL] Restore refuses to run from a linked worktree: "restore
  in the main worktree". A linked worktree is its main tree's project
  (ADR 0009), and restoring the main tree's state from a side checkout
  whose main tree may be missing or unconfigured has no good answer.
  Backup from a linked worktree stays allowed.
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
  there is no lookup by name. Usage errors exit 2 (ADR 0022); refusals
  exit 1.
- [RULING] Default output is `<folder>-<YYYY-MM-DD>.byre-backup.tar.gz`
  in the current directory, `<folder>` being the project directory's
  base name and the date local; `--output PATH` elsewhere. An existing
  entry of any kind at the output path is a refusal, never an overwrite,
  so a second backup the same day refuses naming `--output`.
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
  bundled skill declares a cache-role volume (the node template does).
  Claude's memory, transcripts, Codex's sessions and history all live in
  the project-scoped volume. Physical names: project `byre-<id>-<name>`,
  machine `byre-machine-u<uid>-<name>` (`scopedVolumeName`, naming.go,
  branching on `Volume.MachineScoped()` in config.go); names carry no
  engine component and volumes carry no labels. `volumeNameRe`
  (config.go:141) is Docker's character set and admits `.` and `..`.
  Reset and forget enumerate a project's volumes with `projectVolumes`
  (naming.go), which lists by the `byre-<id>-` prefix and then excludes
  any name a longer known project id claims; they call it on every
  installed engine because state can live in an engine the config no
  longer names.
- Project identity is `<slug>-<6 hex of sha256(canonical dir)>`
  (project.go idFromCanonical); a linked worktree resolves to the main
  worktree's id and store (project.go Resolve). The store
  `~/.byre/projects/<id>/` holds `byre.config`, the `applied` marker that
  `preset apply` writes (hash plus source; the drift states derive from
  it), one `engine.<worktree-id>` record per worktree, launch records,
  the path record and the lock.
- The cascade (config.go Load): `default.config`, then the template, then
  the extends chain root-first, then the project file. `engine`, `base`,
  volumes, mounts, seeds, context files, Claude Skill paths and
  `worktree_base` can all be inherited; the project file alone does not
  determine any of them. A disabled mount is skipped before its host
  path is expanded (runparams.go, ADR 0015).
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
  it yourself and continues with the package marked "not installed --
  grants unknown", renders the grant review with a diff against the
  current store, and on confirm bootstraps the store, takes the setup
  lock, re-reads the store and refuses if it changed (appeared, vanished
  or differs), re-reads the source and refuses only if that re-read
  succeeds with different bytes, writes the config, then writes the
  `applied` marker. An explicit path or URI is read through
  `packages.Fetcher.FetchManifest`, bounded by `packages.MaxManifestBytes`
  (256 KiB), not the 1 MiB config bound. Restore must hand apply the
  config BYTES it already verified, read under `config.MaxConfigBytes`,
  rather than a path.
- Named layers live at `~/.byre/layers/<name>/layer.config`; a missing one
  fails every strict load with "layer X not found -- create <path>"
  (layers.go:146), `status` included. The config editor degrades
  instead: no volume screen when resolution fails, inheritance marks
  dropped for a broken template or skill.
- Host paths at develop: a mount rides `--mount type=bind`, which the
  engine refuses when the source is missing (runparams.go:157 relies on
  it); a `[[context]]` file is read at bake and a missing one fails the
  build naming context and path (build/context.go); a `[[claude_skills]]`
  path is validated as a skill directory at bake (planClaudeSkills).
  Seeding runs only for a volume that does not yet exist: a missing seed
  host prints "seed source not found; starts empty", a present one is
  copied in, and `seed_prefs` (ADR 0013) does the same for a fresh agent
  state volume. `worktree_base` is read by `byre worktree`, not by
  develop.
- Seeding (seed.go, runner SeedVolume/SeedFiles/SeedLiteral): every seed
  function takes the image as a parameter; the copy runs with the image
  entrypoint bypassed, as `-u 0:0` inside the box's userns mapping
  (`appendUserns(args, id.Userns())`), and ends with a recursive chown to
  the box identity; a failed seed removes the volume it created and a
  failed removal says "remove it manually"; an existing volume is left
  alone. `SeedLiteral` streams content over stdin with no host bind and
  no label. The runner has `ImageExists` and no pull method; `run` pulls
  implicitly.
- Identity (identity.go): develop uses `resolveIdentity`, which refuses
  rootless Podman without a keep-id mapping unless
  `BYRE_ALLOW_ROOTLESS_PODMAN=1`; `engineIdentity` is the lifecycle
  commands' never-refusing fallback and lands files on the host uid. The
  built image has several tag candidates (`imageTagCandidates`: the
  identity's tag, the generic rootless tag, the host uid/gid tag, the
  legacy tag).
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
  takes it. Named layers have their own lock (layers.go); a layer can
  change without the project file's bytes changing.
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
  for a stopped container. The existing unreachable-engine refusal names
  the engine ("%s isn't reachable").
- `byre forget` (forget.go): current directory only, no DIR argument;
  prompts y/N unless forced; refuses while a session is live; removes
  every project volume and every image-tag candidate on every installed
  engine, then the store; if any removal fails it leaves the store in
  place and says so. `lifecycleEngines` makes it refuse while an
  installed engine is unreachable or declined.
- `byre config` opens the project's config file directly; a configured
  skill the catalog does not know is kept as an ordinary enabled row
  with no reason and no install command (install hints are printed by
  `preset apply`, `inspect` and `skills.Resolve` at develop); only
  catalog problem rows (invalid, conflict, legacy) show disabled with a
  reason.
- hostopen (publish.go, ADR 0040): `PublishFileExclusive` takes the whole
  content as a string and anchors on `os.OpenRoot(dir)`, which guards the
  final directory and not every ancestor; ADR 0040 accepts that ancestor
  residual on the record. hostopen has no file-removal operation today;
  forget removes the store through its own anchored path.
- Nested-tar hygiene exists in internal/deliver/tar.go (ADR 0037): no
  `..`, no empty components, a leading `/` STRIPPED where this design
  refuses, and only regular files and directories written; links and
  specials are skipped by name. It is not a precedent for materialising
  links.
- GNU tar 1.34 stores a FIFO and exits 0; it omits a socket, prints
  `tar: ./NAME: socket ignored` on stderr and still exits 0 (reproduced
  2026-10-01 by two reviewers). Archiving `-C /vol .` emits a root
  directory header named exactly `./`, then `./` prefixed names, and
  does so for an empty volume too. As root, `tar -x` restores archived
  numeric ownership unless told `--no-same-owner`, and a recursive chown
  clears setuid and setgid bits (reproduced by a reviewer).

## The backup file [PROPOSED]

One gzip-compressed tar. Entry order is fixed and restore refuses any
other:

1. `backup.toml`, the index: `format = 1`, the writing byre version
   (`version.Resolve()`, never a literal) and the minimum byre version
   that reads this format (a constant of the format, set when the unit
   ships), the project folder name, the source engine (information
   only), a `config` row (byte count, sha256, whether credentials are
   included), one `[[volumes]]` row per carried volume (logical name,
   byte count, sha256, entry count), and a `references` table: what
   backup saw in the SOURCE's effective config, named for the human at
   restore (every layer in the chain, the template, the agent, each
   skill, each enabled mount host, each context file, each Claude Skill
   path, each seed host, and `engine` and `worktree_base` when set). The
   index is a table of contents and a note from the source. Every value
   the review prints as a fact about this restore is derived from
   verified content (below); the references table is printed as what the
   source saw, through the funnel, and asserted as nothing more.
2. `byre.config`: the project config, byte-for-byte, or with the
   credentials block and rows deleted through tomldoc under
   `--no-credentials`.
3. `volumes/<name>.tar`: one plain tar per carried volume, the volume
   root as the tar root, written inside a container with
   `--numeric-owner`; restore discards ownership (the pour's chown is the
   ownership step).

Staging. Every payload is produced into a private staging directory
(0700; payload files 0600) under the output file's directory for backup
and under the store's parent for restore, written through an anchored
root so no file-supplied name can choose a path; the disk check is on
that filesystem and counts the payloads plus the outer file. Everything
is hashed in staging before the outer tar is written index-first. The
published file is 0600 and is created exclusively through a streaming
variant of hostopen's exclusive publish, inheriting hostopen's boundary
as it stands (ADR 0040): an existing entry at the output path is a
refusal, never an overwrite. Staging is removed on every exit, success
or failure.

Reading. Restore reads the index first, bounded by `config.MaxConfigBytes`
(1 MiB), refuses an unknown `format` naming the minimum byre version from
the index, then verifies every payload's length and sha256 into staging
before anything is written to its final place. Bounds, all refusals:
the config member may not exceed `config.MaxConfigBytes`; the
decompressed outer stream may not exceed the index's declared totals
plus 64 KiB plus 1 KiB per outer entry; outer entries are exactly the
index's (duplicate, extra, missing, out of order, or nonzero trailing
data refused); logical volume names must pass `volumeNameRe`, must not
be `.` or `..`, and must be unique; staged bytes are checked cumulatively
against the declared totals and against free disk; a nested payload may
hold at most 1,000,000 entries and must match its declared entry count.
Backup enforces the same bounds on what it writes and refuses before
publishing, so a backup byre produced is one byre reads. Digests detect
corruption, not authorship, and the restore review says so.

The nested-tar contract. Restore validates every nested payload in
staging, header by header through `archive/tar`, before any project
state is written; a payload that fails validation refuses the whole
restore, naming the volume and the entry. Rules:

- The root entry: GNU tar's `./` directory header is accepted, not
  counted, and not replayed; the volume root already exists. A payload
  with no root entry is fine too.
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
  must name an earlier regular entry of the same payload. FIFOs and
  devices are dropped by name and listed. Sparse entries are refused
  (the pour would expand them; agent state holds none). PAX long names
  and global headers are accepted. Modes and mtimes are preserved by the
  pour; setuid and setgid bits are not (the final chown clears them, and
  the contract says so rather than promising them); ownership is
  discarded.
- The pour replays the validated stream through `archive/tar`, so the
  container's tar receives a normalised PAX stream with the dropped
  entries absent and ownership fields zeroed, and extracts it with
  `--no-same-owner`, so an archived uid outside this machine's user
  namespace can never fail an extraction. The pour container holds the
  fresh volume and nothing else, so a hostile entry can reach only that
  volume or the throwaway rootfs.

Every string the file supplies that a review or error prints goes through
the existing terminal-data funnels (P4). Host writes ride hostopen's
anchored operations.

Derived, not trusted. The review's symlink-target lists, dropped-entry
lists, credential state and volume names come from the verified payloads
and config bytes, not from the index: the lists from the validation pass,
the credential state from parsing the verified config, each logical
volume name checked as above before it is joined into a physical name.
The destination's requirements come from resolving the verified config
on THIS machine (apply's review does that); the index's references table
is printed separately as "what the source saw".

## byre backup [PROPOSED]

`byre backup [DIR] [--output PATH] [--no-volume NAME]... [--no-credentials]
[--yes]`

1. Resolve the project from DIR (default: current directory; a linked
   worktree resolves to its project). Refuse if the project has no
   config. Resolve the effective config through the cascade; a missing
   layer or template refuses here with the load's own message, because
   the volume set and the references come from the effective config.
2. Pick the source engine (the effective `engine` when set, else
   detection) and the identity as develop does (`resolveIdentity`, so
   rootless Podman without keep-id refuses as develop refuses). List the
   project's volumes on the source engine with `projectVolumes`, and on
   every other installed engine too. Carried: every source-engine volume
   except those the effective config declares with `role = "cache"`,
   minus `--no-volume` names (logical names; an unknown one is refused
   naming the list; repeats are harmless). A project with no volumes
   backs up its config alone and the preview says so. Pick the helper
   image: the first `imageTagCandidates` tag that exists on the source
   engine; otherwise the effective base, pulled if absent and proven
   with `tar --version`, refusing on failure naming the base. An
   image-exists query failure refuses.
3. On a terminal, print the preview and ask y/n unless `--yes`: the
   output path; the config with its credential row count or "credentials
   dropped"; each carried volume by logical name, marked when the
   effective config does not name it; each `--no-volume` volume as "not
   carried"; each project volume found on another installed engine as
   "not carried (on <engine>; this backup reads <source engine>)"; each
   machine-scoped volume the config resolves as "not carried; the
   destination binds its own machine-scoped <name>"; the references
   list; the stillness requirement. Off a terminal, no preview and no
   prompt. No size estimate: the engine offers no cheap one, and the
   summary prints the actual bytes.
4. Take the project setup lock. Under it, re-resolve the effective config
   and re-list the volumes; if the carried set, the cache set, the
   references or the config bytes differ from step 2's resolution (the
   baseline whether or not a preview was shown), refuse: "changed while
   you were reviewing; re-run byre backup". Then the stillness sweep,
   reset's shape minus the removal: every installed engine via
   `lifecycleEngines` (a declined binary refuses), a running container by
   project label refuses with attach/shell/stop, a container in any
   other state refuses with the engine's `rm` line, a query failure
   refuses. Nothing is removed; the container is still there after the
   refusal.
5. Still under the lock: apply `--no-credentials` to a copy if asked,
   write the copy to staging and hash it. For each carried volume, run a
   short-lived container from the helper image as `-u 0:0` in the
   identity's userns, the volume mounted read-only at one path, no
   network, no host bind, labelled as byre's helper (not with the project
   label), entrypoint overridden to `tar --numeric-owner -cf - -C /vol .`
   on stdout, into staging, hashed there, headers inspected on the way
   through to record each payload's absolute and traversing symlink
   targets and its entry count. tar's stderr is captured and printed
   verbatim in the summary (its `socket ignored` lines are the socket
   list; byre parses nothing out of them); a nonzero exit is a failed
   backup, nothing published.
6. Write the index and the outer tar, publish exclusively at the output
   path, release the lock, print the summary: file name, actual bytes,
   volumes carried, the symlink list, tar's stderr lines.

Cancel at the prompt leaves nothing: staging is created in step 5. A
failure at any later step removes staging and publishes nothing.

## byre restore [PROPOSED]

`byre restore FILE [DIR]`, terminal only.

1. Resolve DIR (default: current directory; not yet created; an existing
   non-directory refuses). A linked worktree refuses ("restore in the
   main worktree"). If the project store already has a config, refuse:
   "this project already has a config; restore into a fresh checkout".
2. Read the index, verify every payload into staging, validate every
   nested payload (the contract above), parse the verified config bytes.
   Any failure refuses here.
3. Hand the verified config bytes to the apply review as the proposal.
   Apply resolves the extends chain on this machine (a missing layer
   stops here with the path to create), handles a missing template,
   agent or skill as apply handles them, and yields the DESTINATION's
   effective config. From it: the engine (`engine` when set, else
   detection; unreachable refuses naming the engine, as today's message
   does), the identity (`resolveIdentity`), the base image, the declared
   volumes and the enabled mounts.
4. The review is apply's, plus three sections before the grant summary:
   "Names this machine must satisfy", from the destination's effective
   config, each with what reads it and how it fails (enabled mount host,
   context file, Claude Skill path: develop fails naming it; `engine`:
   this restore uses it; `worktree_base`: `byre worktree` uses it; seed
   hosts are listed under the volume they belong to, below); "What the
   source saw", the index's references table printed as such; and "From
   the backup", listing every volume in play by logical name: each
   carried volume as "will be restored" or "exists here; the backed-up
   copy is dropped", marked when this config does not declare it; each
   volume this config declares that the backup does not carry as "not
   in the backup; starts empty, or seeded from <host> if that exists
   when develop first runs"; each machine-scoped volume as "binds this
   machine's own"; then the credential state, each payload's absolute
   and traversing symlink targets as agent-authored content, each
   payload's dropped entries, and the line that the file's authorship is
   not proven. One y/n. If the user declined a package install during
   the review and the effective base or engine therefore cannot be
   resolved, restore refuses before the prompt, naming the package: it
   cannot prove a base it cannot name.
5. On confirm, in order, one critical section: pull the base image if
   absent and prove `tar --version` in it (failure refuses, nothing
   written, the pulled image stays as any pull does); create DIR if
   absent; bootstrap the store; take the project setup lock; re-check
   under it that the store still has no config and that the carried
   volumes are still absent on the engine (either changed: refuse,
   "changed while you were reviewing; re-run byre restore"); write the
   config bytes through apply's write (an entry point callable under a
   held lock; restore writes no `applied` marker, because no preset was
   applied, so `preset state` reads "unapplied", which is true); then for
   each carried volume: create it and replay its validated stream into a
   pour container (`SeedLiteral`'s shape taking a reader: `-u 0:0`, the
   identity's userns, entrypoint bypassed, labelled as byre's helper,
   `tar -x --no-same-owner` into the mounted volume, then the recursive
   chown to this machine's box identity). Release the lock after the
   last pour, so a develop that arrived meanwhile sees the config and
   every poured volume together.
6. Failure inside the critical section (a pour fails, a volume cannot be
   created, Ctrl-C, the engine stops answering): stop and remove the
   helper container if one is running, remove the volume being poured,
   remove the config restore wrote, release the lock, remove staging.
   Volumes poured whole before the failure stay. The summary names the
   volume, the cause, what stayed, and says "fix the cause and run byre
   restore again". If removing the volume or the config fails, the
   summary says exactly what is left and names the fallback: `byre
   forget` in the project directory, which removes every volume and
   image of the project, kept ones included, and needs every installed
   engine reachable.
7. Otherwise print the summary: config written, N volumes restored,
   volumes kept, dropped entries, "next: byre develop" (or, when a
   package was left uninstalled, "next: install <package>, then byre
   develop").

Declining at the review writes nothing: no DIR, no store, no volume, no
pull; staging is removed. A skill installed during the review stays
installed, as apply already has it.

## Doctrine

- New ADR: box backup (this design), citing P1, P4, P5, P7, with its
  index line in `docs/adr/README.md` in the same commit and its accepted
  residuals on the security-model page.
- 0004: session discovery is unchanged; backup's stillness sweep is
  reset's project-label, any-state, every-engine check, refusing where
  reset removes.
- 0007 and 0017: byre still reads no host credential file and seeds no
  login, and 0007's specific rejection, two independent holders of one
  rotating refresh token, is not reopened as a feature. Restore moves a
  volume of the user's own box, at the user's instruction, and that
  volume may hold a login; if the source box is still in use, the two
  copies are exactly 0007's failure, and the first refresh on either
  side logs the other out. The new ADR records this as an accepted
  consequence of moving box state, named on the security-model page,
  with no warning at the prompt (Pete's ruling). Machine-scoped identity
  never travels.
- 0008 and 0032: the pour and the capture are one-shot root-in-userns
  helpers with no host bind, the seeding shape, under `resolveIdentity`
  and `appendUserns` exactly as develop and seeding select them; the ADR
  names seeding as the precedent and distinguishes this from 0008's ban
  on chown at session launch.
- 0009: worktrees allowed at backup; restore refuses a linked worktree
  and runs no git.
- 0013: a declared volume the backup does not carry is seeded at first
  develop as today; the review says so per volume.
- 0014: unchanged. The base preflight proves tar, not Debian ancestry;
  the Debian boundary stays a develop warning and a build failure.
- 0015: disabled mounts are inert and are not listed as requirements.
- 0016: the index names the writing version from `version.Resolve()`
  and the minimum reading version; an unknown format names the latter.
- 0017: machine-scoped volumes never travel; no narrowing, no amendment.
- 0022: cobra wiring; usage errors exit 2.
- 0030: the project file's own closures travel byte-for-byte. Effective
  egress on the destination also composes its defaults, layers and skill
  manifests, which the apply review shows; the design claims nothing
  stronger.
- 0035: every layer in the chain is named; the backup carries pointers
  only.
- 0040: file-supplied names never choose a host path; staging and
  publish are anchored; hostopen's accepted ancestor residual is
  inherited, not widened and not narrowed.
- 0044: the index is TOML through the one library; the credentials
  deletion rides tomldoc.
- 0047: both verbs' engine enumeration refuses on a declined binary, as
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
- P4: every list above (requirements, what the source saw, carried,
  kept, not-carried and other-engine volumes, symlink targets, dropped
  entries, tar's own lines) is printed, never silent.
- P7: the base image's tar is the pour tool, proved before use. No new
  dependency.
- Security-model page: a backup is an unencrypted 0600 file of the
  project's volumes; the cooperative stillness residual; file authorship
  unproven; a restored login shares its token with the source box.
- GLOSSARY: "backup" (the file and the verb pair).

## Evidence before done

Each arm asserts contents and surviving resources, not only the message.

- Round trip: backup then restore into a fresh checkout yields a config
  byte-identical to the source's, credentials decrypting under the
  source passphrase at the first develop, and `byre credentials rekey`
  working on the restored file. No `applied` marker is written. The
  outer archive's entry set is exactly index, config, one tar per
  carried volume; no image build runs during restore.
- `--no-credentials` leaves no ciphertext, no identity and no
  `[credentials]` block in the backup's config, and the source file is
  byte-identical afterwards; the restore review says credentials are
  absent.
- Volume selection: every source-engine project volume is carried,
  including one no skill in the config declares; a cache-role volume is
  not; a volume whose name is claimed by a longer project id is not;
  `--no-volume` takes the logical name, drops one, and an unknown name
  is refused naming the list; machine-scoped volumes are never carried
  and the preview names them; a volume on the other installed engine is
  not carried and the preview names it with its engine; a project with
  no volumes backs up its config.
- Cascade: an inherited `engine`, `base`, volume, mount, seed, context
  file and `worktree_base` (from a template and from a two-layer chain)
  appear in the carried set and the references; every layer in the
  chain is named; a disabled mount is not listed; a missing layer or
  template refuses backup with the load's message.
- Helper image: a built image present on the source engine is used; with
  none, the base is pulled and proven; an unpullable or tar-less base
  refuses backup naming it; an image-exists query failure refuses.
- Stillness: a running container refuses with attach/shell/stop and is
  still running afterwards; a stopped or created container refuses with
  the `rm` line and still exists afterwards; a sibling worktree's
  container refuses; a container on the other installed engine refuses;
  an engine that cannot be queried refuses; a declined engine binary
  refuses; backup arriving while develop holds the lock waits and runs
  afterwards; develop arriving mid-backup waits and runs afterwards.
- Under-lock re-check: a config save, a layer edit that changes the
  carried set, or a volume appearing between step 2 and the lock refuses
  with the re-run line, on a terminal and off it.
- File: payloads hashed before the index is written, index-first order,
  format refusal naming the minimum version, the writing version is
  `version.Resolve()`'s, digest mismatch refusal for every payload
  including the config, read budget (index bound, config bound,
  decompressed bound, entry order and count, cumulative bytes, free
  disk, trailing data, duplicate or dotted or malformed logical names),
  backup-side refusal before publish on the same bounds, 0600 modes,
  0700 staging, staging removed on success and on failure, escaped
  strings, existing output entry refused naming `--output`, dated
  default name from the project directory's base name, a nonzero tar
  exit publishes nothing, tar's stderr lines reach the summary.
- Forged index: a self-consistent archive whose index misstates the
  credential state, a volume name, an entry count or the references is
  refused where the field is checked (name, count) and the review shows
  the derived value, not the index's, elsewhere.
- Nested-tar contract, each refused before any project state is written:
  leading `/`, `..` or `.` component, empty component, duplicate name
  (with and without trailing slash), an entry under an earlier symlink,
  an entry under an earlier regular file, a hardlink to a missing or
  non-regular or later target, a sparse entry, an entry count over the
  declared one. Accepted and proven: the `./` root header (an empty
  volume round-trips), `./` prefixes, trailing-slash directory, PAX long
  names and global headers, a symlink with an absolute target and one
  with a traversing target carried verbatim and listed at both ends, a
  hardlink to an earlier regular entry restored as a hardlink, FIFO and
  device dropped and listed, modes and mtimes preserved, setuid cleared,
  ownership discarded.
- Restore order and refusals: a linked worktree refuses; an existing
  config refuses; a missing layer stops at the review; a missing skill
  is offered for install where hinted and left marked where not; a
  declined install that leaves the base unresolvable refuses before the
  prompt; the engine down refuses naming the engine; declining the
  review leaves no DIR, no store, no volume, no pull and no staging; an
  unpullable or tar-less base after confirm refuses with no config
  written.
- Restore commit: config and pours land in one lock hold (a develop
  waiting on the lock sees both); a volume appearing between confirm and
  the lock refuses with the re-run line; volumes poured from the base
  image with this machine's box identity's ownership; existing volume
  kept with the line and its bytes unchanged; the review lists a
  declared volume the backup lacks and a machine-scoped volume.
- Restore failure: a pour failing after one volume poured whole removes
  the failing volume and the config, keeps the first volume, prints the
  re-run line, and a plain `byre restore` then succeeds with the first
  volume reported kept; a cancelled pour (context cancellation standing
  in for Ctrl-C) does the same and leaves no helper container; a volume
  removal failure (fake runner) prints what is left and the forget
  fallback with its warning; a config removal failure does the same.
- Login posture: restoring a volume that holds an agent login file reads
  nothing from the host and prints no warning while the volume is
  listed.
- Follow-on verbs: `byre config` opens cleanly on the restored project;
  `byre develop` fails on a missing mount host, context file or Claude
  Skill path with today's messages, starts normally with a missing seed
  host for a restored volume, and seeds a declared volume the backup did
  not carry as today.
- Off-terminal: backup runs without a prompt; restore refuses. `--yes`
  skips the backup prompt on a terminal.
- Worktree: backup from a linked worktree backs up the project.
- Gated (byre-inttest): Docker to rootless Podman and Podman to Docker,
  with a payload whose archived numeric owner is outside the
  destination's user namespace: a real Claude state volume with a memory
  file streamed out and poured back, then a develop on the restored
  project in which the agent's memory file is present at its path,
  readable, with its mode and mtime, and owned by the box identity.

## Hand-off notes for the implementer

Read `CLAUDE.md` for the conventions that bite: plain `os` filesystem
calls are banned outside `internal/hostopen` (every path the file, the
store or the project names is agent-influenced); contracts pin
byte-exact, behaviour asserts the rule that fired; `gofmt` + `go vet` +
`go test ./...` green before every commit; the docs sweep (README,
ARCHITECTURE, GLOSSARY, the commands page pin, the security-model page,
CHANGES, the ADR index line) is part of the unit. Two reviewers,
independently, each given the doctrine-index instruction verbatim;
findings that touch a ruling go to Pete. `byre-inttest` before done,
never piped.

Suggested new package: `internal/backup` for the index, the file
writer and reader with its budget, the nested-tar validator and
normaliser, and the symlink and dropped-entry listings. Command handlers
stay Streams adapters in `internal/commands`; cobra wiring in `cmd/byre`
(ADR 0022).

New seams, each small: the apply review needs an entry point that takes
config bytes and returns the destination's effective config; apply's
confirmed write needs a form callable under a lock the caller holds,
without the `applied` marker; the runner needs `ImagePull`, a seed
variant that takes an `io.Reader` and a label, a capture variant that
runs a labelled container with a read-only volume and returns stdout as
a stream plus stderr, and `ContainerRemove` by label for cleanup;
hostopen needs a streaming exclusive publish (today's
`PublishFileExclusive` takes the whole content as a string), an anchored
staging root, and an anchored single-file removal for the config
rollback (or reuse of forget's store removal, scoped to one file).
Reused as they are: `lifecycleEngines`, `projectVolumes`,
`resolveIdentity`, `imageTagCandidates`, `appendUserns`, reset's
`clearSessionMarkers` shape (refusing instead of removing),
`reportRunning`, the setup lock, the cascade loader, tomldoc deletion.
