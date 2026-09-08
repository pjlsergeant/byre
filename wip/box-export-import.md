# Box export / import

Status: v1 design draft, 2026-09-08. Independently reviewed; corrections and
scope decisions remain before ratification. Not authorization to implement.
TODO.md owns task status. The proposals below remain as reviewed, including
the rejected claim that config can override skill-declared volume scope.

## Purpose and operator decisions

Transfer a box's setup, optionally with its workspace and persistent data, to
another machine or another person. One interactive selection flow serves both
migration and sharing. Import creates a box that byre builds and manages normally.

The following are Pete's rulings, not questions for reviewers to restart:

- Export requires a completely still box. All project/worktree sessions and all
  other holders of selected volumes must be stopped, throughout the copy.
  Refusals name each blocking container and print its actual engine's attach
  and stop commands. Byre does not stop sessions on the user's behalf.
- No built images and no ephemeral container filesystem, even as options.
  Normal byre containers use `--rm`; import rebuilds through byre.
- Import preserves the source effective setup independently of destination
  defaults. Destination changes are explicitly reviewed. Other projects are
  left untouched.
- Export configuration and dependencies by default. Workspace and volume data
  are optional and unchecked initially; volumes are individually selectable.
- Selecting workspace means everything, including ignored and untracked files.
  No Git filtering and no file-exclusion picker. Helper text: "Includes
  everything in the workspace, including ignored and untracked files. Leave
  unchecked if you'll transfer the project separately, for example through Git."
- Workspace restores require a new or empty destination. Setup-only imports
  may target an existing checkout.
- Extra host bind mounts travel as declarations only, never copied directory
  contents. Import remaps their paths or explicitly disables them. Export
  names the reconnection requirements.
- "Include encrypted credentials" is checked by default. Ciphertexts and
  file-local wrapped identities retain their existing passphrases; export
  doesn't decrypt. Unchecking removes encrypted rows and identity blocks from
  exported copies and lists omitted names. Source config files are untouched.
- No archive encryption feature in v1. The operator protects the resulting
  ordinary file. The credential checkbox is not a claim that arbitrary
  workspace or volume data contains no secrets.

Everything below is a proposed implementation/design choice. Reviewers should
challenge it where it fails a ruling, doctrine, or proportionality.

## Current mechanisms and the gaps this design must close

`internal/config/config.go:resolveWithCatalog` folds CoreLayer, machine default,
template, named layers and project; a merged Config alone loses closure state,
layer attribution and file-local credential identities. `CascadeFiles` is a
best-effort display walk and cannot be used as a complete export inventory.
`internal/config/cascadefile.go` and commands/credentials.go already distinguish
raw files from effective values. Preserve that distinction.

The package catalog resolves bundled, installed and local entries, including
local-over-installed shadowing. `packages.Pack` requires publication metadata
and cannot simply package every local working skill. Bundled bytes must still
come from the destination executable's embed.FS (ADR 0029).

Project identity derives from the destination canonical path; linked worktrees
share their main project's config and volumes. `rehome` has same-engine volume
copy and rollback, but neither a cross-machine archive nor durable multi-resource
import recovery. Existing setup locks are per-project and released before start;
the created container is the ownership marker. They do not coordinate a machine
volume shared by different projects. An initial `ps` check is insufficient.

## CLI and interactive flow

Proposed commands: `byre export [--output FILE]`, `byre import FILE [--directory DIR]`.
Both use terminal forms by default. Export starts from the current project;
import chooses a destination (current directory prefilled where appropriate).
There is no automatic build, launch, Git checkout, source deletion or archive
upload. Successful import ends with the destination and `byre develop` remedy.

Export presents setup, encrypted-credential toggle, workspace toggle, and each
resolved volume with logical name, target, role, scope, engine, existence and
estimated bytes. Missing volumes say "not created" and have no data to select;
orphan engine volumes not declared by the current setup are not silently swept
in. A declared volume present on multiple engines requires selecting its source
engine explicitly. The source is the current persisted setup, not a previous
launch record or an old command-line agent override.

The final preview names included contents, omitted credential keys, volume data
that may contain logins, external mounts and other host references, raw blocks
that cannot be translated, and source/destination compatibility requirements.
Sizes are estimates before capture and actual byte counts afterwards. Review
terminal strings as data using existing escaping; never print credential values.

No secret classifiers or per-agent token parsers. Ordinary config literals,
comments, local package files, workspace files and volumes may contain plaintext.
Selecting encrypted credentials preserves only the existing inline scheme; it
neither selects agent-login volumes nor encrypts those other contents.

For automation, propose `--non-interactive` with explicit output/destination,
`--workspace`, repeatable `--volume NAME@ENGINE`, `--without-encrypted-credentials`
and import path mappings; absent data flags mean no data. Import also requires
`--accept` for its displayed plan. Missing choices fail with an actionable list,
not a terminal prompt. Exact flag spelling can follow the Cobra conventions.
`--inspect` validates an archive and prints its inventory without adoption.
Cancellation before applying leaves final outputs/destination state unchanged.

## Capturing configuration without changing other projects

Propose a project-owned captured setup, rather than copying source default.config
over the recipient's default or flattening several credential identities into one.
This is the largest addition and should be judged explicitly by reviewers.

The imported `byre.config` remains the editable project layer in the host store.
A new project-only `setup = "captured"` selector names a fixed sibling `setup/`
directory, never an archive-supplied absolute path. Absence means today's live
machine setup. `setup/` holds a captured default, named-layer chain, non-bundled
package entries and supporting inputs. For a captured project, resolution is:

    current CoreLayer + captured default + selected template
      + captured named layers + editable project config

Source CoreLayer and referenced bundled package digests are compatibility facts,
not executable archive payloads. Import compares them with the current binary.
If they differ, show the actual resulting setup/grant differences and require an
explicit choice to adopt those differences or use a matching byre release.
Never claim an old bundled package is running while using newer embedded bytes.
Unsupported archive/schema versions fail with the required version named.
Unpinned apt/install scripts/base tags may change upon rebuilding; no exact
software-version or offline-build guarantee is made.

The captured default is a distinct default slot, preserving template ordering.
Layers retain individual files, comments, removal markers and credentials;
`extends` still means one named parent within this project's captured root.
The physical files stay live and editable, but edits affect this project only.
The copied default omits onboarding-only preferences; those are not box setup.
Every exported file is independently parsed/validated through a strict inventory
before calculating the effective setup. Preserve dormant declarations too, and
show that encrypted-credential inclusion covers all copied config files.

Captured non-bundled package entries win for their exact IDs within this project;
they never write to or replace machine installs. New explicitly selected IDs may
resolve through the ordinary machine catalog. Preserve original local/installed
ordering for raw blocks; provenance displays "captured from local/installed",
not a false publication version. Installed packages retain validated manifests
and payloads. Local packages are copied as local source trees with independently
hashed bytes, without inventing publishing metadata. Bundled IDs never get an
archive overlay. A missing captured entry errors rather than falling back to a
different machine version of that same ID. Removal markers and inactive package
references retain their strings; only the actually needed package closure is
guaranteed present, with missing inactive references disclosed in inventory.

The selector must be wired through config loading, strict raw cascade walking,
credential writes/unlock, skill resolution, proposed-config/preset review,
status and the config editor. Do not fix only develop's resolver. Import mode
is a flow-owned TUI row ("Setup: captured; managed by import"). The editor can
open the captured default/layers in this project's context; file-local writers
take their physical file's locks. Global layer management still means global.
Users may hand-edit the selector; absent payload is a named error, never fallback.
An ordinary preset cannot activate a captured setup unless that project already
has the complete reviewed payload. Re-export traverses the captured root.
Rehome/forget carry or remove it as project-owned state. No other project's
layer or package index changes. `--self-edit` can affect this project's captured
setup, which does not propagate to other projects; disclose this scope extension.

## Supporting inputs and destination bindings

Inventory typed build inputs using existing field-specific readers: `[files]`,
context `file`, Claude Skill paths and package payloads. Copy only inputs the
resolved setup actually consumes, preserving their source-to-input association.
Build input files are separate from optional workspace transfer, so a setup-only
export can still include files it needs to build. This is not permission to
follow every string that resembles a path or copy a directory mounted at runtime.

For workspace-relative `[files]`, imported captured-input metadata resolves a
logical source to a private captured copy when no workspace source is present.
If workspace contents are restored, the real workspace remains the live input.
For setup-only import into an existing checkout, compare any corresponding live
input with the captured bytes; differing input is an explicit import choice,
never silent precedence. Persist that choice (live workspace or captured copy)
in the setup input map and show it in the config editor's Files screen. The
input map resolves only inventoried inputs, not arbitrary paths. Host context
and Claude Skill paths are rewritten in staged config copies to the selected
destination's private captured input paths. Preserve file-local credentials
while splicing only these reviewed fields with tomldoc.

Runtime host mounts are never followed. Import requires an explicit binding or
disable decision even if the original absolute path happens to exist. Keep
target, mode and disabled status visible. `env:`/`git:`/`tz:` sources retain their
semantics but fetch nothing on export; preview names what the destination must
provide. No new required-env mechanism is introduced. Seed host paths and
`seed_prefs` are destination actions: explicitly confirm/remap or disable them,
and do not run seeding during import. Worktree-base paths get the same review.

Raw Dockerfile blocks, raw run_args, MCP command argv and arbitrary script text
are not parsed or rewritten. Show their verbatim escaped text, disclose external
dependencies may remain, and let the user edit or deliberately retain them.
No promise that arbitrary trusted code is portable. Import grants are recomputed
from the actual adapted config and skills using the existing review semantics,
including raw-block, managed-path and credential annotations; no archive claim
or launch record can stand in for this computation.

## Stillness and capture protocol

Propose one advisory lifecycle transfer barrier per byre home, shared for ordinary
engine-mutating setup operations and exclusive during export/import copying.
Extend internal/lock with shared locking using its inode-identity checks; lock
files live in the host store, never the workspace. Lock order is barrier first,
then project setup locks in lexical order, then contributing config-file locks
in lexical order. No human prompt or credential unwrap while holding it.

Develop holds the shared barrier through preparation and container Create, not
through the interactive session. Worktree helpers, seed/volume helpers, rebuild,
reset/forget/rehome, volume clear and import all participate before any relevant
mutation. The container created before releasing a shared lock blocks an export
even before it starts. Export must not dissolve that ownership marker and then
claim the concurrent launch was safe. Refuse all non-removed project markers;
running/paused/restarting boxes and other containers referencing selected volumes
also block. Report created/stopped markers accurately, with state-appropriate
cleanup guidance; for live sessions print actual engine attach and stop commands.

Export first collects choices and reads the candidate setup. After confirmation,
take the exclusive barrier and project/file locks, enumerate all accessible
installed engines through pinned hostexec, and inspect actual container mounts
for each selected physical volume (including unlabelled holders). Scan all of
this project's worktrees regardless of volume selection. A declined engine,
failed query or unreadable mount inventory is uncertainty and refuses capture.
Multiple source-engine volumes are captured as distinct resources; logical
mapping cannot silently collapse two selected sources onto one imported volume.

Under these locks, strictly re-read/compare configuration and dependencies with
the reviewed plan. Any change requires a new preview. Copy host inputs and volume
data to private staging, verifying source identities and changes during reads.
Copy volume bytes through short-lived, fixed-argv engine helpers with one selected
volume mounted read-only, no network, no host filesystem bind and no agent entry
point. Helpers are identifiable transfer helpers, not byre sessions. The exclusive
barrier blocks even new projects attempting machine-volume mounts. Cancellation
terminates helpers before releasing the barrier. Hold through final source
verification and successful archive publication; don't release after the first
volume merely because the remaining copy is slow.

This coordinates cooperating byre commands under that home. Direct engine users,
other BYRE_HOME instances, older byre binaries and ordinary host editors do not
obey the lock. Inspect such existing container holders, disclose that external
writers must remain stopped, and detect observed source changes; do not claim a
general filesystem snapshot or a daemon-wide write fence. No host filesystem
snapshot service or daemon is proposed. Reviewers should judge whether that
honest boundary satisfies "completely still" under byre's operator threat model.

## Volumes, ownership and agent logins

An archive records logical volume name, role, target, source scope/engine and
source in-container UID/GID, never treating the source physical name as a restore
destination. Import derives project identity and physical names at the destination.
No source volume is deleted or invalidated: export is a copy, not `rehome`.

Project-volume restores require fresh physical destinations. No merging into,
clearing or overwriting existing data. If a destination already exists, explicitly
keep its contents (skip the archived payload) or abort and choose another project.
Declaring a volume without selecting its data leaves normal later creation/seed
behavior, as reviewed. Never restore orphan volumes just because their names match.

For selected machine-scoped data, propose a project-scoped restored copy by
default, with the scope change prominently reviewed; this leaves other projects
untouched. Alternatively the user may choose an existing destination shared
volume and discard the archived payload. Import never overwrites or populates
the destination machine-scoped namespace. Scope overrides ride normal volume
identity merging; prove companion skills still work with that target/mapping.
This adaptation is a review target, not a ratified change to ADR 0017.

Opaque agent state commonly mixes history and rotating OAuth tokens. Copying it
creates independent token holders: export cannot promise both source and imported
login continue to work. Show this when selecting state data, advise re-login if
both copies will be used, and do not implement agent-specific scrubbing or automatic
source revocation. ADR 0007 needs an explicit amendment for deliberate selected
volume transfer; this does not resurrect host credential seeding. Reviewers must
judge whether disclosure is sufficient for this deliberate import/export operation.

Restore via a fixed networkless helper into fresh volume roots. Map entries owned
by the source box's normal dev UID/GID to the destination box's normal dev IDs
(including rootless Podman's container IDs); retain other numeric owners where
representable and report/refuse unrepresentable ownership. No blanket recursive
chown of every owner and no runtime launcher repair. Unknown ownership under raw
`--user`/`--userns` is an explicit numeric mapping choice or a refusal, not guessed.
Cross-engine, cross-UID and rootless restore are gated probes before build ratification.
Cross-architecture binaries/databases inside selected data may need regeneration;
volumes are byte transfer, not application migration.

## Archive, workspace and import publication

Propose a versioned uncompressed tar container with a bounded TOML index first,
setup payloads, optional workspace payload and separately indexed volume streams.
Use Go archive handling for host-side validation, not `tar -xf` on the host.
Each object has expected length, kind, mode and SHA-256; reject duplicate names,
unknown required features, truncated bodies, unexpected entries and digest
mismatches. Digests detect corruption, not sender authenticity. Limits apply to
metadata and parser allocations; large file data streams. Inventory carries total
bytes/entries for explicit capacity checks, not a tiny arbitrary workspace cap.

Archive names never choose a host path. Host writes use hostopen/os.Root-anchored
operations, no-follow parent traversal, fresh staging and no-clobber publication.
Hardlinks must reference an inventoried regular entry inside the same payload;
symlinks are copied as links, never traversed to gather external data or extract
later entries. Preserve their text, flag absolute/outside-workspace link targets
as external dependencies and create links after regular entries. No `.gitignore`,
hidden-file or nested-repository filtering. Do not run repo Git, hooks or filters
on the host during export/import. No `.byre-devlog` special case: everything in
the selected workspace travels, including its ignored files.

Normal directories, regular files, symlinks and hardlinks are supported. Preserve
file bytes, executable bits, ordinary modes and timestamps; destination workspace
files belong to the importing user. Privileged mode bits, ACLs/xattrs, device
nodes, FIFOs and live sockets require an explicit representability decision:
v1 refuses an unsupported entry with its path rather than silently skipping it
or publishing a supposedly complete workspace. Probe macOS case/normalization
collisions and sparse-file behavior; preflight refuses lossy target mappings.

A linked Git worktree contains an external `.git` pointer. Proposed v1 boundary:
workspace-bearing export requires the main checkout with self-contained Git
metadata; linked worktrees/external gitdirs get a clear refusal suggesting the
main checkout or setup-only export. Do not archive a dangling pointer and call it
portable, nor silently include sibling checkouts. This is a material scope limit
for reviewers to assess; portable worktree reconstruction could replace it if
there is a small contained implementation. Detect ordinary repos with external
alternates/gitdirs too using bounded metadata reads, not mutating host Git.

Output must not reside in any selected input tree (including an alias through a
symlink); reject rather than silently exclude the output from "everything".
Temporary archive files are private (0600), outside selected inputs, on the
output filesystem for atomic no-clobber publication. Disk-full/interruption
never publishes a partial archive under the requested final name.

Import validates the whole archive in private staging before adoption, then shows
the resulting grants and all mappings. Archive file contents cannot run scripts
or package hooks during validation/review. Setup-only import may replace an
enrolled destination's config only after a full before/after review and stillness
checks. Captured payloads are private to the destination project; existing volumes
and unrelated project/machine state remain untouched.

After approval, take the barrier/project locks, recheck target identity, emptiness,
config bytes and selected mappings, and make a small import journal in the host
store. Record only roots/engine IDs created by this attempt and inode identities;
no secrets, arbitrary cleanup paths or shell commands. Restore fresh volumes and
stage setup/workspace, then publish configuration last. A pending-import marker
blocks launch and conflicting lifecycle changes until completion or recovery;
the next import can finish or remove only that attempt's provably owned artifacts.
Existing setup is backed up privately for rollback on a setup-only replacement.
Never delete an existing empty destination directory on failure, nor user-created
files after an interrupted operation. Recovery must refuse uncertainty rather
than recursively deleting a path from an untrusted journal. Journals and partial
archives are not returned as successes. Existing source files are never modified.
No restored launch records, applied-preset receipts, engine labels or setup locks:
those describe the old machine/session and are regenerated by normal operations.

## Implementation boundaries, doctrine and validation

Production work is not authorized by this draft. Once ratified, keep command
handlers as Streams adapters. Put inventory/format/planning in a focused transfer
domain package, engine streaming behind runner, captured resolution in config/
packages and anchored I/O in hostopen. Reuse tomldoc, grant review and terminal
form patterns; no generic transaction framework, secret scanner, image transport,
background service or dependency-package publisher.

ADRs needing explicit extensions: 0003/0018/0035 for project-owned captured
resolution and editor attribution; 0029/0041/0055 for private captured catalog
precedence without bundled overrides; 0004/0054 for the transfer barrier and
holder checks; 0007/0017 for selected login-data copies and shared-volume mapping;
0008/0032 for restore-time ownership mapping, not launch-time chown; 0040 for
archive hostile-path handling; 0057 for export omission and preserving file-local
identities. 0009 limits workspace worktree handling. 0014 still forbids image/
Dockerfile replacement; 0044, 0047, 0050, 0052, 0053 and P0-P6 apply to saves,
host execution, claims, reviews, records and reachable editor surfaces. Raw blocks
retain P3 behavior. Export never grants host authority based on a launch record.

Required evidence before calling the implementation done:

- Round-trip effective config including contradictory destination defaults,
  egress closures, canonical aliases, multiple credential identities, disabled
  mounts, tri-state fields, raw block order, local package shadows and re-export.
  Removing encrypted credentials must not expose a lower-layer fallback value
  or retain an identity/ciphertext in the copied setup by accident. If a lower
  noncredential row would reappear, add an explicit empty off-switch in the
  exported winning layer and disclose the omitted key; never a fake value.
- Real terminal export/import flows: defaults and labels above, all workspace
  files, omitted credential inventory, scope/mount mappings, cancel/back,
  destination collision, review and success/failure summaries; no secret echo.
- Concurrency regressions: created-before-start marker, sibling across engines,
  new unrelated project using selected machine volume, volume clear/reset/rehome,
  snapshot config changes, helper failure/cancellation and uncertain engine.
- Hostile archive regressions: traversal and symlink-parent/link races, hardlink
  escapes, duplicate names, control-character names, forged journal, source/output
  overlap, changed destination, size/digest mismatch, metadata exhaustion,
  disk-full, interruption at each publish boundary and source preservation.
- Sacrificial integration: Docker and rootless Podman volume streaming, metadata
  and ownership round-trip at differing IDs, actual mount-holder enumeration,
  no helper network, fresh-volume cleanup, crash recovery and terminal walk.
  Cross-platform archive fixtures plus macOS probes for destination names/modes.
- No full container image comparison as proof of rebuild reproducibility: build
  inputs can fetch changing upstream software. Verify captured setup and selected
  data instead. Format/compatibility refusal and builtin-change review are tested.

Build-gating probes: shared-lock participation and create/start race; captured
resolution through both editor and credentials; rootless restore ownership and
metadata; machine-scope-to-project-scope companion behavior. Record measured
results before turning their assumptions into guarantees. The draft contains no
claim that those probes have already run.

## Questions for the independent reviewers

Give a build-readiness verdict. Identify material defects or disproportionate
machinery, supported by code/ADR references. In particular: is captured setup the
smallest complete answer to source isolation and credential file locality; can
the barrier achieve the agreed stillness with its named residual; are machine
volume mapping, copied agent logins and the workspace/worktree limit acceptable
or do they need Pete's ruling? Separate required product decisions from technical
details the implementer should settle. Do not turn every detail into a question
for Pete. Propose smaller concrete replacements where rejecting machinery.
