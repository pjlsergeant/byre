package commands

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"byre/internal/project"
)

type fakeRehome struct {
	liveIDs     map[string]bool
	vols        map[string]bool // existing volumes
	images      map[string]bool
	created     []string
	removed     []string
	migrated    []string
	failMigrate string // dst to fail migration on
}

func (f *fakeRehome) RunningContainersByLabel(label string) ([]string, error) {
	id := strings.TrimPrefix(label, labelKey+"=")
	if f.liveIDs[id] {
		return []string{"deadbeef0000"}, nil
	}
	return nil, nil
}
func (f *fakeRehome) VolumesByPrefix(prefix string) ([]string, error) {
	var out []string
	for v := range f.vols {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *fakeRehome) VolumeExists(name string) (bool, error) { return f.vols[name], nil }
func (f *fakeRehome) VolumeCreate(name string) error {
	f.created = append(f.created, name)
	f.vols[name] = true
	return nil
}
func (f *fakeRehome) VolumeRemove(name string) error {
	f.removed = append(f.removed, name)
	delete(f.vols, name)
	return nil
}
func (f *fakeRehome) MigrateVolume(src, dst, image string, uid, gid int) error {
	if dst == f.failMigrate {
		return fmt.Errorf("copy boom")
	}
	f.migrated = append(f.migrated, src+"->"+dst)
	return nil
}
func (f *fakeRehome) ImageExists(tag string) (bool, error) { return f.images[tag], nil }

func rehomePaths(t *testing.T) project.Paths {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	p, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil { // rehome takes the setup lock
		t.Fatal(err)
	}
	return p
}

func TestRehomeMigratesAndRemovesOld(t *testing.T) {
	p := rehomePaths(t)
	old := "oldid"
	f := &fakeRehome{
		vols:   map[string]bool{"byre-oldid-.claude": true, "byre-oldid-cache": true},
		images: map[string]bool{"byre-oldid": true},
	}
	var out bytes.Buffer
	if err := rehome(&out, p, old, f, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if len(f.migrated) != 2 {
		t.Fatalf("expected 2 migrations, got %v", f.migrated)
	}
	// Old volumes removed after successful copies.
	if !contains(f.removed, "byre-oldid-.claude") || !contains(f.removed, "byre-oldid-cache") {
		t.Fatalf("old volumes not removed: %v", f.removed)
	}
	// New volumes named with the current id.
	for _, c := range f.created {
		if !strings.HasPrefix(c, "byre-"+p.ID+"-") {
			t.Fatalf("created volume not under new id: %s", c)
		}
	}
}

func TestRehomeRefusesLive(t *testing.T) {
	p := rehomePaths(t)
	f := &fakeRehome{
		liveIDs: map[string]bool{p.ID: true},
		vols:    map[string]bool{"byre-oldid-cache": true},
		images:  map[string]bool{"byre-oldid": true},
	}
	if err := rehome(&bytes.Buffer{}, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.created) != 0 {
		t.Fatal("must not create volumes when refusing")
	}
}

func TestRehomeConflictAborts(t *testing.T) {
	p := rehomePaths(t)
	dst := "byre-" + p.ID + "-cache"
	f := &fakeRehome{
		vols:   map[string]bool{"byre-oldid-cache": true, dst: true}, // dst already exists
		images: map[string]bool{"byre-oldid": true},
	}
	if err := rehome(&bytes.Buffer{}, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected conflict error")
	}
	if len(f.created) != 0 || len(f.migrated) != 0 {
		t.Fatal("must not mutate on conflict")
	}
}

func TestRehomeRollbackOnCopyFailure(t *testing.T) {
	p := rehomePaths(t)
	failDst := "byre-" + p.ID + "-cache"
	f := &fakeRehome{
		vols:        map[string]bool{"byre-oldid-.claude": true, "byre-oldid-cache": true},
		images:      map[string]bool{"byre-oldid": true},
		failMigrate: failDst,
	}
	if err := rehome(&bytes.Buffer{}, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected copy failure")
	}
	// Old volumes must NOT be removed (rollback keeps originals).
	if contains(f.removed, "byre-oldid-.claude") || contains(f.removed, "byre-oldid-cache") {
		t.Fatalf("originals removed despite rollback: %v", f.removed)
	}
}

func TestRehomeSameIDErrors(t *testing.T) {
	p := rehomePaths(t)
	if err := rehome(&bytes.Buffer{}, p, p.ID, &fakeRehome{vols: map[string]bool{}}, 1000, 1000); err == nil {
		t.Fatal("expected error rehoming to the same id")
	}
}

func TestRehomeNoImageErrors(t *testing.T) {
	p := rehomePaths(t)
	f := &fakeRehome{vols: map[string]bool{"byre-oldid-cache": true}} // no images
	if err := rehome(&bytes.Buffer{}, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected error when no image exists for the copy")
	}
}
