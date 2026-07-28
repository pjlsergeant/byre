package packages

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every byre command runs EnsureStore, so two starting at once on a
// just-upgraded binary is ordinary. writeMirror's swap is three renames over
// one path: run twice concurrently, they interleave into a ~/.byre/bundled
// that is missing or holds half a tree. Under the store lock exactly one
// regeneration happens -- the other re-reads the stamp it queued behind and
// finds the work done.
func TestEnsureStoreRegeneratesTheMirrorOnce(t *testing.T) {
	home := t.TempDir()
	outs := make([]bytes.Buffer, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = EnsureStore(home, bundledFS(), "v9.9.9", &outs[i])
		}(i)
	}
	wg.Wait()

	refreshed := 0
	for i, e := range errs {
		if e != nil {
			t.Fatalf("EnsureStore %d: %v", i, e)
		}
		if strings.Contains(outs[i].String(), "refreshed") {
			refreshed++
		}
	}
	if refreshed != 1 {
		t.Errorf("the mirror was regenerated %d times, want exactly 1", refreshed)
	}
	// And the tree the swap left behind is whole.
	for _, rel := range []string{"README.md", "skills/claude/skill.toml", "templates/go/template.config"} {
		if _, err := os.Stat(filepath.Join(home, "bundled", rel)); err != nil {
			t.Errorf("mirror is missing %s after a concurrent ensure: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "bundled.old")); !os.IsNotExist(err) {
		t.Errorf("the swap left its scratch tree behind: %v", err)
	}
}

func pinMidSwap(t *testing.T, fn func()) {
	t.Helper()
	orig := midSwap
	midSwap = fn
	t.Cleanup(func() { midSwap = orig })
}

// The swap has an open window -- after the old mirror is renamed aside, before
// the new one is renamed in -- in which ~/.byre/bundled does not exist. Nothing
// outside the lock may create it there: an MkdirAll landing in that window
// leaves the winner's rename-in meeting an occupied path, its best-effort
// restore finding the path taken too, and the only complete mirror stranded at
// bundled.old under a name nothing reads.
//
// The seam holds the window open so the test can look, instead of hoping the
// scheduler produces the interleaving. The 200ms is a negative observation
// with a lot of slack: at HEAD the second ensure's unlocked MkdirAll runs
// within microseconds of it starting, so the path is back long before then.
func TestEnsureStoreMirrorPathIsNotRecreatedDuringTheSwap(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "bundled")
	// Prime the store, so the swap under test has an old tree to move aside.
	if err := EnsureStore(home, bundledFS(), "v1", nil); err != nil {
		t.Fatal(err)
	}

	inSwap, proceed := make(chan struct{}), make(chan struct{})
	pinMidSwap(t, func() {
		close(inSwap)
		<-proceed
	})

	aDone := make(chan error, 1)
	go func() { aDone <- EnsureStore(home, bundledFS(), "v2", nil) }()
	<-inSwap

	bDone := make(chan error, 1)
	go func() { bDone <- EnsureStore(home, bundledFS(), "v2", nil) }()
	select {
	case err := <-bDone:
		t.Fatalf("a second ensure finished while the swap window was open: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the mirror path was recreated behind the lock-holder's back: %v", err)
	}

	close(proceed)
	if err := <-aDone; err != nil {
		t.Fatalf("the swapping ensure failed: %v", err)
	}
	if err := <-bDone; err != nil {
		t.Fatalf("the queued ensure failed: %v", err)
	}
	// Both finished, one mirror, nothing stranded.
	if _, err := os.Stat(filepath.Join(root, "skills", "claude", "skill.toml")); err != nil {
		t.Errorf("mirror incomplete after the swap: %v", err)
	}
	if _, err := os.Stat(root + ".old"); !os.IsNotExist(err) {
		t.Errorf("the swap stranded its scratch tree: %v", err)
	}
}

// A mirror whose TREE is gone (a hand-deleted ~/.byre/bundled) is stale even
// when the stamp still matches -- otherwise the create-under-the-lock rule
// would just mean the directory never comes back.
func TestEnsureStoreRebuildsAMirrorWhoseTreeIsGone(t *testing.T) {
	home := t.TempDir()
	if err := EnsureStore(home, bundledFS(), "v1", nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "bundled")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := EnsureStore(home, bundledFS(), "v1", &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "claude", "skill.toml")); err != nil {
		t.Fatalf("a deleted mirror must be rebuilt, stamp or no stamp: %v", err)
	}
	if !strings.Contains(out.String(), "refreshed") {
		t.Errorf("rebuilding the mirror must be said out loud: %q", out.String())
	}
}
