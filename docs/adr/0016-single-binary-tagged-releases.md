# Distribution is tagged single-binary releases via goreleaser

byre is distributed as one static cross-compiled binary per platform,
attached to a GitHub Release cut by pushing a `v*` tag; goreleaser (run
from `.github/workflows/release.yml`) owns the cross-compile, archives,
checksums, changelog, and Homebrew publish. Decided 2026-07-06, building
the "versioning + distribution" item flagged 2026-07-01.

The scope follows from ADR-0008: images are baked per-host (build-time
UID) and are never shipped, and skills and templates are embedded in the
binary — so the binary IS the distribution, and anything image-shaped
(registries, pulls) is out of scope. goreleaser over a hand-rolled
Makefile-plus-Actions matrix because the pipeline is pure commodity —
cross-compile, tar, checksum, upload, formula — with no byre opinion in
it; the parts with judgment in them (what a version means, when brew
publishes) live in config we own.

Version identity has three sources and they must agree. Release binaries
carry the tag via `-ldflags -X main.version`; the stamp is deliberately
v-prefixed (`v{{ .Version }}`) so it's byte-identical to what a
`go install ...@vX.Y.Z` build reports from module build info — the
fallback when nothing is stamped. Plain local builds report `(devel)`
plus the VCS revision rather than pretending to a version. So
`byre version` answers honestly for all three ways a binary can exist.

The Homebrew publish (a cask, not a formula — goreleaser deprecated
`brews` for pre-built binaries) is gated on the tap token being present
and silently skipped otherwise. Releases must work from the first tag;
the tap repo and its token are a later nicety, and a missing nicety must
not fail the release that everything else depends on.

Not decided here: any promise about version-number semantics (semver
discipline pre-1.0), auto-update, or package-manager presence beyond the
tap. `byre skill add`-style distribution of skills is a separate
milestone (TODO §4), not a release concern — skills ship inside the
binary.
