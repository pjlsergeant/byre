# Box export / import

Status: v5 design draft, 2026-09-21. v2 was reviewed fresh by Grok, Z.AI
and Claude (two BUILD-READY WITH CONDITIONS, one NOT BUILD-READY); v3
folded every verified finding and four further rulings from the same
evening's grill; v3 was reviewed fresh by the same three (all BUILD-READY
WITH CONDITIONS, no operator decision requested) and v4 folds those
conditions. v4 was reviewed fresh by Codex, Z.AI and Grok on 2026-09-21
and v5 folds the three rulings from the grill that followed (the remaining
findings are still being ruled on). Every ruling below is Pete's; every
mechanism is a proposal awaiting a fresh external review. Not
authorization to implement. TODO.md owns task status. Ships as ONE unit.
wip/ lifecycle: absorbed into an ADR and the docs on ship, then deleted.

Provenance: [RULING] = Pete's, settled, do not reopen to close a finding.
[CODE] = verified against the tree. [PROPOSED] = this draft's mechanism.

## What this is

Transfer a box's setup, optionally with its state volumes and its workspace,
to another machine or another person, as one ordinary file. Import writes a
project byre builds and manages normally. Nothing engine-side exists on the
destination until the first `byre develop`.

## Rulings

Carried from the earlier grill (2026-09-08, morning):

- [RULING] Export requires a completely still box: no container for this
  project in any state, and the stillness holds for the whole copy.
  Refusals print the actual engine's attach and stop commands
  (reportRunning in develop.go already has the shape [CODE]). byre never
  stops a session on the user's behalf.
- [RULING] No built image and no ephemeral container filesystem travel,
  not even as options. Import rebuilds through byre; unpinned build inputs
  may resolve to different versions.
- [RULING] Workspace inclusion means EVERYTHING, ignored and untracked files
  included, no exclusion picker. Helper text: "Includes everything in the
  workspace, including ignored and untracked files. Leave unchecked if you'll
  transfer the project separately, for example through Git."
- [RULING] An archive carrying a workspace imports only into a new or empty
  directory. An archive without one may target an existing checkout. No
  merge, no overwrite.
- [RULING] Extra host bind mounts travel as declarations only. Import remaps
  or disables each; export names the reconnection needs.
- [RULING] "Include encrypted credentials" is checked by default.
- [RULING] No archive encryption. The user protects the file with their own
  tools. Selected workspace and volume contents may contain plaintext
  secrets and the credential checkbox makes no claim about them.

From today's grill:

- [RULING] Machine-scoped volumes are never offered by export, whether a
  skill or the project config declares them -- the rule is by resolved
  scope, not by declarer. The export preview and the import review each
  name them once: "not carried; the destination binds its own
  machine-scoped <name> volume (its login, or a fresh empty one)". Every
  machine-scoped volume byre ships is identity state, and the destination
  logs in once, which is ADR 0017's intended path.
- [RULING] Skills, the agent and templates are references, exactly like
  external mounts: "these must exist on the destination". Nothing is
  carried or installed. The archive carries `[sources]` hints. An installed
  package at a different digest is used as is, with a warning naming both
  digests.
- [RULING] The source cascade is flattened into ONE project config. The
  destination's own machine default applies underneath it.
- [RULING] Export prompts for the contributing files' passphrases and
  re-encrypts every credential row into the one flattened file. If one
  passphrase opened every contributing file, the flattened file's identity
  is wrapped under that same passphrase. If more than one was needed,
  export asks for a fresh passphrase for the flattened file.
- [RULING] Unchecked credentials: the rows are dropped, the archive lists
  the omitted key names, the import review shows them with the remedies.
- [RULING] Scope is the preset, the selected project-scoped state volumes
  and the optional workspace; the agent's state volume is checked by
  default, other project-scoped state volumes offered unchecked, caches
  never offered.
- [RULING] Volume restoration is deferred to the first develop through the
  seed path. Import is host-side only.
- [RULING] An existing destination volume of the same name is kept; the
  review says the archived copy is dropped.
- [RULING] Stillness rides a machine-wide barrier lock: shared by develop
  through container Create, exclusive by export. The disclosed residual is
  a hand-driven engine command.
- [RULING] Linked worktrees and separate git dirs are refused at export.
  Import carries no `.git/worktrees/` registrations (skipped at unpack;
  no git runs on the host) and says so.
- [RULING] Export is an editor-style form with mirroring flags. Import is
  the preset review extended with the archive sections and one confirm.
- [RULING] One gzip tar, manifest first, nested payload tars, SHA-256 per
  payload in the manifest. Glossary term: "box archive".
- [RULING] Verbs: `byre preset export`, `byre export`, `byre import`.
- [RULING] One shipping unit, one review loop, one engine-side run.
- [RULING] Export asks EVERY reachable installed engine for a container
  labelled for this project in any state -- the shape develop's
  cross-engine check already has (ADR 0004). A hit refuses with that
  engine's remedies. The source engine unreachable is a refusal (nothing
  to stream from); a cleanly-unreachable non-source engine is skipped and
  disclosed. Volumes are engine-local but the workspace is not, so a stale
  session on another engine still writes it.
- [RULING] Symlinks travel with their target stored verbatim. The
  nested-tar validator constrains `Name` only (a leading `/` refused, no
  `..`, no empty components); `Linkname` is unconstrained bytes, and
  import writes every symlink no-follow and never dereferences one.
  Hardlinks keep their rule: only to an already-materialised regular
  entry of the same payload. Export's preview lists every absolute or
  traversing target by path, the import review lists them again as
  agent-authored content, and the manifest carries the list per payload.
  The agent can already write any symlink on the source, so the archive
  adds no capability, and containment is a property of `Name` plus
  no-follow writes.
- [RULING] `engine` and `worktree_base` are stripped from the flattened
  preset; the manifest records the source engine as information only.
- [RULING] Entries an archive cannot carry (sockets, devices, FIFOs) are
  listed and the export asks "These files can't be archived and will be
  skipped. Continue anyway?"; the skipped paths go into the manifest and
  the import review. The flag form needs an explicit skip flag or fails
  with the same list.

## What already exists [CODE]

- A preset is a complete config proposal with a `[sources]` table.
  `byre preset apply` (internal/commands/preset.go) refuses off a TTY,
  resolves the extends chain, reports every missing package with the exact
  install command from the hint, chauffeurs each install under its own
  consent, then renders the composed box's grant review with a diff against
  the current store and writes the project config on confirm. A declined
  install leaves the reference in place and develop fails loudly later.
- ADR 0057's preset bridge: a preset may carry a `[credentials]` block and
  encrypted rows; the review annotates any credential change and any
  identity change at warning weight. So a flattened preset with credentials
  lands through existing, reviewed machinery.
- Credential unlock (ADR 0057): a plan line names the contributing files
  and counts, then one prompt per still-locked file, root-most first, each
  passphrase tried against every remaining identity, three attempts.
  Identity unwrap runs before any lock is taken. The recipient is cleartext,
  so encrypting to it needs no passphrase.
- Volume naming (internal/commands/naming.go): project scope gives
  `byre-<id>-<name>`, machine scope gives `byre-machine-u<uid>-<name>`,
  chosen by `Volume.MachineScoped()` alone. Resolution (resolve.go
  `combine`) appends config volumes to skill volumes and `validate` refuses
  a duplicate logical name with an attributed message. The grant review
  (`skillGrantSummary`) walks the SKILL's own declarations, and runParams
  emits one mount per resolved volume.
- The cascade fold (config.go `mergeStep`, mergestate.go): scalars are
  non-empty-wins, maps (`env`, `env_from_host`, `files`, `sources`) union
  with the later layer winning per key, string lists union, identity lists
  replace by identity. `!name` closures for egress, MCP and Claude Skills
  are NOT in the merged Config: they live in `Merged.Closures` and are
  subtracted after the skill union (ADR 0030). A removal marker acts only
  from the layer that carries it (`mergeStrings` collects removals from
  the overlay alone): a destination-default `!skill` is spent folding the
  default over CoreLayer and cannot remove a skill the later preset names,
  and a later plain egress entry reopens a base closure.
- Preset reads are bounded by `packages.MaxManifestBytes` (256 KiB), the
  package-manifest cap; config files by `config.MaxConfigBytes` (1 MiB).
  ADR 0057 allows one file credential of 256 KiB plaintext.
- Preset inspect and the passive drift probe read through the same
  256 KiB reader as apply.
- The applied-preset marker records the preset's content hash and source
  string only; there is no per-package receipt. Installed packages carry
  their source URI and digest in the install record; bundled packages have
  a display digest and no `[sources]` entry (a hint requires a URI).
- Seeding (internal/commands/seed.go, runner SeedVolume/SeedFiles/
  SeedLiteral): one-time, fresh volumes only, an existing volume is left
  alone, a failed seed removes the volume it created. The copy runs in the
  project's BUILT image as root inside the box's userns and finishes with a
  recursive chown to the box identity. Host-path seeds are containment-
  checked against the workspace (runparams.go checkContainedHostSource).
- The setup lock (internal/lock): one `flock` per project store with an
  inode requeue so a lock file deleted under a waiter cannot split the
  lock. Exclusive only today. Noisy waiter line. Develop prompts for
  credentials BEFORE taking it and takes it twice (resolution, then the
  prepare-and-Create span). The editor's volume Clear removes a volume
  under the project lock alone.
- Nested-archive hygiene already exists in internal/deliver/tar.go: no
  `..`, no empty components, anchored writes (ADR 0040); its name splitter
  STRIPS a leading `/` rather than refusing, and its consumer handles
  directories and regular files only and never validates a hardlink's
  Linkname.
- `SeedLiteral` already streams content into a seed container over stdin
  with no host bind. `resolveWithCatalog` folds CoreLayer first.
- ADR 0057 states byre writes decrypted plaintext to exactly one place and
  never into a host file or volume content; the security-model page says
  the same.
- Sessions are found by engine labels (ADR 0004); reportRunning prints
  attach/shell/stop remedies for the engine in use.
- `byre reset` wipes all of a project's named volumes after a prompt and
  refuses while a session runs.

## The archive [PROPOSED]

One gzip-compressed tar, conventional name `<project>.byre.tar.gz`. Entry
order is fixed and import refuses any other:

1. `manifest.toml`: `format = 1`, the writing byre version, the project
   name, the source engine (information only), a `preset` row (byte count,
   sha256), one `[[volumes]]` row per included volume (logical name, role,
   target, byte count, sha256, and the payload's list of absolute or
   traversing symlink targets), a `workspace` row (present or absent, byte
   count, sha256, the list of skipped unarchivable paths, the same symlink
   list for that payload), a `[credentials]` summary (`included`, the
   omitted key names when false, and when true whether the passphrase is
   the source's or a fresh one), and one `[[packages]]` provenance row per
   referenced package (id, kind, origin: bundled/installed/local, digest
   where one exists) separate from the `[sources]` acquisition hints.
2. `byre.preset`: the flattened config (next section).
3. `volumes/<name>.tar`: one plain tar per included volume, the volume root
   as the tar root.
4. `workspace.tar`: the workspace directory as a plain tar, when selected.

Writing: every payload is produced into a private staging directory and
hashed there FIRST; only then is the gzip tar written manifest-first.
Nothing is hashed on the fly into a stream whose manifest is already out.
The volume `tar` runs inside the box with `--numeric-owner` and the host
unpack DISCARDS ownership (the seed chown is the ownership step); logical
volume names must match `volumeNameRe`.

Import reads the manifest first (bounded like a config file), refuses an
unknown `format` naming the required byre version, then verifies every
payload's length and sha256, the preset included, before writing anything
to its final place. One read budget: the outer decompressed stream is
bounded by the manifest's declared totals plus a stated tar-overhead
allowance, the entry count is bounded, staged bytes are checked
cumulatively against those totals and against free disk before staging,
and duplicate, extra or missing outer entries and nonzero trailing data
are refused. Digests detect corruption, not authorship, and the review
says so. One nested-tar validator serves both payload kinds, and it
constrains `Name` alone: a leading `/` refused (not stripped), no `..`, no
empty components. Regular files, directories and symlinks-as-links only;
hardlinks only to an already-materialised regular entry of the same
payload; anything else refused BY NAME; sizes bounded by the manifest. A
symlink's `Linkname` is unconstrained bytes, written verbatim [RULING]:
import creates every symlink no-follow and never dereferences one, so an
absolute or traversing target is inert data inside the unpacked tree.
Absolute and traversing targets are listed in the manifest per payload and
named again in the import review as agent-authored content. Every
archive-derived string a review or error prints (names, paths, digests,
skipped entries) goes through the existing terminal-data funnels (P4). The
published archive and its staging are mode 0600; the archive is a
plaintext artefact of whatever the user selected. Host writes go through
hostopen's anchored operations, no-follow parents, into private staging;
nothing is published under its final name until the whole archive has been
verified. The archive is never written into a selected input tree,
including through a symlink alias. Preset reads for `byre.preset`, in
apply and in import, use `config.MaxConfigBytes`, not the package-manifest
cap.

## The flattened preset [PROPOSED]

`byre preset export [--output PATH] [--no-credentials]` writes the project's
effective cascade as one preset. It is the config half of `byre export` and
usable alone: a bare preset is a better thing to send someone than an
archive containing only one. Alone it is config-only: a plain
`byre preset apply` of it does no mount or seed remapping, so the sender's
host paths land as written and the apply review's mount lines are where
the recipient sees them; the full `byre import` is the flow with remap.

Flattening is the cascade merge byre already runs at every load
(default ⊕ template ⊕ chain ⊕ project) started from the EMPTY config, not
CoreLayer (which the destination folds in itself), with these differences
from a merged Config:

- Removal markers for identity lists and string lists are applied and
  gone. Surviving CLOSURES (`Merged.Closures`: egress, MCP, Claude Skills,
  contexts) are RE-EMITTED as `!name` entries in the corresponding list of
  the preset, because they are subtracted after the skill union and a
  merged Config alone does not carry them (ADR 0030). `extends` is gone.
  `template` is gone, its contribution folded in, so no template package
  needs to exist on the destination. Skills and agent stay as references.
- Picker and onboarding-only state is stripped exactly as resolution strips
  it today. `engine` and `worktree_base` are stripped too [RULING]: they
  describe the sender's machine, not the box.
- Export preflights the rendered preset against `config.MaxConfigBytes`
  and refuses naming the rows that push it over; it never emits a file
  apply cannot read.
- Raw blocks travel verbatim in their resolved order. They are not parsed.
- Every credential row from every contributing file travels re-encrypted
  to ONE `[credentials]` block. Export runs an export-specific variant of
  the standard unlock: same plan line, one prompt per still-locked file,
  root-most first, reuse tried, but it RETAINS the one passphrase string
  when a single passphrase opened every file (nil once a second one was
  needed), since the existing unlock returns identities only. Rows are
  decrypted in memory and encrypted to the flattened file's recipient.
  Passphrase rule per the ruling: one passphrase opened everything, the
  flattened identity is wrapped under it; otherwise export prompts for a
  fresh passphrase (entered twice) and the manifest says so. In stdin
  mode the fresh passphrase is one extra trailing line, read only when a
  fresh wrap is needed. The passphrase is never logged.
- Consistency: the prompts run before any lock, as develop's do. After the
  barrier, export takes the project setup lock, re-reads every
  contributing file, and refuses if the raw bytes, the credential rows or
  identities, or the resolved volume set differ from what the form
  reviewed and the plan line counted (ADR 0057's consented-set bound; the
  same refresh develop runs at resolve.go). Decrypts only rows the plan
  counted.
- `--no-credentials`: no prompts, no ciphertext, no `[credentials]` block.
  Every omitted credential key is written as an explicit EMPTY row
  (`KEY = ""`), the idiomatic disable: it carries no value and wins over
  any destination-default row under the same key (env_from_host merges as
  a map, later wins per key). An empty row does not say WHY it is empty
  (the review's host-env lines skip empty sources), so the import review
  takes the omitted names from the manifest, and a standalone apply lists
  empty env_from_host keys as "disabled" without claiming they were
  credentials. No live config key carries the omitted list.
- `[sources]`: for every referenced package the merged cascade's
  `[sources]` entry where one exists, else the installed package's
  recorded source URI and digest. Only https hints travel; a path or file
  hint is treated as no hint, and a package with no hint travels as a bare
  reference the review names as "not carried; the source had it as a
  local install". There is no preset receipt to read.
- Package provenance rides the manifest's `[[packages]]` rows (bundled
  display digest, installed digest, or local/unknown). Import compares
  each with the destination catalog and warns naming both digests
  [RULING]; where the source row has no digest, or the kind/origin differs
  (a local source package resolved by a bundled or installed destination
  one, ADR 0055's shadow class), the review prints "version cannot be
  verified; the grants below are the destination package's" instead.
  `missingRefs` stays acquisition-only.

The destination's own machine default sits underneath the imported preset
with ordinary cascade semantics, which are NOT changed by this design: a
non-empty scalar in the preset wins, an EMPTY one inherits the
destination's; maps union with the preset winning per key; lists union; a
destination-default removal marker is spent at its own layer and cannot
remove a name the preset carries; the default's surviving CLOSURES still
subtract after the skill union unless the preset reopens them. So the
default can add (empty scalars, missing map keys, list entries, raw-block
lines) and can subtract only through closures the preset does not reopen.
The apply review diffs file bytes and prints the composed grant summary;
that does not show this delta. Import therefore adds one IMPORT-SPECIFIC
review section computed by the SAME resolver, never an approximation:
resolve the source-effective config (CoreLayer ⊕ preset) and the
destination-effective proposal (CoreLayer ⊕ destination default ⊕
preset), and list every difference at review weight: additions, closures
that subtract, empty-scalar inheritance, map fallbacks, raw-block
additions. That section is the evidence the round-trip claim rests on.

## Volumes [PROPOSED]

Export offers each resolved PROJECT-scoped state-role volume that exists
on the source engine, the agent's own state volume checked by default.
Cache-role volumes are not offered; the preview says caches are rebuilt.
Machine-scoped volumes are never offered [RULING], whether a skill or the
project config declares them -- the rule reads the RESOLVED scope, not the
declarer. Each is named once in the export preview and once in the import
review: "not carried; the destination binds its own machine-scoped <name>
volume (its login, or a fresh empty one)".

Streaming out: for each selected volume, a short-lived container from the
project's built image (the same image seeding uses), the volume mounted
read-only at one path, no network, no host bind, entrypoint overridden to
`tar --numeric-owner` on stdout, fixed argv, into staging (hashed there,
see The archive). If the built image is absent on the source (never
built, or pruned; volumes outlive images) the export refuses naming
`byre rebuild` as the remedy.

Restoration on import: each archived volume the review did not mark as
existing is STAGED as its verified payload tar, unmodified, under the
project store's staged-seeds directory, addressed by logical volume name,
never by a path from the archive. Beside it import writes ONE stage
receipt per project (a small TOML, outside the config): per volume the
logical name, the destination scope, the engine and physical name the
restore is for, the archive identity, the payload's byte count and
sha256, and a state. Status and develop read the RECEIPT, not directory
presence, so "never staged" and "staged and vanished" are distinguishable.
The written config gains nothing for it; the seed step learns a
store-owned seed source kind beside host-path and literal.

At first develop, after the image builds, per state volume in the
resolved set: if the receipt names it, develop opens the payload no-follow
through hostopen (a symlink or non-file is refused), verifies length and
sha256 against the receipt, and only if the receipt's physical name is
still absent on the engine develop is running on, creates the volume and
STREAMS the tar into a seed container over stdin, as SeedLiteral already
does, so no directory under `~/.byre` is ever bind-mounted (the store is
the self-edit rw root, and a daemon resolves a pathname). The existing
recursive chown finishes it. The ordinary config/skill seed is suppressed
for that first fill; `seed_prefs` runs AFTER, so a restored agent volume
is left alone. If the volume already exists at develop, the stage is
DISCARDED with one banner line ("<name> already exists here; the archived
copy was dropped"), matching the ruling. A receipt entry whose volume is
no longer in the resolved set is disclosed and discarded, never
retargeted: a stage is only ever written to the exact volume it was
recorded for. A payload missing or failing its digest where the receipt
records one is an error, never "start empty".
The banner names each volume filled from the import; a consumed stage and
its receipt entry are removed after the seed succeeds; a failed seed rolls
the volume back and keeps both for the retry. A later import atomically
replaces the receipt and removes stages it no longer lists. `forget`
sweeps stages with the store; `reset` leaves them (import-owned pending
state, not box state); `byre import --discard-staged` removes them on
request; status counts them: "N volumes waiting to be restored on first
develop".

Existing volume on the destination (setup-only import into an existing
project): the review lists it as "exists here; the archived copy is
dropped" [RULING]; there is no remedy sentence: dropped means dropped.
The import-time existence check is best-effort on the engine the next
develop would pick (detection from the destination default, since
`engine` is stripped): reachable, the review states exists/absent per
volume; unreachable, the review says so and the stage is still written.
The AUTHORITATIVE keep-or-fill is the first develop's exists check on the
engine it actually runs on (below).

Every host-path-bearing key in the flattened preset gets the same
remap-or-disable review line as external mounts, since the path is the
sender's: `[[mounts]].host`, volume `seed.host`, `[[context]].file` and
`[[claude_skills]].path` (the latter two are read and staged at build
time from the host, so a coincidental destination path would bake a
stranger's file into the box). Their off-switches are the existing
`disabled`/`!name` spellings.

Copied login state: ADR 0007 forbids byre copying HOST credentials into a
box; it says nothing about a user deliberately carrying a box's own login
state to another box. The ADR is amended to say so, and the residual
(two valid token holders, no revocation, re-login if both are used) goes on
the security-model page in the same unit.

## Stillness [PROPOSED]

One barrier lock file under the byre home, `transfer.lock`. `lock` gains a
shared mode (`LOCK_SH` in place of `LOCK_EX`; the inode requeue is
unchanged), exposed as one outermost helper every engine-mutating path
calls BEFORE its project or file locks. Develop takes it shared AFTER its
credential and onboarding prompts, around its second setup-lock span
(prepare through container Create), and releases it before the
interactive session. The inventory of other callers: worktree helpers,
seeding, rebuild, reset, forget, rehome, import, and the editor's volume
Clear. Reset and forget keep their fail-fast shape against the barrier.
Export takes it exclusive after the choices are confirmed and the
passphrases are entered, and holds it until the archive is published.
Lock order is barrier first, then the project setup lock, then any
config-file lock. No prompt runs under it.

Under the barrier export asks EVERY reachable installed engine once for a
container labelled for this project in any state (ps -a by label, ADR
0004) -- the shape develop's cross-engine check already has [RULING].
Volumes are engine-local, but the workspace is not: a stale session on
another engine still writes it. A hit prints reportRunning's remedies for
THAT engine and the export stops. The source engine unreachable is a
refusal (nothing to stream from). A non-source engine that is cleanly
unreachable is skipped and disclosed: "podman could not be queried; a box
on it could not be checked"; any other query failure is fatal, as in
develop, since sole-session cannot then be established.

Waiters behind an exclusive holder print "waiting for a byre export to
finish (ctrl-C to stop waiting)"; an export waiting behind shared holders
prints "waiting for another byre setup to finish" (a flock cannot count
its holders). The barrier is cooperative: a hand-run
`docker start` or `docker run -v <name>` does not take it. That residual is
named in the export preview and on the security page. It is the same
residual a check-and-refuse design would have, so the barrier strictly
narrows the window.

## Workspace [PROPOSED]

Selected means the project directory as bytes: regular files, directories,
symlinks copied as links with their target stored verbatim and never
followed, hardlinks resolved within the payload, modes and mtimes kept,
ownership not recorded (destination files belong to the importer).
Absolute and traversing symlink targets travel as they are and are listed
by path in the export preview, the manifest and the import review
[RULING]. Entries no archive can carry (sockets, device nodes, FIFOs) are
listed and the export asks "These files can't be archived and will be
skipped. Continue anyway?" [RULING]; the skipped paths are recorded in the
manifest and printed in the import review. The flag form needs
`--skip-unarchivable` or fails with the same list.

Git: the source must be a main checkout whose `.git` is a directory; a
linked worktree (`.git` file) or a separate git dir is refused with
"export from the main checkout, or export without the workspace". `.git`
travels as is EXCEPT `.git/worktrees/`: on a directory the import just
created every registration is stale by construction, so import skips
those entries at unpack and the summary says "worktree registrations from
the source were not carried" [RULING]. No git runs on the host (ADR 0009
stands without an exception); `git worktree add` recreates the directory.

Import target: with a workspace payload, `--directory DIR` (default cwd)
must not exist or must be an empty directory; the review names it. A
target that is a linked worktree is refused. If the target does not exist,
import creates it. Unpacking goes into the target itself under an anchored
directory root (never a rename over a pre-existing directory, which would
strand a shell whose cwd it is); on failure import removes what it wrote
under that root and leaves the directory as it found it (absent, or
empty). Project identity is derived from the created directory, so the
store is written LAST; a failure after the store write rolls the store
back. Without a workspace payload, the target may be an existing checkout
and the import writes only the project store.

## The two verbs [PROPOSED]

`byre export [--output PATH] [--workspace] [--no-credentials]
             [--volume NAME]... [--no-volume NAME]... [--skip-unarchivable]`
Interactive: an editor-style form (configui) with the output path field
(default: `<project>.byre.tar.gz` in the project's PARENT directory, so
the default is never inside a selected workspace; a path inside the
selected tree, including through a symlink alias, is refused), the
credentials checkbox, the workspace checkbox with its helper text, one
checkbox per offered state volume, and a live preview panel: what
travels, estimated bytes, omitted credential names, the machine-scoped
volumes and what the destination binds for each, absolute and traversing
symlink targets by path, external mounts to reconnect, referenced
packages, unarchivable entries, the stillness requirement. Confirm leads
to the passphrase prompts, then the barrier, the engine checks, the
stream, the publication, and a summary with actual bytes and the file
name. Flags mirror checkboxes exactly: `--output` alone never
skips the form; any checkbox flag skips it, and unspecified checkboxes keep
the form defaults (credentials on, agent state volume on, others off).
`--volume` adds a volume, `--no-volume` removes one (the way to leave the
agent's out); unknown or duplicate names are refused against the offered
set. Off a TTY the form cannot render, so export is flags-only with the
form defaults, and requires `--no-credentials` or the existing stdin
passphrase mode (`--credentials=stdin`, one line per still-locked file, a
final line for the fresh passphrase when one is needed). Cancel leaves no
file.

`byre import FILE [--directory DIR]`
Interactive, TTY-only like preset apply, which it extends. Sections in
order: archive identity (format, writer version, project name, source
engine as information); packages (each missing one with its install
command, chauffeured as apply does; each present-at-other-digest one with
the two digests); credentials (included and which passphrase to use, or
the omitted names with `byre credentials set` and `--credentials=skip` as
remedies, and "version cannot be verified" where it applies); volumes
(each carried one, "will be restored on first develop", "exists here;
archived copy dropped", or "could not check; decided at first develop",
plus that payload's absolute and traversing symlink targets; each
machine-scoped one and the destination volume it binds); workspace (target
directory, empty requirement, the skipped-entries list, the absolute and
traversing symlink targets as agent-authored content, the worktree
registrations note); host paths (mounts, seeds, context files, Claude
Skill paths, each with a remap or disable choice); the import-specific
effective delta (what the destination default adds or closes); a line that
archive authorship is not proven and that carried workspace and volume
bytes may be agent-authored and shape the next agent; then the composed
grant review with the credential annotations and one confirm.
Import needs the destination engine only for the best-effort existence
lines; unreachable is disclosed, not refused. After confirm:
unpack the workspace, stage the seeds, write the store config last, print
`byre develop` as the next step. Declining writes no store config, stages
nothing and leaves the target as found; packages chauffeured before the
review stay installed, as apply already does.

`byre export --inspect FILE` is not proposed; `tar tzf` and the manifest
serve, and the import review is the inspection.

## Doctrine

- New ADR: box archive (this design), citing P1, P4, P5, P6.
- 0007 amended: deliberate transfer of a box's own login state through a
  box archive is the user's choice, disclosed; host credential seeding
  stays forbidden.
- 0057: the flattened preset is a new writer of `[credentials]` blocks;
  file locality holds (one file, one block); export is a reader of every
  contributing file's block under the standard unlock, with the under-lock
  re-read as develop's. AMENDED: the "plaintext in exactly one place"
  clause gains the box archive as a user-selected copy of workspace and
  volume bytes that may contain plaintext, credential rows staying
  encrypted; the security page carries it.
- 0055: a source package the destination cannot verify is announced as
  such.
- 0004 and 0054: the barrier is an addition to session discovery, not a
  replacement; export's stillness check asks every reachable installed
  engine, the shape develop's cross-engine check already has, and a
  cleanly-unreachable non-source engine is skipped and disclosed;
  exclusive-volume checks are unchanged.
- 0009: linked worktrees refused at export; `.git/worktrees/` skipped at
  unpack; no host git, no exception to the ADR.
- 0030: closures are re-emitted as markers in the flattened preset, so the
  imported allowlist equals the source's.
- 0047: import spawns no host tool over the imported tree.
- 0029: no package bytes travel; hints only.
- 0040: archive-supplied names never choose a host path; nested payloads
  ride the deliver tar rules.
- 0051: digests are integrity, not authenticity, and the design says so.
- P0: the export form is the editor surface; staged seeds are flow-owned
  state named in status ("N volumes waiting to be restored on first
  develop").
- Security-model page: copied login state, the cooperative barrier,
  imported executable workspace content, the archive as an unencrypted
  0600 plaintext artefact, and archive authorship being unproven.
- GLOSSARY: "box archive".

## Evidence before done

- Round trip of the effective config through export and apply, including
  removal markers, surviving egress/MCP/Claude Skill closures (the ENFORCED
  allowlist compared on both sides), a template, a chain, tri-state
  fields, raw block order, engine and worktree_base stripped, and three
  credential identities under one and under two passphrases.
- `--no-credentials` never leaves a ciphertext or an identity, and every
  omitted key is an explicit empty row that beats a destination-default
  row under the same key.
- The import delta names a destination-default surviving egress closure,
  an empty-scalar inheritance and a map fallback, and shows that a
  destination-default `!skill` does NOT remove a preset-named skill.
- Preset size preflight refuses at export; apply, inspect and the passive
  drift probe all read a 1 MiB preset (one bound, three readers).
- Export under the lock refuses when a contributing file changed between
  the form and the stream.
- Every host-path key (mount, seed, context file, Claude Skill path) gets
  its remap line; unremapped ones are disabled, never read from the
  destination path.
- Seeding from a staged payload: receipt drives it, payload verified
  before use, streamed over stdin with no store bind, stage beats config
  seed, seed_prefs runs after, fresh volume filled and chowned, stage and
  receipt entry removed, failed seed rolls back and keeps both, missing or
  mismatched payload is an error, existing-at-develop discards with a
  line, an unresolved entry discards with a line, never retargets, later
  import replaces the receipt, forget sweeps, reset keeps,
  `--discard-staged` removes, status counts, payload symlink refused.
- Barrier: develop holds shared through Create and only after its
  prompts; volume Clear participates; export exclusive blocks a concurrent
  develop with the waiting line; a container created before the export's
  check refuses it; a container on a non-source engine refuses it with
  that engine's remedies; an unreachable non-source engine
  is disclosed, an unreachable source engine refuses.
- Archive: payloads hashed before the manifest is written,
  manifest-first order, format refusal, digest mismatch refusal for every
  payload including the preset, hostile Name in nested tars refused, an
  absolute and a traversing Linkname carried, written no-follow and
  listed, leading `/` in a Name refused, special entry refusal at unpack,
  ownership discarded, read budget (manifest bound, decompressed bound,
  entry count, cumulative bytes, trailing data, duplicates), 0600 modes,
  escaped archive strings, output inside the input tree refused, default
  output in the parent, path-source hints dropped, provenance rows
  compared and unverifiable ones announced.
- Workspace: unarchivable entries listed and confirmed, empty-target
  requirement, created target removed on failure, store written last and
  rolled back, linked worktree refusal at export and as target,
  `.git/worktrees/` absent after unpack, flag grammar (`--output` alone
  keeps the form, `--no-volume`, non-TTY passphrase mode).
- Gated (byre-inttest, Docker and rootless Podman): stream out and restore
  of a real state volume, and the TUI walk of the export form and the
  import review.
