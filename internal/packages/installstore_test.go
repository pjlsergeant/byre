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
