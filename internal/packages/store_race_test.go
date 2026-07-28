package packages

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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
