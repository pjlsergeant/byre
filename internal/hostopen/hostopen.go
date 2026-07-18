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
	"os"
	"syscall"
)

// ErrNotRegular reports that the opened path is not a regular file. Callers
// with skip-vs-fail semantics (deliver's tree walk) branch on it with
// errors.Is; everyone else just propagates.
var ErrNotRegular = errors.New("not a regular file")

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
