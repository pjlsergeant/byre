package hostopen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// probe.go answers "what, if anything, is sitting at this name?" without
// following a symlink standing at the final component.
//
// These are the cheapest calls byre makes and the easiest to get subtly
// wrong: os.Stat resolves the leaf, so a probe of a byre record that a box
// has replaced with a link reports on the LINK TARGET -- byre asks about its
// own file and is answered about /etc/passwd. Nothing byre probes here is
// ever legitimately a symlink, so the refusal belongs in the function rather
// than in a judgement re-made at each call site.
//
// Neither function opens anything, so neither can block and neither returns
// content: a wrong answer (from a swapped ANCESTOR, which no leaf rule can
// close) costs a wrong existence or mode, never a read. Every caller that
// goes on to ACT on the answer opens through this package's real functions,
// where the swap would be caught.
//
// NOT for the config family. A user symlinking ~/.byre/default.config out of
// a dotfiles repo is a supported arrangement, and whether to follow there is
// the caller's trust ruling, carried explicitly by config.ParseFile's follow
// argument.

// ExistsNoFollow reports whether anything at all sits at path -- a dangling
// symlink counts, because something is there. The three states are distinct
// on purpose: (true, nil) something is here, (false, nil) PROVABLY nothing is
// here, (false, err) could not tell (EACCES and friends). Callers that act on
// absence must check the error, not just the bool: "I could not look" is not
// "it is gone".
func ExistsNoFollow(path string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// StatNoFollow describes the object at path itself, never what a symlink
// there points at. For callers that need the mode or the mtime rather than
// mere existence.
func StatNoFollow(path string) (fs.FileInfo, error) { return os.Lstat(path) }

// ReadDirNoFollow lists dir through an anchored root rather than by pathname.
// The leaf rule the two probes above apply to a file, applied to a directory:
// os.ReadDir(dir) follows a symlink standing at dir and enumerates whatever it
// points at, so byre asks what is in its own store and is answered with the
// contents of somewhere else. That answer is what callers then act on -- and
// one of them DELETES what it was told (forget's store clear), which is why
// the honest listing is worth a descriptor.
//
// The entries are the directory's own, valid at the moment of the listing.
// A caller that must then operate on them safely wants the root, not the
// names: use OpenDirRootNoFollow directly and act through it, so a swap after
// the listing cannot redirect the action.
func ReadDirNoFollow(dir string) ([]os.DirEntry, error) {
	root, err := OpenDirRootNoFollow(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return fs.ReadDir(root.FS(), ".")
}

// MkdirAllIn creates rel beneath parent, refusing to traverse a symlink at
// any component of rel.
//
// The split is the whole point. parent is the head byre does not own -- a user
// symlinking ~/.byre out of a dotfiles repo or onto another disk is a
// supported arrangement, so it is created and resolved normally. rel is the
// tail byre creates for itself (a project id, its context dir), and a symlink
// standing at one of those is not a setup anyone chose: it is byre's own
// directory replaced. os.MkdirAll cannot tell the two apart -- it succeeds
// silently on an existing symlink-to-directory -- and every later write into
// the store then lands wherever it points.
func MkdirAllIn(parent, rel string, perm fs.FileMode) error {
	if err := os.MkdirAll(parent, perm); err != nil {
		return err
	}
	// Follows, deliberately: parent is the user's, and OpenDirRootNoFollow
	// here would refuse the very dotfiles-symlinked ~/.byre this split exists
	// to keep working.
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()

	cur := root
	closeCur := func() {
		if cur != root {
			cur.Close()
		}
	}
	defer closeCur()
	for _, comp := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if comp == "" || comp == "." {
			continue
		}
		if comp == ".." {
			return fmt.Errorf("mkdir %s under %s: %q escapes the parent", rel, parent, comp)
		}
		if err := cur.Mkdir(comp, perm); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		// Mkdir returning EEXIST says the name is taken, not that a directory
		// is there. Judge it before descending.
		fi, err := cur.Lstat(comp)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s under %s: %w", comp, parent, ErrSymlinkRoot)
		}
		next, err := cur.OpenRoot(comp)
		if err != nil {
			return err
		}
		closeCur()
		cur = next
	}
	return nil
}
