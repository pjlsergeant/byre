package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	if !slices.Contains(f.removed, "byre-oldid-.claude") || !slices.Contains(f.removed, "byre-oldid-cache") {
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
		live:   liveProject(p, "deadbeef0000"),
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
	if slices.Contains(f.removed, "byre-oldid-.claude") || slices.Contains(f.removed, "byre-oldid-cache") {
		t.Fatalf("originals removed despite rollback: %v", f.removed)
	}
}

// A malformed old id is refused before any resolution: it's user-typed input
// that becomes a store path component and a volume-name prefix, and byre never
// generates ids outside the slug-6hex grammar.
func TestRehomeRefusesMalformedID(t *testing.T) {
	s, _, _ := testStreams("", false)
	err := Rehome(s, t.TempDir(), "../../escape")
	if err == nil || !strings.Contains(err.Error(), "not a byre project id") {
		t.Fatalf("expected the malformed-id refusal, got %v", err)
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

// Bare `byre rehome` lists candidates: stored projects whose recorded path no
// longer exists, newest activity first. The current project, ids whose path
// still exists, invalid dir names, and recordless dirs are all excluded.
func TestRehomeCandidatesListsMovedProjects(t *testing.T) {
	paths, proj := testPaths(t)
	store := filepath.Join(paths.Home, "projects")
	stillThere := t.TempDir() // an existing path: its project has not moved

	mk := func(id, was string, dockerfileAge time.Duration) {
		t.Helper()
		dir := filepath.Join(store, id)
		if err := os.MkdirAll(filepath.Join(dir, "context"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "path"), []byte(was+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		df := filepath.Join(dir, "context", "Dockerfile.generated")
		if err := os.WriteFile(df, []byte("FROM x"), 0o644); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-dockerfileAge)
		if err := os.Chtimes(df, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	mk("older-aaaaaa", "/gone/older", 48*time.Hour)
	mk("newer-bbbbbb", "/gone/newer", 1*time.Hour)
	mk("home-cccccc", stillThere, 1*time.Hour) // path exists: not a candidate
	// Not a valid id and no path record respectively: both ignored.
	if err := os.MkdirAll(filepath.Join(store, "not a valid id"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store, "bare-dddddd"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	s := Streams{Out: &out, Err: &out}
	if err := RehomeCandidates(s, proj); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"older-aaaaaa", "newer-bbbbbb", "/gone/newer"} {
		if !strings.Contains(got, want) {
			t.Errorf("candidates listing missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"home-cccccc", "bare-dddddd", paths.ID} {
		if strings.Contains(got, absent) {
			t.Errorf("candidates listing must not include %q:\n%s", absent, got)
		}
	}
	if strings.Index(got, "newer-bbbbbb") > strings.Index(got, "older-aaaaaa") {
		t.Errorf("candidates not newest-first:\n%s", got)
	}
}

// With a store but nothing moved, the listing says so rather than printing an
// empty table.
func TestRehomeCandidatesNoneMoved(t *testing.T) {
	paths, proj := testPaths(t)
	stillThere := t.TempDir()
	dir := filepath.Join(paths.Home, "projects", "home-eeeeee")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "path"), []byte(stillThere+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RehomeCandidates(Streams{Out: &out, Err: &out}, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "look moved") {
		t.Fatalf("expected the none-moved message, got:\n%s", out.String())
	}
}
