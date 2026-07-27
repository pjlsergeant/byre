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

func TestReadDirNoFollowRefusesASymlinkedDirectory(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(base, "store")
	if err := os.Symlink(victim, store); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := ReadDirNoFollow(store); err == nil {
		t.Fatal("listing through a symlinked directory must be refused")
	}
	if _, err := os.Lstat(filepath.Join(victim, "secret")); err != nil {
		t.Fatalf("victim contents changed: %v", err)
	}
}

func TestReadDirNoFollowListsARealDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := ReadDirNoFollow(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 || ents[0].Name() != "a" || ents[1].Name() != "b" {
		t.Fatalf("entries = %v, want a and b", ents)
	}
}

func TestMkdirAllInCreatesTheTailAndIsIdempotent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "byre", "projects")
	if err := MkdirAllIn(parent, filepath.Join("projects", "proj-abc123", "context"), 0o755); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(parent, "projects", "proj-abc123", "context"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("context dir: %v", err)
	}
	// Bootstrap runs on every develop, not only the first.
	if err := MkdirAllIn(parent, filepath.Join("projects", "proj-abc123", "context"), 0o755); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestMkdirAllInRefusesASymlinkedTailComponent(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "byre")
	if err := os.MkdirAll(filepath.Join(parent, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	// byre's own store directory, replaced with a link out of the store.
	if err := os.Symlink(elsewhere, filepath.Join(parent, "projects", "proj-abc123")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := MkdirAllIn(parent, filepath.Join("projects", "proj-abc123", "context"), 0o755); err == nil {
		t.Fatal("a symlinked store component must be refused")
	}
	if _, err := os.Lstat(filepath.Join(elsewhere, "context")); err == nil {
		t.Fatal("created a directory through the symlink")
	}
}

func TestMkdirAllInFollowsASymlinkedParent(t *testing.T) {
	// The head is the user's: ~/.byre symlinked out of a dotfiles repo or onto
	// another disk is a supported arrangement and must keep working.
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := MkdirAllIn(link, "proj-abc123", 0o755); err != nil {
		t.Fatalf("symlinked parent must still work: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(real, "proj-abc123")); err != nil || !fi.IsDir() {
		t.Fatalf("landed in the real directory? %v", err)
	}
}

// A CONTAINED symlink -- one pointing inside the root -- is the case os.Root
// resolves happily, so it is the one a descent has to refuse for itself. The
// other half of the guard (the identity check, for a name swapped in the
// Lstat->open window) is not reachable without a seam; its predicate is
// pinned by TestOpenDirRootNoFollowIdentityGuard, and MkdirAllIn inherits it
// by going through this same function rather than a second copy.
func TestOpenChildNoFollowRefusesAContainedSymlink(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	if err := os.MkdirAll(filepath.Join(parent, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(parent, "child")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if r, err := openChildNoFollow(root, "child", filepath.Join(parent, "child")); err == nil {
		r.Close()
		t.Fatal("a contained symlink must be refused, not resolved")
	}
}

func TestMkdirAllInRefusesAContainedSymlinkComponent(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "byre")
	if err := os.MkdirAll(filepath.Join(parent, "projects", "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Contained, so os.Root resolves it: only the descent's own check refuses.
	if err := os.Symlink("target", filepath.Join(parent, "projects", "proj-abc123")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := MkdirAllIn(parent, filepath.Join("projects", "proj-abc123", "context"), 0o755); err == nil {
		t.Fatal("a contained symlink at a store component must be refused")
	}
	if _, err := os.Lstat(filepath.Join(parent, "projects", "target", "context")); err == nil {
		t.Fatal("created through the contained symlink")
	}
}
