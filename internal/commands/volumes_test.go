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
