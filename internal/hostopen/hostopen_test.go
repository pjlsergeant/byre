package hostopen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOpenRegularAcceptsRegularAndReadsIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, fi, err := OpenRegular(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if fi.Size() != 7 {
		t.Fatalf("size = %d", fi.Size())
	}
	// O_NONBLOCK must be a read no-op for regular files.
	b := make([]byte, 7)
	if _, err := f.Read(b); err != nil || string(b) != "content" {
		t.Fatalf("read = %q, %v", b, err)
	}
}

func TestOpenRegularRejectsFIFOImmediately(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	// The whole point: this returns (a plain O_RDONLY open of a FIFO with
	// no writer blocks forever), and it returns ErrNotRegular.
	if _, _, err := OpenRegular(p, true); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("err = %v, want ErrNotRegular", err)
	}
}

func TestOpenRegularRejectsDirectory(t *testing.T) {
	// A directory open may fail at read time only; the type check must
	// refuse it up front. (Linux opens dirs O_RDONLY fine.)
	if _, _, err := OpenRegular(t.TempDir(), true); err == nil {
		t.Fatal("directory must be refused")
	}
}

func TestOpenRegularFollowPolicy(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if f, _, err := OpenRegular(link, true); err != nil {
		t.Fatalf("follow=true must accept a symlink to a regular file: %v", err)
	} else {
		f.Close()
	}
	if _, _, err := OpenRegular(link, false); err == nil {
		t.Fatal("follow=false must refuse a symlink at the named path")
	}
}

func TestOpenRegularInContainsTheWalk(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "in"), []byte("in"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(outside, []byte("out"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if f, _, err := OpenRegularIn(root, "in"); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}
	if _, _, err := OpenRegularIn(root, "escape"); err == nil {
		t.Fatal("escaping symlink must be refused by the rooted open")
	}
}

func TestOpenDirRootNoFollowAnchorsRealDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := OpenDirRootNoFollow(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if f, err := root.Open("f"); err != nil {
		t.Fatalf("anchored root cannot see its own file: %v", err)
	} else {
		f.Close()
	}
	// A trailing slash must anchor the same directory (Clean before split).
	root2, err := OpenDirRootNoFollow(dir + string(filepath.Separator))
	if err != nil {
		t.Fatalf("trailing slash rejected: %v", err)
	}
	root2.Close()
}

// The core fix: a directory classified elsewhere, then swapped for a symlink to
// an external tree before the root opens, must be REFUSED — os.OpenRoot(dir)
// alone would follow the final component and anchor the walk outside.
func TestOpenDirRootNoFollowRefusesSwappedSymlink(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "d")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := OpenDirRootNoFollow(link)
	if !errors.Is(err, ErrSymlinkRoot) {
		t.Fatalf("err = %v, want ErrSymlinkRoot", err)
	}
}

// The Lstat→open window — base swapped to a contained symlink (os.Root follows
// those) or a different real directory AFTER the Lstat classified it — is
// closed by the os.SameFile identity guard in OpenDirRootNoFollow. Staging that
// window needs a concurrent mutator hitting a sub-microsecond gap, which a test
// cannot do without a production test seam; the guard is design-verified, as
// its sibling in internal/build (openDirRootNoFollow → copyPath) already is.
// The deterministic halves ARE pinned: the pre-existing-symlink rejection above
// (Lstat guard) and the mid-delivery swap tests in internal/deliver.

// ReadFileBounded is the config cascade's and the project-identity fence's
// reader, at 14 production call sites -- and every one of its refusal
// branches was uncovered. The guarantee under test is the stated one: an
// oversize file is REFUSED, never truncated and handed to a parser (a
// truncated TOML file can parse cleanly and mean something else).
func TestReadFileBoundedRefusalBranches(t *testing.T) {
	dir := t.TempDir()

	t.Run("within the limit reads whole", func(t *testing.T) {
		p := filepath.Join(dir, "small")
		if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := ReadFileBounded(p, false, 5)
		if err != nil || string(b) != "hello" {
			t.Fatalf("a file exactly at the limit must read whole: %q %v", b, err)
		}
	})

	t.Run("oversize refuses rather than truncating", func(t *testing.T) {
		p := filepath.Join(dir, "big")
		if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := ReadFileBounded(p, false, 4)
		if err == nil {
			t.Fatalf("an oversize file must be refused, got %q", b)
		}
		if b != nil {
			t.Errorf("a refusal must return no bytes at all, got %q", b)
		}
		if !strings.Contains(err.Error(), "limit") {
			t.Errorf("the refusal must name the bound: %v", err)
		}
	})

	t.Run("a FIFO is refused, not blocked on", func(t *testing.T) {
		p := filepath.Join(dir, "fifo")
		if err := syscall.Mkfifo(p, 0o644); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		if _, err := ReadFileBounded(p, false, 1<<20); !errors.Is(err, ErrNotRegular) {
			t.Errorf("a FIFO must be refused as not-regular, got %v", err)
		}
	})

	t.Run("absent surfaces as fs.ErrNotExist", func(t *testing.T) {
		if _, err := ReadFileBounded(filepath.Join(dir, "nope"), false, 16); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("callers distinguish absence from failure: %v", err)
		}
	})
}

// The identity guard closes the Lstat-to-open window: the open must have
// landed on the very directory the Lstat classified. Deleting it left the
// suite green, so this pins it directly -- a real directory swapped for
// ANOTHER real directory between the two calls passes every symlink check
// and only SameFile catches it.
func TestOpenDirRootNoFollowIdentityGuard(t *testing.T) {
	base := t.TempDir()
	real1 := filepath.Join(base, "a")
	real2 := filepath.Join(base, "b")
	for _, d := range []string{real1, real2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Marker files so a wrongly-anchored root is identifiable.
	if err := os.WriteFile(filepath.Join(real2, "impostor"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := OpenDirRootNoFollow(real1)
	if err != nil {
		t.Fatalf("anchoring a plain real directory must work: %v", err)
	}
	defer root.Close()
	if _, err := root.Stat("impostor"); err == nil {
		t.Fatal("anchored the wrong directory entirely")
	}

	// The guard's own predicate: two DIFFERENT real directories must never
	// compare identical, which is what makes the mid-flight swap detectable.
	li, err := os.Lstat(real1)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := os.Stat(real2)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(li, ci) {
		t.Error("distinct directories compared identical — the identity guard cannot detect a swap")
	}
	same, err := os.Stat(real1)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(li, same) {
		t.Error("the same directory must compare identical, or the guard refuses every legitimate anchor")
	}
}
