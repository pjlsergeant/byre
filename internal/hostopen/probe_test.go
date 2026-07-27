package hostopen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistsNoFollowThreeStates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "record")

	if ok, err := ExistsNoFollow(p); ok || err != nil {
		t.Fatalf("absent: ok=%v err=%v, want false, nil", ok, err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := ExistsNoFollow(p); !ok || err != nil {
		t.Fatalf("present: ok=%v err=%v, want true, nil", ok, err)
	}

	// A dangling symlink is SOMETHING: byre's own record name is taken, even
	// though following it would report nothing there.
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if ok, err := ExistsNoFollow(dangling); !ok || err != nil {
		t.Fatalf("dangling symlink: ok=%v err=%v, want true, nil", ok, err)
	}
}

func TestExistsNoFollowDistinguishesGoneFromUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses a 0000 directory")
	}
	dir := t.TempDir()
	closed := filepath.Join(dir, "closed")
	if err := os.Mkdir(closed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(closed, 0o700) })
	// "I could not look" must not read as "it is gone" -- callers act on
	// absence (reset deciding there is nothing to reset).
	ok, err := ExistsNoFollow(filepath.Join(closed, "record"))
	if ok || err == nil {
		t.Fatalf("unreadable: ok=%v err=%v, want false and an error", ok, err)
	}
}

func TestStatNoFollowDescribesTheLinkNotItsTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	fi, err := StatNoFollow(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 || fi.IsDir() {
		t.Fatalf("mode = %s, want a symlink and not a directory", fi.Mode())
	}
}
