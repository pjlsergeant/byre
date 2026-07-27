package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// forget's store clear is the most destructive loop byre has: enumerate a
// directory, delete what is in it. By pathname, a store dir a --self-edit box
// had replaced with a symlink would be listed THROUGH the link and then have
// the link's target emptied one RemoveAll at a time. Refusal is the contract,
// and so is the victim surviving intact.
func TestClearStoreContentsRefusesASymlinkedStoreDir(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"thesis.txt", "photos"} {
		if err := os.WriteFile(filepath.Join(victim, n), []byte("irreplaceable"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(base, "store")
	if err := os.Symlink(victim, store); err != nil {
		t.Skipf("symlink: %v", err)
	}

	if err := clearStoreContents(store); err == nil {
		t.Fatal("clearing a symlinked store dir must be refused")
	}
	ents, err := os.ReadDir(victim)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("victim holds %d entries, want 2 — the refusal did not protect it", len(ents))
	}
}

func TestClearStoreContentsEmptiesARealStoreKeepingTheLock(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"lock", "path", "byre.config"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "context", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clearStoreContents(dir); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The lock survives the critical section it serializes; everything else,
	// nested directories included, goes.
	if len(ents) != 1 || ents[0].Name() != "lock" {
		t.Fatalf("store holds %v, want only the lock", ents)
	}
}

// A missing store is not an error: forget re-runs and concurrent deletions
// both land here.
func TestClearStoreContentsToleratesAMissingStore(t *testing.T) {
	if err := clearStoreContents(filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatalf("missing store: %v", err)
	}
}

// removeEmptiedStore's ordering is delicate: the lock file it must unlink
// lives INSIDE the directory it must remove, so the lock is re-taken, both
// names are unlinked under the held flock, and only then is it released.
func TestRemoveEmptiedStoreRemovesLockAndDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptiedStore(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); err == nil {
		t.Fatal("the store directory survived")
	}
}

// The store is removed NON-RECURSIVELY on purpose, and that is what actually
// protects a concurrent byre: if a develop repopulated the store between the
// clear and this removal, the rmdir fails rather than taking the new contents
// with it. (The inode check against the held lock covers only the sliver
// between re-acquiring and anchoring; this is the guarantee with teeth.)
func TestRemoveEmptiedStoreLeavesARepopulatedStoreAlone(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "store")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A concurrent develop re-bootstrapped this store after the clear.
	if err := os.WriteFile(filepath.Join(dir, "byre.config"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeEmptiedStore(dir); err == nil {
		t.Fatal("removing a repopulated store must fail loudly, not silently succeed")
	}
	if _, err := os.Lstat(filepath.Join(dir, "byre.config")); err != nil {
		t.Fatalf("the repopulated store's contents were deleted: %v", err)
	}
}
