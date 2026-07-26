package version

import (
	"runtime/debug"
	"testing"
)

func TestSemverDevel(t *testing.T) {
	// go test applies no ldflags, so Version is already "" here -- the old
	// version of this test set it to "" and then re-ran the identical
	// predicate, asserting the same thing twice. What matters is the contract:
	// a paren form must map to a constraint-parseable semver, not merely to
	// "something that doesn't start with '('".
	old := Version
	Version = ""
	t.Cleanup(func() { Version = old })
	if got := Semver(); got != "0.0.0-devel" {
		t.Fatalf("Semver() on an unstamped build = %q, want 0.0.0-devel", got)
	}
}

func TestSemverStamped(t *testing.T) {
	old := Version
	Version = "v0.2.1"
	t.Cleanup(func() { Version = old })
	if got := Semver(); got != "0.2.1" {
		t.Fatalf("Semver() = %q, want 0.2.1", got)
	}
	if got := String(); got != "v0.2.1" {
		t.Fatalf("String() = %q, want v0.2.1", got)
	}
}

// TestResolve pins the resolution order: stamped tag, then module
// version, then (devel) with the VCS revision when recorded.
func TestResolve(t *testing.T) {
	withRev := &debug.BuildInfo{}
	withRev.Main.Version = "(devel)"
	withRev.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}
	shortRev := &debug.BuildInfo{}
	shortRev.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}
	fromModule := &debug.BuildInfo{}
	fromModule.Main.Version = "v0.2.1"
	cases := []struct {
		stamped string
		bi      *debug.BuildInfo
		want    string
	}{
		{"v1.0.0", fromModule, "v1.0.0"},      // stamped wins over build info
		{"", fromModule, "v0.2.1"},            // go install ...@vX.Y.Z
		{"", withRev, "(devel) 0123456789ab"}, // local build with VCS info
		{"", shortRev, "(devel) abc"},         // revision shorter than display width
		{"", &debug.BuildInfo{}, "(devel)"},   // build info without a version
		{"", nil, "(devel)"},                  // no build info at all
	}
	for _, tc := range cases {
		if got := resolve(tc.stamped, tc.bi); got != tc.want {
			t.Errorf("resolve(%q, %+v) = %q, want %q", tc.stamped, tc.bi, got, tc.want)
		}
	}
}
