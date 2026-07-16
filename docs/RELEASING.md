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

That's it. The workflow runs the tests, then goreleaser cross-compiles
linux/darwin × amd64/arm64, writes checksummed `tar.gz` archives, and
publishes a GitHub Release with a changelog from the commit messages.

Version stamping: release binaries carry the tag via
`-ldflags -X main.version`; `go install ...@vX.Y.Z` builds report the same
string from Go's module build info; other builds report what build info
recorded (a pseudo-version, or `(devel)` plus the VCS revision when there
is no version at all). `byre version` (or `byre --version`) prints it.

Dry-run the whole pipeline locally with:

```sh
goreleaser release --snapshot --clean   # artifacts land in dist/, nothing published
```

## Install paths

All three are live and in the README's Install section:

- **`go install github.com/pjlsergeant/byre/cmd/byre@latest`** — builds
  from the module proxy, no release involved. What the README leads with.
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
  step is skipped rather than failing the release.
