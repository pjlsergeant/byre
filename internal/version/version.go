// Package version reports the byre executable's version string.
//
// Release builds stamp Version via ldflags:
//
//	-X github.com/pjlsergeant/byre/internal/version.Version=vX.Y.Z
//
// Unstamped builds resolve from Go's module build info, then "(devel)".
// Bundled package manifests carry this same string (ADR 0029).
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the release-stamped tag. Empty in unstamped builds.
var Version string

// String returns the version byre reports to users and embeds in bundled
// package manifests. Priority: stamped Version, then module build info, then
// "(devel)" (with a short VCS revision when available).
func String() string {
	if Version != "" {
		return Version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi == nil {
		return "(devel)"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			n := 12
			if len(s.Value) < n {
				n = len(s.Value)
			}
			return "(devel) " + s.Value[:n]
		}
	}
	return "(devel)"
}

// Semver returns a parseable semver for requires_byre checks: strips a leading
// "v", drops a "(devel) ..." suffix (treated as 0.0.0-devel so constraints
// still evaluate), and returns the rest. Empty/unknown yields "0.0.0-devel".
func Semver() string {
	s := String()
	s = strings.TrimPrefix(s, "v")
	if s == "" || strings.HasPrefix(s, "(devel)") {
		return "0.0.0-devel"
	}
	// Pseudo-versions like "0.2.1-0.20260101..." keep working with our
	// comparator's core MAJOR.MINOR.PATCH prefix.
	return s
}
