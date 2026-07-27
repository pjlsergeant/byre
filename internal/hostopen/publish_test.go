package hostopen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPublishFileWritesAndLeavesNoStagedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "record")
	if err := PublishFile(p, "one\n", 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishFile(p, "two\n", 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "two\n" {
		t.Fatalf("content = %q, %v; want \"two\\n\"", got, err)
	}
	assertOnlyEntry(t, dir, "record")
}

func TestPublishFileReplacesASymlinkRatherThanWritingThroughIt(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "record")
	if err := os.Symlink(victim, p); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := PublishFile(p, "byre wrote this\n", 0o600); err != nil {
		t.Fatal(err)
	}
	// rename(2) does not follow the destination's final component: the name
	// now holds byre's record and the victim is untouched.
	got, err := os.ReadFile(victim)
	if err != nil || string(got) != "host secret" {
		t.Fatalf("victim = %q, %v; want unchanged", got, err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the destination is still a symlink")
	}
}

func TestPublishFileExclusiveRefusesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "record")
	if err := PublishFileExclusive(p, "first\n", 0o600); err != nil {
		t.Fatal(err)
	}
	err := PublishFileExclusive(p, "second\n", 0o600)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	got, rerr := os.ReadFile(p)
	if rerr != nil || string(got) != "first\n" {
		t.Fatalf("content = %q, %v; want the first write intact", got, rerr)
	}
	// The refused attempt staged a temp file and must have cleaned it up --
	// otherwise a store fills with orphans one loser at a time.
	assertOnlyEntry(t, dir, "record")
}

func TestPublishFileExclusiveRefusesASymlinkedName(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "record")
	if err := os.Symlink(victim, p); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := PublishFileExclusive(p, "byre wrote this\n", 0o600); err == nil {
		t.Fatal("an existing name must be refused even when it is a symlink")
	}
	got, rerr := os.ReadFile(victim)
	if rerr != nil || string(got) != "host secret" {
		t.Fatalf("victim = %q, %v; want unchanged", got, rerr)
	}
}

func TestPublishFileMissingDirectoryIsNotExist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gone", "record")
	err := PublishFile(p, "x\n", 0o600)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestPublishFileRefusesANonFileName(t *testing.T) {
	// A directory path with no final component would otherwise be staged and
	// renamed onto ".", which is not a publish anyone asked for.
	if err := PublishFile(t.TempDir()+"/.", "x\n", 0o600); err == nil {
		t.Fatal("a path with no file name must be refused")
	}
}

// assertOnlyEntry is how the tests see staged-file cleanup: a leftover temp
// shows up as an extra directory entry, so "only the record is here" and "no
// temp was orphaned" are the same assertion.
func assertOnlyEntry(t *testing.T, dir, want string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != want {
		t.Fatalf("directory holds %v, want only %q", names, want)
	}
}

func TestPublishRefusesWhenTheDirectoryWasRenamedAway(t *testing.T) {
	// The window anchoring opens: os.Root holds a DESCRIPTOR, so a directory
	// renamed away mid-publish still accepts the write -- into an inode
	// nothing can reach by the name the caller asked about. Reaching it
	// deterministically is why publishInto takes an already-open root.
	for _, exclusive := range []bool{false, true} {
		name := "rename"
		if exclusive {
			name = "link"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "store")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			// Whoever else is running (a concurrent byre forget/rehome) moves
			// the store aside and a fresh one appears at the same spelling.
			if err := os.Rename(dir, filepath.Join(base, "store.bak")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}

			if err := publishInto(root, dir, "record", "x\n", 0o600, exclusive); err == nil {
				t.Fatal("publishing into a detached directory must not report success")
			}
			// The live path is what the caller asked about, and it has no record.
			if _, err := os.Lstat(filepath.Join(dir, "record")); err == nil {
				t.Fatal("a record appeared at the live path, so the premise is wrong")
			}
		})
	}
}

func TestPublishThroughASymlinkedDirectory(t *testing.T) {
	// os.OpenRoot follows, so the post-publish re-assert must follow too: a
	// project or store reached through a symlinked path is an ordinary setup,
	// and comparing a followed open against an unfollowed lookup would fail
	// every publish into one.
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := PublishFile(filepath.Join(link, "record"), "x\n", 0o600); err != nil {
		t.Fatalf("publishing through a symlinked directory: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(real, "record"))
	if err != nil || string(got) != "x\n" {
		t.Fatalf("content = %q, %v; want it landed in the real directory", got, err)
	}
}

func TestPublishFileHonoursPermUnderAHostileUmask(t *testing.T) {
	// The create goes through umask, so a caller asking for 0644 would
	// silently get 0600 and no error. Callers that need a mode (the AGENTS.md
	// guide) previously chmod'd explicitly; the primitive owes them the same.
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	p := filepath.Join(t.TempDir(), "guide")
	if err := PublishFile(p, "x\n", 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want 0644", got)
	}
}
