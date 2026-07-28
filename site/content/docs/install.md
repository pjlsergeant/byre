---
title: Install
weight: 10
description: go install, curl, Homebrew, or build from source
---

**⚠️ byre is a young project. I spend all day, every day inside it, for literally all of my work. All the major planned features have been added, so the interface should be pretty stable at this point.**

byre is a single Go binary. With Go 1.25+ on your machine:

```sh
go install github.com/pjlsergeant/byre/cmd/byre@latest
```

(that puts `byre` in `$(go env GOPATH)/bin` -- make sure it's on your PATH).
Or, no Go toolchain needed, a download of the latest release binary,
verified against the release's `checksums.txt` (this catches a corrupted
download, not a compromised release -- the checksums ship from the same
GitHub release as the binary):

```sh
curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | sh
```

Or on macOS, via Homebrew:

```sh
brew install --cask pjlsergeant/tap/byre
```

Or build from a checkout:

```sh
go build -o ~/bin/byre ./cmd/byre
```

You need Docker (or Podman) running on the host.

## Pinning a version

`install.sh` installs the latest release. Two environment variables
change that:

```sh
# a specific release instead of the latest
curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | BYRE_VERSION=v1.4.0 sh

# somewhere other than /usr/local/bin (or ~/.local/bin when that is not writable)
curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | BYRE_INSTALL_DIR=~/bin sh
```

`BYRE_VERSION` is the way back if an upgrade breaks you. byre is 1.x
and a minor release may retire a config key -- always with a `CHANGES`
entry and an error message carrying the migration, never silently --
so if a new version refuses something your config relies on, pin the
last version that worked while you migrate. `byre version` prints what
you are running.

## Platform

Linux and macOS, over Docker or Podman -- rootful or rootless (rootless
Podman 4.3+ runs under `--userns=keep-id`). byre bakes a dev identity into
the image so the agent runs unprivileged as you and files land correctly
owned. Debian-derived base images only.
