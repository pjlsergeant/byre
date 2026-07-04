package commands

import (
	"strings"
	"testing"
)

func TestRehomeMigratesAndRemovesOld(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-.claude": true, "byre-oldid-cache": true},
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", f, 1000, 1000); err != nil {
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
	p, _ := testPaths(t)
	f := &fakeRunner{
		live:   liveFamily(p, "deadbeef0000"),
		vols:   map[string]bool{"byre-oldid-cache": true},
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected refusal while a session is live")
	}
	if len(f.created) != 0 {
		t.Fatal("must not create volumes when refusing")
	}
}

func TestRehomeConflictAborts(t *testing.T) {
	p, _ := testPaths(t)
	dst := "byre-" + p.ID + "-cache"
	f := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-cache": true, dst: true}, // dst already exists
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected conflict error")
	}
	if len(f.created) != 0 || len(f.migrated) != 0 {
		t.Fatal("must not mutate on conflict")
	}
}

func TestRehomeRollbackOnCopyFailure(t *testing.T) {
	p, _ := testPaths(t)
	failDst := "byre-" + p.ID + "-cache"
	f := &fakeRunner{
		vols:        map[string]bool{"byre-oldid-.claude": true, "byre-oldid-cache": true},
		images:      map[string]bool{imageTag("oldid", 1000, 1000): true},
		failMigrate: failDst,
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected copy failure")
	}
	// Old volumes must NOT be removed (rollback keeps originals).
	if contains(f.removed, "byre-oldid-.claude") || contains(f.removed, "byre-oldid-cache") {
		t.Fatalf("originals removed despite rollback: %v", f.removed)
	}
}

func TestRehomeSameIDErrors(t *testing.T) {
	p, _ := testPaths(t)
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, p.ID, &fakeRunner{}, 1000, 1000); err == nil {
		t.Fatal("expected error rehoming to the same id")
	}
}

func TestRehomeNoImageErrors(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{"byre-oldid-cache": true}} // no images
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", f, 1000, 1000); err == nil {
		t.Fatal("expected error when no image exists for the copy")
	}
}

// A project built before the build-time-UID milestone has only the legacy
// unqualified `byre-<id>` image; rehome must still find it as the copy vehicle.
func TestRehomeFallsBackToLegacyImageTag(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-cache": true},
		images: map[string]bool{"byre-oldid": true}, // legacy tag only, not UID-qualified
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", f, 1000, 1000); err != nil {
		t.Fatalf("rehome should fall back to the legacy image tag: %v", err)
	}
	if len(f.migrated) != 1 {
		t.Fatalf("expected 1 migration via the legacy image, got %v", f.migrated)
	}
}
