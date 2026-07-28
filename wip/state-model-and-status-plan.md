# Ratified plan: the launch record, the status overhaul, and four smaller items

**Status: RATIFIED, UNIMPLEMENTED.** Every decision below was settled with Pete
in a grilling session on 2026-07-28 (immediately after the 82-commit review
worklist landed). Nothing here is speculative: where a claim about the code
appears, it was verified at HEAD that day -- see
`wip/state-model-launch-record.md` for the four external-review findings with
their file:line evidence, which this plan is the answer to.

Delete this file when the work ships (absorbed into ADRs + docs).

---

## 0. What this covers

Seven pieces of work. Sequencing at the end -- it matters, because two items
depend on the launch record existing.

| # | Item | Depends on |
|---|---|---|
| A | Save/launch ordering: re-resolve under the lock | -- |
| B | Host exec pinning (the urgent security item) | -- |
| C | `skill.go` escaping funnel | -- |
| D | Deliver porcelain audit | -- |
| E | Status overhaul: three tiers + the mess in the current output | -- |
| F | The launch record + status Running/Next-launch split | A, E |
| G | Volume `sharing = "exclusive"` + enforcement | F |
| H | Editor Reserved-env row (skews-which-claims note) | -- |

Plus one out-of-tree change: **`BYRE_SCRATCH` -> prefix-free rename** in
`/home/dev/pjlsergeant-byre-skills` (mounted). Exactly two sites:
`skills/devlog/skill.toml:15`, `skills/devlog/context.md:12`. Carries the
published-package tail: version bump, repack, new digest, and the `[sources]`
pin in this repo's `byre.preset`. Same shape as the `INTTEST_*` rename that
already happened once. Pete's call on timing; it is the *cause* of the
nonsense `Reserved env` row in the pasted status output.

---

## A. Save/launch ordering -- re-resolve under the lock

**The bug (verified).** `develop` resolves the whole config cascade and the
skill set BEFORE taking the project setup lock (resolve at `develop.go:86`,
lock at `:213` -- the code's own comment names the ordering); the only
post-lock revalidation is `requireRecorded`, a forget-fence. The editor's save
takes the same lock. So: develop reads a config granting `~/secrets`, waits for
the lock the editor holds; you delete the mount and save (succeeds); develop
acquires the lock and builds from its **stale in-memory resolution** -- the
revoked grant launches, silently. Same shape in `rebuild` and
`ensureProjectImage` (worktree).

**Decision.** Move the authoritative read inside the lock: after acquiring it,
re-run the same resolve, and use THAT resolution for everything the lock
guards (generate, build, seed, create). The pre-lock resolve survives only for
what genuinely precedes the lock (onboarding prompts, engine detection).

**Rejected: generation counters / compare-and-retry** (the external reviewer's
suggestion). Counters earn their keep when re-reading is expensive or when
drift must be detected without re-reading. Here the re-read is milliseconds and
engine-free, and it doesn't merely *detect* the drift -- it resolves it by
construction. A counter would be a second identity to keep honest.

**Accepted consequence:** something printed before the lock could differ from
what launches if a save lands in the gap. That is the honest direction --
the launch banner prints from the post-lock resolution, so the banner always
describes what actually launched.

---

## B. Host exec pinning -- the urgent item

**The bug (verified).** `runner.Detect` calls `LookPath` and DISCARDS the
absolute path, keeping the bare name; all ~20 engine invocations re-resolve
`"docker"`/`"podman"` through PATH per call, and `gitprobe.go:27` runs bare
`"git"`. Nothing pins a resolved path or checks it against agent-writable
roots. On a host whose PATH holds an ABSOLUTE entry inside an agent-writable
dir (direnv `.venv/bin`, a project `.bin`) ahead of the real binary, the agent
plants `git`/`docker` and byre executes it host-side, as the user,
**automatically** -- the exit report's own probes fire at every session end.
Go's `ErrDot` covers relative entries only, so the vector needs an absolute
one. ADR 0047 covers the project tree as an execution channel with THE HUMAN as
trigger; byre as the AUTOMATIC trigger is uncovered doctrine -- and 0047's own
probes are such a trigger.

**Decisions.**

1. **Scope: one resolver for every host-side spawn.** The engine and `git` (the
   verified automatic triggers), plus `ssh` (deliver transport), the editor
   spawn, and the clipboard tools. Same helper, one line each -- no reason to
   leave user-invoked spawns on the old path once it exists.
2. **Pin once per process.** Resolve the absolute path at first use per binary
   and reuse it for the process lifetime; the runner stores the absolute engine
   path instead of the bare name. PATH is read once per byre invocation.
3. **Refusal rule (the doctrine fork, resolved):** the helper refuses a binary
   whose resolved path lies under a known agent-writable root for this project
   -- the project tree, its worktrees, the common git dir, and (under
   `--self-edit`) the mounted store. Everything else pins silently and runs.
   This is NOT path-nannying (parked: "a knife needs to be sharp"): byre never
   judges the user's PATH, only whether the AGENT can author the binary byre
   executes -- exactly TODO.md's recorded confused-deputy carve-out ("byre must
   never let agent-writable state amplify into host actions beyond the grant").
4. **Per-caller disposition:** `develop` finding its ENGINE under an
   agent-writable root is a hard named refusal (nothing safe can proceed). The
   EXIT REPORT finding `git` there DEGRADES -- skips the probes, prints one
   disclosure line -- because a session end must never be blockable by the
   thing it reports on.
5. **Explicitly not doing:** checksums, signature checks, or any judgement of
   binary CONTENT. Resolution locus is the whole test.

---

## C. `skill.go` escaping funnel

`internal/commands/skill.go` still escapes at each of ~51 print sites, while
Unit 10 funneled `install.go`/`layer.go`/`preset.go` through `dataf` (escape
per argument, one place). Correct today; one forgotten line from a gap.

**Decision:** mechanical conversion to `dataf`/`escaped()`, removing the
per-site `EscapeTerminal` calls, extending the existing per-surface arms. No
design content. (`dataf` and the `escaped(string)` named-exemption type live in
`internal/commands/escape.go`.)

---

## D. Deliver porcelain audit

deliver/grab print machine-readable output on **stdout** that scripts parse; it
was deliberately excluded from terminal-escaping, with landed filenames
sanitized where the claims are made (`sanitizeBase`). Unit 10's open question:
has the whole stdout stream been audited for other externally-authored strings?
No hole is known.

**Decision:** run it as a read-only verification task -- every stdout write
site in `internal/deliver`, classifying each interpolated string by author and
sanitization -- and **record the result either way**: clean becomes a package
comment stating the porcelain contract and who sanitizes what; a hole becomes a
fix. No machinery beyond that.

---

## E. Status overhaul -- three tiers, and the mess

Pete pasted real `byre status` output; it is a mess three separate ways.

**Class 1 -- mechanical, no wrapping discipline.** `row()` prints
`label + value` and lets the TERMINAL wrap, so `Host env:`, `Reserved env:` and
`Raw build:` continuations land at column 0 and shred the two-column layout;
the Skills provenance column is a fixed width that
`pjlsergeant/claude-skills-pocock` overflows; the `Instructions:` delivery note
wraps mid-parenthetical. **Fix:** width-aware wrapping in the row funnel itself
-- hanging indent to the value column, break at separators not mid-token,
name-column width computed from the longest name. This one change fixes five of
the visible messes.

**Class 2 -- content noise.** The disclosure arrows have accreted (`-> the
agent session receives:`, `-> the agent command injects (...)`, the
preset-differs sentence). Each was added honestly; the sum buries the exposure
rows the page exists for. An editing problem, not a mechanism problem.

**Class 3 -- substance: the `Reserved env` row cries wolf.** The pasted example
reads `⚠ pjlsergeant/devlog sets BYRE_SCRATCH -- byre runtime control; the
network + launch claim(s) above ride it`. But `BYRE_SCRATCH` is the devlog
skill's OWN variable naming its scratch volume; it controls nothing of byre's.
It squats the reserved prefix, so ADR 0050's conservative unknown-key default
maps it to network+launch and the row announces byre-runtime-control for a
scratch path -- on a box whose network is `open` and asserts nothing
degradable. Doctrinally correct, practically nonsense. **Two fixes:** (3a) the
UNKNOWN-key wording must stop claiming knowledge byre lacks -- "sets
BYRE_SCRATCH -- not a control this byre recognizes; treated cautiously" rather
than "byre runtime control"; (3b) the `BYRE_SCRATCH` rename, out of tree (see
§0).

**The three tiers (ratified).**

- **default** -- pretty-printed, values cut down sensibly.
- **`--full`** -- everything, no truncation.
- **`--data`** -- the same information as `--full`, as JSON.

**The honesty rule for the default tier (the fork, resolved):** the default
tier may TRUNCATE VALUES and COLLAPSE NOTES, but **never elides a row's
existence**. If a grant, mount, volume, skill, port, or reserved-env key
exists, the default view shows that it exists. Concretely:

- Truncate with a count and a pointer: `Raw build: 1 line (--full to show)`;
  `Host env: 6 keys from host (--full for sources)`; skills show
  name+provenance, digests under `--full`; `Instructions: 2 blocks` with no
  text preview.
- Collapse the delivery arrows into the row they qualify (`MCP servers:
  agentblocks (config) -- delivered`), full mechanism sentence under `--full`.
- **Never collapse a claim degradation or a containment disclosure.** The
  Reserved env warning, containment rows and any claim hedge stay at full
  strength in the default view -- they are the point, and they are short.
- Every truncation is VISIBLE as a truncation (count or ellipsis, never silent
  absence), so the default view is a summary of everything, never a subset.

**`--data`:** same content as `--full`, carries a `version` field,
**versioned-but-not-frozen** to start (changes noted in CHANGES). Nothing
consumes it yet; `deliver/proto.go`'s FROZEN + `ProtoVersion` is the precedent
for when it gains an external consumer, and it becomes frozen the moment it is
documented as scriptable.

**Scope decision:** classes 1, 2, 3a and the three tiers land as ONE status
unit, so the renderer is touched once rather than twice, and F's new rendering
arrives into a clean page.

---

## F. The launch record + the status Running/Next-launch split

**The gap (verified).** `Status` resolves the CURRENT config/skills/host-env and
renders those; its only engine queries are liveness (`RunningContainersByLabel`)
and identity labels (`ContainerLabels`, orphan detection). It never inspects the
running container's mounts, ports, env or network mode, and NOTHING queryable
survives a launch -- container labels carry identity only. Edit the config while
a box runs and status shows the safer next-launch world above a container still
holding the old mounts and open network. The develop banner IS an accurate
launch-time record but is transient stderr.

**Decisions.**

1. **Content: record what byre TOLD THE ENGINE, not the config that produced
   it.** Exposure-level facts as they went out the door: bind mounts, ports,
   volumes, env KEYS (never values -- the exit-report rule), network posture,
   resolved egress string, the reserved-env set (attributed strings), image tag
   AND digest, base image, `run_args` verbatim, and the skill set with
   identities (name, provenance, version/digest). Explicitly NOT the whole
   resolved `config.Config`: that is a second serialization of a moving schema
   and it invites re-deriving exposure from config. The record should be one
   step CLOSER to reality than config, not a copy of it. Most of this is
   already computed at launch (runParams + the Exposure inputs) -- the work is
   capturing, not deriving.
2. **Storage: a store file named by its own hash, pointed at by a container
   label.** Written under the setup lock from the post-lock resolution (§A) to
   `~/.byre/projects/<id>/launches/<hash>.toml`; container created with
   `byre.launch=<hash>`. Content-addressing gives integrity free -- status
   re-hashes what it reads and a mismatch is disclosed, not trusted. **Honest
   caveat for the ADR:** under `--self-edit` the store is agent-writable, so
   the record is forgeable there; that sits inside the existing self-edit trust
   ruling and its confused-deputy carve-out (the record only ever INFORMS,
   never triggers host action) -- but it must be said.
3. **Lifecycle:** written at create, read by status, reaped with the container
   (opportunistic, the `reapStaleEmbedRoots` pattern). A MISSING record (any
   container launched by an older byre, or a hand-deleted file) degrades
   honestly -- status says the box predates launch records and qualifies the
   rows; never guesses. The record carries a schema version; an older byre
   reading a newer record renders liveness only (the `index.toml` lenient-decode
   precedent).
4. **No generation counter** -- the content hash IS the identity, and §A closed
   the race a counter would have served.
5. An **ADR** rides the implementation (point-in-time architecture, cites P4).

**Illustrative serialization** (hand-written, reviewed and accepted by Pete;
three things it surfaced are folded into the content decision above -- `run_args`
belongs verbatim; the image digest is the quiet win, since `byre rebuild` moves
the tag but the record pins what the running box was BUILT from; `reserved_env`
is attributed strings, not a map):

```toml
# byre launch record -- written under the setup lock at container create,
# from the same resolution that fed the engine. Addressed by the sha256 of
# these bytes; the container carries byre.launch=9f3ab2c41d70. Records what
# byre TOLD the engine -- env KEYS only, values never.
record = 1
byre = "v1.4.0"
created = 2026-07-28T21:40:11Z
project = "002-byre-4f21bc"
workdir = "/home/pete/byre"
engine = "docker"

[image]
tag = "byre-002-byre-4f21bc"
digest = "sha256:9f1c8d2e..."      # what actually ran, whatever the tag says now
base = "golang:1.26-bookworm"

[network]
posture = "deny-by-default"        # or "open"; absent if no posture skill
egress = "api.anthropic.com:443,github.com:443"
reserved_env = ["skill:knobs BYRE_LAUNCH_GATE_FILE"]

[[binds]]
host = "/home/pete/byre"
target = "/workspace"
mode = "rw"

[[binds]]
host = "/home/pete/secrets"        # the row that makes the feature worth it:
target = "/secrets"                # still here after you deleted it from config
mode = "ro"

[[ports]]
interface = "127.0.0.1"
host = 15432
container = 5432

[[volumes]]
name = "byre-002-byre-4f21bc-claude-state"
target = "/home/dev/.claude"
scope = "project"

env_keys = ["GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "NGROK_AUTHTOKEN"]

run_args = ["-e", "INTTEST_VM=172.17.0.1"]   # raw tier, verbatim
```

**No hashing of values** (considered, rejected): everything in the record is
non-secret by construction (env KEYS only, user-configured paths/ports, skill
identities, an image digest), and hashing would destroy the record's entire
purpose -- a hashed bind row cannot tell you `/secrets` is still mounted. The
one place a secret could lurk is `run_args` verbatim, which status already
prints verbatim today and the configuration reference already warns is not for
secrets. No new exposure.

**The status split (Pete's ruling -- the running box is ALWAYS the subject when
one exists):**

- **Box running:** the exposure block renders FROM THE RECORD and says so
  (e.g. `Container: running (9f3ab2c4) -- rows describe this box`). If the
  current config differs, a **`Changes on next launch:`** section follows with
  the delta rows in the existing row vocabulary (`- Bind /home/pete/secrets ->
  /secrets (ro)`, `+ network deny-by-default`). If it does not differ, that
  section simply does not appear (absence reads fine; no "unchanged" line).
  NOT a full second block -- doubling the screen for two changed rows buries
  the signal.
- **No box:** today's semantics -- the rows ARE the next launch, no relabeling.
- **Box running, no record:** today's render plus one qualifier line. Degrade,
  never guess.
- The diff is a row-by-row compare of record fields against the values status
  already computes -- no new resolution, one new compare.

**The config editor (both accepted):**

- **At OPEN:** a headline qualifier (`exposure: ... · box running -- changes
  apply at next launch`), using status's own unsolicited-liveness-probe +
  quiet-degrade discipline -- precedented, not new policy. Staleness is
  harmless: if the box exits mid-session, "changes apply at next launch" stays
  TRUE; only the "box running" half goes stale, in the harmless direction.
- **At SAVE:** the report line gains the clause when true -- `byre: config
  written -- a box is running; changes apply at the next develop.` Fresh at the
  moment it is actionable (you just revoked something and it has not happened
  yet). Engine unreachable degrades to the plain message silently.
- The editor's exposure headline keeps NEXT-LAUNCH semantics throughout (it
  describes the config being edited -- Unit 12 made that claim precise); the
  qualifier labels it rather than re-scoping it.
- The **develop banner stays as-is** -- it already describes the launching box
  accurately; it is the record's SOURCE, not a consumer.

---

## G. Volume concurrency vocabulary

**The gap (verified).** Worktree boxes are DESIGNED to run concurrently and
volume names are project-scoped (`byre-<id>-<name>`, `naming.go`), so
concurrent worktree boxes mount the IDENTICAL volume set -- ARCHITECTURE
presents that as the feature. ADR 0009 justified it by rejecting a creds/history
split as "unnecessary: agents already handle concurrent access to one state dir"
-- a rationale specific to AGENT STATE DIRECTORIES. But volumes are now generic
grammar: skills contribute arbitrary `[[volumes]]`, config declares them, the
editor writes them (as of 2026-07-28). `Volume` carries
Name/Role/Target/Seed/Scope, and `Scope` only WIDENS sharing. Nothing lets an
author say "single-writer SQLite" or "must not be shared".

**Decisions.**

1. **`sharing = "shared" | "exclusive"`, default `shared`** -- today's
   behavior for every existing skill and config, and the honest default since
   0009's reasoning does hold for the agent-state case it was written about. A
   WORD not a bool, so a future third value (`per-worktree`) has somewhere to
   land; explicitly not building that now.
2. **Enforcement: a named refusal at develop, riding the launch record.** When
   develop would mount an `exclusive` volume, it checks running sibling boxes
   on the project and reads THEIR LAUNCH RECORDS for which volumes they
   actually mounted -- not their current config, which is exactly the guess the
   record abolishes. A live sibling holding the same exclusive volume gets a
   refusal in the session-already-live family (exit 3, naming the volume and the
   worktree holding it). This BLOCKS, deliberately: degrade-never-block governs
   byre's claims ABOUT ITSELF, whereas this is byre honoring a volume's own
   declared contract against corruption -- same family as the
   one-session-per-workdir refusal and ADR 0004's cross-engine refusal, both of
   which already block.
3. **Surfaces + doctrine:** the volumes widget gains the field (P0 -- a new key
   ships with its row); status's volume rows and Worktrees line disclose
   `exclusive`; GLOSSARY and SKILLS.md define it; an ADR records the decision
   AND **amends ADR 0009's rationale** so the "agents tolerate shared state
   dirs" sentence is scoped to what it was about -- closing the drift the
   reviewer found. Nothing bundled changes on day one.

---

## H. Editor Reserved-env row

`byre status` gives a skill's `BYRE_` key a dedicated attributed row naming the
key AND which claims it skews. The editor now degrades its exposure headline
(Unit 12) and its Env screen shows the key attributed to the skill -- but no
editor surface says WHAT the key skews.

**Decision:** build it -- a `skews: network` style annotation on the existing
attributed Env row, reusing `skills.ReservedEnvClaims` (the single owner Unit 12
created), NOT a new screen. Needs a new `listRow` field plus paint support in
`listitem.go`. P0: a TUI claim gap ranks with an engine one; the claim
vocabulary now has one home, so the note cannot drift. Fold 3a's wording fix
(§E) in here or in the status unit -- same vocabulary, either is fine.

---

## Sequencing

1. **B (host exec pinning)** -- first, independent, and the one item with a
   live security consequence.
2. **A (re-resolve under the lock)** -- independent, small, and F's record must
   be written from the post-lock resolution.
3. **E (status overhaul: tiers + wrapping + noise + 3a wording)** -- so F's new
   rendering lands on a clean page.
4. **F (launch record + Running/Next-launch split + editor qualifiers)** -- the
   centerpiece; ADR.
5. **G (volume `exclusive`)** -- reads F's records; ADR + 0009 amendment.
6. **C, D, H** -- independent, any time; H shares vocabulary with E's 3a.
7. **The `BYRE_SCRATCH` rename** in `/home/dev/pjlsergeant-byre-skills`
   whenever Pete wants the published-package tail (bump, repack, digest,
   `byre.preset` pin).

## Process (unchanged from the session that just shipped 82 commits)

Opus subagents implement one unit at a time; the orchestrator owns the
two-reviewer loop (codex + grok, concurrent, unbriefed, each required to
produce a `Doctrine:` line against `docs/adr/README.md`) and routes findings
back; subagents never run `byre-codereview` or `byre-inttest`. Green before
every commit (`gofmt`, `go vet`, `go test ./...`); every bug fix red-verified
against real pre-fix code (`git show <commit>^:<file>`, not `git stash`);
rejection tests name the rule that fired; no commit trailers; `git add` only
touched files; nothing pushed. One `-count=1` gated runner pass at the end
(`byre-inttest`), never piped. TUI work cannot run in this box (no tmux) --
the runner has tmux and sets `BYRE_TUI_TESTS`.
