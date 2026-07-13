# Skill packages: identity, immutable bundled content, installation, presets

**Status:** Design of record, rev 5 -- FINAL (grilled with Pete 2026-07-13,
all rulings his; rev 2 folded codex + grok design round 1; rev 3 resolved
the doctrine forks and added presets + adoption retirement; rev 4 folded
design round 2; rev 5 lands the last ruling: template `agent` banned)
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

Alongside packages, a **preset** (D16) is a byre.config-format file
distributed from anywhere and applied explicitly -- and the unsolicited
repo-config adoption offer is retired with it (D17).

`devlog` and `codereview` move out of the bundle into a first-party repo
(`github.com/pjlsergeant/byre-skills`) and become the installation
mechanism's first real cargo.

## Motivation

A skill can run arbitrary image-build commands as root, mount host sockets,
open egress, alter agent instructions, and be the agent. A template -- a
config-cascade layer -- can set base image, env, volumes, offered egress.
This breadth is the product's differentiation, and it makes provenance and
exact contents part of the safety story.

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
silent fetching, ever (interactive, per-package-consented acquisition walks
exist only inside flows the user explicitly invoked -- D16c); no automatic
updates; no signatures or publisher identity; no policing of what a package
may do (legibility, not gates); no reproducibility claims for raw Dockerfile
commands; no user-defined alias table (deferred; the config UI drop-down is
the primary enablement surface and nobody types IDs).

## Terminology (GLOSSARY-bound)

- **Package** -- a manifest plus the payload files it names. Comes in two
  kinds: skill and template.
- **Template** -- the box's **type**: the shape preset it starts from.
  Cascade semantics -- defaults you override per-key; exactly one per box.
  Shape only: a template never references packages -- no `skills`, no
  `agent` (D3b).
- **Skill** -- a box **capability**: contributions you add. Union semantics,
  attributed, many per box.
- **Preset** -- a saved answer to onboarding's questions: a
  byre.config-format file obtained from anywhere and applied explicitly.
  Not a package; no identity, version, or installation (D16).
- **Bundled / Installed / Local** -- the three provenance kinds, above.
- **Fork** -- an explicit copy of an immutable package into a new local
  identity. Records its origin as documentation; nothing may ever depend on
  ancestry.
- **Manifest URI** -- where a package manifest is fetched from (`https:` or
  `file:` in v1). Transport, never identity.
- **Retired name** -- a bare name a past byre release bundled and a later
  release does not. Stays protected (D15).
- **Adoption** -- RETIRED as an offer (D17). `byre.config` in a repo is
  renamed `byre.preset` and is inert until applied.
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
allowlist needed to *start* the set; the reserved set grows when the bundled
roster grows (a deliberate release-notes event) and never shrinks -- names
that leave the bundle move to the retired table (D15) rather than returning
to the pool. Consequence accepted with eyes
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

**D1h.** ID grammar and hostile-input handling (from review). Canonical
IDs match `segment(/segment)?` where `segment = [a-z0-9][a-z0-9-]{0,63}`;
lowercase only, no dots, no leading `!`, and the literal `none` is reserved
(config sentinel). Everything a remote manifest or preset controls (IDs,
versions, descriptions, paths, raw Dockerfile lines) is terminal-escaped
before rendering -- control characters and ANSI sequences must not be able
to forge grant rows or prompt text (same rule the containment key already
follows). Fetch limits: manifest <= 256 KiB, <= 64 payload files, streamed
payload cap (default 64 MiB total), bounded timeouts. Raw Dockerfile
content stays semantically uninterpreted but is always escaped for display.

## D2 -- Companions: pairing, offers, favourites (rev 3: ruling landed)

`shared_auth_for` pairs by exact canonical ID. A fork of `byre/claude` is a
different agent; the bundled companion does not follow it. Fork provenance
stays purely documentary (D6). `byre skill fork` prints the note: fork the
companion too (a one-line `shared_auth_for` edit) if the forked agent needs
shared credentials. References to `byre/*` are unrestricted (they are the
same class as `agent = "byre/gemini"` in a config); only ID *claims* are
protected.

**D2a. Offer eligibility -- all claimants, provenance-labeled.** Packages
enter the catalog only by explicit user act (the agent cannot run
host-side installs), so the onboarding shared-auth offer presents **every**
catalog claimant for the chosen agent, labeled: `bundled, byre's` /
`installed <ver>, third-party` / `local`. Refusing to offer what the user
deliberately installed would be nannying; legibility, not gates. Bundled
claimants list first. The single-claimant case keeps today's `[y/N]` shape
plus a provenance line and the companion's one-line grant reality
(machine-scoped credential volume, named). A package's companionship claim
is also disclosed at install time in the D9b grant summary ("declares
itself a shared-auth companion for byre/gemini"), so the offer is never a
surprise appearance.

**D2b. Multiple claimants -- a picker, not fail-closed vanishing.** Today's
multi-claim rule silently suppresses the offer; under the catalog that
would let a stranger's reference switch off byre's own courtesy. Replaced
by a provenance-labeled picker (bundled first, `N` = none available).

**D2c. Favourites prefill; only save-as-default writes them.** The
`shared_auth` favourite upgrades from an agent list to **agent ->
companion pick** -- picker-owned, cascade-stripped, prefill-only, exactly
like every other favourite. Compatibility (from review): v0.1 stores hold
`shared_auth = ["claude"]` as a TOML array and the parser is strict, so the
decoder accepts **both shapes** -- a legacy array entry is a yes-inclination
with no pick (it prefills `[Y/n]` in the single-claimant case and does
nothing in a picker; migration never invents a pick); the next
save-as-default rewrites the entry in the new shape. The saved pick preselects its row in the next
box's picker and Enter accepts it: consent is answering *this box's* live
question; prefill is ergonomics (the template/agent favourites precedent).
Favourites are written **only** by answering "Save these as your default?"
-- the offer/picker answer itself affects only this box. Sanitized like
every favourite: a pick that is uninstalled or no longer claims the agent
silently drops and the picker asks fresh. A *new* claimant appearing never
moves the preselection -- it just appears, labeled. The only state that
skips the question entirely remains a companion already enabled in config
-- the file with teeth.

## D3 -- Templates are packages; templates are shape (rev 3: ruling landed)

**D3a.** Template = the box's type (one per box, cascade semantics); skill =
a capability (many, union semantics, attributed). Both concepts earn their
keep; neither absorbs the other.

**D3b. Templates never reference packages.** A `skills` **or `agent`** key
in a `template.config` is a validation error: "composition belongs in a
preset" (D16). The agent half closes the round-2 residual both reviewers
found: the agent is a skill (ADR 0005), so a template `agent` was implicit
enablement of the highest-power skill class through a key the `skills` ban
left open. Nothing is lost: a composition that wants an agent is a preset;
a machine-wide usual agent is `default.config` (the user's own layer, not
a package); onboarding's picker already asks per box. Bonus simplification:
the "`none` sentinel beats template agent" special case loses its reason
to exist. A hand-made local template that sets `agent` today goes INVALID
with an error naming the fix (move it to a preset or your config). Rationale: a template that *enables* skills makes "I picked a
template" stand in for "I granted what those skills grant" -- visibility is
not consent, and convenience never justifies a default grant. With
composition removed, a template's consent surface is exactly its own file:
selecting a template is trusting it, and `template inspect` / `byre status`
/ the exposure line render and attribute its direct keys (base, env,
volumes, egress_offered, ...) with grant-bearing keys prominent. No
restricted key set beyond the no-composition rule: restricting *content* is
policing, which byre refuses (docker-host Network-row precedent). No
recursive rendering machinery is needed anywhere -- the hole is closed
structurally, not disclosed around.

(Today's bundled templates set base/egress_offered only; no shipped
template sets `skills` or `agent`, so nothing breaks.)

**D3c.** Full CLI parity: `byre template list / inspect / install /
uninstall / fork / init / validate / pack` -- shared engine, per-kind verbs,
`kind = "template"` discriminator in the manifest. The engine checks kind
against verb: `byre skill install` of a template manifest errors and prints
the right command (and vice versa).

**D3d.** One package = one kind. A distributed composition ("our company's
Rails box") is a **preset** (D16), not a package property.

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
Pack enumerates **every file in the package directory except the primary
file itself** (scripts, context files, hooks -- not just `[build].files`
entries); an incomplete manifest that installs "successfully" and fails at
enable is the failure mode this rule exists to prevent. The primary file
is excluded from its own `[[package.files]]` list by necessity -- it cannot
contain its own hash (review-found fixed point) -- and needs no entry: the
fetched manifest bytes *are* the primary file, and the package digest
(D5f) covers them directly.

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

**D5f.** The package digest is defined, not implied: sha256 over a
domain-separated canonical encoding of (manifest bytes) + (sorted list of
destination path, payload sha256, executable bit). The manifest is inside
the preimage -- contributions and grants live there, and a digest that
excluded it would let a manifest change ride an unchanged digest. This
digest keys the snapshot directory, the index entry, and the same-ID no-op
rule (D9a). **Integrity claim scope:** the digest establishes what was
acquired; snapshots live on user-writable disk, so byre verifies at
acquisition and does not re-hash on every load (a `verify` subcommand can
be added on demand). Status wording says "installed 1.1.0 (sha256:8fe3...)"
-- provenance of acquisition, not a runtime attestation. Same honesty rule
as `--self-edit`: a writable store is host trust (SECURITY.md already says
so for config; extend it to packages).

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

Both package nouns, full parity (D3c):

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

Plus the preset verbs (not a package -- see D16):

```
byre preset apply [<uri>|<path>]   default ./byre.preset; review + write byre.config
byre preset inspect [<uri>|<path>] the review without the write
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
  contributions, with **new or widened grant declarations in the package
  called out separately** (declarations, not per-box effective grants --
  a project layer may override them; the wording must not imply every
  affected box gains them); the prompt **states its machine-wide scope
  and enumerates affected boxes** (from the D9d scan) -- replacement
  changes what those boxes run next launch; confirm; atomic swap under
  the store lock.
- Different ID: install alongside.
- Incompatible (`package_api` / `requires_byre`): reject before anything.

Per-box effective before/after diffs for replacement are consciously
deferred (proportionality): the prompt names the boxes and shows the
package-level diff; a box's own launch surfaces (status, exposure line)
show the result. There is no update discovery, no remembered update
channel, and no concept of latest. The recorded manifest URI and install
time are provenance for humans, never an instruction byre follows.

**D9b.** First install prints the same grant summary `inspect` leads with
(mounts, caps, sock_groups, containment, egress, run_args, companionship
claims -- attributed and prominent), then confirms, then states the
boundary: **installed -- grants nothing until enabled in a box.**
Installation is acquisition; enablement in a config is consent, per box, as
ever. `install` accepts `--digest sha256:...` and fails on mismatch (D12).

**D9b'. Install-as-activation (rev 3: ruling landed -- yes).** If stored
configs already reference the candidate ID (dangling -- written in
anticipation, left by an uninstall, or applied from a preset while
declining the install), installing it flips those boxes from failing to
running new code at next launch. The install path therefore runs the D9d
reference scan **first**; on any hit the install is treated like a
replacement -- affected boxes enumerated, grant summary shown, TTY confirm
or `--yes`. Truly-new IDs stay frictionless.

**D9c.** Non-TTY: fresh install of a new ID **with no existing references**
proceeds (it is a verified download that grants nothing -- and scriptable
bootstrap matters). Replacement, uninstall, and reference-activating
installs (D9b') refuse in a pipe without `--yes`; state-changing
confirmation never defaults.

**D9d.** The reference scan is a **conservative reference extractor**, not
a full effective resolution (from review: the configs that matter most --
dangling refs, INVALID packages -- are exactly the ones fail-fast
resolution dies on). It collects package references syntactically per
layer (skills, agent, template, `!` markers), canonicalizes them through
the alias table, and follows each project's selected template's own
references; a config that cannot be parsed well enough to *prove* it does
not reference the candidate counts as a hit (guarded path). Scope: project
configs (`~/.byre/projects/*/byre.config`) and `default.config`. A local
file walk plus catalog lookups; no engine calls. Uninstall lists affected projects, confirms, removes the
snapshot under the store lock. A project left referencing a missing
package hits the resolve error at next develop -- loud, attributed,
self-repairing via D9e.

**D9e. Missing-reference errors always print the remedy.** The resolve
error names the missing ID and prints the exact install command --
including URI and digest when the config carries a `[sources]` hint (D16b),
and the D15 tombstone text for names byre itself retired. It never
fetches: acquisition on a third party's initiative is banned (D16c).

## D10 -- Migration from materialized stores

Minimal by ruling ("I am the only user"), and safe by construction:

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
- Repo-shipped `byre.config` files: byre stops reading them (D17); a
  status note explains the rename to `byre.preset` and the explicit apply.
  Already-adopted store configs are untouched -- they are the project's
  live config and remain so.
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
  handed-out hints SHOULD carry the expected digest via `--digest
  sha256:...` -- cheap end-to-end integrity for printed instructions
  without a signature system.
- `devlog-lib.sh` stays duplicated into both packages -- packages are
  self-contained; no shared-payload mechanism.
- The source of truth **moves**: `internal/builtins/skills/{devlog,
  codereview}` are deleted from the byre repo. Byre's own config
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

## D15 -- Retired names

When a package leaves the bundled roster, its bare name does **not** return
to the free pool: it joins a small, permanent, in-binary **retired names
table** -- `{name -> one-line tombstone}`. Retired names stay protected
exactly like bundled bare names (no local or installed package may claim
them; legacy dirs bearing them are LEGACY rows, D10). Rationale: freeing a
name byre's own documentation and users' configs spent releases typing is
habit-typosquatting bait; both reviewers found the hole independently.

Doctrine note (PRINCIPLES #2, "core knows no skill by name"): the table is
core knowing **its own history**, not opinions about the ecosystem. The
`pjlsergeant/...` install hints inside the `codereview`/`devlog` tombstones
are a migration aid and may be trimmed to bare "retired; see CHANGES" text
in a later release; the protection itself is permanent.

Initial table: `codereview`, `devlog` (move, D12). `devloop` and
`grok-shared-auth` remain bundled stubs, not retirees, until someday they
join this table instead.

## D16 -- Presets (new in rev 3)

**D16a. A preset is a byre.config-format file, not a package.** Template =
the box's type; skill = a capability; **preset = a saved answer to
onboarding's questions**: a complete config proposal (template, skills,
env, mounts, ports, ...) obtained from anywhere -- a repo file, an https
URI, a path, a gist. No `[package]` header, no identity, no version, no
digest bookkeeping, no installation, no catalog entry ("I don't think we
need versioning or anything"). The conventional in-repo filename is
**`byre.preset`**; `byre.config` is reserved for the box's live consent
document and nothing else wears its name.

**D16b. `[sources]` hints (config vocabulary, so presets get it free).**
A config/preset may annotate package references with acquisition hints:

```toml
skills = ["pjlsergeant/codereview"]

[sources]
"pjlsergeant/codereview" = { uri = "https://raw.github.../skill.toml", digest = "sha256:8fe3..." }
```

Hints are never auto-fetched. Anywhere byre reports a missing package
(D9e, preset review), it prints the exact `byre skill install --digest ...
<uri>` from the map instead of a shrug. A hostile hint buys the attacker
an install review, not running code. Digest optional but recommended in
published presets. Semantics (from review): entries merge across the
cascade **last-wins by ID**, and the printed command names the layer the
hint came from ("hint from project config") so a lower layer overriding a
digest is visible; `[sources]` in a `template.config` is a validation
error (templates are shape and reference no packages, D3b -- they have
nothing to hint about).

**D16c. Apply flow, and the solicitation rule.** `byre preset apply
[<uri>|<path>]` (default `./byre.preset`; when absent, a legacy-named
`./byre.config` is accepted with the D17 rename note). The flow order is
fixed (from review -- installs come **before** the write, so chauffeured
installs are genuinely fresh and D9b' never fires inside apply, and the
final review can actually be complete):

1. Fetch and validate the exact preset bytes (D1h escaping applies).
2. Identify every missing **package reference of any kind** -- skills,
   the selected template, the agent -- with their `[sources]` hints.
3. Offer the chauffeur: each missing package gets its normal,
   kind-specific install flow -- manifest fetched, its own grant summary,
   its own confirm, digest verified. Declining any is allowed.
4. Rebuild the catalog; recompute the effective config.
5. Show the final review -- grant summary of every key and every
   referenced package, provenance-labeled; anything still missing is
   marked "not installed -- grants unknown" (the review never claims
   completeness it does not have). Against an existing `byre.config`,
   the review shows the diff (the adoption diff machinery survives here).
6. Confirm; atomically write the reviewed bytes as the project's
   `byre.config`.

The chauffeur is not the banned transitive install (which is *silent*
fetching); it is byre walking the user through N explicit consents they
solicited.

The rule, stated once: **byre initiates acquisition walk-throughs only
inside flows the user explicitly invoked to compose a box (preset apply).
Anywhere a third party's document introduces the references -- a cloned
repo, a develop that trips on dangling refs -- byre reports, prints exact
commands, and stops.** Different headspaces: "I am building a box" versus
"my box wants things." Declining an install inside apply still completes
the apply honestly: the reference stays in the written config, marked in
the step-5 review, and that box fails loudly at develop with the D9e
remedy (which is why D9b' guards later installs of already-referenced
IDs). Non-TTY apply refuses (the review is the point).

## D17 -- Adoption retires; repo configs become presets (new in rev 3)

The unsolicited adoption offer ("this repo ships a byre.config -- adopt?
[y/N]") is **removed**. A repo-shipped config is like `package.json`:
cloning gives you a file, not a prompt, and nothing reads it into effect
until you explicitly apply it.

- The file is renamed by convention to **`byre.preset`** and goes through
  `byre preset apply` like any preset (D16c). One mechanism, always
  solicited, so the chauffeur is always appropriate inside it.
- With no unsolicited prompt there is nothing to decline: the sticky
  decline records and re-prompt-on-edit machinery are deleted (existing
  `adopted`/`declined` records in stores become inert and are swept by the
  D10 migration). The adoption *review and diff* code survives as the
  preset apply review. This **partially supersedes ADR 0003**: its
  host-side-store premise stands; its offer-and-adopt-on-develop clause is
  reversed (and the 2026-07-10 sticky-decline work with it) -- the new ADR
  names it explicitly.
- Passive visibility, never questions -- with the three states specified
  (from review, so drift is legible, not just absence):
  1. **Not applied**: "this repo ships a byre.preset (not applied);
     `byre preset apply` to review it."
  2. **Applied, matches**: no noise (steady state).
  3. **Applied, diverged**: "the repo's byre.preset differs from this
     project's byre.config (repo file changed since you applied it);
     `byre preset apply` to review the changes" -- the outdated-lockfile
     state, in both the develop preamble and a `byre status` row.
  A repo shipping a legacy-named `byre.config` gets the same notes plus
  the rename hint (D10), and `preset apply` accepts it as a fallback
  (D16c).

## D14 -- What this deletes

- Materialization of bundled content into the user store, and the word
  "materialize" from the vocabulary.
- `UpdateSkills`/`UpdateTemplates` overwrite-and-backup, `skills.bak/`,
  `sameTree`, first-materialized-wins shadowing.
- The devloop-rename upgrade-path machinery and its tests (stubs stay; the
  clobber-avoidance dance goes).
- `byre skill update`'s current meaning (after the D11 stub release).
- The adoption offer, sticky declines, and re-prompt-on-edit (D17; in
  phase 4, never before preset apply ships); the review/diff machinery
  survives inside preset apply.
- The fail-closed companion multi-claim vanishing act (replaced by the
  D2b picker).

## Docs shipping with the milestone

New ADR (this design; **partially supersedes ADR 0003** -- host-side store
stands, offer-and-adopt-on-develop reversed -- and the 0024/0025 amendments
the D2 favourite change implies); GLOSSARY
(package, bundled/installed/local, fork, manifest URI, retired name,
preset, byre.preset, template = "the box's type" and "shape only", the
symmetry doctrine line, adoption retired-as-offer, materialize retired);
ARCHITECTURE (skills section + store layout + preset flow); **docs/
skills.md** -- the promotion-facing user guide (discover, inspect, install,
author, fork, publish, presets); README how-do-I + install example;
CHANGES; the byre-skills repo README.

SECURITY.md additions (all wording work): "a skill is trusted code"
extended to installed packages and templates; a template is a full config
layer, not a "language preset"; a preset is a config proposal reviewed at
apply; a hash is integrity, never publisher identity or endorsement;
`file:` installs are "installing an unsigned tree from that path" (prefer
tag-pinned https + `--digest` in shared instructions); package
immutability, like config, is host-side integrity -- `--self-edit` and any
host process can write the store; presets carry **no** integrity pin at
all -- their trust story is the review at apply time, nothing else
(review-time WYSIWYG; the packages a preset leads to are what carry
digests).

## Build order

Each phase lands green (gofmt/vet/test) and codereview-looped before the
next starts. Sizing honesty (from review, both reviewers): phase 1 is a
large rewrite, not a refactor -- bare single-path-element names and the flat
skills dir are load-bearing in the loader, staging escape checks, every
`skillsDir` call site, template cascade loading, onboarding, and the config
UI; `EnsureStore` is the spine every command crosses; and D1g pushes the
catalog **into** config resolution. Priced in, not discovered later.

Sequencing rule (from review, both reviewers): **the adoption offer stays
alive until preset apply exists** -- an intermediate release must never
lack a team-shared-config path. Adoption removal ships in the same release
as presets (phase 4), not phase 1.

1. **The model.** Two-stage `[package]` parsing; catalog + package-
   filesystem abstraction (nested local dirs per D1a); bundled loading from
   `embed.FS` + the D7b mirror; generated bundled manifests (D4d); alias/
   protected/retired/INVALID/conflict rules; catalog-aware config
   resolution (D1g); template no-composition validation (D3b); D10 sweep
   on the store-ensure path; `list`/`inspect`/`fork`/`init`/`validate` for
   both nouns; D11 stub; D13 rendering; D2 offer/picker/favourite rework
   (incl. the dual-shape favourite decode); tests rewritten, D14 deletions
   **except** the adoption offer, which keeps working over the new catalog
   until phase 4. Where practical, extract adoption's review/diff/
   locked-write core into a generic review-and-apply primitive now, so
   phase 4 reuses instead of resurrects.
2. **Installation.** `https:`/`file:` manifest fetch with D1h/D5d
   hardening; digest verification (D5f); content-addressed store + index +
   store lock (D7c); `install`/`uninstall`/`pack`; the D9 flows including
   the conservative reference scan and D9b'.
3. **The move.** byre-skills repo populated (pushes are Pete's, host-side);
   devlog/codereview deleted from builtins; D15 tombstones wired; byre's
   own config + docs updated; Pete's store hand-fixed.
4. **Presets + adoption retirement (one release).** `byre preset apply`/
   `inspect` over the extracted review primitive + `[sources]` + the
   chauffeur (D16); the D17 removal, rename notes, and three-state drift
   copy; record sweep; byre's own repo ships a `byre.preset`.

**Acceptance (definition of done):** on a clean store, install both skills
from the real GitHub URIs and self-host byre's own dev box with them --
the dogfood, end to end. Preset acceptance: `byre preset apply` on a fresh
clone of a repo shipping `byre.preset` composes a working box, chauffeured
installs included.

## Open items

None pending -- every fork carries Pete's ruling (grilling + two review
rounds, 2026-07-13). Consciously deferred, for the record: per-box before/after diffs at
replacement (D9a); re-hash on every load (D5f); user-defined alias table;
OCI/signatures/mirrors; template publishing by us; trimming tombstone
install-hints; uninstall-scan courtesies beyond the store walk; a preset
integrity pin (review-time trust is the model).
