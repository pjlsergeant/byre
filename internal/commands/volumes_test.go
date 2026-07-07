package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectVolumesDisambiguatesByLongestID(t *testing.T) {
	home := t.TempDir()
	// Two projects whose ids are in a prefix relationship.
	for _, id := range []string{"web-a1b2c3", "web-a1b2c3-x-9f8e7d"} {
		if err := os.MkdirAll(filepath.Join(home, "projects", id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := &fakeRunner{vols: map[string]bool{
		"byre-web-a1b2c3-cache":            true, // belongs to web-a1b2c3
		"byre-web-a1b2c3-x-9f8e7d-cache":   true, // belongs to the longer id, but the short prefix matches it too
		"byre-web-a1b2c3-x-9f8e7d-.claude": true, //   "
	}}

	got, err := projectVolumes(r, home, "web-a1b2c3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "byre-web-a1b2c3-cache" {
		t.Fatalf("short id captured the longer project's volumes: %v", got)
	}

	got2, _ := projectVolumes(r, home, "web-a1b2c3-x-9f8e7d")
	if len(got2) != 2 {
		t.Fatalf("longer id should own its 2 volumes, got %v", got2)
	}
}

// A project whose id begins with "machine" (a repo directory literally named
// that) must not capture machine-scoped volumes in its listings — reset/forget
// build their kill list from projectVolumes (ADR 0017). The exclusion matches
// ^byre-machine-u<digits>-, so it errs FAIL-SAFE: in the pathological corner
// where a unix username itself looks like u<digits> (id machine-u501-...),
// the project's own volumes are over-excluded (never deleted) rather than a
// machine-scoped volume ever being under-excluded (wrongly deleted).
func TestProjectVolumesExcludesMachineScoped(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "machine-pjl-abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{vols: map[string]bool{
		"byre-machine-pjl-abc123-cache":     true, // the project's own volume (dir named "machine")
		"byre-machine-u501-claude-identity": true, // a machine-scoped volume — MUST be excluded
		"byre-machine-u501-abc123-cache":    true, // the u<digits>-username corner's own volume (see below)
	}}
	got, err := projectVolumes(r, home, "machine-pjl-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "byre-machine-pjl-abc123-cache" {
		t.Fatalf("machine-scoped volume captured by a project listing: %v", got)
	}
	// The fail-safe direction of the u<digits> corner, pinned:
	if err := os.MkdirAll(filepath.Join(home, "projects", "machine-u501-abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	got2, err := projectVolumes(r, home, "machine-u501-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Fatalf("u<digits> corner must over-exclude (fail-safe), got %v", got2)
	}
}
