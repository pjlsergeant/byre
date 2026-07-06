# Releasing byre

byre ships as a single static binary; images are built per-host and never
distributed (ADR-0008), so a release is just cross-compiled binaries on a
GitHub Release. The pipeline is goreleaser, run by
`.github/workflows/release.yml` on every `v*` tag push (ADR-0016).

## Cutting a release

```sh
git checkout main && git pull
go test ./...                # the workflow re-runs this, but fail fast
git tag v0.1.0
git push origin v0.1.0
```

That's it. The workflow runs the tests, then goreleaser cross-compiles
linux/darwin × amd64/arm64, writes checksummed `tar.gz` archives, and
publishes a GitHub Release with a changelog from the commit messages.

Version stamping: release binaries carry the tag via
`-ldflags -X main.version`; `go install ...@vX.Y.Z` builds report the same
string from Go's module build info; plain local builds report `(devel)`
plus the VCS revision. `byre version` (or `byre --version`) prints it.

Dry-run the whole pipeline locally with:

```sh
goreleaser release --snapshot --clean   # artifacts land in dist/, nothing published
```

## Install paths (state per path)

- **`go install github.com/pjlsergeant/byre/cmd/byre@latest`** — works
  today, no release needed. What the README leads with.
- **`install.sh`** (`curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | sh`)
  — checksum-verified download of the latest release binary. Works as soon
  as the **first tag** is pushed; 404s before that, so don't put it in the
  README until then.
- **Homebrew** — goreleaser publishes a cask (`brew install --cask
  pjlsergeant/tap/byre`; cask, not formula — goreleaser deprecated `brews`
  for pre-built binaries, and the cask strips the quarantine bit for the
  unsigned binary). Gated: the publish step is skipped while
  `HOMEBREW_TAP_GITHUB_TOKEN` is unset, so releases never block on it.
  To switch it on:
  1. Create the repo `pjlsergeant/homebrew-tap` (public, can be empty).
  2. Create a fine-grained PAT with **Contents: read/write** on that repo.
  3. Add it as the `HOMEBREW_TAP_GITHUB_TOKEN` Actions secret on
     `pjlsergeant/byre`.
  The next release publishes the cask automatically.

## After the first release

Update the README Install section: add the `install.sh` one-liner and (once
the tap is live) the brew line, per the copy note in TODO §2.
