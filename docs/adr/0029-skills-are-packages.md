# Skills and templates are packages; presets replace adoption

Skills and templates became **packages** with three provenances, an
installation pipeline, and a preset flow that replaces the develop-time
adoption offer. Decided 2026-07-13 with the maintainer via /grilling plus
three codex/grok design-review rounds; built and reviewed the same day
(the working design of record -- skill-packages-design.md rev 6, a
`wip/`-lifecycle document -- is absorbed by this ADR and deleted; git
history keeps it). This is the project's largest
single decision set; the rationale threads below are the ones that would
be re-litigated without a record.

## The problem

Built-in skills were **materialized**: copied from the binary into
`~/.byre/skills/` as editable files that the loader then read back. That
made "bundled" a lie after first write (first-materialized-wins meant a
hand-edited or hand-dropped near-namesake silently shadowed the shipped
version), forced `skills.bak/` backup machinery, and gave third-party
distribution no story at all: no identity, no integrity, no way to say
"get this skill onto this machine" except copying directories around.

## The decisions

**Package model.** A package is a skill or a template (one package = one
kind; both nouns get the full verb set). Three provenances:

- **Bundled**: inside the byre binary, loaded from `embed.FS` only --
  immutable structurally, not by convention. Ids are `byre/<name>`, and
  `byre/*` is permanently reserved. A **display mirror** at
  `~/.byre/bundled/` is regenerated on every version change for grep-and-
  read workflows; the loader never reads it, so shadowing and drift are
  impossible. Bundled manifests are generated at release build (version ==
  byre version; versions are descriptive, never compared -- the digest is
  the change signal).
- **Local**: editable directories under `~/.byre/skills|templates/`,
  bare or `owner/name` nested; the id defaults to the store path. No
  hashes -- the directory is the package.
- **Installed**: content-addressed snapshots under
  `~/.byre/packages/<sha256>/` with an `index.toml`, acquired by
  `byre skill|template install <manifest-url>`. Installed ids must be
  qualified (`owner/name`) -- strictness lives at the trust boundary.

**Identity.** One resolution function serves every name surface, and
config canonicalizes references through the catalog BEFORE cascade merge,
so `!byre/claude` cancels a lower layer's bare `claude`. A bundled
package always owns its bare name (the alias); duplicates and broken
packages are per-identity problem rows (INVALID/conflict/LEGACY --
listed, disabled-with-reason in pickers, hard error only when
referenced), never a global failure and never silent shadowing. Names a
past release bundled join a permanent **retired-names table** with
tombstone remedies (`codereview`, `devlog` are the first) -- freeing a
name users spent releases typing is typosquatting bait.

**Manifest.** `[package]` in the primary file (id, version, kind,
`package_api`, `requires_byre`); two-stage parse -- stage 1 reads only
that frozen core leniently so a package needing a newer byre says so
instead of dying on "unknown key"; stage 2 is today's strict parse.
Local packages may omit the block entirely. A dev build (unstamped)
passes every `requires_byre` constraint -- the check is compatibility,
not security. Stage-2 strictness ALSO runs eagerly at catalog scan for
local and installed packages (ruled 2026-07-13, amending the earlier
use-time-only stance): a typo'd package shows INVALID in `list` instead
of looking fine until enabled.

**Payloads and integrity.** Installed manifests carry an exhaustive
`[[package.files]]` list (dest, src, sha256, exec bit) -- installation
fetches exactly that and verifies every hash; the primary file cannot
list itself (it is the fetched manifest). The **package digest** is
sha256 over a domain-separated encoding of the manifest bytes plus the
sorted payload records -- the manifest is inside the preimage so a
manifest change can never ride an unchanged digest. The digest keys the
snapshot, the same-id no-op rule, and `--digest` pins on printed install
commands (a git tag is a convention; the digest is the integrity).
Verification happens at acquisition; snapshots live on user-writable
disk, so the label reads "installed 1.1.0 (sha256:...)" -- provenance of
acquisition, never a runtime attestation. Fetch is https/`file:` only,
payload sources relative to the manifest and contained after resolution
(origin pinned across redirects, symlinks resolved, encoded traversal
rejected, bounded sizes). `byre skill pack` emits the distribution
manifest from a local package.

**Install semantics.** Install and replacement are one operation keyed
on the declared id: same digest = no-op; same id = replacement with the
payload diff, grant-declaration delta (raw Dockerfile lines diff
verbatim -- a swapped build command must never hide behind an unchanged
count), machine-wide wording, and the affected boxes from a conservative
syntactic reference scan of stored configs. Installing an id that stored
configs already reference is **activation** and gets the same consent.
Replacement, uninstall, and activating installs demand a TTY confirm or
`--yes`; a FRESH, unreferenced install is a verified download that
grants nothing, so it confirms on a TTY and **proceeds in a pipe**
(scriptable bootstrap matters) -- "installed -- grants nothing until
enabled in a box" is the boundary.
Store mutations run under a store-global lock with crash-safe ordering
(snapshot, then index atomically, then superseded-snapshot delete; the
consent's precondition is re-checked under the lock).

**`[sources]` hints and remedies.** A config or preset may annotate
package references with `{ uri, digest }` acquisition hints -- never
auto-fetched. Anywhere byre reports a missing package it prints the
exact, kind-correct, layer-attributed install command (terminal-escaped
AND shell-quoted: a hostile hint buys an install review, not command
injection).

**Presets replace adoption** (partially supersedes ADR 0003: the
host-side-store premise stands; the offer-and-adopt-on-develop clause is
reversed). A preset is a **saved answer to onboarding's questions** -- a
complete config proposal from anywhere, conventionally `byre.preset` in
a repo. It is not a package: no identity, no version, no install.
`byre preset apply` is the one flow in which byre initiates acquisition
walk-throughs -- the **solicitation rule**: inside a flow the user
invoked to compose a box, the chauffeur walks each missing package
through its own install consent; anywhere a third party's document
introduces references (a cloned repo, a develop tripping on dangling
refs), byre reports and prints exact commands, never prompts. Apply
reviews the composed box's full grant summary (diff against the current
config), writes `byre.config` on confirm, and records an `applied`
marker (sha + source). Drift is passive and three-state -- not applied /
silent steady state / "differs from the version you applied" -- claiming
only what the marker proves. The develop-time adoption prompt and its
sticky-decline records are deleted; `adopted` records migrate to
`applied` markers so existing projects keep their history.

## Consequences

- Enabling remains the only grant: install, like cloning, changes what
  is *available*, not what runs. The consent surfaces (status, config UI,
  install/replace reviews) all speak provenance.
- The first-party skills (`pjlsergeant/codereview`, `pjlsergeant/devlog`)
  live in github.com/pjlsergeant/pjlsergeant-byre-skills; byre's own
  repo ships a `byre.preset` and self-hosts on installed packages -- the
  dogfood is the acceptance test.
- `byre skill update` is a transitional stub (bundled packages update
  with byre itself); the materialize/update/backup machinery and
  `skills.bak/` are gone.
- Consciously deferred: bundled display digests in inspect (since shipped,
  2026-07-14: computed from `embed.FS`), authenticated
  (private-repo) https fetch, and the interactive one-confirm legacy
  archive the design draft wanted (replaced by ruling with the
  `byre skill archive-legacy` command plus loud store-ensure notices).

User guide: `docs/SKILLS.md`. Vocabulary: `GLOSSARY.md` (Packages
section).
