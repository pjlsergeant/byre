# The default base tracks Debian stable

Decided 2026-11-02. byre's default base -- the image a project builds FROM
when no `base` is configured and no template supplies one -- is Debian's
CURRENT stable release, spelled by codename: `debian:<codename>`, today
`debian:trixie` (Debian 13). The three shipped templates track the matching
`-<codename>` variants of their official images: `golang:1.27-trixie`,
`node:24-trixie`, `python:3.13-trixie`. When a new Debian stable ships, byre
moves the default and the templates together, within a release, once the
agent-contract tier (`BYRE_AGENT_TESTS`) builds green on the new codename.

Principles: P7 (gen's Dockerfile is a designed, golden-pinned surface; a
moving tag would let Docker Hub change what a byte-identical `FROM` line
means, so the codename is spelled out and a bump is a commit a human can
read); P4 (the base is something `byre status` names and the launch record
pins, so a bump shows up as a `~ Base` line rather than a silent change
under the box); P1 (a project's own `apt` and `dockerfile_pre` lines are the
user's base, not byre's to vet -- they fail loudly at build and byre adds no
gate). Related: ADR 0053 (launch records hold the effective base), ADR 0059
(restore pours into the effective base).

## Why now

Debian 12 (bookworm) left regular security-team support around June 2026
and is on volunteer LTS until mid 2028. Trixie is current stable, and
`debian:latest` on Docker Hub already resolves to it (same digest, checked
2026-11-02). Shipping an oldstable default past its security-team window
would be byre choosing a weaker base on the user's behalf for no gain.

The move costs byre's own surface nothing:

- Every apt package byre's shipped skills install (gosu, iptables,
  netcat-openbsd, bind9-dnsutils, telnet, curl, ca-certificates, gawk, jq,
  util-linux, unzip, ripgrep, xz-utils) exists in trixie under the same
  name (checked against qa.debian.org/madison, 2026-11-02).
- Docker's apt repository publishes a trixie dist, and the docker-host skill
  derives the codename from `/etc/os-release`, so it follows the base
  without an edit.
- Every agent installer ships prebuilt binaries; a newer glibc only widens
  what runs.

## Consequences

- **A cold rebuild for every unset-base project.** The next `byre develop`
  pulls `debian:trixie` and rebuilds from an empty layer cache. Expected,
  one-off, and not avoidable without pinning the old base.
- **The `~ Base` line is only as good as the record.** Launch records
  written under ADR 0053's effective-base rule hold `debian:bookworm`, so
  `byre status` shows `~ Base debian:bookworm -> (default: debian:trixie)`
  before the rebuild. Records written before that rule hold `""`, which
  reads as today's default on both sides -- those boxes show no pending line.
  ADR 0053 accepted that limit; this is the first bump that exercises it.
- **Restore pours into trixie.** ADR 0059 pours a backup's volumes from the
  effective base, so a backup of an unset-base project restored on this
  byre pours with `debian:trixie`'s tar. A tar into a fresh volume does not
  care which Debian runs it, but it is a change in behaviour and is named
  here.
- **User apt lists may break.** A package name that changed between
  bookworm and trixie fails the build with apt's own error. That is the
  user's config meeting a new base; byre does not pre-check it (P1). The
  remedy is to pin `base` -- the editor's Base image row, or
  `base = "debian:bookworm"` in the config -- or update the list.

## Rejected

- **`debian:stable` or `debian:latest`.** Both float: a rebuild with no
  config change and no byre change could silently move the box to a new
  distro. The generated Dockerfile would stop meaning one thing, and the
  launch record's base would stop naming one image family.
- **Staying on bookworm.** Keeps every cache warm, at the price of shipping
  an oldstable default past its security-team window -- trading the user's
  base security for one rebuild.
- **Moving the default without the templates.** A Go project with no
  template on trixie while the go template sits on bookworm is a split
  story with no reason behind it; the default and the templates are one
  claim about what byre builds on, and they move together.

[arm: TestDockerfileDefaultBase,
TestBackupReferencesBaseDefaultsToTheGeneratorsBase,
TestNextLaunchBaseSurvivesADefaultBaseChange,
TestNextLaunchBaseSpellingTheDefaultIsNotADelta,
TestInitScaffoldsAreLoadableWithTheirKeys]
