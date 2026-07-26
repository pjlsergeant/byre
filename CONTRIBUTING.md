# Contributing to byre

byre is a young, single-maintainer project that I use all day, every
day. Issues and ideas are welcome; responses may take time. Two things
make this repo unusual, and both shape how to contribute well: it is
documented deeply enough that an agent can answer most questions about
it, and its changes move through written design (the ADRs) rather than
drive-by patches.

## Think you've found a bug?

**Please ask several agents to confirm it against the source before
filing.** The repo is built to be legible to coding agents -- point
yours at it, describe what you saw, and have it verify the behavior
against the actual code paths; a second agent's independent read is
cheap and catches most misdiagnoses. A report that arrives with "two
agents traced this to X" plus the legible artifacts --
`byre version`, `byre status`, the generated Dockerfile
(`byre dockerfile`) -- usually gets fixed fast. Security-sensitive
reports go via GitHub security advisories instead (`docs/SECURITY.md`).

## Want a feature?

**Pass in as much information about your use-case as humanly
possible.** What you were doing, what you expected, what surrounds the
gap -- the real workflow, not the abstracted request. byre's design
decisions are made against concrete use-cases (and recorded in
`docs/adr/`), so a rich description of your situation is worth far more
than a proposed API.

## Prefer descriptions over code

**In general, detailed descriptions of the changes you want are
preferred over actual code.** This repo has strong conventions --
binding vocabulary, settled principles, golden-pinned artifacts, docs
that must move in lockstep with behavior -- and a PR that doesn't ride
them costs more to absorb than a precise description of the intended
change. Small obvious fixes are the exception; for anything larger,
write the change down first and let's agree on the shape.

## How the repo works

The conventions, for humans and their agents alike:

- **`TODO.md` is authoritative** for what's open and what was
  consciously dropped; git history is the archive. Don't restructure it.
- **`docs/GLOSSARY.md` is binding vocabulary**; `docs/PRINCIPLES.md`
  holds standing commitments; `docs/adr/` holds the point-in-time
  decisions that cite them.
- **`docs/` is settled reference only**; work-in-flight lives in `wip/`
  and is deleted when absorbed.
- **The site is a doc surface**: `site/content/docs/` (getbyre.com) is
  the canonical operational documentation; the README carries
  conversion summaries. Behavior changes update the describing doc in
  the same unit of work.
- **Green before commit**: `gofmt`, `go vet`, `go test ./...`. Some
  enforcement is mechanical -- the generated Dockerfile is
  golden-tested, the site's commands page is pinned to the cobra tree,
  the README's tldrs are pinned verbatim against the cookbook.
- **Keep the core opinion-free**: opinions live in skills. If your
  feature is an opinion about how a box should behave, it is probably
  a skill. Dependencies are added on demonstrated merit.

Settled positions a reviewer will be tempted to flag -- each is
deliberate, with its reason on file. These aren't taboo, they're
pre-litigated: if you want to re-open one, steelman it first -- state
the current position's strongest case in your own words, then say
precisely where it fails. An objection that clears that bar gets
engaged on the merits; one that doesn't was already answered by the
reason each entry carries:

- **`internal/commands` is one flat package.** It is a thin adapter
  layer whose private substrate refactors freely BECAUSE it is
  unexported; the package comment carries the reasoning.
- **Doc comments that look like they restate the signature mostly
  don't.** Measured, not assumed: nearly all carry a constraint in the
  redundant-looking words ("a DEEP copy", "in enable order"); the
  survivors are Go's exported-symbol convention.
- **`docs/AGENT-CREDENTIAL-MECHANICS.md` keeps its empirical correction
  log.** Hard-won field records, corrections in place; it is a lab
  notebook by design, not an unedited draft.
- **Per-package test helpers are duplicated on purpose.** Small
  unexported helpers beat a shared test-util package.
- **Feature scale-backs proposed by complexity reviews were adjudicated
  and rejected.** The package manager, typed skill fields, and the
  legibility rows stay; ADR 0048 records the rulings and the guardrails
  that replaced the cuts.
- **Long cobra `Long` texts are not bloat.** They render the site's
  commands page via a byte pin -- one source, two surfaces; trimming
  the help trims the docs.

`docs/BYRE-DEVELOPMENT.md` describes the dev environment, including how
byre develops itself in its own box.
