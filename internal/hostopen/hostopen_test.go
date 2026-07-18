package hostopen

import (
	"errors"
	"os"
	"path/filepath"
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
