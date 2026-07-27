package hostopen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	if strings.HasPrefix(names[0], ".byre-publish-") {
		t.Fatalf("staged file %q was left behind", names[0])
	}
}
