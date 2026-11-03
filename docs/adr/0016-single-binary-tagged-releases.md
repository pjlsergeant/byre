# Distribution is tagged single-binary releases via goreleaser

byre is distributed as one static cross-compiled binary per platform,
attached to a GitHub Release cut by pushing a `v*` tag; goreleaser (run
from `.github/workflows/release.yml`) owns the cross-compile, archives,
checksums, changelog, and Homebrew publish. Decided 2026-07-06.

The scope follows from ADR-0008: images are baked per-host (build-time
UID) and are never shipped, and skills and templates are embedded in the
binary -- so the binary IS the distribution, and anything image-shaped
(registries, pulls) is out of scope. goreleaser over a hand-rolled
Makefile-plus-Actions matrix because the pipeline is pure commodity --
cross-compile, tar, checksum, upload, cask -- with no byre opinion in it
(the "core ships no opinions" instinct of PRINCIPLES.md #2, applied to
build tooling); the parts with judgment in them (what a version means,
when brew publishes) live in config we own.

Version identity has three sources and they must agree. Release binaries
carry the tag via `-ldflags -X
github.com/pjlsergeant/byre/internal/version.Version`, the one stamp (two
stamps for one value can disagree); the stamp is deliberately
v-prefixed (`v{{ .Version }}`) so it's byte-identical to what a
`go install ...@vX.Y.Z` build reports from module build info -- the
fallback when nothing is stamped. Unstamped builds report whatever Go
recorded in build info -- on Go 1.24+ even a plain `go build` in a clone
gets a VCS-derived pseudo-version; only a build with no version recorded
anywhere falls back to `(devel)` plus the VCS revision. Honest reporting
either way (PRINCIPLES.md #4: legibility), never a pretended version.

The Homebrew publish (a cask, not a formula -- goreleaser deprecated
`brews` for pre-built binaries) is gated on the tap token being present.
A missing token does not fail the release that everything else depends
on, but it is not silent: PLACEMENT P3 makes
`brew install --cask pjlsergeant/tap/byre` the ONE blessed install
command the README and the site's install page carry, so a skipped cask
push means the blessed command serves the previous version while the
release reports success. `.github/workflows/release.yml` therefore ends
with a step that raises a GitHub warning annotation and a run-summary note
when the token is absent.

Not decided here: any promise about version-number semantics (semver
discipline pre-1.0), auto-update, or package-manager presence beyond the
tap. Skill distribution is a separate concern (the
package model, ADR 0029), not a release one -- bundled skills ship
inside the binary.
