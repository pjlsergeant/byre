package commands

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeLister struct{ vols []string }

func (f fakeLister) VolumesByPrefix(prefix string) ([]string, error) {
	var out []string
	for _, v := range f.vols {
		if len(v) >= len(prefix) && v[:len(prefix)] == prefix {
			out = append(out, v)
		}
	}
	return out, nil
}

func TestProjectVolumesDisambiguatesByLongestID(t *testing.T) {
	home := t.TempDir()
	// Two projects whose ids are in a prefix relationship.
	for _, id := range []string{"web-a1b2c3", "web-a1b2c3-x-9f8e7d"} {
		if err := os.MkdirAll(filepath.Join(home, "projects", id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := fakeLister{vols: []string{
		"byre-web-a1b2c3-cache",            // belongs to web-a1b2c3
		"byre-web-a1b2c3-x-9f8e7d-cache",   // belongs to the longer id, but the short prefix matches it too
		"byre-web-a1b2c3-x-9f8e7d-.claude", //   "
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
