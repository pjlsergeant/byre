---
title: Install
weight: 10
description: go install, curl, Homebrew, or build from source
---

**⚠️ byre is a young project. I spend all day, every day inside it, for literally all of my work, but features are liable to change quickly.**

byre is a single Go binary. With Go 1.25+ on your machine:

```sh
go install github.com/pjlsergeant/byre/cmd/byre@latest
```

(that puts `byre` in `$(go env GOPATH)/bin` -- make sure it's on your PATH).
Or, no Go toolchain needed, a checksum-verified download of the latest
release binary:

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

## Platform

Linux and macOS, over Docker or Podman -- rootful or rootless (rootless
Podman 4.3+ runs under `--userns=keep-id`). byre bakes a dev identity into
the image so the agent runs unprivileged as you and files land correctly
owned. Debian-derived base images only.
