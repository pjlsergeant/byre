// Package hostopen is the one place byre opens host files that something
// untrusted may have shaped (an agent writing into /workspace, a hostile
// package directory): open with O_NONBLOCK, judge the OPEN descriptor, and
// require a regular file — never a pathname re-check (ADR 0021's transport
// rule). The three lines matter as a unit: a FIFO stats as size 0 (so any
// size preflight passes) and then blocks a plain open or read forever; a
// device node reads unbounded; and any check done by pathname instead of
// descriptor can be swapped between the check and the use. This package
// exists because the pattern was re-derived per call site and one site
// missed it (the local-manifest fetch, found 2026-07-18) — reading policy
// (caps, budgets, streaming) stays with the caller, where it genuinely
// differs.
package hostopen

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ErrNotRegular reports that the opened path is not a regular file. Callers
// with skip-vs-fail semantics (deliver's tree walk) branch on it with
// errors.Is; everyone else just propagates.
var ErrNotRegular = errors.New("not a regular file")

// ErrSymlinkRoot reports that OpenDirRootNoFollow could not anchor on the
// directory the caller classified: the name now resolves to a symlink, or
// the open landed on a different real directory than the one checked (both
// arms of the identity guard below). Callers with their own contract
// language rewrap it via errors.Is.
var ErrSymlinkRoot = errors.New("no longer the directory that was checked -- swapped for a symlink or another dir (refusing to anchor there)")

// OpenRegular opens path for reading and returns the file with its fstat
// info. follow states whether a symlink AT path is followed — true when the
// USER named the path (their explicit choice), false when the path came from
// something untrusted. Interior path components are resolved normally; use
// OpenRegularIn to contain the whole walk. On any failure (including a
// non-regular target) no file is returned and nothing stays open.
func OpenRegular(path string, follow bool) (*os.File, os.FileInfo, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if !follow {
		flags |= syscall.O_NOFOLLOW
	}
	return finishOpen(os.OpenFile(path, flags, 0))
}

// OpenRegularIn is OpenRegular through an os.Root: every component of rel
// (symlinks included) resolves beneath the root, so a swap between any
// check and the open cannot escape it. Symlinks inside the tree are
// followed within the root; an absolute-target symlink is re-rooted (and so
// effectively rejected) — os.Root's documented containment tradeoff.
func OpenRegularIn(root *os.Root, rel string) (*os.File, os.FileInfo, error) {
	return finishOpen(root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0))
}

// OpenDirRootNoFollow anchors an os.Root at dir WITHOUT following a symlink
// that something untrusted may have swapped in for dir's final component after
// it was classified as a directory. os.OpenRoot(dir) alone open(2)s the whole
// path and follows the final component, so a directory swapped to a symlink →
// external tree would anchor the whole walk OUTSIDE the tree that was selected
// — with an agent-writable source, a host-file exfiltration primitive. Anchor
// at the parent and descend the final component via openat (proot.OpenRoot),
// which refuses a component resolving OUTSIDE its root.
//
// A same-parent target is NOT outside proot, though: os.Root emulates a chroot
// and resolves a contained terminal symlink (e.g. proj → ../.ssh under the same
// home), so the parent-anchored openat alone still leaks anything beneath the
// parent. Two guards close that:
//   - Lstat the final component and reject a symlink present up front.
//   - After opening, require the opened root to be the SAME directory (identity
//     by os.SameFile) the Lstat saw. A swap in the Lstat→open window — to a
//     contained symlink OR to a different real directory — lands the open on a
//     different inode, which the identity check refuses. The returned root then
//     holds an fd to that one verified directory; later path swaps can't move
//     it, because every interior open is openat-relative to this fd.
//
// The companion rule: enumerate the tree through the returned root
// (fs.WalkDir(root.FS(), ".")), never by re-walking the pathname — a pathname
// walk observes whatever is at the path NOW while the opens ride this root,
// and the two can be different directories.
func OpenDirRootNoFollow(dir string) (*os.Root, error) {
	// Clean first: a trailing slash (skill `path` values are not Clean'd
	// upstream) makes filepath.Dir(dir) return dir itself and Base the leaf,
	// so the split below would look for <dir>/<leaf> and ENOENT. os.OpenRoot
	// tolerated a trailing slash; this must too.
	dir = filepath.Clean(dir)
	parent, base := filepath.Dir(dir), filepath.Base(dir)
	proot, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	defer proot.Close() // the child root below holds its own descriptor
	return openChildNoFollow(proot, base, dir)
}

// openChildNoFollow descends ONE component of parent and returns a root
// anchored on it, refusing a symlink there — both the one already standing at
// the name and the one swapped in behind the check. display names the
// directory the way the caller's user knows it, for the error.
//
// Both guards are load-bearing and neither replaces the other:
//
//   - Lstat first, because an ESCAPING symlink is refused by OpenRoot anyway
//     but an in-root one would be silently followed (os.Root emulates a chroot
//     and resolves a contained terminal symlink), and every caller classified
//     this as a real directory before arriving.
//   - Identity after, because the Lstat→open window is real: name swapped to a
//     contained symlink, or a different real directory renamed onto it, and
//     the open lands on an inode the Lstat never saw. Verified against this Go
//     toolchain, not assumed.
//
// It lives here as one function because it was retyped once and the retyped
// copy dropped the identity half -- exactly the silent-inheritance this
// package exists to stop. Every no-follow descent goes through here.
func openChildNoFollow(parent *os.Root, name, display string) (*os.Root, error) {
	li, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: %w", display, ErrSymlinkRoot)
	}
	croot, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	ci, err := croot.Stat(".")
	if err != nil {
		croot.Close()
		return nil, err
	}
	if !os.SameFile(li, ci) {
		croot.Close()
		return nil, fmt.Errorf("%s: %w", display, ErrSymlinkRoot)
	}
	return croot, nil
}

// OpenLockFile opens the advisory-lock file at path, creating it if absent,
// and refuses to follow a symlink standing at the name. A lock file is byre's
// own bookkeeping, living in a directory a --self-edit box can write: it is
// never legitimately a symlink, and a FIFO planted at the name would hang a
// plain open forever with the lock still unheld. Neither refusal is a
// judgement worth re-making per call site, so both ride the signature.
//
// The returned FileInfo is the descriptor's own — hand it to SameFileAt after
// locking rather than stat'ing the path a second time, so the "am I still
// holding what this name means?" question is asked of one inode byre obtained
// safely, not of two independent lookups.
func OpenLockFile(path string) (*os.File, os.FileInfo, error) {
	return finishOpen(os.OpenFile(path,
		os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644))
}

// SameFileAt reports whether path still names the inode described by fi,
// WITHOUT following a symlink at the final component — a name that now holds
// a symlink is by definition not the file that was opened with O_NOFOLLOW.
// A missing path answers false rather than erroring: "gone" and "replaced"
// are the same answer to the caller's question, and only a lookup that fails
// for some other reason is worth propagating.
func SameFileAt(path string, fi os.FileInfo) (bool, error) {
	cur, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(fi, cur), nil
}

// ReadFileBounded opens path per OpenRegular (fd-judged; follow per the
// caller's trust ruling) and reads at most limit bytes. Overflow fails
// loudly -- an oversize file is never truncated and parsed, because a
// silently-partial config is worse than a refused one. The fstat gate is
// advisory (the size can move after the stat); the limited read is what
// actually bounds allocation, and the post-read check catches growth.
func ReadFileBounded(path string, follow bool, limit int64) ([]byte, error) {
	f, fi, err := OpenRegular(path, follow)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes (limit %d)", path, fi.Size(), limit)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s exceeds the %d byte limit", path, limit)
	}
	return b, nil
}

// finishOpen is the judgment half: fstat the descriptor, require a regular
// file, and never leak the handle on a refusal. O_NONBLOCK made the open of
// a FIFO return instead of hanging; it is a no-op for regular-file reads.
func finishOpen(f *os.File, err error) (*os.File, os.FileInfo, error) {
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%w (mode %s)", ErrNotRegular, fi.Mode())
	}
	return f, fi, nil
}
