# The default base tracks Debian stable

Decided 2026-11-02. byre's default base -- the image a project builds FROM
when no `base` is configured and no template supplies one -- is Debian's
CURRENT stable release, spelled by codename: `debian:<codename>`, today
`debian:trixie` (Debian 13). The three shipped templates track the matching
`-<codename>` variants of their official images: `golang:1.27-trixie`,
`node:24-trixie`, `python:3.13-trixie`. When a new Debian stable ships, byre
moves the default and the templates together, within a release, once the
agent-contract tier (`BYRE_AGENT_TESTS`) builds green on the new codename.

The template move is a toolchain bump as well as a codename bump: Go 1.26 ->
1.27, Node 22 -> 24, Python 3.12 -> 3.13. Template language lines are chosen
at each bump and recorded here, not derived by one rule. This bump:

- **Go 1.27** is the current Go release; Go supports two lines, so the
  newest is the one with the longest runway.
- **Node 24** is the LTS line supported to 2028-04-30. Node 26 became
  Active LTS on 2026-10-28, five days before this decision, and was judged
  too new for a starter template; the next bump revisits it.
- **Python 3.13** is the `python3` line trixie itself ships, so apt-installed
  Python tooling in the box and the template's interpreter agree on a
  version.

(Checked 2026-11-02: Go against go.dev/dl, which lists Go 1.27 as current
and two lines supported; Node dates against nodejs.org/dist/index.json and
the nodejs/Release schedule.json; Python against Debian trixie's `python3`
package, 3.13.5-1, packages.debian.org/trixie/python3.)

What the codename pins is the Debian RELEASE FAMILY, not an image.
`debian:trixie` and the template tags are mutable: Docker republishes them
for point releases and security rebuilds, and that same-codename movement is
accepted -- it is how the base picks up security fixes. What the codename
spelling prevents is the one change a `stable`/`latest` tag would add: an
unannounced move to a different distro family under a byte-identical
Dockerfile.

Principles: P7 (gen's Dockerfile is a designed, golden-pinned surface; a
floating `stable`/`latest` tag would let Docker Hub decide which Debian
release a byte-identical `FROM` line means, so the codename is spelled out
and a family change is a commit a human can read); P4 (the base is something
`byre status` names and the launch record records as the effective base
string, so a codename bump shows up as a `~ Base` line rather than a silent
change under the box); P1 (a project's own `apt` and `dockerfile_pre` lines
are the user's base, not byre's to vet -- they fail loudly at build and byre
adds no gate). Related: ADR 0053 (launch records hold the effective base),
ADR 0059 (restore pours into the effective base).

## Why now

Debian 12 (bookworm) left the security team on 12 July 2026 -- "Security
support for Bookworm handed over to the LTS team"
(https://www.debian.org/News/2026/20260712) -- and is on volunteer LTS until
30 June 2028. Trixie is current stable, and `debian:latest` on Docker Hub
already resolves to it (same digest, checked 2026-11-02). Shipping an
oldstable default past its security-team window would be byre choosing a
weaker base on the user's behalf for no gain.

The move costs byre's own surface nothing:

- Every apt package installed by the core block, every skill under
  `internal/builtins/skills`, the shipped templates, and `skills/inttest`
  (gosu, iptables, netcat-openbsd, iputils-ping, traceroute,
  bind9-dnsutils, telnet, curl, ca-certificates, gawk, jq, util-linux,
  unzip, ripgrep, xz-utils, build-essential, openssh-client) exists in
  trixie under the same name (checked against qa.debian.org/madison,
  2026-11-02).
- Docker's apt repository publishes a trixie dist, and the docker-host skill
  derives the codename from `/etc/os-release`, so it follows the base
  without an edit.
- Every agent installer ships prebuilt binaries; a newer glibc only widens
  what runs.

Verified 2026-11-02: the agent-contract tier built every agent's box cold on
`debian:trixie` and the full gated integration suite passed. docker-host's
apt step and the three template images were exercised directly on the
runner. The gemini skill's keep-the-base-Node branch was exercised on
`node:24-trixie` (Node 24 kept) and its install branch on bare
`debian:trixie` (Node 22 fetched, checksum OK), and the gemini
agent-contract test passed on trixie.

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
- **Same-codename tag movement is not shown.** The record holds the base as
  a string, and the digest it holds is the BUILT image's, so a point release
  or security rebuild of `debian:trixie` picked up at the next build is not
  a `~ Base` line. That movement is the accepted half of the codename rule.
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
- **Template projects may break on the new toolchain.** Project code can
  fail on a new Node or Python major/minor (or a Go release) with no change
  of its own. The remedy is the same pin of `base` to the old line's tag:
  `golang:1.26-trixie`, `node:22-trixie` and `python:3.12-trixie` exist
  (checked on Docker Hub, 2026-11-02), so the codename can move while the
  language stays put.

## Rejected

- **`debian:stable` or `debian:latest`.** Both float across releases: a
  rebuild with no config change and no byre change could silently move the
  box to a new distro family. The generated Dockerfile would stop meaning
  one Debian release, and the launch record's base string would stop naming
  one.
- **Pinning the base by digest.** It would make the `FROM` line name one
  image exactly, at the price of freezing security updates into byre's
  release cadence: a Debian security rebuild would reach users only when
  byre shipped a new digest.
- **Staying on bookworm.** Keeps every cache warm, at the price of shipping
  an oldstable default past its security-team window -- trading the user's
  base security for one rebuild.
- **Moving the default without the templates.** A Go project with no
  template on trixie while the go template sits on bookworm is a split
  story with no reason behind it; the default and the templates are one
  claim about what byre builds on, and they move together.

The gemini skill's keep-the-base-Node branch has one gated arm,
`TestAgentContractGeminiKeepsTheTemplatesNode` (`BYRE_AGENT_TESTS=1`,
`internal/commands/agent_contract_test.go`): it builds `template = "node"`
with `agent = "gemini"` and asserts the box still runs Node 24 with npm and
the gemini CLI. It is absent from the list below because an index line
carries one marker: `go test ./...` says nothing about it, and the
agent-contract tier is where it fires.

[arm: TestDockerfileDefaultBase, TestDefaultBaseContract,
TestTemplateBasesContract,
TestBackupReferencesBaseDefaultsToTheGeneratorsBase,
TestNextLaunchBaseSurvivesADefaultBaseChange,
TestNextLaunchBaseSpellingTheDefaultIsNotADelta,
TestInitScaffoldsAreLoadableWithTheirKeys]
