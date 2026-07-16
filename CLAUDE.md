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
**Design principles live in `docs/PRINCIPLES.md`** (standing commitments);
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

> The `moarcode/` dir is the **legacy bootstrap harness** (gitignored, not part
> of byre) used to develop byre before it could host itself. If you are running
> inside the moarcode container, follow `moarcode/CLAUDE.md`. It is being retired
> in favor of the self-hosted box above.

## Workflow

- **Autonomy.** Keep going through the work; don't stop to ask "should I
  continue?" after each step. Stop only when genuinely blocked.
- **Commit frequently** — after each coherent unit (a function + tests, a fix, a
  green refactor). Small, well-described commits.
- **Code review (mandatory after a feature/fix).** Run `byre-codereview`
  yourself, read every finding, fix or consciously defer each, then re-run with
  `byre-codereview --continue "..."`. Stop when clean. (In the legacy moarcode
  box the equivalent is `moarcode/codereview.sh`.)
- **Green before commit:** `gofmt` + `go vet` + `go test ./...` clean.
- **Docs sweep (part of shipping, not a follow-up).** When a change alters
  behavior a settled doc describes, update the doc in the same unit of
  work: does README / ARCHITECTURE / GLOSSARY still state the pre-change
  behavior in the present tense ("today this is manual", "planned")?
  Stale shipped-over prose is the docs' main rot vector; RELEASING.md's
  release-time sweep is the backstop, not the mechanism.

## Tech Stack

- **Go 1.22+**, single static binary. Module `github.com/pjlsergeant/byre`
  (full path so `go install .../cmd/byre@latest` resolves).
- CLI: `spf13/cobra` command tree in `cmd/byre` (ADR 0022). The `app` struct
  seam keeps flag->function wiring test-pinned; the exit-code contract
  (usage errors = 2) is byre's, preserved deliberately around cobra.
  Dependencies are added on demonstrated merit, not collected.
- TOML config via `github.com/BurntSushi/toml` (byre's own merge/`!name` layer).
- Container engine: shells out to the `docker`/`podman` **CLI** (no SDK).
- Layout: `cmd/byre`, `internal/{project,config,gen,build,runner,skills,
  packages,builtins,onboard,commands,deliver,lock,configui,version}`.

## Coding Conventions

- Standard Go style; `gofmt` + `go vet` clean before every commit.
- Unit tests per package; Docker-touching logic is tested via injected runner
  interfaces (fakes). Gated integration tests (`BYRE_DOCKER_TESTS=1`) run
  host-side.
- Determinism matters in `internal/gen` (byte-stable Dockerfile output; a golden
  test pins it).
- Keep core opinion-free: opinions live in skills. The agent is a skill.
