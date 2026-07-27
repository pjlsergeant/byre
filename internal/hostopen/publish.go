package hostopen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// publish.go is the write half of this package: byre's own records land here.
//
// Every one of them was the same three calls -- CreateTemp in the destination
// directory, write, then Rename or Link onto the final name -- and every one
// of them resolved that directory TWICE, once per call. A box that can rename
// a component between the two (a project store is agent-writable under
// --self-edit) puts the staged file in one directory and the publish in
// another. Opening the directory once and doing both through that descriptor
// closes the window for every caller at once, which is why this is a
// primitive and not a Reason argued three times over.
//
// The publish step itself was already the safe half and stays so: rename(2)
// and link(2) act on the destination's final component WITHOUT following it,
// so a symlink planted at the record name is replaced or refused, never
// written through.

// PublishFile writes content to path via a staged temp file in the same
// directory, REPLACING whatever the name held. Atomic against process failure
// only: no fsync rides the rename, so a killed byre never leaves a truncated
// record, but power loss may drop a write that appeared to succeed.
func PublishFile(path, content string, perm fs.FileMode) error {
	return publish(path, content, perm, false)
}

// PublishFileExclusive is the no-clobber form: it fails with fs.ErrExist if
// the name is already taken, and that refusal is load-bearing -- it is how
// two concurrent first-enrollments racing for one project id are reduced to
// one winner and one loser who re-checks (project.Paths.Bootstrap).
func PublishFileExclusive(path, content string, perm fs.FileMode) error {
	return publish(path, content, perm, true)
}

func publish(path, content string, perm fs.FileMode, exclusive bool) error {
	path = filepath.Clean(path)
	dir, name := filepath.Dir(path), filepath.Base(path)
	switch name {
	case ".", "..", string(filepath.Separator):
		return fmt.Errorf("publish %s: not a file name", path)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	tmp, tmpName, err := createTempIn(root, perm)
	if err != nil {
		return err
	}
	// Best-effort, and deliberately so. After a Rename the staged name is
	// already gone and this is a no-op; only the exclusive form can orphan
	// anything, and its one caller publishes into byre's own store. Failing a
	// publish that SUCCEEDED because a stray .byre-publish-* could not be
	// tidied would trade a cosmetic leftover for a spurious error.
	defer func() { _ = root.Remove(tmpName) }()

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if exclusive {
		return root.Link(tmpName, name)
	}
	return root.Rename(tmpName, name)
}

// createTempIn mints an unguessable name under root and creates it O_EXCL, so
// nothing else chose the file byre is about to write. The retry loop is for a
// name collision, which 64 random bits make vanishingly unlikely -- but a
// collision is also what a box pre-creating names would look like, and
// retrying is the right answer to both.
func createTempIn(root *os.Root, perm fs.FileMode) (*os.File, string, error) {
	for range 100 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		name := ".byre-publish-" + hex.EncodeToString(b[:])
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("staging a file in %s: no free name after 100 tries", root.Name())
}
