package hostopen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrPublishedUnsynced reports that PublishStreamExclusive linked the complete
// file into place and then could not fsync the directory: the file is there,
// whole, but its NAME may not survive a power loss. Callers that promise
// durability tell the user so (the backup verb exits 1 with the file written)
// instead of either unlinking a good file or reporting plain success.
var ErrPublishedUnsynced = errors.New("the file is complete, but the directory could not be synced; its durability is unconfirmed")

// PublishStreamExclusive is PublishFileExclusive for content too large or too
// streaming to hold as a string: write produces the bytes into the staged
// file, and the name appears only once every byte is written and fsynced.
//
// beforeLink, when non-nil, is the caller's last word before the commit: it
// runs after the staged file is synced and closed and immediately before the
// link, and an error from it aborts the publish with nothing at path. The
// window it closes is the fsync, which over a large file is the slowest part
// of the whole publish: a caller that was cancelled while write was streaming
// hears it through write's own error, and one cancelled during the sync has
// no other place to be asked.
//
// Three things differ from the string publishers, each for a reason the
// caller depends on:
//
//   - the destination directory is anchored with OpenDirRootNoFollow, not
//     os.OpenRoot: a backup's output directory is wherever the user is
//     standing, and by default that is the project tree the box mounts, so
//     a symlink planted at the directory name must not relocate the publish.
//     ADR 0040's ancestor residual is inherited as it stands.
//   - the file is fsynced before the link and the directory after it. The
//     string publishers give atomic visibility and no power-loss durability
//     by design (configs are recoverable text); a backup is the one copy of
//     a volume the user may have, so here the link is a real commit point.
//   - a failure after the link (the directory fsync) is reported as
//     ErrPublishedUnsynced with the file left in place -- the only failure
//     this function reports over an existing complete file.
//
// Every failure before the link removes the staged temp and leaves nothing
// at path. An existing entry at path of any kind makes the link fail with
// fs.ErrExist; nothing is ever overwritten.
func PublishStreamExclusive(path string, perm fs.FileMode, write func(io.Writer) error, beforeLink func() error) error {
	path = filepath.Clean(path)
	dir, name := filepath.Dir(path), filepath.Base(path)
	switch name {
	case ".", "..", string(filepath.Separator):
		return fmt.Errorf("publish %s: not a file name", path)
	}
	root, err := OpenDirRootNoFollow(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tmp, tmpName, err := createTempIn(root, perm)
	if err != nil {
		return err
	}
	// The staged name is unlinked on every exit: before the link that is
	// the cleanup, after a successful link it is the second name of the
	// published file (link, not rename), and leaving it would be litter
	// beside the backup. A failure here cannot be reported over a publish
	// that succeeded, so it is best-effort like the string publishers'.
	defer func() { _ = root.Remove(tmpName) }()
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if beforeLink != nil {
		if err := beforeLink(); err != nil {
			return err
		}
	}
	if err := root.Link(tmpName, name); err != nil {
		return err
	}
	if err := confirmSameDir(root, dir); err != nil {
		return err
	}
	// Sync the directory so the new name is durable too. os.Root has no
	// Sync; open the directory it anchors through its own descriptor.
	d, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPublishedUnsynced, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("%w: %v", ErrPublishedUnsynced, err)
	}
	return nil
}

// NewRunID mints a per-invocation hex id for names that must never collide
// between two concurrent byre processes: a staging directory, a helper
// container's run label. Fresh randomness, never derived from anything the
// project names.
func NewRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("no randomness for a run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// MkdirPrivateIn creates one directory named rel directly under the anchored
// parent, mode 0700 regardless of umask, and returns a root anchored on it.
// It refuses an existing entry (the name is a fresh run id; a collision is a
// collision) and never follows a symlink at either component.
func MkdirPrivateIn(parent, rel string) (*os.Root, error) {
	proot, err := OpenDirRootNoFollow(parent)
	if err != nil {
		return nil, err
	}
	defer proot.Close()
	if err := proot.Mkdir(rel, 0o700); err != nil {
		return nil, err
	}
	croot, err := openChildNoFollow(proot, rel, filepath.Join(parent, rel))
	if err != nil {
		return nil, err
	}
	// Mkdir went through umask; the directory holds volume bytes, plaintext
	// secrets included, so 0700 means 0700.
	if err := croot.Chmod(".", 0o700); err != nil {
		croot.Close()
		return nil, err
	}
	return croot, nil
}
