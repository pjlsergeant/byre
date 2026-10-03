package hostopen

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The link is the commit point, and beforeLink is the last question asked in
// front of it: an error there publishes nothing -- no name at the path, and no
// staged temp left beside it. The content was whole and fsynced by then, which
// is the whole point of the hook: the caller that gave up during that fsync
// must not find a file.
func TestPublishStreamExclusiveBeforeLinkErrorPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.byre-backup.tar.gz")
	cancelled := errors.New("cancelled while the staged file was being synced")
	err := PublishStreamExclusive(p, 0o600, func(w io.Writer) error {
		_, werr := io.WriteString(w, "every byte of the archive\n")
		return werr
	}, func() error { return cancelled })
	if !errors.Is(err, cancelled) {
		t.Fatalf("err = %v, want the beforeLink error", err)
	}
	if ok, perr := ExistsNoFollow(p); ok || perr != nil {
		t.Errorf("an entry stands at %s (exists=%v, err=%v)", p, ok, perr)
	}
	ents, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(ents) != 0 {
		t.Errorf("the directory holds %d entries, want nothing -- not even the staged temp", len(ents))
	}
}

// A beforeLink that answers nil is the ordinary publish: the hook is asked
// after the content is complete, so what lands is every byte write produced.
func TestPublishStreamExclusiveBeforeLinkNilAnswerPublishes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.byre-backup.tar.gz")
	asked := false
	err := PublishStreamExclusive(p, 0o600, func(w io.Writer) error {
		_, werr := io.WriteString(w, "every byte of the archive\n")
		return werr
	}, func() error {
		asked = true
		if ok, _ := ExistsNoFollow(p); ok {
			t.Error("the name appeared before beforeLink was asked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !asked {
		t.Error("beforeLink was never asked")
	}
	got, rerr := os.ReadFile(p)
	if rerr != nil || string(got) != "every byte of the archive\n" {
		t.Fatalf("content = %q, %v", got, rerr)
	}
	assertOnlyEntry(t, dir, filepath.Base(p))
}
