package commands

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/config"
	"byre/internal/project"
)

// fakeSeeder records calls and can fail SeedVolume to exercise rollback.
type fakeSeeder struct {
	existing map[string]bool
	created  []string
	removed  []string
	seeded   []string
	literals []string
	fileSeed []string // "name:f1,f2" recorded by SeedFiles
	failSeed bool
}

func (f *fakeSeeder) VolumeExists(name string) (bool, error) { return f.existing[name], nil }
func (f *fakeSeeder) VolumeCreate(name string) error         { f.created = append(f.created, name); return nil }
func (f *fakeSeeder) VolumeRemove(name string) error         { f.removed = append(f.removed, name); return nil }
func (f *fakeSeeder) SeedVolume(name, host, image string, uid, gid int) error {
	if f.failSeed {
		return io.EOF
	}
	f.seeded = append(f.seeded, name)
	return nil
}

func (f *fakeSeeder) SeedLiteral(name, destPath, content, image string, uid, gid int) error {
	if f.failSeed {
		return io.EOF
	}
	f.literals = append(f.literals, name+":"+destPath+"="+content)
	return nil
}

func (f *fakeSeeder) SeedFiles(name, srcDir string, files []string, image string, uid, gid int) error {
	if f.failSeed {
		return io.EOF
	}
	f.fileSeed = append(f.fileSeed, name+":"+strings.Join(files, ","))
	return nil
}

func paths(t *testing.T) project.Paths {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	p, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSeedVolumesFreshSeedsOnce(t *testing.T) {
	p := paths(t)
	f := &fakeSeeder{existing: map[string]bool{}}
	vols := []config.Volume{
		{Name: ".claude", Role: "state", Target: "/h/.claude", Seed: &config.Seed{Host: "~/.claude"}},
		{Name: "cache", Role: "cache", Target: "/c"}, // not seeded
	}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	want := VolumeName(p.ID, ".claude")
	if len(f.seeded) != 1 || f.seeded[0] != want {
		t.Fatalf("expected to seed %s once, got %v", want, f.seeded)
	}
}

func TestSeedVolumesSkipsExisting(t *testing.T) {
	p := paths(t)
	name := VolumeName(p.ID, ".claude")
	f := &fakeSeeder{existing: map[string]bool{name: true}}
	vols := []config.Volume{{Name: ".claude", Role: "state", Target: "/t", Seed: &config.Seed{Host: "~/.claude"}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.seeded) != 0 {
		t.Fatalf("existing volume must not be re-seeded: created=%v seeded=%v", f.created, f.seeded)
	}
}

func TestSeedVolumesRollbackOnFailure(t *testing.T) {
	p := paths(t)
	f := &fakeSeeder{existing: map[string]bool{}, failSeed: true}
	vols := []config.Volume{{Name: ".claude", Role: "state", Target: "/t", Seed: &config.Seed{Host: "~/.claude"}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err == nil {
		t.Fatal("expected seed failure")
	}
	name := VolumeName(p.ID, ".claude")
	if len(f.removed) != 1 || f.removed[0] != name {
		t.Fatalf("expected rollback removal of %s, got %v", name, f.removed)
	}
}

func TestSeedVolumesMissingHostSkips(t *testing.T) {
	p := paths(t)
	f := &fakeSeeder{existing: map[string]bool{}}
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
	p := paths(t)
	f := &fakeSeeder{existing: map[string]bool{}}
	vols := []config.Volume{{Name: "cfg", Role: "state", Target: "/t", Seed: &config.Seed{Literal: "hello", Path: "etc/foo.conf"}}}
	if err := seedVolumes(f, io.Discard, p, "img", vols, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	want := VolumeName(p.ID, "cfg") + ":etc/foo.conf=hello"
	if len(f.literals) != 1 || f.literals[0] != want {
		t.Fatalf("literal seed wrong: %v", f.literals)
	}
}

func TestSeedPrefsFreshSeedsOnce(t *testing.T) {
	p := paths(t)
	from := t.TempDir() // an existing host source dir
	f := &fakeSeeder{existing: map[string]bool{}}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json", "themes"}, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	want := VolumeName(p.ID, ".claude") + ":keybindings.json,themes"
	if len(f.created) != 1 || len(f.fileSeed) != 1 || f.fileSeed[0] != want {
		t.Fatalf("expected one fresh prefs seed %q, got created=%v fileSeed=%v", want, f.created, f.fileSeed)
	}
}

func TestSeedPrefsSkipsExisting(t *testing.T) {
	p := paths(t)
	name := VolumeName(p.ID, ".claude")
	from := t.TempDir()
	f := &fakeSeeder{existing: map[string]bool{name: true}}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json"}, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(f.fileSeed) != 0 {
		t.Fatalf("existing volume must not be re-seeded: created=%v fileSeed=%v", f.created, f.fileSeed)
	}
}

func TestSeedPrefsMissingSourceSkips(t *testing.T) {
	p := paths(t)
	from := filepath.Join(t.TempDir(), "nope") // does not exist
	f := &fakeSeeder{existing: map[string]bool{}}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json"}, 1000, 1000); err != nil {
		t.Fatalf("missing prefs source should be skipped, got error: %v", err)
	}
	if len(f.created) != 0 || len(f.fileSeed) != 0 {
		t.Fatalf("missing source must not create/seed: created=%v fileSeed=%v", f.created, f.fileSeed)
	}
}

func TestSeedPrefsNoOpWithoutSpec(t *testing.T) {
	p := paths(t)
	f := &fakeSeeder{existing: map[string]bool{}}
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
	p := paths(t)
	from := t.TempDir()
	f := &fakeSeeder{existing: map[string]bool{}, failSeed: true}
	if err := seedPrefs(f, io.Discard, p, "img", ".claude", from, []string{"keybindings.json"}, 1000, 1000); err == nil {
		t.Fatal("expected prefs seed failure")
	}
	name := VolumeName(p.ID, ".claude")
	if len(f.removed) != 1 || f.removed[0] != name {
		t.Fatalf("expected rollback removal of %s, got %v", name, f.removed)
	}
}
