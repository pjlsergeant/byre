# Contributing to byre

byre is a young, single-maintainer project that I use all day, every
day. Issues and ideas are welcome; response times are honest rather
than instant. Two things make this repo unusual, and both shape how to
contribute well: it is documented deeply enough that an agent can
answer most questions about it, and its changes move through written
design (the ADRs) rather than drive-by patches.

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

`docs/BYRE-DEVELOPMENT.md` describes the dev environment, including how
byre develops itself in its own box.
