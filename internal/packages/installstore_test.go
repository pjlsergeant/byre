package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSnapshot(id, version string) Snapshot {
	manifest := []byte(`[package]
id = "` + id + `"
version = "` + version + `"
kind = "skill"
package_api = 1
requires_byre = ">=0.1.0"
description = "installed test skill"
`)
	files := map[string][]byte{"hooks/run.sh": []byte("#!/bin/sh\necho " + version + "\n")}
	recs := []PayloadRecord{{Dest: "hooks/run.sh", SHA256: HashBytes(files["hooks/run.sh"]), Executable: true}}
	digest := PackageDigest(manifest, recs)
	return Snapshot{
		ID: id, Digest: digest, Primary: "skill.toml",
		Manifest: manifest, Files: files, Exec: map[string]bool{"hooks/run.sh": true},
		Entry: IndexEntry{Digest: digest, Version: version, Kind: "skill",
			URI: "https://example.test/" + id + "/skill.toml", InstalledAt: "2026-07-13T00:00:00Z"},
	}
}

func TestLandSnapshotAndCatalog(t *testing.T) {
	home := t.TempDir()
	s := testSnapshot("pete/tool", "1.0.0")
	if err := WithStoreLock(home, func() error { return LandSnapshot(home, s) }); err != nil {
		t.Fatal(err)
	}
	// Snapshot on disk, executable bit set.
	fi, err := os.Stat(filepath.Join(SnapshotDir(home, s.Digest), "hooks", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatal("exec bit lost")
	}
	// Catalog lists it as installed with the digest label.
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ent, err := cat.ResolveName("pete/tool")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Provenance != ProvInstalled || ent.Digest != s.Digest {
		t.Fatalf("ent = %+v", ent)
	}
	if !strings.Contains(ent.ProvenanceLabel(), "sha256:"+s.Digest[:8]) {
		t.Fatalf("label = %q", ent.ProvenanceLabel())
	}
	// HostDir is the snapshot dir.
	if d, err := ent.HostDir(); err != nil || d != SnapshotDir(home, s.Digest) {
		t.Fatalf("HostDir = %q, %v", d, err)
	}
}

func TestReplaceDeletesSupersededSnapshot(t *testing.T) {
	home := t.TempDir()
	v1 := testSnapshot("pete/tool", "1.0.0")
	v2 := testSnapshot("pete/tool", "2.0.0")
	if v1.Digest == v2.Digest {
		t.Fatal("test needs distinct digests")
	}
	if err := WithStoreLock(home, func() error { return LandSnapshot(home, v1) }); err != nil {
		t.Fatal(err)
	}
	// The replacement's consent was given against v1's digest (TOCTOU guard):
	// landing without the right ExpectPrior must refuse.
	if err := WithStoreLock(home, func() error { return LandSnapshot(home, v2) }); err != ErrStoreChanged {
		t.Fatalf("stale ExpectPrior must refuse with ErrStoreChanged, got %v", err)
	}
	v2.ExpectPrior = v1.Digest
	if err := WithStoreLock(home, func() error { return LandSnapshot(home, v2) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SnapshotDir(home, v1.Digest)); !os.IsNotExist(err) {
		t.Fatal("superseded snapshot must be deleted")
	}
	if _, err := os.Stat(SnapshotDir(home, v2.Digest)); err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if idx["pete/tool"].Version != "2.0.0" {
		t.Fatalf("index = %+v", idx)
	}
}

func TestUninstallRemovesSnapshotAndRow(t *testing.T) {
	home := t.TempDir()
	s := testSnapshot("pete/tool", "1.0.0")
	if err := WithStoreLock(home, func() error { return LandSnapshot(home, s) }); err != nil {
		t.Fatal(err)
	}
	if err := WithStoreLock(home, func() error { return RemoveInstalled(home, "pete/tool") }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SnapshotDir(home, s.Digest)); !os.IsNotExist(err) {
		t.Fatal("snapshot must be deleted")
	}
	if err := WithStoreLock(home, func() error { return RemoveInstalled(home, "pete/tool") }); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("double uninstall must error as not-installed, got %v", err)
	}
}

func TestOrphanSweep(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(packagesDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := SnapshotDir(home, strings.Repeat("ab", 32))
	mustMkdirAll(t, orphan, 0o755)
	stage := filepath.Join(packagesDir(home), ".stage-crash")
	mustMkdirAll(t, stage, 0o755)
	keeper := filepath.Join(packagesDir(home), "not-a-digest")
	mustMkdirAll(t, keeper, 0o755)
	if err := WithStoreLock(home, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan snapshot must be swept")
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("stage dir must be swept")
	}
	if _, err := os.Stat(keeper); err != nil {
		t.Fatal("non-digest dirs must be left alone")
	}
	if _, err := os.Stat(filepath.Join(packagesDir(home), ".gitignore")); err != nil {
		t.Fatal("self-ignoring .gitignore must exist")
	}
}

func TestInstalledBrokenRowsAreScoped(t *testing.T) {
	home := t.TempDir()
	s := testSnapshot("pete/tool", "1.0.0")
	if err := WithStoreLock(home, func() error { return LandSnapshot(home, s) }); err != nil {
		t.Fatal(err)
	}
	// Hand-corrupt: index row whose snapshot is gone.
	idx, _ := ReadIndex(home)
	idx["pete/gone"] = IndexEntry{Digest: strings.Repeat("cd", 32), Version: "1.0.0",
		Kind: "skill", URI: "https://example.test/gone/skill.toml"}
	if err := writeIndex(home, idx); err != nil {
		t.Fatal(err)
	}
	cat, err := LoadCatalog(home, nil, "v0.2.0", "0.2.0", Stage2Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ResolveName("pete/tool"); err != nil {
		t.Fatalf("healthy install must still resolve: %v", err)
	}
	_, err = cat.ResolveName("pete/gone")
	if err == nil || !strings.Contains(err.Error(), "reinstall") {
		t.Fatalf("broken row must be INVALID with a reinstall remedy, got %v", err)
	}
}

// A digest from the index becomes a path component that removal deletes
// RECURSIVELY — a corrupted or hand-edited index must be rejected whole at
// read time, and the deletion gate must hold even if a bad value slips past.
func TestReadIndexRejectsMalformedDigests(t *testing.T) {
	for _, bad := range []string{
		"../victim",
		"../../etc",
		"/abs/path",
		strings.ToUpper(strings.Repeat("a", 64)), // uppercase: not canonical
		"abc123",                                 // short
		strings.Repeat("a", 63) + "/",            // separator
	} {
		home := t.TempDir()
		if err := os.MkdirAll(packagesDir(home), 0o755); err != nil {
			t.Fatal(err)
		}
		idx := "[packages.\"pete/tool\"]\ndigest = " + `"` + bad + `"` + "\nversion = \"1.0.0\"\nkind = \"skill\"\nuri = \"https://x/s.toml\"\ninstalled_at = \"2026-07-19T00:00:00Z\"\n"
		if err := os.WriteFile(indexPath(home), []byte(idx), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ReadIndex(home)
		if err == nil || !strings.Contains(err.Error(), "malformed digest") {
			t.Errorf("digest %q: want malformed-digest rejection, got %v", bad, err)
		}
	}
}

func TestRemoveSnapshotRefusesNonDigestNames(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(packagesDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	// An outside sentinel a traversal digest would reach.
	victim := filepath.Join(home, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeSnapshot(home, "../victim")
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("outside sentinel deleted by traversal digest: %v", err)
	}
	// A well-formed digest still deletes its snapshot dir.
	good := strings.Repeat("ab", 32)
	snap := SnapshotDir(home, good)
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatal(err)
	}
	removeSnapshot(home, good)
	if _, err := os.Stat(snap); !os.IsNotExist(err) {
		t.Fatalf("well-formed digest should still delete its snapshot: %v", err)
	}
}

// LandSnapshot fails closed on a malformed digest before touching anything —
// the digest becomes a path component it both creates and (Repair) removes.
func TestLandSnapshotRejectsMalformedDigest(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(packagesDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	s := testSnapshot("pete/tool", "1.0.0")
	s.Digest = "../victim"
	err := LandSnapshot(home, s)
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("want malformed-digest rejection, got %v", err)
	}
	ents, rerr := os.ReadDir(packagesDir(home))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(ents) != 0 {
		t.Fatalf("rejected land must leave the store untouched, found %d entries", len(ents))
	}
}

// A regular file (or a symlink) at the digest path is not a snapshot:
// treating Lstat success as "already present" would flip the index onto a
// snapshot that isn't there. Fail closed — do not rewrite, do not flip the
// index. Lstat (not Stat) so a link to a directory is refused too.
func TestLandSnapshotRejectsNonDirectoryAtDigestPath(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(packagesDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	s := testSnapshot("pete/tool", "1.0.0")
	// Pre-place a regular file where the snapshot directory should be.
	if err := os.WriteFile(SnapshotDir(home, s.Digest), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WithStoreLock(home, func() error { return LandSnapshot(home, s) })
	if err == nil || !strings.Contains(err.Error(), "non-directory") {
		t.Fatalf("want non-directory refusal, got %v", err)
	}
	// Index must not have flipped: the package is not installed.
	idx, rerr := ReadIndex(home)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if _, ok := idx[s.ID]; ok {
		t.Fatalf("index must stay unflipped after non-directory refusal: %+v", idx)
	}
	// Victim file unchanged (fail closed, no remove-and-rewrite).
	body, rerr := os.ReadFile(SnapshotDir(home, s.Digest))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(body) != "not a dir" {
		t.Fatalf("refused land must leave the non-directory untouched, got %q", body)
	}
}

// Sibling of the regular-file case: a symlink at the digest path must not
// pass as a landed directory just because Stat would follow it and IsDir
// would succeed for a link to any directory.
func TestLandSnapshotRejectsSymlinkAtDigestPath(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(packagesDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	s := testSnapshot("pete/tool", "1.0.0")
	elsewhere := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, SnapshotDir(home, s.Digest)); err != nil {
		t.Fatal(err)
	}
	err := WithStoreLock(home, func() error { return LandSnapshot(home, s) })
	if err == nil || !strings.Contains(err.Error(), "non-directory") {
		t.Fatalf("want non-directory refusal for symlink, got %v", err)
	}
	idx, rerr := ReadIndex(home)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if _, ok := idx[s.ID]; ok {
		t.Fatalf("index must stay unflipped after symlink refusal: %+v", idx)
	}
	fi, lerr := os.Lstat(SnapshotDir(home, s.Digest))
	if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("refused land must leave the symlink untouched: fi=%v err=%v", fi, lerr)
	}
}
