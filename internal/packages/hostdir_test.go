package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func bundledEntry(id string) *Entry {
	return &Entry{ID: id, Version: "v1", Sub: "p",
		FS: fstest.MapFS{"p/skill.toml": &fstest.MapFile{Data: []byte("x")},
			"p/hooks/run.sh": &fstest.MapFile{Data: []byte("y")}}}
}

// Extraction lands under ONE process root and CleanupHostDirs removes it —
// the success path used to leak one temp dir per package per invocation
// until the OS swept /tmp (external review counted 2,201 leaked dirs).
func TestHostDirCleanupRemovesExtractionRoot(t *testing.T) {
	CleanupHostDirs() // isolate from other tests' extractions
	d1, err := bundledEntry("byre/one").HostDir()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := bundledEntry("byre/two").HostDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(d1) != filepath.Dir(d2) {
		t.Fatalf("extractions must share one process root: %q vs %q", d1, d2)
	}
	root := filepath.Dir(d1)
	if !strings.Contains(filepath.Base(root), fmt.Sprintf("byre-embed-%d-", os.Getpid())) {
		t.Fatalf("root %q must carry this process's pid for the reap", root)
	}
	CleanupHostDirs()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("cleanup left the extraction root behind: %v", err)
	}
	// The cache must not hand back the removed dir afterwards.
	d3, err := bundledEntry("byre/one").HostDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d3, "skill.toml")); err != nil {
		t.Fatalf("post-cleanup extraction must be fresh and readable: %v", err)
	}
	CleanupHostDirs()
}

// The reap clears dead-owner pid roots and day-old legacy dirs, and keeps a
// live sibling's root and a fresh legacy dir.
func TestReapStaleEmbedRoots(t *testing.T) {
	tmp := os.TempDir()
	mk := func(name string) string {
		d := filepath.Join(tmp, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(d) })
		return d
	}
	deadPid := mk("byre-embed-999999999-qa")
	livePid := mk(fmt.Sprintf("byre-embed-%d-qa", os.Getpid()))
	oldLegacy := mk("byre-embed-1234567890")
	if err := os.Chtimes(oldLegacy, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	freshLegacy := mk("byre-embed-987654321")

	hostDirMu.Lock()
	reapStaleEmbedRoots()
	hostDirMu.Unlock()

	if _, err := os.Stat(deadPid); !os.IsNotExist(err) {
		t.Error("dead-owner pid root should be reaped")
	}
	if _, err := os.Stat(livePid); err != nil {
		t.Error("live process's own root must survive the reap")
	}
	if _, err := os.Stat(oldLegacy); !os.IsNotExist(err) {
		t.Error("day-old legacy dir should be reaped")
	}
	if _, err := os.Stat(freshLegacy); err != nil {
		t.Error("fresh legacy dir must survive (a live old binary may own it)")
	}
}
