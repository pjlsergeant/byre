# Skill packages: identity, immutable bundled content, installation

**Status:** Design of record (grilled with Pete 2026-07-13, all rulings his)
**Lifecycle:** working doc -- absorb into an ADR + docs/skills.md when built, then
delete (the docker-host-design.md pattern). Git history keeps it regardless.

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
- **Materialize** -- RETIRED. The mechanism it named is deleted.

## D1 -- Identity

**D1a.** A package's canonical ID is declared in its manifest
(`[package] id = "..."`), not derived from a directory name or source URI.
For local packages `id` is optional and defaults to the directory name; a
local package whose declared ID disagrees with its directory name is a
validation error. Bundled and installed packages always declare explicitly.

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
are for personal scratch; anything shared gets a qualified ID.**

**D1d.** Installed packages must have qualified IDs (`owner/name`); the
install path refuses a bare-ID manifest ("ask the publisher to namespace
it"). Only local packages may be bare. Strictness lives exactly at the trust
boundary.

**D1e.** Duplicate canonical IDs across providers are a hard error **scoped
to that identity**: a conflict row in `list`, a resolve error naming both
locations and the remedies, and zero effect on unrelated packages or boxes.
Never shadowing, never a global catalog failure. Unparsable manifests get
the same treatment: INVALID-with-reason in `list`, hard error only when
referenced.

**D1f.** Packages cannot declare aliases. The alias table is closed and
derivable: it is exactly the bundled bare names (D1c). Resolution is two
steps anyone can hold in their head: bundled alias? expand it; otherwise
it is a canonical ID.

**D1g.** Every name surface resolves identically -- `agent =`, `skills =`,
`template =`, `!name` removal markers, `shared_auth_for` -- and every
comparison (dedup, `!` stripping, companion pairing) happens on canonical
IDs. One resolution function.

## D2 -- Companion pairing across forks

`shared_auth_for` pairs by exact canonical ID. A fork of `byre/claude` is a
different agent; the bundled companion does not follow it. Fork provenance
stays purely documentary (D6), and credential-gating machinery must name its
agent exactly -- auto-pairing the shared-credential volume with code byre no
longer warrants would be backwards. `byre skill fork` prints the note: fork
the companion too (a one-line `shared_auth_for` edit) if the forked agent
needs shared credentials.

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

**D3c.** Full CLI parity: `byre template list / inspect / install /
uninstall / fork / init / validate / pack` -- shared engine, per-kind verbs,
`kind = "template"` discriminator in the manifest.

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
write it at all (D1a default).

**D4b.** Fields:

```toml
[package]
id = "pjlsergeant/codereview"   # optional for local (defaults to dir name)
version = "1.1.0"                # required installed; optional local
kind = "skill"                   # or "template"; default "skill"
skill_api = 1                    # manifest-format contract, frozen core
requires_byre = ">=0.2.0"        # semver constraint on the byre executable
description = "..."
```

**D4c.** Two-stage parse, the load-bearing piece: stage 1 reads only
`[package]` leniently (a core frozen forever: id, version, kind, skill_api,
requires_byre) and checks compatibility, so a package needing a newer byre
gets "requires byre >= 0.4; you have 0.2.1" instead of the strict parser's
"unknown key" death. Stage 2 strict-parses the whole file exactly as today.
`skill_api` guards the format itself -- one-integer insurance that cannot be
retrofitted into published manifests later. Release tests assert every
bundled manifest accepts the byre version carrying it.

**D4d.** Version is descriptive, never resolved -- the digest is the change
signal; there is no "latest" and no version-comparison logic anywhere.
Bundled package versions are identical to the byre release version (one
source of truth; a hand-maintained per-skill counter is a lockstep that
would rot).

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

**D5d.** v1 payload sources are **relative to the manifest only**; absolute
URLs in `package.files` are rejected with a clear error. Deny-by-default
applied to distribution -- cross-origin payload spread is not supported
until someone real needs it; loosening later is additive.

**D5e.** Stated limit, unchanged from existing doctrine: the manifest
accounts for the package's own payloads. It cannot account for what a raw
Dockerfile line downloads. Inspection distinguishes hash-verified payloads,
typed package-manager declarations (apt/npm), raw Dockerfile commands
(verbatim, marked not-introspected), and runtime grants.

## D6 -- Fork

`byre skill fork <id> <new-id>` (and template equivalent) copies an
immutable package into the local area under a new identity. The new ID must
not collide with protected names (D1c) -- forking `claude` requires picking
a new name. Provenance is a comment, deliberately not machine-readable:

```toml
# Forked from byre/claude@0.2.0, sha256:abc...
# Informational only: byre never reads this for resolution, updates, or trust.
```

Fork output prints the config edit required to use the fork, and the
companion note (D2) when the source is an agent skill. Volume names and
runtime semantics are preserved unless the user edits them. Upstream
updates never touch a fork.

## D7 -- Storage

```
~/.byre/packages/<sha256-digest>/   installed snapshots, immutable
~/.byre/packages/index.toml         id -> {digest, version, kind, manifest URI, installed-at}
~/.byre/bundled/<name>/             MIRROR of bundled packages (see D7b)
~/.byre/skills/<name>/              local skills, editable, as today
~/.byre/templates/<name>/           local templates, editable, as today
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

**D7c.** Index writes are atomic (existing AtomicWrite pattern). A replaced
package's superseded snapshot is deleted once the new install succeeds --
rollback is reinstalling the old manifest URI; the source is the archive,
not our disk. No GC machinery.

**D7d.** `~/.byre/packages/` ships a self-ignoring `.gitignore` (the
`.byre-devlog` trick): a version-controlled store tracks local packages and
configs (source), not installed snapshots (reproducible artifacts).

## D8 -- CLI surface

Both nouns, full parity (D3c):

```
byre skill list                 ID, version, kind, provenance; INVALID/conflict rows shown
byre skill inspect <id|uri>     metadata, payloads+hashes, contributions, grants prominent;
                                fetches remote manifests without installing anything
byre skill install <uri>        fetch, verify, snapshot; see D9
byre skill uninstall <id>       scan stored configs, warn + confirm; see D9
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

- ID not present: install.
- Same ID, same digest: no-op.
- Same ID, different digest: show version/digest, changed payloads and
  contributions, with **new or widened grants called out separately**;
  confirm; atomic swap.
- Different ID: install alongside.
- Incompatible (`skill_api` / `requires_byre`): reject before anything.

There is no update discovery, no remembered update channel, and no concept
of latest. The recorded manifest URI and install time are provenance for
humans, never an instruction byre follows.

**D9b.** First install prints the same grant summary `inspect` leads with
(mounts, caps, sock_groups, containment, egress, run_args -- attributed and
prominent), then confirms, then states the boundary: **installed -- grants
nothing until enabled in a box.** Installation is acquisition; enablement in
a config is consent, per box, as ever.

**D9c.** Non-TTY: fresh install of a new ID proceeds (it is a verified
download that grants nothing -- and scriptable bootstrap matters).
Replacement and uninstall refuse in a pipe without `--yes`; state-changing
confirmation never defaults.

**D9d.** Uninstall scans the store's configs (`~/.byre/projects/*/
byre.config`, `default.config` -- a local file walk, no engine calls), lists
affected projects, confirms, removes the snapshot. A project left referencing
a missing package hits the resolve error with the install hint at next
develop -- loud, attributed, self-repairing.

**D9e.** A config referencing a missing or INVALID package always errors
with the exact remedy. For the two first-party names the error is
first-class: `codereview` is no longer bundled -> `byre skill install
<pinned pjlsergeant URI>` + the config edit, printed verbatim.

## D10 -- Migration from materialized stores

Minimal by ruling ("I am the only user"), and safe by construction: legacy
dirs in `~/.byre/skills/` **cannot shadow anything** -- bundled names are
protected (D1c), so the loader never reads `~/.byre/skills/claude` as
`byre/claude`.

- Byte-identical-to-current-shipped legacy copies: swept automatically
  (byre-placed, provably redundant noise).
- Anything differing -- edited or merely stale: left exactly where it is,
  never loaded, surfaced as an INVALID row with the fork hint. Nothing
  user-placed is destroyed; no backups needed; the overwrite-and-backup
  machinery (`skills.bak/`, `sameTree`, non-clobber materialize) is deleted,
  not preserved.
- No config auto-rewrite ever -- configs are consent documents; the D9e
  error guides each project.
- Pete's own store: hand-fixed in-session at ship time, per precedent.

## D11 -- `byre skill update` transitional stub

Survives exactly one release as an explainer: prints that bundled packages
now update with byre itself (and templates likewise), runs the D10 sweep if
it has not run, exits 0. Dies the release after, with a CHANGES sunset note.
Shipped release notes must not dead-end (the devloop-stub logic applied to a
command). It is never repurposed for update discovery.

## D12 -- The first-party repo

```
github.com/pjlsergeant/byre-skills/
  README.md                     what these are, pinned install commands
  skills/devlog/...             skill.toml + payloads
  skills/codereview/...         skill.toml + payloads (own devlog-lib.sh copy)
```

- Manifest URIs are GitHub raw URLs, **tag-pinned in every printed hint and
  doc** (`/v1.0.0/`, not `/main/`); `main` is for development. Install pins
  bytes locally regardless (digest recorded), so this is about handed-out
  URIs meaning the same bytes forever.
- `devlog-lib.sh` stays duplicated into both packages -- packages are
  self-contained; no shared-payload mechanism.
- The source of truth **moves**: `internal/builtins/skills/{devlog,
  codereview}` are deleted from the byre repo. Byre's own `byre.config`
  references the qualified IDs; the self-hosted dev-box bootstrap gains one
  documented install step. The `devloop` and `grok-shared-auth` no-op stubs
  stay bundled (configs must not break; they are lines, not liabilities).
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

The config UI pickers show the same provenance dimmed per row; INVALID and
conflict entries render disabled-with-reason rather than vanishing.
Provenance and hashes establish what is running, never whether it should be
trusted -- byre does not claim a valid hash makes a package safe.

Standing tripwire applies: status output appears in README/site as proof;
re-verify after these changes.

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
manifest URI, template = "the box's type", the symmetry doctrine line,
materialize retired); ARCHITECTURE (skills section + store layout);
**docs/skills.md** -- the promotion-facing user guide (discover, inspect,
install, author, fork, publish); README how-do-I + SECURITY.md ("a skill is
trusted code" extended to installed packages and templates); CHANGES; the
byre-skills repo README.

## Build order

Each phase lands green (gofmt/vet/test) and codereview-looped before the
next starts.

1. **The model.** Two-stage `[package]` parsing; catalog + package-
   filesystem abstraction (resolver stops assuming `~/.byre/skills/<name>`
   roots); bundled loading from `embed.FS` + the D7b mirror; alias/
   protected-name/INVALID/conflict rules; D10 sweep; `list`/`inspect`/
   `fork`/`init`/`validate` for both nouns; D11 stub; D13 rendering; tests
   rewritten, D14 deletions.
2. **Installation.** `https:`/`file:` manifest fetch; digest verification;
   content-addressed store + index; `install`/`uninstall`/`pack`; the D9
   flows.
3. **The move.** byre-skills repo populated (pushes are Pete's, host-side);
   devlog/codereview deleted from builtins; D9e hints wired; byre's own
   config + docs updated; Pete's store hand-fixed.

**Acceptance (definition of done):** on a clean store, install both skills
from the real GitHub URIs and self-host byre's own dev box with them --
the dogfood, end to end.

## Open items

None blocking -- every fork above carries Pete's ruling (grilling session,
2026-07-13). Consciously deferred: user-defined alias table (revive on
demonstrated qualified-ID fatigue); OCI/signatures/mirrors (post-v1
transports); template publishing by us (capability ships, cargo later);
uninstall config-scan as warning-only courtesy elsewhere.
