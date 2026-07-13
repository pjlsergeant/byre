# Skill packages: identity, immutable bundled content, installation

**Status:** Design of record, rev 2 (grilled with Pete 2026-07-13, all rulings
his; rev 2 amends after codex + grok design review round 1 -- mechanical
findings folded in, doctrine-shaped findings marked **[PENDING]** for Pete)
**Lifecycle:** working doc -- absorb into an ADR + docs/skills.md when built,
then delete (the docker-host-design.md pattern). Git history keeps it
regardless.

## Summary

Skills and templates become **packages**: units with declared identity, a
manifest, provenance, and defined mutability. Three kinds exist, differing
only in provenance, storage, mutability, and update mechanism -- one manifest
format, one resolver:

- **Bundled** -- shipped inside the byre binary, immutable, updated atomically
  with byre itself.
- **Installed** -- fetched from a manifest URI as an immutable, hash-verified,
  content-addressed snapshot.
- **Local** -- editable source trees under the user's store, created from
  scratch or forked from an immutable package.

The central doctrine:

> Bundled and installed packages are artifacts. Local packages are source.
> Moving from artifact to source is an explicit fork.

And its companion, ratified verbatim:

> **Everything true of skills is true of templates.** Trusted code, consent by
> selection, attributed grants, immutable when bundled or installed, fork to
> edit, same namespace rules.

`devlog` and `codereview` move out of the bundle into a first-party repo
(`github.com/pjlsergeant/byre-skills`) and become the installation
mechanism's first real cargo.

## Motivation

A skill can run arbitrary image-build commands as root, mount host sockets,
open egress, alter agent instructions, and be the agent. A template -- a full
config-cascade layer -- can set mounts, ports, env, an agent, and enable
skills. This breadth is the product's differentiation, and it makes
provenance and exact contents part of the safety story.

The current model weakens that story:

1. Bundled skills are materialized into `~/.byre/skills` as editable copies.
   Byre cannot tell whether a copy still matches the release; a compatibility
   or safety fix takes effect only after `byre skill update`.
2. Ownership is ambiguous -- byre and the user both appear to own the same
   files, requiring overwrite-and-backup machinery (`skills.bak/`).
3. First-materialized-wins is shadowing: a stale or edited copy silently
   replaces what byre shipped.
4. There is no way to get a skill onto a machine other than hand-dropping
   files, and no identity, versioning, or compatibility vocabulary at all.
5. Templates share every one of these problems through the same machinery.

This milestone is the blocker for promoting byre: the pitch includes "a
personal toolkit that follows you into any folder," which requires discovery,
authoring, forking, and installation to actually exist.

## Non-goals

No marketplace or registry; no dependency resolution between packages; no
automatic installation from project config (a config referencing a missing
package errors with the install hint -- it never fetches); no automatic
updates; no signatures or publisher identity; no policing of what a package
may do (legibility, not gates); no reproducibility claims for raw Dockerfile
commands; no user-defined alias table (deferred; revive if qualified-ID
fatigue proves real -- unlikely, since the config UI drop-down is the primary
enablement surface and nobody types IDs).

## Terminology (GLOSSARY-bound)

- **Package** -- a manifest plus the payload files it names. Comes in two
  kinds: skill and template.
- **Template** -- the box's **type**: the config preset it starts from.
  Cascade semantics -- defaults you override per-key; exactly one per box.
- **Skill** -- a box **capability**: contributions you add. Union semantics,
  attributed, many per box.
- **Bundled / Installed / Local** -- the three provenance kinds, above.
- **Fork** -- an explicit copy of an immutable package into a new local
  identity. Records its origin as documentation; nothing may ever depend on
  ancestry.
- **Manifest URI** -- where a package manifest is fetched from (`https:` or
  `file:` in v1). Transport, never identity.
- **Retired name** -- a bare name a past byre release bundled and a later
  release does not. Stays protected (D15).
- **Materialize** -- RETIRED. The mechanism it named is deleted.

## D1 -- Identity

**D1a.** A package's canonical ID is declared in its manifest
(`[package] id = "..."`), not derived from a directory name or source URI.
For local packages `id` is optional and defaults to the store-relative
directory path; a declared ID must equal that path, or it is a validation
error. **The on-disk mapping for local packages is nested directories**:
bare `my-linter` lives at `skills/my-linter/`, qualified `pete/claude` at
`skills/pete/claude/`. Catalog discovery walks up to two levels looking for
a `skill.toml` / `template.config`; a directory containing one is a package
root (no deeper nesting). Fork destinations follow the same mapping.
Bundled and installed packages always declare `id` explicitly.

**D1b.** `byre/*` is permanently reserved and means exactly one thing:
**bundled in this binary** -- shipped with, tested against, and warranted by
the byre release you are running. Not even first-party installed packages
may claim it (that would reopen "who verifies first-party?"). Hence
`pjlsergeant/devlog`, `pjlsergeant/codereview`.

**D1c.** A bundled package always owns its bare name: `byre/<name>`
automatically gets the alias `<name>`, and that bare name is protected -- no
user or installed package may claim it as an ID. Uniform rule, no historical
allowlist; the reserved set grows only when the bundled roster grows, which
is already a deliberate release-notes event. Consequence accepted with eyes
open: if byre later bundles `byre/opencode` and a user has a bare-ID local
`opencode`, that package goes INVALID (scoped, loud, printed remedy: fork/
rename + config edit). The documented convention keeps this rare: **bare IDs
are for personal scratch; anything shared gets a qualified ID.** Names that
leave the bundle stay protected -- see D15. New config writes (onboarding,
pickers) keep writing the friendly bare alias for bundled packages;
canonical IDs appear in status and verbose output.

**D1d.** Installed packages must have qualified IDs (`owner/name`); the
install path refuses a bare-ID manifest ("ask the publisher to namespace
it"). Only local packages may be bare. Strictness lives exactly at the trust
boundary.

**D1e.** Duplicate canonical IDs across providers are a hard error **scoped
to that identity**: a conflict row in `list`, a resolve error naming both
locations and the remedies, and zero effect on unrelated packages or boxes.
Never shadowing, never a global catalog failure. Unparsable manifests get
the same treatment: INVALID-with-reason in `list`, hard error only when
referenced. Skills and templates share one ID namespace (an ID names one
package; its kind is a property) -- a cross-kind duplicate is the same
conflict.

**D1f.** Packages cannot declare aliases. The alias table is closed and
derivable: it is exactly the bundled bare names (D1c). Resolution is two
steps anyone can hold in their head: bundled alias? expand it; otherwise
it is a canonical ID.

**D1g.** Every name surface resolves identically -- `agent =`, `skills =`,
`template =`, `!name` removal markers, `shared_auth_for` -- and every
comparison (dedup, `!` stripping, companion pairing) happens on canonical
IDs. One resolution function. Implementation consequence (from review):
list-removal today happens textually during `config.Merge`, before skills
resolve -- so `!byre/claude` would not cancel a lower layer's `claude`. The
config resolver therefore takes the catalog (an injected resolver seam) and
canonicalizes each layer's package references, including removal markers
and the selected template, **before** merging. This is a real dependency
inversion (config currently path-joins `~/.byre/templates/<name>` directly)
and is priced into phase 1.

**D1h.** ID grammar and hostile-input handling (new; from review). Canonical
IDs match `segment(/segment)?` where `segment = [a-z0-9][a-z0-9-]{0,63}`;
lowercase only, no dots, no leading `!`, and the literal `none` is reserved
(config sentinel). Everything a remote manifest controls (IDs, versions,
descriptions, paths, raw Dockerfile lines) is terminal-escaped before
rendering -- control characters and ANSI sequences must not be able to forge
grant rows or prompt text (same rule the containment key already follows).
Fetch limits: manifest <= 256 KiB, <= 64 payload files, streamed payload
cap (default 64 MiB total), bounded timeouts. Raw Dockerfile content stays
semantically uninterpreted but is always escaped for display.

## D2 -- Companion pairing across forks

`shared_auth_for` pairs by exact canonical ID. A fork of `byre/claude` is a
different agent; the bundled companion does not follow it. Fork provenance
stays purely documentary (D6), and credential-gating machinery must name its
agent exactly -- auto-pairing the shared-credential volume with code byre no
longer warrants would be backwards. `byre skill fork` prints the note: fork
the companion too (a one-line `shared_auth_for` edit) if the forked agent
needs shared credentials.

Two rules made explicit after review:

- **Multi-claim refusal stays.** Today `SharedAuthCompanion` returns nothing
  when more than one skill claims the same agent; that fail-closed rule is
  preserved across the multi-provider catalog and becomes a stated
  invariant, not an accident.
- **[PENDING Pete] Offer eligibility.** An installed-but-never-enabled
  package can declare `shared_auth_for = "byre/gemini"` and would today
  enter the onboarding offer path -- a one-keystroke route to a
  machine-scoped credential volume, recommended by core. Recommendation:
  **only bundled companions are ever auto-offered**; installed or local
  companions work fully when enabled by hand in a config, but core never
  proposes them. (Alternative: offer with loud provenance in the prompt.
  Not recommended -- the offer is a recommendation, and core should not
  recommend code byre does not warrant.)

## D3 -- Templates are packages, full parity

**D3a.** Template = the box's type (one per box, cascade semantics); skill =
a capability (many, union semantics, attributed). Both concepts earn their
keep; neither absorbs the other.

**D3b.** Distribution makes templates a trust surface for the first time:
a template layer can set mounts, ports, env, an agent, and enable skills.
Ruling: **selecting a template is trusting it**, symmetrical with enabling a
skill. Byre's job is legibility -- `template inspect` shows every key it
sets with grants prominent; `byre status` and the exposure line attribute
template-contributed grants to the template. No restricted key set for
installed templates: restricting content is policing, which byre refuses
(same ruling as docker-host's Network row).

**[PENDING Pete] Point-of-consent depth.** Both reviewers converged on the
same hole: a template that *enables skills* (`skills = ["docker-host"]`)
makes "I selected this template" stand in for "I enabled docker-host" --
and a picker showing only ID/description does not state the effect of the
write (PRINCIPLES #5). Recommendation: at every selection surface
(`template inspect`, the onboarding template pick, adoption of a config
that sets a template, and replace-confirm), byre renders the template's
keys **plus the recursively resolved grant summaries of the skills it
enables** (containment lines, sock_groups, mounts, egress -- attributed),
and flags enabled skills that are not present. Cost acknowledged: config
resolution today flattens the cascade and loses provenance; meeting this
requires provenance-bearing resolved config values (or an equivalent
second resolution), which is real phase-1 work, priced in.

**D3c.** Full CLI parity: `byre template list / inspect / install /
uninstall / fork / init / validate / pack` -- shared engine, per-kind verbs,
`kind = "template"` discriminator in the manifest. The engine checks kind
against verb: `byre skill install` of a template manifest errors and prints
the right command (and vice versa).

**D3d.** One package = one kind. "Our company's Rails setup" is several
co-hosted manifests installed independently -- a distribution convenience,
not a package concept. A distributed template that enables skills the user
lacks produces the missing-package error with the install hint; never
transitive installation.

**D3e.** Bundled templates: `go`, `node`, `python` (aliases per D1c). Byre
publishes no installable templates in v1; the capability ships anyway
(parity is doctrine, and the machinery is shared).

## D4 -- The manifest

**D4a.** The package's primary file carries the manifest: `[package]` in
`skill.toml`; `[package]` in `template.config` (the config parser peels it
off before cascade merging). One file per package; scratch authors never
write it at all (D1a default). For a local package with no `[package]`
block, **kind is inferred from store location** (`skills/` vs `templates/`)
-- the discriminator is required only where the location does not answer it
(installed manifests).

**D4b.** Fields:

```toml
[package]
id = "pjlsergeant/codereview"   # optional for local (defaults per D1a)
version = "1.1.0"                # required installed; optional local
kind = "skill"                   # or "template"; required installed
package_api = 1                  # manifest-format contract, frozen core
requires_byre = ">=0.2.0"        # semver constraint on the byre executable
description = "..."
```

(`package_api`, not `skill_api` -- the field is frozen forever and covers
both kinds; naming it after one kind would contradict parity.) It and
`requires_byre` are required for installed and bundled packages, optional
for local.

**D4c.** Two-stage parse, the load-bearing piece: stage 1 reads only
`[package]` leniently (a core frozen forever: id, version, kind,
package_api, requires_byre) and checks compatibility, so a package needing
a newer byre gets "requires byre >= 0.4; you have 0.2.1" instead of the
strict parser's "unknown key" death. Stage 2 strict-parses the whole file
exactly as today. `package_api` guards the format itself -- one-integer
insurance that cannot be retrofitted into published manifests later.
Release tests assert every bundled manifest accepts the byre version
carrying it.

**D4d.** Version is descriptive; **package versions are never compared**
(the only comparison anywhere is `requires_byre` against the executable's
own version). The digest is the change signal; there is no "latest".
Bundled package versions are identical to the byre release version, and
that equality is produced by a single generation path at release build
(injected, not hand-maintained -- a hand-kept per-skill counter is a
lockstep that would rot; today's bundled skill.tomls carry no `[package]`
at all, so these headers are new, generated content).

## D5 -- Payloads

**D5a.** Installed manifests: `[[package.files]]` is mandatory and
exhaustive -- every payload named with package-relative destination, source,
and sha256. Installation fetches exactly that list and verifies every
digest. No crawling, no conventions, no magic filenames.

**D5b.** Local packages: no files list, no hashes -- the directory is the
package, walked as today. Bundled packages: no hand-written hashes either
(the manifest travels with its payloads inside the binary; there is no
fetch for hashes to protect); display digests are computed from `embed.FS`.
Hashes exist exactly where bytes cross a trust boundary, and nowhere else.

**D5c.** `byre skill pack <name>` / `byre template pack <name>` emits the
distribution manifest from a local package, hashes computed. Near-free
(verification needs the same code) and required for our own publishing.
Pack enumerates **every file in the package directory** (scripts, context
files, hooks -- not just `[build].files` entries); an incomplete manifest
that installs "successfully" and fails at enable is the failure mode this
rule exists to prevent.

**D5d.** v1 payload sources are **relative to the manifest only**, and
"relative" is enforced after resolution, not syntax: network-path
references (`//host/x`), encoded traversal, and redirects that leave the
manifest's scheme+origin are rejected (HTTPS redirects are re-validated
against the origin; `file:` sources are contained to the manifest's
directory with symlinks resolved and checked). Destination paths must be
clean, relative, duplicate-free (including case-collisions), and are staged
beneath a fresh temporary root before the snapshot move. Absolute source
URLs are rejected with a clear error. Deny-by-default applied to
distribution; loosening later is additive.

**D5e.** Stated limit, unchanged from existing doctrine: the manifest
accounts for the package's own payloads. It cannot account for what a raw
Dockerfile line downloads. Inspection distinguishes hash-verified payloads,
typed package-manager declarations (apt/npm), raw Dockerfile commands
(verbatim, marked not-introspected), and runtime grants.

**D5f.** The package digest (new; from review) is defined, not implied:
sha256 over a domain-separated canonical encoding of (manifest bytes) +
(sorted list of destination path, payload sha256, executable bit). The
manifest is inside the preimage -- contributions and grants live there, and
a digest that excluded it would let a manifest change ride an unchanged
digest. This digest keys the snapshot directory, the index entry, and the
same-ID no-op rule (D9a). **Integrity claim scope:** the digest establishes
what was acquired; snapshots live on user-writable disk, so byre verifies
at acquisition and does not re-hash on every load (a `verify` subcommand
can be added on demand). Status wording says "installed 1.1.0
(sha256:8fe3...)" -- provenance of acquisition, not a runtime attestation.
Same honesty rule as `--self-edit`: a writable store is host trust
(SECURITY.md already says so for config; extend it to packages).

## D6 -- Fork

`byre skill fork <id> <new-id>` (and template equivalent) copies an
immutable package into the local area under a new identity. The new ID must
not collide with protected names (D1c, D15) -- forking `claude` requires
picking a new name. Provenance is a comment, deliberately not
machine-readable:

```toml
# Forked from byre/claude@0.2.0, sha256:abc...
# Informational only: byre never reads this for resolution, updates, or trust.
```

Fork output prints the config edit required to use the fork, the companion
note (D2) when the source is an agent skill, and -- when the source declares
machine-scoped volumes -- a warning that the fork still names the **same
volume** (same credentials/identity) until the user renames it. Volume
names and runtime semantics are otherwise preserved unless edited. Upstream
updates never touch a fork.

## D7 -- Storage

```
~/.byre/packages/<sha256-digest>/   installed snapshots, immutable
~/.byre/packages/index.toml         id -> {digest, version, kind, manifest URI, installed-at}
~/.byre/bundled/<name>/             MIRROR of bundled packages (see D7b)
~/.byre/skills/<path>/              local skills, editable (nested per D1a)
~/.byre/templates/<path>/           local templates, editable (nested per D1a)
```

**D7a.** Authoritative bundled bytes live in `embed.FS` and are loaded from
there -- the loader never reads `~/.byre/bundled/`, so shadowing, drift, and
fake immutability (chmod games, hash-check-and-restore) are structurally
impossible.

**D7b.** The mirror exists for humans: written loudly, regenerated on every
byre version change (stamp check in the store-ensure path), topped with a
README: these are display copies of the packages inside your byre binary;
edits are ignored and overwritten; to modify one, `byre skill fork`. This
keeps the local-first inspect-with-grep workflow that materialization used
to provide, without letting disk bytes influence what runs.

**D7c.** Store mutations (install, replace, uninstall) run under a
**store-global lock** (the existing lock machinery is project-scoped and
does not cover this) with crash-safe ordering: snapshot directory written
completely first, index flipped atomically second, superseded snapshot
deleted last; an orphaned snapshot (crash between steps) is swept at next
lock acquisition. A replaced package's superseded snapshot is deleted once
the new install succeeds -- rollback is reinstalling the old manifest URI;
the source is the archive, not our disk. No further GC machinery.

**D7d.** `~/.byre/packages/` ships a self-ignoring `.gitignore` (the
`.byre-devlog` trick): a version-controlled store tracks local packages and
configs (source), not installed snapshots (reproducible artifacts).

## D8 -- CLI surface

Both nouns, full parity (D3c):

```
byre skill list                 ID, version, kind, provenance; INVALID/conflict/LEGACY rows shown
byre skill inspect <id|uri>     metadata, payloads+hashes, contributions, grants prominent;
                                fetches remote manifests without installing anything
byre skill install <uri>        fetch, verify, snapshot; see D9
byre skill uninstall <id>       scan effective configs, warn + confirm; see D9
byre skill fork <id> <new-id>
byre skill init <name>          scaffold with commented example
byre skill validate [<name>]    two-stage parse + resolve-check
byre skill pack <name>          emit distribution manifest
byre skill update               transitional stub; see D11
```

One verb for inspection (`inspect`, the doctrinal word); no `show`, no
`edit` (local packages are plain directories; the fork hint lives in
`inspect` output and in immutable/INVALID error copy).

## D9 -- Install, replace, uninstall semantics

**D9a.** `install <manifest-uri>` is the only acquisition verb; explicit
install and replacement are the same operation, keyed on the candidate's
declared ID:

- ID not present anywhere: install.
- Same ID, same digest (D5f): no-op.
- Same ID, different digest: show version/digest, changed payloads and
  contributions, with **new or widened grants called out separately**;
  the prompt **states its machine-wide scope and enumerates affected
  boxes** (from the D9d scan) -- replacement changes what those boxes run
  next launch; confirm; atomic swap under the store lock.
- Different ID: install alongside.
- Incompatible (`package_api` / `requires_byre`): reject before anything.

Per-box effective before/after diffs for replacement are consciously
deferred (proportionality): the prompt names the boxes and shows the
package-level diff; a box's own launch surfaces (status, exposure line)
show the result. There is no update discovery, no remembered update
channel, and no concept of latest. The recorded manifest URI and install
time are provenance for humans, never an instruction byre follows.

**D9b.** First install prints the same grant summary `inspect` leads with
(mounts, caps, sock_groups, containment, egress, run_args -- attributed and
prominent; for templates, the D3b recursive rendering), then confirms, then
states the boundary: **installed -- grants nothing until enabled in a box.**
Installation is acquisition; enablement in a config is consent, per box, as
ever.

**[PENDING Pete] D9b'. Dangling references make some installs activations.**
Codex's blocker 1, and it is correct: if a stored config already references
`acme/foo` (enabled while installed, or typed in anticipation, or left
dangling after an uninstall), then installing `acme/foo` turns a
currently-failing box into one that runs new trusted code at next launch --
acquisition and activation collapse, without a per-box question (violates
the D9b boundary and PRINCIPLES #5 as written). Recommendation: the install
path runs the D9d effective-reference scan **first**; if any box already
references the candidate ID, the install is treated like a replacement --
affected boxes enumerated, grant summary shown, TTY confirm or `--yes`
required (the non-TTY free pass in D9c applies only when the scan finds
nothing). Cheap (the scan exists for uninstall) and closes the boundary.

**D9c.** Non-TTY: fresh install of a new ID **with no existing references**
proceeds (it is a verified download that grants nothing -- and scriptable
bootstrap matters). Replacement, uninstall, and reference-activating
installs (D9b') refuse in a pipe without `--yes`; state-changing
confirmation never defaults.

**D9d.** The reference scan resolves **effective** configs through the
catalog -- project configs (`~/.byre/projects/*/byre.config`),
`default.config`, and the template layer each selects (local or installed),
so a skill referenced only via a template is still found. A local file
walk plus catalog lookups; no engine calls. Uninstall lists affected
projects, confirms, removes the snapshot under the store lock. A project
left referencing a missing package hits the resolve error with the install
hint at next develop -- loud, attributed, self-repairing.

**D9e.** A config referencing a missing or INVALID package always errors
with the exact remedy. For names byre itself retired from the bundle, the
remedy comes from the D15 tombstone table (for `codereview`/`devlog`: the
pinned `pjlsergeant/...` install command + config edit, printed verbatim).

## D10 -- Migration from materialized stores

Minimal by ruling ("I am the only user"), and safe by construction -- but
rev 2 corrects two review-found holes: the sweep must not depend on anyone
running the D11 stub, and byte-comparison against current shipped content
is meaningless once `[package]` headers change every bundled file.

- The migration **rides the version-stamped store-ensure path** (the same
  hook that regenerates the D7b mirror), so it runs on the first invocation
  of any byre command after upgrade -- not only on `skill update`.
- No hash archaeology. Any legacy directory in the flat store whose name
  matches a **bundled or retired** name (D1c, D15) is never loaded --
  protection makes shadowing structurally impossible -- and is surfaced as a
  LEGACY row in `list` with its remedy (fork to keep changes; archive to
  dismiss). The store-ensure notice offers a one-confirm archive of all
  LEGACY dirs to `~/.byre/skills.legacy/` (and `templates.legacy/`);
  declining leaves them in place, inert. Old `skills.bak/` stashes are left
  untouched (outside the store's package areas; harmless).
- Legacy dirs whose names match nothing bundled or retired are ordinary
  local packages and simply keep working (D1a default identity).
- Templates get the identical treatment (`go`/`node`/`python` copies are
  LEGACY; hand-made templates keep working).
- No config auto-rewrite ever -- configs are consent documents; the D9e
  error guides each project.
- Pete's own store: hand-fixed in-session at ship time, per precedent.

## D11 -- `byre skill update` transitional stub

Survives exactly one release as an explainer: prints that bundled packages
now update with byre itself (and templates likewise), points at any LEGACY
rows (D10), exits 0. Dies the release after, with a CHANGES sunset note.
Shipped release notes must not dead-end (the devloop-stub logic applied to
a command). It is never repurposed for update discovery.

## D12 -- The first-party repo

```
github.com/pjlsergeant/byre-skills/
  README.md                     what these are, pinned install commands
  skills/devlog/...             skill.toml + payloads
  skills/codereview/...         skill.toml + payloads (own devlog-lib.sh copy)
```

- Manifest URIs are GitHub raw URLs, **tag-pinned in every printed hint and
  doc** (`/v1.0.0/`, not `/main/`); `main` is for development. Honesty note
  from review: a git tag is a convention, not an integrity guarantee -- tags
  can be moved. Install pins bytes locally at acquisition (digest recorded);
  handed-out hints SHOULD carry the expected digest, and `install` accepts
  an optional `--digest sha256:...` that fails the install on mismatch --
  cheap end-to-end integrity for printed instructions without a signature
  system.
- `devlog-lib.sh` stays duplicated into both packages -- packages are
  self-contained; no shared-payload mechanism.
- The source of truth **moves**: `internal/builtins/skills/{devlog,
  codereview}` are deleted from the byre repo. Byre's own `byre.config`
  references the qualified IDs; the self-hosted dev-box bootstrap gains one
  documented install step (a one-shot host-side bootstrap note in
  CLAUDE.md/README -- CI and fresh clones fail loudly via D9e until it
  runs). The `devloop` and `grok-shared-auth` no-op stubs stay bundled
  (configs must not break; they are lines, not liabilities); the `devloop`
  stub's copy mentions the full chain (devloop -> devlog ->
  pjlsergeant/devlog).
- A prettier URL on an owned domain can front the raw URLs later without
  any model change (the manifest URI is transport, not identity).

## D13 -- Rendering and provenance

`byre status` shows provenance per package line and attributes template
grants alongside skill grants:

```
Template:  byre/go (bundled 0.2.0)
Skills:
  byre/claude              bundled 0.2.0
  pjlsergeant/codereview   installed 1.1.0 (sha256:8fe3...)
  pete/claude              local
```

The config UI pickers show the same provenance dimmed per row; INVALID,
conflict, and LEGACY entries render disabled-with-reason rather than
vanishing. Provenance and hashes establish what was acquired (D5f), never
whether it should be trusted -- byre does not claim a valid hash makes a
package safe.

Standing tripwire applies: status output appears in README/site as proof;
re-verify after these changes.

## D15 -- Retired names (new; from review)

When a package leaves the bundled roster, its bare name does **not** return
to the free pool: it joins a small, permanent, in-binary **retired names
table** -- `{name -> one-line tombstone}`. Retired names stay protected
exactly like bundled bare names (no local or installed package may claim
them; legacy dirs bearing them are LEGACY rows, D10). Rationale: freeing a
name byre's own documentation and users' configs spent releases typing is
habit-typosquatting bait, and both reviewers found the same hole
independently.

Doctrine note (PRINCIPLES #2, "core knows no skill by name"): the table is
core knowing **its own history**, not opinions about the ecosystem -- names
byre once shipped and what CHANGES says happened to them. The
`pjlsergeant/...` install hints inside the `codereview`/`devlog` tombstones
are a migration aid and may be trimmed to bare "retired; see CHANGES" text
in a later release; the protection itself is permanent.

Initial table: `codereview`, `devlog` (move, D12). `devloop` and
`grok-shared-auth` remain bundled stubs, not retirees, until someday they
join this table instead.

## D14 -- What this deletes

- Materialization of bundled content into the user store, and the word
  "materialize" from the vocabulary.
- `UpdateSkills`/`UpdateTemplates` overwrite-and-backup, `skills.bak/`,
  `sameTree`, first-materialized-wins shadowing.
- The devloop-rename upgrade-path machinery and its tests (stubs stay; the
  clobber-avoidance dance goes).
- `byre skill update`'s current meaning (after the D11 stub release).

## Docs shipping with the milestone

New ADR (this design); GLOSSARY (package, bundled/installed/local, fork,
manifest URI, retired name, template = "the box's type", the symmetry
doctrine line, materialize retired); ARCHITECTURE (skills section + store
layout); **docs/skills.md** -- the promotion-facing user guide (discover,
inspect, install, author, fork, publish); README how-do-I + install
example; CHANGES; the byre-skills repo README.

SECURITY.md additions (all from review, all wording work): "a skill is
trusted code" extended to installed packages and templates; third-party
templates are **full cascade layers**, not language presets; a hash is
integrity, never publisher identity or endorsement; `file:` installs are
"installing an unsigned tree from that path" (prefer tag-pinned https +
`--digest` in shared instructions); package immutability, like config, is
host-side integrity -- `--self-edit` and any host process can write the
store.

## Build order

Each phase lands green (gofmt/vet/test) and codereview-looped before the
next starts. Sizing honesty (from review, both reviewers): phase 1 is a
large rewrite, not a refactor -- bare single-path-element names and the flat
skills dir are load-bearing in the loader, staging escape checks, every
`skillsDir` call site, template cascade loading, onboarding, and the config
UI; `EnsureStore` is the spine every command crosses; and D1g/D3b push the
catalog **into** config resolution. Priced in, not discovered later.

1. **The model.** Two-stage `[package]` parsing; catalog + package-
   filesystem abstraction (nested local dirs per D1a); bundled loading from
   `embed.FS` + the D7b mirror; generated bundled manifests (D4d); alias/
   protected/retired/INVALID/conflict rules; catalog-aware config
   resolution with template provenance (D1g, D3b); D10 sweep on the
   store-ensure path; `list`/`inspect`/`fork`/`init`/`validate` for both
   nouns; D11 stub; D13 rendering; tests rewritten, D14 deletions.
2. **Installation.** `https:`/`file:` manifest fetch with D1h/D5d
   hardening; digest verification (D5f); content-addressed store + index +
   store lock (D7c); `install`/`uninstall`/`pack`; the D9 flows including
   the reference scan.
3. **The move.** byre-skills repo populated (pushes are Pete's, host-side);
   devlog/codereview deleted from builtins; D15 tombstones wired; byre's
   own config + docs updated; Pete's store hand-fixed.

**Acceptance (definition of done):** on a clean store, install both skills
from the real GitHub URIs and self-host byre's own dev box with them --
the dogfood, end to end.

## Open items -- pending Pete's rulings

1. **D2 offer eligibility:** bundled-only auto-offers for shared-auth
   companions (recommended), or offer-with-loud-provenance.
2. **D3b consent depth:** recursive grant rendering at template selection
   surfaces (recommended, priced), or defer some surfaces.
3. **D9b' install-as-activation:** reference scan gates fresh installs
   (recommended), or keep all fresh installs frictionless.

Consciously deferred (rulings already implied by proportionality, listed
for the record): per-box before/after diffs at replacement (D9a); re-hash
on every load (D5f -- verify-at-acquisition instead); user-defined alias
table; OCI/signatures/mirrors; template publishing by us; trimming
tombstone install-hints to bare text.
