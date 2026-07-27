# CLAUDE.md

## Project Overview

**byre** is a small Go binary that runs an AI coding agent in a throwaway,
project-scoped container — a local-first, inspectable, Docker-native harness.
`cd ~/project && byre develop` generates a Dockerfile from a config cascade,
builds it (Docker/Podman), and runs the agent in a sandbox that sees only the
project and what you explicitly grant. Mechanics: `docs/ARCHITECTURE.md`. User
docs: `README.md`.

**Vocabulary is canonical in `docs/GLOSSARY.md`** (the domain-modeling skill's
`CONTEXT.md`, renamed -- treat it as that skill's glossary file). It is binding
for prose, docs, and user-facing strings; vocabulary only, never behavior. When
another doc disagrees with it on naming, one of them is wrong -- reconcile.
**The TUI is the gold (PRINCIPLES.md P0).** Without it byre is a fusty
one-of-many sandbox -- containerising an agent is table stakes; the screen
over it is the differentiator. So: a config key without a widget is a hole in
the product, not a deferred nicety; a TUI bug ranks with an engine bug; the
demo casts are product, not decoration; and "expert vocabulary, hand-edit it"
is not an answer byre gives.
**Design principles live in `docs/PRINCIPLES.md`** (standing commitments);
**doc/site placement rules in `docs/PLACEMENT.md`** (cited as `PLACEMENT.md Pn`);
**point-in-time decisions live in `docs/adr/`** and cite principles as
rationale. Litmus: could it be "superseded by ADR-NNNN"? ADR. Would changing
it re-litigate the project? Principle.

**`docs/` holds settled references only** -- ALL-CAPS files, plus `adr/`
(decision records) and `marketing/`. Anything with a lifecycle -- designs in
flight, research drafts, parked options -- lives in `wip/` at the repo root
and is DELETED when the work ships (absorbed into an ADR and/or the docs;
git history keeps it -- see `wip/README.md`). Never start a working document
in `docs/`.

**byre runs on the host** (where Docker is). The dev *container* is where the
agent writes Go, runs `go build`, and runs unit tests; the actual `byre develop`
/ integration runs happen host-side.

## Dev environment (self-hosted)

byre develops itself. `byre develop` in this repo (config applied from `byre.preset`) builds a
**Go + Claude** box with these skills:

- **codex** — installs the `codex` binary (the independent reviewer; not launched
  as the agent). Authenticate once per box with `codex login`.
- **pjlsergeant/codereview** — ships **`byre-codereview`** (on `PATH`) and the
  review-loop conventions.
- **pjlsergeant/devlog** — the dev-workflow conventions (diary, commit
  discipline, the scratch volume). Both skills' conventions are placed in the
  box as agent memory, so the workflow rules below are reinforced automatically.
- **pjlsergeant/inttest** — ships **`byre-inttest`** (on `PATH`): sync the tree
  to the sacrificial Lima VM and run the gated `BYRE_DOCKER_TESTS=1` suite
  there. Lives IN this repo (`skills/inttest/`); the VM template rides the
  package. The dev-environment mechanics — this skill, the `skills/` dir's
  packed-manifest edit loop, VM setup — are in `docs/BYRE-DEVELOPMENT.md`.

**One-shot bootstrap (fresh machine):** `codereview` and `devlog` moved out of
the byre binary (2026-07-13) into
[pjlsergeant-byre-skills](https://github.com/pjlsergeant/pjlsergeant-byre-skills).
On a fresh clone, `byre preset apply` here reviews this repo's `byre.preset`
and chauffeurs the installs (once per machine); the preset's `[sources]`
block pins their URIs and digests. A config that references the qualified ids
without the installs fails loudly at develop with those exact commands.
(`pjlsergeant/inttest` rides the same flow but installs from a path source —
this repo's own `skills/inttest/skill.toml` — so run the apply from the repo
root; see `docs/BYRE-DEVELOPMENT.md`.)

## Workflow

- **Autonomy.** Keep going through the work; don't stop to ask "should I
  continue?" after each step. Stop only when genuinely blocked.
- **Commit frequently** — after each coherent unit (a function + tests, a fix, a
  green refactor). Small, well-described commits.
- **Code review (mandatory after a feature/fix).** Run `byre-codereview`
  yourself, read every finding, fix or consciously defer each, then re-run with
  `byre-codereview --continue "..."`. Stop when clean.
- **Substantial work gets TWO reviewers, not one reviewer twice.** After the
  first reviewer is clean, run the other (`--reviewer grok` when codex went
  first, and the reverse) with an explicit "you are a second opinion, here
  is what the first already found" framing, so it hunts fresh ground instead
  of re-deriving. `--continue` deepens one perspective; it does not add
  another. Type specimen, 2026-07-28: codex ran five rounds on a batch and
  called it clean; grok then found three real defects it had never seen,
  two of them the sibling-bound class — including a concurrency hole that
  protected the first save and not the next one.
- **Every review checks the doctrine index.** Give every reviewer — the
  `byre-codereview` run and any hand-rolled subagent alike — this instruction
  verbatim as (part of) its focus: *"Check the change against the index in
  docs/adr/README.md: report which entries apply and whether the change
  complies, or state 'Doctrine: none apply'."* A review whose output has no
  Doctrine line has not done the check — send it back before reading findings.
- **Green before commit:** `gofmt` + `go vet` + `go test ./...` clean.
- **Docs sweep (part of shipping, not a follow-up).** When a change alters
  behavior a settled doc describes, update the doc in the same unit of
  work: does README / ARCHITECTURE / GLOSSARY still state the pre-change
  behavior in the present tense ("today this is manual", "planned")?
  Stale shipped-over prose is the docs' main rot vector; RELEASING.md's
  release-time sweep is the backstop, not the mechanism.

## Tech Stack

- **Go 1.25+**, single static binary. Module `github.com/pjlsergeant/byre`
  (full path so `go install .../cmd/byre@latest` resolves).
- CLI: `spf13/cobra` command tree in `cmd/byre` (ADR 0022). The `app` struct
  seam keeps flag->function wiring test-pinned; the exit-code contract
  (usage errors = 2) is byre's, preserved deliberately around cobra.
  Dependencies are added on demonstrated merit, not collected.
- TOML via `github.com/pelletier/go-toml/v2` (the ONE TOML library, ADR
  0044): strict decode plus the unstable parser under `internal/tomldoc`,
  byre's style-preserving document editor — every config-file write rides
  it. Merge/`!name` semantics are byre's own layer.
- Container engine: shells out to the `docker`/`podman` **CLI** (no SDK).
- Layout: `cmd/byre`, `internal/{project,config,gen,build,runner,skills,
  packages,builtins,onboard,commands,deliver,lock,configui,version,
  editorcmd,hostopen,tomldoc,tuitest}`.

## Coding Conventions

- Standard Go style; `gofmt` + `go vet` clean before every commit.
- **Plain `os` filesystem calls are BANNED outside `internal/hostopen`** —
  reads, writes and probes alike. Ask three questions of the path: can the
  agent author the STRING, control a component of the ROUTE, or replace the
  TARGET? Any yes and the call rides hostopen's real functions (O_NONBLOCK
  so nothing hangs, type judged from the descriptor, bounded reads,
  openat-anchored roots). All no, and you say so AT THE CALL SITE:
  `hostopen.PlainStat(p, hostopen.StoreOwned)`. The Reason is a closed set
  the compiler enforces and `rg` can audit; `hostopen.Unreviewed` is the
  honest marker when nobody has checked, and is the backlog. Three external
  reports found three misses in one day (2026-07-18) before this became a
  rule; a table of exemptions replaced it and rotted (a new call could ride
  an old entry), so the justification now lives with the code.
  Unsolicited probes (drift checks, env resolution) must still DEGRADE on
  refusal, never block — and subprocesses probing the project (git) get
  timeouts.
- Unit tests per package; Docker-touching logic is tested via injected runner
  interfaces (fakes). Gated integration tests (`BYRE_DOCKER_TESTS=1`) run
  host-side.
- **Tests pin contracts byte-exact; behavior they assert by rule, not
  prose.** Contracts (the gen Dockerfile golden, mcp.json, exit codes, the
  commands-page pin) stay byte-exact, commented as contracts. A rejection
  or output test asserts the RULE fired — a stable identifying fragment
  plus the offending value/remedy — never full sentences; `err == nil`
  alone under-asserts (the wrong rule keeps it green). **Two tiers**: a
  grammar, shape, or arity rejection MUST name the rule that fired, because
  a dozen rules can reject the same input and the wrong one keeps the test
  green. A *containment* refusal (an escaping symlink, a forged worktree
  pointer, a FIFO where a file was named) may assert the refusal plus the
  unchanged victim and stop there — refusal IS the contract, the message is
  incidental, and pinning it couples a security test to wording. Do not
  "improve" the containment tests by adding fragments. Prose asserted in
  3+ tests gets an exported const/func the product prints and tests
  reference (e.g. `onboard.SharedAuthPrompt`), so wording changes in one
  place — presence is the assertion, prose is not.
- Determinism matters in `internal/gen` (byte-stable Dockerfile output; a golden
  test pins it).
- Keep core opinion-free: opinions live in skills. The agent is a skill.
