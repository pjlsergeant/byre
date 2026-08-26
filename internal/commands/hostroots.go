package commands

import (
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/project"
)

// boxWritableRoots names the host directories a box for this project can
// write, for the one purpose of declining to RUN a host tool byre resolved
// out of one of them (hostexec). Four entries, each one byre resolved itself:
//
//   - WorkDir -- bound at /workspace, the box's whole point.
//   - Canonical -- the main worktree. Not mounted into a worktree's box, but
//     it is mounted into the box a session in the main tree gets, and the
//     shadowing file outlives the session that wrote it.
//   - CommonGitDirHost -- bound rw into every worktree box (runparams).
//   - Dir -- ~/.byre/projects/<id>, which --self-edit mounts rw. Included on
//     every invocation, not just a --self-edit one: byre does not record
//     whether a PAST session carried the flag, and a store that a prior
//     self-edit session wrote is exactly as shadowing as one this session
//     would. Nothing legitimate resolves a host binary out of byre's store,
//     so the wider set costs nothing.
//
// SIBLING worktrees are deliberately absent, and this is a departure from the
// obvious reading of "the project tree and its worktrees". The only listing of
// them is git's own -- `worktree list`, or the gitdir pointers under
// <common>/worktrees/ -- and both live in a directory the box writes. Taking
// the root set from there would hand the agent the pen: a planted pointer
// naming /usr/bin makes byre refuse to run docker at all. A refusal an
// untrusted party can aim is worse than the shadow it would close, and the
// tree the user is standing in (WorkDir), the main tree and the common git dir
// are the ones a PATH entry realistically points into.
func boxWritableRoots(paths project.Paths) hostexec.Roots {
	return hostexec.NewRoots(paths.WorkDir, paths.Canonical, paths.CommonGitDirHost, paths.Dir)
}

// boxWritableRootsFor is boxWritableRoots for the commands that hold only a
// project DIRECTORY at the point they need the root set.
//
// A Resolve failure must not empty the set. Resolve reads worktree metadata
// the box writes, so failing it is something an agent can ARRANGE -- corrupt
// the gitdir pointer and every root disappears, which would turn the check off
// for the very tree that turned it off. It is also not caught downstream in
// time: deliver spawns clipboard, picker and notifier helpers BEFORE any later
// path resolution fails.
//
// So the fallback is what needs no resolution at all: the directory the caller
// is standing in, both as spelled and canonicalized (containment is judged by
// identity, but a root that can't be stat'd degrades to a lexical test, and
// then the spelling matters). It is genuinely narrower -- the main tree, the
// common git dir and the store are precisely what Resolve computes, and a
// worktree-derived id would name the WRONG store -- but the tree a PATH entry
// realistically points into is this one, and a narrower set is a smaller hole
// than no set.
//
// "Narrower" inverts at one degenerate cwd: "/". A graphical launch (the
// deliver app's Dock icon, a .desktop entry) runs byre from the filesystem
// root, and a root of "/" contains every binary on the machine -- the check
// becomes a refusal to run anything, aimed by nobody. NewRoots drops that
// entry, so this fallback degrades to the empty set there: with no project
// in hand and no real directory to name, no-check is the honest answer
// (hostexec's own doctrine for a caller with nothing mounted rw).
func boxWritableRootsFor(projectDir string) hostexec.Roots {
	if paths, err := project.Resolve(projectDir); err == nil {
		return boxWritableRoots(paths)
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return hostexec.NewRoots()
	}
	dirs := []string{abs}
	// Canonicalize falls back to the cleaned absolute path on its own failure,
	// so this only ever ADDS a spelling; the duplicate is dropped.
	if canon, cerr := project.Canonicalize(abs); cerr == nil && canon != abs {
		dirs = append(dirs, canon)
	}
	return hostexec.NewRoots(dirs...)
}
