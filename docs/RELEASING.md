# Releasing byre

byre ships as a single static binary with skills and templates embedded.
The dev-box *container images* are never part of a release -- every host
bakes its own (they carry the host's UID, ADR-0008) -- so a release is
just cross-compiled binaries on a GitHub Release. The pipeline is goreleaser, run by
`.github/workflows/release.yml` on every `v*` tag push (ADR-0016).

## Cutting a release

```sh
git checkout main && git pull
go test ./...                # the workflow re-runs this, but fail fast
$EDITOR CHANGES.md           # date the release's entry (see below)
git tag v0.1.0
git push origin v0.1.0
```

`CHANGES.md` is the hand-curated, user-facing history; the GitHub
Release changelog is commit-derived and noisier. Before tagging, turn
the top `unreleased` heading into the tag's version + date (and start
the next `unreleased` section when work resumes).

While editing `CHANGES.md`, sweep the settled docs for claims this
release obsoletes: for each entry, does README / ARCHITECTURE / GLOSSARY
/ the site (`site/content/` -- the canonical operational docs) still
describe the pre-change behavior in the present tense? Shipped
features leave stale "today this is manual" / "planned" prose behind
them -- that drift is the docs' main rot vector (the 2026-07-16 audit
found seven such claims, all left by ship waves days earlier).

Before tagging, run the engine-side gate: the WHOLE suite on the inttest
runner, every gated tier the wrapper sets (Docker, ssh-loop, TUI). The
agent-contract tier (`BYRE_AGENT_TESTS`) runs live agent installers on its
own schedule and is not part of this gate, so its tests are the expected
skips. Pass explicit args, so the wrapper's no-arg default (`-run
Integration`) does not apply, and no `-run` filter. Redirect, never pipe: a
pipe takes the exit status and a red suite reports success.

```sh
byre-inttest ./... -count=1 -v > ~/scratch/release-gate.log 2>&1; echo "exit $?"
```

Then read the log: zero `--- FAIL`, no package reporting
`[no tests to run]`, and every `--- SKIP` reviewed -- a skip is a test that
did not run, so zero is the target and each one left needs a reason. The
filter is banned because the v1.12.0 gate ran with `-run Integration`: every
unit package reported `[no tests to run]`, and "0 SKIP" could not see tests
that never ran -- 12 runner-only failures hid behind it (fixed in de900284).

Also before tagging: run the field-QA playbook against the release
candidate -- the journey recipes in `docs/QA-PLAYBOOK.md`, driven on the
sacrificial inttest VM. Report-only, NEVER a gate: findings go to TODO.md
(never fixed mid-pass) and harden into deterministic tuitest regression
tests afterwards.

That's it. The workflow runs the tests, then goreleaser cross-compiles
linux/darwin × amd64/arm64, writes checksummed `tar.gz` archives, and
publishes a GitHub Release with a changelog from the commit messages.

Version stamping: release binaries carry the tag via `-ldflags -X
github.com/pjlsergeant/byre/internal/version.Version`; `go install
...@vX.Y.Z` builds report the same
string from Go's module build info; other builds report what build info
recorded (a pseudo-version, or `(devel)` plus the VCS revision when there
is no version at all). `byre version` (or `byre --version`) prints it.

Dry-run the whole pipeline locally with:

```sh
goreleaser release --snapshot --clean   # artifacts land in dist/, nothing published
```

## What a version number promises

byre is **1.x**. The tag is a release ordinal with a stability
expectation, not a semver contract over every config key: a **minor
release may remove a live config key**, and byre's answer to the
breakage is loudness, not a window.

A live-key removal ships three things together, or it does not ship:

1. a `CHANGES.md` entry that names the key and the replacement;
2. a **refusal carrying the remedy** -- the key parses to an error whose
   text is the migration (`internal/config/config.go`'s removed-key map
   and `internal/skills`' equivalent are where those live), never a
   silent ignore or a bare "unknown key";
3. the whole apparatus gone with it -- parser field, editor row, docs,
   tests.

This is a judgment call per key, made at removal time: how much config
in the wild plausibly carries it, and how cheap the migration is. The
`npm_global` removal (2026-07-28, v1.x) is the **exceptional** case on
the record -- a key that named one ecosystem in byre's vocabulary,
removed in a minor with a `dockerfile_pre` remedy in the refusal. Treat
it as precedent for the mechanism, not as a licence to remove keys
casually.

*Compatibility paths* are the other thing and are governed differently:
they get the stated window in ADR 0049 (two minors or 90 days,
whichever is longer). A tolerated legacy spelling waits; a key byre
means to stop supporting outright gets judgment.

A user whom an upgrade breaks has one answer, and it must stay true:
`install.sh` takes `BYRE_VERSION` to pin a tag (documented on the
site's install page). Anything that removes a live key is one
`BYRE_VERSION=vX.Y.Z` away from being reversible.

## Install paths

All three are live; the README's Install section blesses Homebrew and
links the full set on the site's install page
(`site/content/docs/install.md`):

- **`go install github.com/pjlsergeant/byre/cmd/byre@latest`** — builds
  from the module proxy, no release involved.
- **`install.sh`** (`curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | sh`)
  — checksum-verified download of the latest release binary; no Go
  toolchain needed.
- **Homebrew** — goreleaser publishes a cask on every release
  (`brew install --cask pjlsergeant/tap/byre`; cask, not formula —
  goreleaser deprecated `brews` for pre-built binaries, and the cask
  strips the quarantine bit for the unsigned binary). Publishing rides
  the `HOMEBREW_TAP_GITHUB_TOKEN` Actions secret on `pjlsergeant/byre`
  (a fine-grained PAT, **Contents: read/write** on
  `pjlsergeant/homebrew-tap`); if the secret is ever absent the publish
  step is skipped rather than failing the release -- and the release run
  ends with a warning annotation and a run-summary note saying so, since
  the blessed install command would otherwise keep serving the previous
  version from a release that reported success.
