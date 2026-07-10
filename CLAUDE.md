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

**byre runs on the host** (where Docker is). The dev *container* is where the
agent writes Go, runs `go build`, and runs unit tests; the actual `byre develop`
/ integration runs happen host-side.

## Dev environment (self-hosted)

byre develops itself. `byre develop` in this repo (see `byre.config`) builds a
**Go + Claude** box with these skills:

- **codex** — installs the `codex` binary (the independent reviewer; not launched
  as the agent). Authenticate once per box with `codex login`.
- **codereview** — ships **`byre-codereview`** (on `PATH`) and the review-loop
  conventions.
- **devloop** — the dev-workflow conventions (diary, commit discipline, the
  scratch volume). Both skills' conventions are placed in the box as agent
  memory, so the workflow rules below are reinforced automatically.

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

## Tech Stack

- **Go 1.22+**, single static binary. Module `github.com/pjlsergeant/byre`
  (full path so `go install .../cmd/byre@latest` resolves).
- CLI: hand-rolled per-command arg loops + manual subcommand dispatch (no
  `flag` package; minimal deps).
- TOML config via `github.com/BurntSushi/toml` (byre's own merge/`!name` layer).
- Container engine: shells out to the `docker`/`podman` **CLI** (no SDK).
- Layout: `cmd/byre`, `internal/{project,config,gen,build,runner,skills,
  builtins,onboard,commands,lock,configui}`.

## Coding Conventions

- Standard Go style; `gofmt` + `go vet` clean before every commit.
- Unit tests per package; Docker-touching logic is tested via injected runner
  interfaces (fakes). Gated integration tests (`BYRE_DOCKER_TESTS=1`) run
  host-side.
- Determinism matters in `internal/gen` (byte-stable Dockerfile output; a golden
  test pins it).
- Keep core opinion-free: opinions live in skills. The agent is a skill.
