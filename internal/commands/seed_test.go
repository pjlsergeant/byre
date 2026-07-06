package commands

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestSeedVolumesFreshSeedsOnce(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	vols := []config.Volume{
		{Name: ".claude", Role: "state", Target: "/h/.claude", Seed: &config.Seed{Host: t.TempDir()}},
		{Name: "cache", Role: "cache", Target: "/c"}, // not seeded
	}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	want := volumeName(p.ID, ".claude")
	if len(f.seeded) != 1 || f.seeded[0] != want {
		t.Fatalf("expected to seed %s once, got %v", want, f.seeded)
	}
}

func TestSeedVolumesSkipsExisting(t *testing.T) {
	p, _ := testPaths(t)
	name := volumeName(p.ID, ".claude")
	f := &fakeRunner{vols: map[string]bool{name: true}}
	vols := []config.Volume{{Name: ".claude", Role: "state", Target: "/t", Seed: &config.Seed{Host: t.TempDir()}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.seeded) != 0 {
		t.Fatalf("existing volume must not be re-seeded: created=%v seeded=%v", f.created, f.seeded)
	}
}

func TestSeedVolumesRollbackOnFailure(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{failSeed: true}
	vols := []config.Volume{{Name: ".claude", Role: "state", Target: "/t", Seed: &config.Seed{Host: t.TempDir()}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err == nil {
		t.Fatal("expected seed failure")
	}
	name := volumeName(p.ID, ".claude")
	if len(f.removed) != 1 || f.removed[0] != name {
		t.Fatalf("expected rollback removal of %s, got %v", name, f.removed)
	}
}

func TestSeedVolumesMissingHostSkips(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	// Absolute host path that doesn't exist -> skip (empty volume), not fatal.
	vols := []config.Volume{{Name: ".claude", Role: "state", Target: "/t", Seed: &config.Seed{Host: filepath.Join(t.TempDir(), "nope")}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatalf("missing seed source should be skipped, got error: %v", err)
	}
	if len(f.created) != 0 || len(f.seeded) != 0 {
		t.Fatalf("missing source must not create/seed: created=%v seeded=%v", f.created, f.seeded)
	}
}

func TestSeedVolumesLiteralWritesOnce(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	vols := []config.Volume{{Name: "cfg", Role: "state", Target: "/t", Seed: &config.Seed{Literal: "hello", Path: "etc/foo.conf"}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	want := volumeName(p.ID, "cfg") + ":etc/foo.conf=hello"
	if len(f.literals) != 1 || f.literals[0] != want {
		t.Fatalf("literal seed wrong: %v", f.literals)
	}
}

func TestSeedPrefsFreshSeedsOnce(t *testing.T) {
	p, _ := testPaths(t)
	from := t.TempDir() // an existing host source dir
	f := &fakeRunner{}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json", "themes"}, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	want := volumeName(p.ID, ".claude") + ":keybindings.json,themes"
	if len(f.created) != 1 || len(f.fileSeed) != 1 || f.fileSeed[0] != want {
		t.Fatalf("expected one fresh prefs seed %q, got created=%v fileSeed=%v", want, f.created, f.fileSeed)
	}
}

func TestSeedPrefsSkipsExisting(t *testing.T) {
	p, _ := testPaths(t)
	name := volumeName(p.ID, ".claude")
	from := t.TempDir()
	f := &fakeRunner{vols: map[string]bool{name: true}}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json"}, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.fileSeed) != 0 {
		t.Fatalf("existing volume must not be re-seeded: created=%v fileSeed=%v", f.created, f.fileSeed)
	}
}

func TestSeedPrefsMissingSourceSkips(t *testing.T) {
	p, _ := testPaths(t)
	from := filepath.Join(t.TempDir(), "nope") // does not exist
	f := &fakeRunner{}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json"}, 1000, 1000); err != nil {
		t.Fatalf("missing prefs source should be skipped, got error: %v", err)
	}
	if len(f.created) != 0 || len(f.fileSeed) != 0 {
		t.Fatalf("missing source must not create/seed: created=%v fileSeed=%v", f.created, f.fileSeed)
	}
}

func TestSeedPrefsNoOpWithoutSpec(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	// No state volume, or no from, or no files -> nothing happens, no error.
	for _, c := range []struct {
		state, from string
		files       []string
	}{
		{"", t.TempDir(), []string{"x"}},
		{".claude", "", []string{"x"}},
		{".claude", t.TempDir(), nil},
	} {
		if err := seedPrefs(f, io.Discard, p, "img", c.state, c.from, c.files, 1000, 1000); err != nil {
			t.Fatalf("expected no-op, got error: %v", err)
		}
	}
	if len(f.created) != 0 || len(f.fileSeed) != 0 {
		t.Fatalf("no-op must not touch volumes: created=%v fileSeed=%v", f.created, f.fileSeed)
	}
}

func TestSeedPrefsRollbackOnFailure(t *testing.T) {
	p, _ := testPaths(t)
	from := t.TempDir()
	f := &fakeRunner{failSeed: true}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json"}, 1000, 1000); err == nil {
		t.Fatal("expected prefs seed failure")
	}
	name := volumeName(p.ID, ".claude")
	if len(f.removed) != 1 || f.removed[0] != name {
		t.Fatalf("expected rollback removal of %s, got %v", name, f.removed)
	}
}
