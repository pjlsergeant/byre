# Contributing to byre

byre is a young, single-maintainer project that I use all day, every
day. Issues and PRs are welcome; response times are honest rather than
instant. This file is the map of how the repo actually works -- written
for humans and for their coding agents alike, because agents read this
repo as a first-class audience.

## Where truth lives

The repo has an unusual documentation discipline, and it is load-bearing:

- **`TODO.md` is authoritative.** It is the single source of truth for
  what is open and what was consciously dropped. When another document
  disagrees with it about status or scope, `TODO.md` wins. Finished and
  dropped items are removed, not struck through -- git history is the
  archive. Please don't send PRs that restructure it.
- **`docs/GLOSSARY.md` is binding vocabulary.** Prose, docs, and
  user-facing strings use its terms; when two docs disagree on naming,
  one of them is wrong. It governs vocabulary only, never behavior.
- **`docs/PRINCIPLES.md` holds standing commitments;
  `docs/adr/` holds point-in-time decisions** that cite them. The
  litmus: could it be "superseded by ADR-NNNN"? It's an ADR. Would
  changing it re-litigate the project? It's a principle.
- **`docs/` is for settled references only** (the ALL-CAPS files).
  Anything with a lifecycle -- designs in flight, research drafts --
  lives in `wip/` at the repo root and is deleted when the work ships;
  its durable content is absorbed into an ADR or a settled doc first.
- **The site is a doc surface too.** `site/content/docs/` (published at
  getbyre.com) is the canonical home of operational documentation; the
  README carries conversion summaries. If your change alters behavior a
  settled doc or site page describes, update it in the same PR -- stale
  present-tense prose is the docs' main rot vector.

## The bar for changes

- **Green before commit:** `gofmt`, `go vet`, and `go test ./...` clean.
- Some enforcement is mechanical: the generated Dockerfile output is
  golden-tested, the site's commands page is pinned to the cobra tree
  (regenerate with `go run ./cmd/byre commands-page >
  site/content/docs/commands.md`), and the README's "How do I...?"
  tldrs are pinned verbatim against the site cookbook.
- Docker-touching logic is tested through injected runner fakes; the
  gated integration suite (`BYRE_DOCKER_TESTS=1`) runs where an engine
  is. `docs/BYRE-DEVELOPMENT.md` describes the dev environment,
  including how byre develops itself in its own box.
- Dependencies are added on demonstrated merit, not collected.
- Keep the core opinion-free: opinions live in skills. If your feature
  is an opinion about how a box should behave, it is probably a skill.

## Security

The threat model and the sharp facts live at
<https://getbyre.com/docs/security-model/>. Report security issues via
GitHub security advisories on `pjlsergeant/byre` (see
`docs/SECURITY.md`). Two classes of report to save you time: byre
deliberately does not protect the user from themselves (the box is
locked against the *agent*), and `--self-edit` deliberately hands the
agent the keys -- both are documented decisions, not bugs.
