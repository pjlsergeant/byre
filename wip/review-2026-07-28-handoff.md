# Whole-repo review, 2026-07-28 -- findings, decisions, and the worklist

**Status: EXECUTED (2026-07-28, ~63 commits on main).** The worklist below was
verified against HEAD (two claims refuted), grilled to ratified decisions, and
implemented across eight units -- each through a two-reviewer loop (codex +
grok, unbriefed), the doctrine unit additionally through a delegated
adjudication seat. Gated suite green -count=1 on the runner, pty tier
included. What survives this file: the follow-ups accrued during execution
(recorded in the session devlog's dispatch plan -- onboarding problem rows,
deliver sibling-printer escaping, the config UI's reserved-env surface,
tomldoc value-encoding residuals) and one one-line CLAUDE.md edit awaiting
the maintainer. Deletion of this file is the maintainer's call per
wip/README.md -- the decisions it records are now in the tree (PRINCIPLES,
the ADRs, CHANGES) and its git history.

This is a session handoff. It carries everything needed to resume without the
original conversation.

---

## 1. What happened

byre was reviewed from scratch by **nine independent reviewers**, none briefed
on the others' output: `codex` and `grok` (via `byre-codereview --raw`, run
concurrently so neither could read the other's `reviews.md` entry), one Opus
whole-repo pass, and six Opus passes each scoped to a subsystem
(config/tomldoc/gen, runner/build/lock/firewall, skills/packages/onboard,
hostopen/deliver/credentials, configui/cmd, tests).

Every reviewer was told to ignore git history and `.byre-devlog/`, so they
judged the tree as it stands rather than as a sequence of changes. `wip/` and
`TODO.md` were context-only; `site/` was in scope for discrepancies only.

Afterwards: git history was consulted to settle "decision or oversight?"
questions the blackout had made unanswerable; five checks were run on the
inttest runner to settle claims that needed a real engine; a focused pass
examined whether the legibility model still holds; the resulting decisions were
put through an adversarial review that found three errors in them; and a final
pass found a fourth.

**Codex and grok produced entirely disjoint finding sets.** The Opus whole-repo
pass overlapped both. Read that as a coverage signal: no single pass over this
repo finds most of what is there.

## 2. Where the artifacts are

| What | Where | Survives? |
|---|---|---|
| Full collated findings (~60, ranked) | `.byre-devlog/whole-repo-review-2026-07-28.md` | on disk, not committed |
| All 12 raw reviewer reports + decisions record | `.byre-devlog/review-2026-07-28/` | on disk, not committed |
| This handoff | `wip/review-2026-07-28-handoff.md` | committed |

`.byre-devlog/` is self-ignoring, so those survive a session clear but not a box
rebuild. Copy them to `~/scratch` before any rebuild.

## 3. Shipped already

**ADR 0052 -- runtime mount shadowing gets one blanket disclosure.** Merged to
`main` (`--no-ff`, 13 files, +540/-79). gofmt clean, `go vet` clean, 19 packages
pass.

The issue: byre bakes files into the image and then makes claims that read from
them -- the launcher, the launch gate, a network-posture skill's enforcement
script, and the delivery artifacts under `/etc/byre` (`mcp.json`, the agent
context, the Claude Skill tree). A `[[mounts]]` or `[[volumes]]` target covering
one of those replaces it in the *running* box. Unlike a `files` entry -- which
the Dockerfile tail re-COPYs after the project block, so byre's copy wins --
there is no runtime equivalent, so any claim derived from a covered path
describes the built image rather than the container.

Two independent reporting gaps existed: `artifactShadows` computed shadowing
only from `cfg.Files` and never inspected mounts or volumes at all; and
`guardMountVolumeHits` inspected mounts and volumes but only the project's own,
excluding skill-declared ones on the grounds that skill contributions are
trusted "as with `files`" -- an analogy that holds only because of the
re-assertion the same comment says does not exist for mounts.

What shipped: one disclosure line per offending target, in the containment
register, on `status`, at develop, and in the preset-apply grant review (from
one exported prose function so the three cannot drift). Skills included and
attributed. `gen.ByreDir` was refactored so `MCPConfigPath` and
`ClaudeSkillsPath` derive from it and the whole directory is a root -- an
artifact added later is covered automatically rather than needing a list entry.

**Deliberate behaviour change:** `GuardMountShadow` is gone from `statusInfo`. A
covering mount no longer degrades the Network row; it produces a Containment row
instead. Reasoning (ADR 0052): byre knows a path is covered and nothing about
what covers it, so naming which claims broke would overclaim.

**Note for the ADR 0010/0050 annotation below:** a `BYRE_` env key still
degrades the network claim while a mount now does not. That is deliberate and
coherent -- an env key names the knob, so byre knows which claims it skews; a
mount covers a path and byre cannot tell what is behind it -- but it must be
stated in the annotation or it will read as an inconsistency later.

## 4. Decisions taken, with corrections applied

Eleven decisions were reached in interview. An adversarial pass then found three
errors and a follow-up found a fourth. **Corrections are folded in below** --
where a decision changed, the superseded version is marked so the reasoning is
not re-derived.

### 4.1 Escaping at the output surfaces (P4)

**The issue.** Five reviewers each independently found a *different* output site
where strings originating outside byre reach the terminal without passing
through `packages.EscapeTerminal` -- the helper used at ~111 sites elsewhere.
`status.go`, `exitreport.go` and `selfedit.go` contain **zero** calls to it.

The strings involved are config values (raw `run_args`, raw build lines, mount
targets), skill metadata, filenames created in the project during a session, git
config keys and values, and env var names. Terminal control sequences in such a
string are *interpreted* by the terminal rather than displayed, so a row can
render as something other than its actual content, including overwriting lines
already printed above it. The surfaces most affected are the ones whose job is
to report accurately: the exit report, the `--self-edit` review diff, and `byre
skill inspect` (which renders a package's contents before it is installed).

**History.** `909180e1` scoped an earlier sweep and checked status explicitly --
*"byre status was checked unaffected (key names only, grammar-refused)"*. True of
the `Env` row; false of `Raw run args` and `Raw build` three lines below in the
same function. `948c5f24` then moved the strip to **paint funnels** in
`internal/configui`, with the rationale *"Two rounds of per-site sweeps each
missed peers; the funnel is the primitive that makes the safe behaviour
unavoidable."* The CLI surfaces were in neither pass.

**Decision.** Funnel-level strip in `status.go` (`row`, `:349-354`) and
`exitreport.go` (the `for _, l := range lines` loop). A **new** funnel for
`deliver/transport.go`, which has 16 separate `fmt.Fprintf(cfg.Err, ...)` calls
and none today. Per-site fixes at `skill.go:254,357`.

**Correction (was wrong in the first draft):** for self-edit the site to fix is
**`selfedit.go:212`**, the unified-diff body over the project config's content --
not `:233`, which prints file names. `:212` is the one that matters, since the
diff is the review a human reads before the next develop applies the change.
Name it explicitly so the arm below cannot be satisfied by the file-name list
alone.

**Verified safe:** `status.go` emits no ANSI of its own, and none of its row
helpers (`pkgLine`, `mcpStatusLine`, `claudeSkillStatusLine`, `closureLine`,
`portStatusLine`, `contextLine`) style. Only `pick.go` and `beat.go` touch
lipgloss/ansi in all of `internal/commands`. So stripping inside `row()` cannot
eat byre's own codes.

**Rejected: a typed `Data`/`Verbatim` seam** mirroring hostopen's closed set.
Because those renderers emit no ANSI, the type would be `Data` at every call
site and `Verbatim` at none -- a type with one inhabitant is ceremony, and
ceremony is what gets skipped on the next surface.

**Plus an arm:** feed each reporting surface a fixture whose every
externally-sourced field carries ESC/CSI/OSC payloads, assert **no raw `0x1b`**
in the output. Exact rather than approximate, because these renderers have no
legitimate ANSI to except.

**Plus one implication added to P4:** the reporting surfaces render
externally-sourced strings as data, never as terminal control. **Scope it to
report surfaces** -- `909180e1` knowingly accepts a residual where the item
editor's textinput and the raw-block textarea render prefilled values through
bubbletea widgets that do not strip, and an absolute wording would contradict
that on day one.

### 4.2 `sharedNetMode` -- no code change, comment only

**The issue.** `netns.go:19-21` decides whether to run a `root + CAP_NET_ADMIN`
helper against a container's network namespace by matching three known-*shared*
spellings (`host`, `container:`, `ns:`). Anything unrecognised is treated as
private and gets the helper. Every other branch on that path declines on
uncertainty. What is being protected, per `netns.go:104-116`, is that byre's own
helper would otherwise rewrite network state **outside the box** -- default-DROP
landing on the host's stack or another container's -- and that in a shared
namespace the launch gate (a listener on a loopback port) can be opened by
something other than byre's own helper.

**Engine checks run** (docker 29.6.2 / podman 5.4.2, on the inttest runner):
plain podman reports `pasta` (private, correctly unmatched); `--network
container:X` reports `container:<full-id>` (matched); `--network host` reports
`host` (matched); docker default reports `bridge` (unmatched). So no known
misclassification exists today.

**Decision: keep the denylist.** Inverting to an allowlist of *private*
spellings is not available -- a NetworkMode can be an arbitrary user-defined
network name, so the private set is open-ended and an allowlist would refuse
legitimate configurations (P1).

**Correction (the first draft's justification was wrong).** The proposed comment
said *"sharing a namespace requires naming the target, there are exactly three
namings"*. `host` is a shared mode, is a bare keyword, and names nothing -- the
code's own comment at `netns.go:17-18` already lists it that way. So "exhaustive"
is precisely what the comment cannot claim. Write it honestly instead: the list
is a denylist, kept open-by-default deliberately, and the `--pod` case is
unverified.

**Open and cheap:** the podman `--pod` case could not be tested because
`catatonit` is not installed on the inttest runner, so `podman pod create`
fails. What `NetworkMode` reports for a pod member is the one real unknown here.
Install `catatonit` on the runner and settle it before writing the comment; if a
pod member reports something outside the three spellings, this decision changes.

### 4.3 `resolveIdentity` -- disclosure only

**The issue.** `resolveIdentity:50` is `if err != nil || !rootless { return
hostIdentity(), nil }`, so a failed rootless-Podman probe is folded into "not
rootless" and byre silently selects the host-UID-bake path. On an engine that
really is rootless Podman that yields files owned by subordinate namespace ids
rather than by the invoking user. `status.go:226` prints a bare `podman`,
showing nothing about the probe having been inconclusive.

**Context found on reading:** `identity.go:40-41` enumerates the error case as a
*decision* -- *"rootful engine (or a rootless-detection error -- better to run
than to refuse on a guess): the invoking host user, quietly (ADR 0008)"*. So this
is a documented position, not an oversight, and the objection is a disagreement
with a stated choice.

**Decision: leave control flow alone; add the `status` disclosure** so a bare
`podman` stops hiding that detection was inconclusive.

**Correction / still open.** The finding named a *second* mechanism the decision
does not address: `IsRootlessPodman` returns
`strings.TrimSpace(out) == "true"`, so any successful-but-unexpected output
(a renamed field, an empty string) yields `(false, nil)` -- indistinguishable
from a confident rootful answer, so there is nothing for the disclosure to fire
on. Either check for `"false"` explicitly and treat anything else as
inconclusive, or record that this case is consciously deferred. As it stands
half the finding evaporates silently.

### 4.4 `tomldoc` -- inline-table edits (**shape changed; read this one carefully**)

**The issue, reproduced.** A config spelling a table inline --
`defaults = { skip_questions = true }` -- makes `SetKey` append a second
`[defaults]` header. The result is syntactically valid and semantically
illegal: `toml.Unmarshal` rejects it, `unstable.Parser` does not. `splice`'s
restore-on-corruption guard reindexes with the *unstable syntax* parser, so it
cannot detect the one corruption class tomldoc produces. `configui.Save` writes
it and reports success; every later byre command in that project then fails at
config load -- machine-wide for `default.config`. Separately `RemoveKey` inside
an inline table returns nil and changes nothing, so unticking a checkbox reports
saved and silently does not save.

**Root cause, confirmed:** `keyValueExpr` (`tomldoc.go:146`) recognises
`unstable.InlineTable` (`:164`) only to compute an opaque value span and never
descends, so inline members are never indexed and `findKeyValue` cannot see
them. `SetKey`'s fallback (`ops.go:43-45`) then appends a `[table]` block.

**SUPERSEDED -- decision D:** descend into `InlineTable` nodes when indexing so
`SetKey` edits in place. Do not do this.

**CURRENT -- decision E, which ADR 0044 already specifies.** `docs/adr/0044:78-80`:

> *"Exotic-but-valid constructs are untouched until an edit TARGETS them; **then
> that construct alone is rewritten in house shape** (an interior comment belongs
> to the construct)."*

So the specified behaviour when an edit targets an inline table is to rewrite
that construct in house shape -- a `[defaults]` block carrying the existing
members plus the edit, original inline line removed. D is both non-compliant
(it preserves the inline spelling, which is *not* "rewritten in house shape")
and the harder implementation: the comma/brace/first-last span arithmetic is
work the ADR never asked for. The span E needs is **already computed** --
`keyValueExpr:164` derives the value span for the inline table today.
`RemoveKey` falls out of the same path: rewrite the construct minus the member.

**Plus B:** keep a semantic guard (a real `toml.Unmarshal`) in
`splice`/`Bytes()` as a backstop. With E in place its job is catching the *next*
unhandled shape loudly, which makes the existing `"(internal bug)"` wording
accurate again.

**Correction (a claim in the first draft was false).** It said no tomldoc test
asserts the output still parses. `tomldoc_test.go:57` defines `mustParse` --
*"reparses under the strict product decoder shape: the edit engine must never
emit TOML the parser refuses"* -- called **16 times**. The postcondition is
already there. The real gap is narrower: **no test mutates inside an inline
table**. `TestInlineTableValueSecondEdit:458` replaces the whole inline value and
never touches a member. Write that test, not more parse assertions.

**Also rejected: (C)** parse-and-compare-to-intent in `configui.Save`, lifting
`onboard.SaveSharedAuthDefaultPick`'s net. Its unique value was catching the
`RemoveKey` no-op, which E fixes at source, and comparing a whole parsed
`config.Config` against `cfg` risks false refusals in the editor's save path
(zero-vs-absent, nil-vs-empty-slice, `*bool` tri-states).

### 4.5 ADR 0010 vs ADR 0050 -- annotate, and one disclaimer paragraph

**The issue.** ADR 0010 says *"skill contributions never degrade the posture
claim (enabling a skill is trusting it)."* ADR 0050 tier 2 says a skill's
reserved-env key **does** degrade the network claim. The code implements 0050;
0010 carries no annotation, and its sentence is the one a future reader would
cite to remove the degradation as a defect.

**Decision -- annotate ADR 0010 with a three-way split**, grounded in
`docs/DOCKER-HOST.md:81-84` (*"byre warrants its own construction, never the
consequences of your hole... The Containment line disclaims everything done
through the socket in one place"*):

- **Skill contributions** (declared egress, mounts) -- byre built what the skill
  asked for; that *is* byre's construction working. **Do not degrade.**
- **Skill overrides of byre's own `BYRE_` vocabulary** (launch gate, egress
  knobs) -- byre's construction is no longer in place, so byre's claim about its
  own construction has stopped being true. **Degrade.**
- **What runs in the box using a granted channel** (a tunnel over an allowlisted
  443, DNS through the resolver, the docker socket) -- consequences, not
  construction. **Disclaim once, loudly, in one place.**

**Correction: state the boundary by the test, not by field kind.** As first
drafted, the split sorts by *which config field* something rides -- and a skill
mount over a byre-managed path displaces byre's machinery exactly the way a
`BYRE_` env knob does. ADR 0052 has since decided that case (blanket disclosure).
Write the boundary as the second bullet's own test -- *a contribution that
displaces byre's own machinery is disclosed, whatever field it rides* -- and
cite ADR 0052 rather than re-deriving it. Also record why a `BYRE_` key degrades
a specific claim while a mount gets a blanket line (see §3).

**Plus one paragraph on `site/content/docs/security-model.md`:** an allowlisted
host is a **channel, not a permission**. This covers the DNS residual ADR 0010
accepted and never published, an allowlisted CDN, and tunnel-shaped skills, in
one disclaimer instead of three. It **absorbs** the separate finding that ADR
0010's residual never reached the user-facing page.

**Plus, mechanically:** `config.Exposure`'s per-launch banner populates fewer
degradation inputs than `status`'s `networkLine`, so the banner can assert
deny-by-default where `status` declines to. Have both surfaces consume **one
resolved degradation set** rather than adding two more fields that can drift
again -- ADR 0050 already states this rule.

### 4.6 Version semantics

**The issue.** ADR 0016 defers version-number semantics, scoped *"pre-1.0"*.
byre passed 1.0.0 on 2026-07-18 and is at v1.4.0, having hard-refused a
previously-valid config key (`npm_global`) in a minor. ADR 0049's window policy
covers *compatibility paths* only, so live-key removal is governed by nothing.

**Two reviewer findings struck as wrong:** ADR 0049 is applicable as written --
the clause is *"two minor releases **or 90 days, whichever is longer**"*, so 90
days is a hard floor and the undefined unit can only push removal later. And
`install.md:7`'s stability line is hedged (*"should be pretty stable"*) inside a
block opening *"byre is a young project"* -- softer than reported. Left as-is.

**Decision.** State the position in `docs/RELEASING.md`: byre is 1.x, and a minor
may remove a live config key loudly, with a CHANGES entry and a refusal carrying
a remedy. `npm_global` was exceptional and is noted as such. One sentence in ADR
0049 recording that live keys get judgment rather than a window. Document
`install.sh`'s `BYRE_VERSION` pin knob on the install page -- today it exists
only in the script's own header, and it is the only answer a user has when an
upgrade breaks them.

### 4.7 Config keys with no editor surface (P0/P6)

**The issue.** Four toml-visible keys have no widget. Verified: the `fieldID`
enum runs `fBase`..`fSkipQuestions` with no `SeedPrefs`/`Sources`/`SharedAuth`,
and `volumes.go` is *engine volume admin* (list and clear), a different job from
declaring `[[volumes]]`. `site/content/docs/configuration.md:66-67` claims
*"every config feature is editable there"*. P0 and P6 are both `[no arm]`.

**Decisions:**

- **`seed_prefs` gets a real tri-state widget.** Argument independent of P0: the
  seed fires only into a **fresh** state volume, so developing once without it
  closes the window until `byre reset`. It is a perishable option gated on a key
  currently discoverable only in the configuration reference -- the population
  that will find it after it stopped being available.
- **`[sources]` shows read-only with attribution** -- written by `byre preset
  apply`, a chauffeured flow; hand-editing a digest pin in a TUI is not the
  interface.
- **`shared_auth` shows read-only, adjacent to the `skip_questions` checkbox**,
  with the staleness notice the interactive path already uses
  (`StalePickNotice`), **and** the staleness filter applied to the
  `skip_questions` apply path so a stale pick is not applied. Rationale: on its
  own `shared_auth` is a *favourite* (ADR 0025, cascade-inert by construction,
  changing only what the next offer prefills) and needs no editable surface --
  but `skip_questions` **does** have a widget and turns it into a standing
  instruction, so byre currently offers an editable toggle that arms a value the
  editor will not show.
- **P0 gains a clause:** a key whose owner is a *flow* rather than the editor is
  shown **read-only**, not hidden. This keeps what P0 protects (invisibility)
  while naming an exception class the product already has vocabulary for (the
  read-only "Skill files" screen).
- The site's *"every config feature is editable there"* is corrected to match.
- The third reflection guard becomes buildable -- *every toml-visible field names
  its widget **or its read-only owner*** -- same shape as
  `TestReconcileCoversEveryField`'s `migrationOnly` map, which must be *named*
  rather than silently skipped. This is the arm P0 has never had.

**REOPENED -- `[[volumes]]`.** The decision was "read-only, GLOSSARY is already
right." That rests on a misreading. `GLOSSARY.md:427` says *"**Usually**
contributed by a skill"* and `:434-435` says the grammar is *"General
`[[volumes]]` grammar, **config or skill**"* -- the glossary names config as a
source, and `config.Config` carries `Volumes []Volume` (`config.go:400`) with its
own validation. Worse, the new P0 clause does not license the bullet: it permits
read-only for a key whose owner is a *flow*, and config-declared `[[volumes]]`
has no flow -- its only writer is hand-editing. `[sources]` and `shared_auth`
both have flows and are fine.

Resolve one of two ways: **deprecate config-declared `[[volumes]]`** (§4.6's new
minor-removal policy is the instrument), or **give it a widget**. Read-only
display of *skill-contributed* volumes is fine and precedented (`fSkillFiles`),
but that is a different thing from the config-declared grammar.

Also unresolved: decision 4.8 scopes P6 while this decision edits P0, and P6's
headline (*"`byre config` is how configuration is edited -- for every config
feature, always"*) is untouched by either. A read-only class contradicts that
sentence as written. Settle both in one sitting (§6).

### 4.8 An unparseable config file locks the editor

**The issue.** `commands/config.go:209` calls `config.ParseFile` then
`return err`. `byre config --global` against a `default.config` with one bad line
prints `byre: <path>: toml: expected '=' after key` and exits 1 -- no remedy, no
line number. P6 says no error remedy may expect a user to open a config file in
a text editor; the site says no error message will ever send you into the files.
Reachable via `ctrl+e` -> leave invalid -> quit -> locked out.

**Decision (maintainer's call, against a recommendation of an in-editor repair
screen).** A user reaches this by hand-editing; naming what is wrong and which
file to edit is enough. The error gains the parse error, the **line and column**
(go-toml/v2's `DecodeError` carries them), and the path.

**Consequences accepted:** P6 gets a written scope -- it governs *parseable*
config. The site sentence gains the exception. `configui.go:64-67`'s comment
stops pointing at an "editor's flow" for repair that does not exist.

**Correction to the wording.** As drafted the P6 scope note says a file
hand-edited into invalidity is out of scope *because the user authored it*. That
is only true once §4.4 ships -- today `configui.Save` can write a file that later
fails to load, from a valid hand-authored spelling. Either state the dependency
("...because every byre writer verifies semantically before writing -- ADR 0044,
§4.4B") or word it so it does not assert byre cannot be the author. Note also
that `ctrl+e` is byre's own offered route into this state.

### 4.9 `skill validate`'s tier gap

**The issue.** Value-level rules (`network_posture` spelling, `netns_init`
absolute-path, egress grammar, MCP/claude-skills shape, `sock_groups`,
`[build].files` containment, closed-set adapter values) live only in `Resolve`,
which runs at develop. `validateOne` (`skill.go:669-671`) calls `skills.Load`
only. So `network_posture = "Deny-Default"` passes validate, pack, inspect,
install and `list`, then fails at first develop. Separately, `list()`
(`skills/skills.go:680-682`) does `if err != nil || !keep(sk) { continue }` -- a
skill that parses but fails `loadEntry` is dropped from
`ListSkills`/`ListAgentSkills` **and** is not a problem row (its catalog row is
healthy), so it appears in neither the config UI's skills screen nor the
onboarding agent picker. Absent, rather than listed-with-a-reason.

**Decision.**
- Extract the **intra-skill** value checks into one function called by `Resolve`,
  `validateOne`, and the stage-2 catalog hook.
- Convert `list()`'s silent drop into a **problem row**. This is the half that
  affects users, and it is small.
- Note in `docs/SKILLS.md` that **cross-skill** checks (duplicate MCP names,
  `[agent].state` naming a contributed volume) are resolve-time only, because
  they are properties of a set -- so `validate` is structurally a partial
  promise.
- Preserve the existing seam: **install refuses** (`acquire.go:66`), **catalog
  scan marks INVALID** (ADR 0029's amendment).

**Accepted consequences:** a package with a bad value that installs today will
start failing to install, and an installed-but-never-enabled one will flip to
INVALID. **Additional consequence found later and not in the first draft:** this
couples installability to byre's version vocabulary -- a package using vocabulary
a *newer* byre accepts will refuse to install on an older one, and a byre upgrade
that adds a check can flip idle packages to INVALID on the next catalog scan.
Today's late failure is what gives older byres graceful degradation on newer
vocabulary. Probably still the right trade (the parse-strictness seam already
made it) but it belongs on the record, and it argues for landing §4.6 first.

### 4.10 Two documentation items

- **`site/content/docs/how-it-works.md:24-26`** says the build-tail guard means
  *"nothing earlier can quietly replace them."* ADR 0011 **measured** that the
  guard covers a `files` entry only -- a raw `dockerfile_post` or skill line can
  point the launch gate at `/dev/null` and the COPY writes through it, which byre
  covers by qualifying the claim rather than by the guard.
  `ARCHITECTURE.md:176-186` states it correctly. **Correct the public page to the
  ADR's measured scope.** This is the one documentation drift that concerns a
  containment claim.
- **`.goreleaser.yaml:64`** skips the Homebrew cask push when
  `HOMEBREW_TAP_GITHUB_TOKEN` is absent and the release still reports success --
  while brew is the README's only blessed install command and the landing page's
  only console line. Fine-grained PATs expire by design.
  **Revised remedy:** the first draft added a line to RELEASING.md's release-time
  sweep, but CLAUDE.md itself calls that sweep *"the backstop, not the
  mechanism"*, and a human checklist is weakest against a *silent* skip. Prefer a
  loud non-fatal warning in the release output, or a post-release check that the
  published cask version equals the tag. Same size, self-executing. ADR 0016's
  "the tap is a later nicety" rationale gets a note that PLACEMENT P3 promoted
  brew to the blessed command.

### 4.11 Mechanical fixes, no decision required

Each is a defect with one obvious fix, or a fact now measured:

- **`TestFieldInfosCoverEveryField` loops `f <= fExtends`** but `fSkipQuestions`
  is last, so a field appended at the end passes both halves and renders a blank
  label and a `kindScalar` misclassification. **P0's arm is failing open today.**
  One token.
- **`skip_questions` is missing from `configui/complete.go`'s `sig()`** -- so
  dirty tracking never sees it. `esc` quits without arming the confirm and prints
  `byre: config unchanged.`; `ctrl+e` reloads from disk over it; the footer reads
  "No unsaved changes" while the box on screen is ticked. `ctrl+s` works; only the
  notification is missing. Its sibling `wtSibling` *is* signed.
- **`onboard/config.go:187-190`** returns early on an unchanged answer, *before*
  the migration at `:200-205` -- the un-fixed sibling of the migration already
  corrected in `reconcile.go` by `30da1b87`. This makes ADR 0049 item 1's
  affected population much larger than the ADR states.
- **Four `hostopen.ExistsNoFollow` results discard the error**
  (`onboard.go:46`, `preset.go:249,254`, `rehome.go:198`), collapsing "could not
  determine" into "absent". The rule is written down twice in the tree
  (`probe.go:36-38`, `exitreport.go:392-401`) and handled correctly at
  `reset.go:57` and `preset.go:454`.
- **Two discarded `Close` errors on files byre just wrote** --
  `build/context.go:967-972` (a short staged `files` entry gets baked while
  `AssembleWarn` reports success) and `deliver/remote.go:318-323` (a short flush
  aborts the delivery with the misleading *"%s changed while being sent"*). The
  only two write-mode discards among 36 deferred `Close` calls.
- **`selfedit.go:88-89`** does `s.config, _ = io.ReadAll(...)` then
  unconditionally sets `configReadable = true`, so a partial read is diffed and
  presented as complete.
- **`ShellArg` missing at `pack.go:63`** -- `inspect` prints a ready-to-paste
  install command terminal-escaped but not shell-quoted, unlike the two other
  sites that print the same command. ADR 0029 states the rule.
- **A determinism test for `gen.writeFiles`** -- deleting its `sort.Strings`
  leaves the suite green while the Dockerfile stops being byte-stable for any
  config with two or more `files` entries. The sibling `writeEnv` has exactly
  this test, written after the same defect shipped there.
- **The 125-127 exit band in `develop.go`.** **Measured on the runner:**
  `start -a` returns **1** for an engine-level failure and passes **126/127**
  through from the container. So the reservation does not apply on byre's
  `create`+`start -a` path, and a box whose entrypoint cannot execute the agent
  command currently reports as a byre failure. `StartAttach`'s own comment
  already says the premise changed; `develop.go` applies it anyway.
  **This is an observable behaviour change** to a contract the project treats as
  designed -- it needs an accepted-consequence line, which the first draft did
  not give it.
- **`firewall.sh`'s agent context** tells the agent to diagnose with
  `ping`/`traceroute`, *"an allowlisted host answers, a blocked one hangs"*. The
  rules are per-(ip,port) **TCP** accepts under a DROP policy, so ICMP is dropped
  to every destination and an agent following the instruction will report a
  correctly-allowlisted host as blocked. The prose predates ADR 0012's
  port-scoping. `firewall-open`'s identical bullet is fine (policy stays ACCEPT).
  Fix is wording, possibly minus two apt packages.
- **"The depth ruling"** is cited as settled at `install.go:319` and exists
  nowhere in the tree -- it was recorded only in commit `be5edaf8`'s message
  (*"grant diff is now content-sensitive: raw Dockerfile lines and template
  run_args diff verbatim (escaped, marked not-introspected) so a swapped build
  command can never hide behind an unchanged count"*). Move it somewhere
  findable.
- **A dependency note for `gopkg.in/yaml.v3`** -- **verified archived 1 April
  2025**, upstream explicitly unmaintained, so Dependabot will never signal. It
  parses `SKILL.md` frontmatter. The finding is the asymmetry: `textdiff` gets a
  full written rationale at `diff.go:16-22`; this gets nothing.
- **Documentation corrections:** `seed_prefs` and `worktree_base` are filed under
  "Preferences (picker-owned)" in the configuration reference and neither is
  picker-owned; the doctrine index line for ADR 0013 says "never a directory"
  where the ADR says "never a directory *copy*" and then lists `themes/` (per the
  index's own rule the line is the bug); ADR 0049 and TODO.md name
  `EncodeTOMLLine`, which is `EncodeTOMLValue`; ADR 0029's Consequences reference
  `byre skill update`, a verb no longer registered.

## 5. Consciously deferred

Real, and none earns machinery now: exec-bit provenance (not live -- every
bundled payload needing the bit carries an explicit `chmod +x`);
`hostopen.PublishFile`'s missing docstring caveat (latent, all current callers
publish into paths whose final component cannot be swapped);
the unexplained `:` in `egressHostRe`; `listitem.go:303`'s `Atoi` fallback;
`packages/store.go:152`'s remove-then-rename; `clipread.go:44`'s missing timeout
and cap; and cosmetic documentation drift (a `Mounts` vs `Extra mounts` section
name, a `HEALTHCHECK NONE` placement description, a `GLOSSARY` workspace-vs-inbox
ownership line, and three surfaces showing `bundled v1.3.1` at v1.4.0).

**Not yet triaged at all** -- these were found but never reached a decision, and
should be picked up before the worklist is called done: `lock.Acquire`'s
unbounded requeue against a path `--self-edit` mounts rw; `EnsureStore`'s
bundled-mirror rewrite being the one store mutation that does not take
`WithStoreLock` (two worktrees racing after a version upgrade can fail develop
with `rename: directory not empty`); the netns goroutine and `ProbeSockGroup`
having no time bound, where `netnsWait` runs *after* the session ends;
`planGuard` resolving the guarded source through a Go map so intra-skill
duplicate dests make output non-deterministic; `reconcilePorts`' comment
documenting semantics `mergePorts` forbids; and ADR 0042's premise
(*"apt layers are the only skill layers with a network dependency on mutable
external state"*) being false of four bundled skills that `curl | bash` an
installer.

Also untriaged: the whole test-and-arm tier -- gated-arm markers in the doctrine
index (four entries name arms behind `BYRE_DOCKER_TESTS=1` and the index gives
no signal); `reset`/`forget` resting on two engine behaviours no test verifies;
`runNetnsInits`/`sharedNetMode`/`stopClosed` having no test at any tier while
`firewall_integration_test.go:15-17` states they are covered; `firewall.sh`'s
entry parser having no behavioural test in a package with 2,995 lines of test
against 81 lines of Go; exit code 2 never being observed as a process status;
`withSetupLock`'s placement being convention with no arm and `withTwoSetupLocks`
having no test at all; 23 tests silently skipping on missing `git`/`sh`/`bash`/
symlink support (including ADR 0047's named arm); zero fuzz targets; and
`internal/tuitest` being invisible to Go's test cache, so `go test ./...` after
editing `configui` returns a cached pass for the whole pty tier.

## 6. Recommended sequencing

The worklist is ~40 items and several are order-dependent.

1. **§4.4 (E+B) first**, with the inline-table mutation test written before the
   fix. Highest severity, riskiest file, wants the freshest attention -- and
   §4.8's wording depends on it.
2. **§4.1 escaping funnel + arm.** Mechanically verified, self-contained, and the
   arm's shape is reusable for the P0 guard later.
3. **§4.11 mechanical tier.** Cheap, and `TestFieldInfosCoverEveryField` is an
   arm failing open today.
4. **Reopened items** -- `[[volumes]]` (§4.7), and the `resolveIdentity`
   garbage-output case (§4.3).
5. **§4.6 version semantics**, before §4.9, which needs the policy to exist.
6. **All doctrine edits as one unit, last.** P4 (§4.1), P0 (§4.7), P6 (§4.7,
   §4.8), ADR 0010's annotation (§4.5), ADR 0049's sentence (§4.6), RELEASING
   (§4.6, §4.10). **Do not do these piecemeal.** Written separately they will
   contradict each other -- which is not speculative: it is what the review found
   had already happened between ADR 0010 and 0050, and between 0018 and
   0030/0033/0039, and it happened again *during* this session (the new P0 clause
   did not license the volumes bullet it was drafted for, and the P6 scope note
   collided with P6's headline without either decision noticing).
7. Remaining documentation, release and test items.

## 7. Process notes worth keeping

- **Two whole-repo reviewers produced disjoint sets.** Budget for more than one
  pass, and expect a single pass to miss most of what is there.
- **The doctrine index works.** All 48 `[arm: ...]` markers resolve to real
  tests; the ADR file set and the index entry set are exactly equal in both
  directions; the config reference is pinned in both directions so a new key
  cannot ship undocumented; the commands page cannot go stale by construction.
  Two reviewers verified this independently and both remarked on it.
- **Git history answers "decision or oversight?"** better than the tree does, and
  the blackout that made the review honest also made that question unanswerable.
  Consult it during triage, not during review.
- **Recommendations formed before reading the primary source flipped on the
  first follow-up question** -- three of four, in this session. The reviewer
  reports elide the load-bearing detail (a comment that already states the
  threat, a doc comment that already enumerates the case as a decision, an
  indexer's actual shape). Read the code, then recommend.
- **The adversarial pass over the decisions was worth more than any single
  review.** It found three errors, two of them sentences headed for permanent
  homes -- a test rationale and a code comment -- both false as written. Commission
  that pass explicitly against the *decisions*, and brief it that unanimous
  approval is a failed result.
