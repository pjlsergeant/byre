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
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err != nil {
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
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err == nil {
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
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err == nil {
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
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err == nil {
		t.Fatal("expected copy failure")
	}
	// Old volumes must NOT be removed (rollback keeps originals).
	if slices.Contains(f.removed, "byre-oldid-.claude") || slices.Contains(f.removed, "byre-oldid-cache") {
		t.Fatalf("originals removed despite rollback: %v", f.removed)
	}
}

// The rehome transaction spans engines: a conflict on the SECOND engine must
// surface before the first engine mutated anything — no source may be removed
// until every engine's copies landed.
func TestRehomeSecondEngineConflictLeavesFirstUntouched(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-.claude": true},
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	podman := &fakeRunner{
		engine: "podman",
		vols:   map[string]bool{"byre-oldid-cache": true, "byre-" + p.ID + "-cache": true}, // dst conflict
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", engines(docker, podman), 1000, 1000); err == nil {
		t.Fatal("expected the podman-side destination conflict to fail the rehome")
	}
	if len(docker.created) != 0 || len(docker.migrated) != 0 || len(docker.removed) != 0 {
		t.Fatalf("docker must be untouched when podman's plan conflicts: created=%v migrated=%v removed=%v",
			docker.created, docker.migrated, docker.removed)
	}
}

// A copy failure on the second engine rolls back the FIRST engine's created
// destinations too, with every source left intact.
func TestRehomeCrossEngineRollbackOnCopyFailure(t *testing.T) {
	p, _ := testPaths(t)
	docker := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-.claude": true},
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	podman := &fakeRunner{
		engine:      "podman",
		vols:        map[string]bool{"byre-oldid-cache": true},
		images:      map[string]bool{imageTag("oldid", 1000, 1000): true},
		failMigrate: "byre-" + p.ID + "-cache",
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", engines(docker, podman), 1000, 1000); err == nil {
		t.Fatal("expected the podman copy failure to fail the rehome")
	}
	// Docker's copy succeeded before podman failed — its destination must be
	// rolled back and its source kept.
	if !slices.Contains(docker.removed, "byre-"+p.ID+"-.claude") {
		t.Errorf("docker's created destination must be rolled back: removed=%v", docker.removed)
	}
	if slices.Contains(docker.removed, "byre-oldid-.claude") || slices.Contains(podman.removed, "byre-oldid-cache") {
		t.Errorf("no source may be removed on failure: docker=%v podman=%v", docker.removed, podman.removed)
	}
}

// mkOldStore populates the old id's store dir the way a real project leaves
// it: a path record pointing at the (now gone) old location, plus any files.
func mkOldStore(t *testing.T, home, oldID string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(home, "projects", oldID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "path"), []byte("/gone/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The stored config follows the identity, and a completed rehome retires the
// old id: its store dir (else it haunts the candidate list forever) and its
// image.
func TestRehomeMigratesStoreAndRetiresOldID(t *testing.T) {
	p, _ := testPaths(t)
	oldDir := mkOldStore(t, p.Home, "oldid", map[string]string{
		"byre.config": "agent = \"claude\"\n",
		"adopted":     "abc123",
	})
	f := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-.claude": true},
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if err != nil || string(b) != "agent = \"claude\"\n" {
		t.Fatalf("stored config not migrated: %v / %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "adopted")); err != nil {
		t.Errorf("adoption record not migrated: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old store must be removed after a successful rehome (it would haunt the candidate list): %v", err)
	}
	if len(f.rmImages) != 1 || f.rmImages[0] != imageTag("oldid", 1000, 1000) {
		t.Errorf("old image not retired: %v", f.rmImages)
	}
}

// A conflicting config at the new id is never clobbered — and the old store
// (holding the only copy of the old config) is kept for hand reconciliation.
func TestRehomeKeepsOldStoreOnConfigConflict(t *testing.T) {
	p, _ := testPaths(t)
	oldDir := mkOldStore(t, p.Home, "oldid", map[string]string{"byre.config": "agent = \"claude\"\n"})
	if err := os.WriteFile(filepath.Join(p.Dir, "byre.config"), []byte("agent = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{
		vols:   map[string]bool{"byre-oldid-.claude": true},
		images: map[string]bool{imageTag("oldid", 1000, 1000): true},
	}
	s, _, out := testStreams("", false)
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(p.Dir, "byre.config"))
	if string(b) != "agent = \"codex\"\n" {
		t.Fatalf("new id's config must not be clobbered: %q", b)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "byre.config")); err != nil {
		t.Errorf("old store must survive a conflict (only copy of the old config): %v", err)
	}
	if !strings.Contains(out.String(), "NOT copied") {
		t.Errorf("conflict should be reported:\n%s", out.String())
	}
}

// A store-only project (config adopted, volumes long gone) still rehomes: the
// config moves and the old id is retired — this is not the 'nothing found'
// case.
func TestRehomeStoreOnlyProject(t *testing.T) {
	p, _ := testPaths(t)
	oldDir := mkOldStore(t, p.Home, "oldid", map[string]string{"byre.config": "agent = \"claude\"\n"})
	f := &fakeRunner{}
	s, _, out := testStreams("", false)
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "byre.config")); err != nil {
		t.Fatalf("store-only config not migrated: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old store must be removed: %v", err)
	}
	if !strings.Contains(out.String(), "rehomed oldid") {
		t.Errorf("store-only rehome should report success:\n%s", out.String())
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
	if err := rehome(s, p, p.ID, engines(&fakeRunner{}), 1000, 1000); err == nil {
		t.Fatal("expected error rehoming to the same id")
	}
}

func TestRehomeNoImageErrors(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{vols: map[string]bool{"byre-oldid-cache": true}} // no images
	s, _, _ := testStreams("", false)
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err == nil {
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
	if err := rehome(s, p, "oldid", engines(f), 1000, 1000); err != nil {
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

// A store containing only the current project must not claim to be empty —
// and an unreadable (EACCES) recorded path is not evidence of a move.
func TestRehomeCandidatesHonestMessagesAndUnverifiablePaths(t *testing.T) {
	paths, proj := testPaths(t)
	var out bytes.Buffer
	if err := RehomeCandidates(Streams{Out: &out, Err: &out}, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no other stored projects") {
		t.Fatalf("current-only store should say 'no other stored projects', got:\n%s", out.String())
	}

	// A recorded path under a 000-mode parent stats EACCES, not ENOENT: it
	// cannot be verified missing, so it must not be offered as moved.
	if os.Getuid() == 0 {
		t.Skip("EACCES is not enforceable as root")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	dir := filepath.Join(paths.Home, "projects", "denied-ffffff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "path"), []byte(filepath.Join(locked, "inner")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := RehomeCandidates(Streams{Out: &out, Err: &out}, proj); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "denied-ffffff") {
		t.Fatalf("EACCES path offered as moved:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "look moved") {
		t.Fatalf("expected the none-moved message (the denied id still counts as stored):\n%s", out.String())
	}
}
