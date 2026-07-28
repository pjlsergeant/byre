package commands

import (
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
// project DIRECTORY at the point they need the root set. A directory that
// won't resolve degrades to the empty set rather than failing the command:
// every caller here is about to resolve the same paths itself and fail on its
// own terms with a better message, and a resolution failure is not evidence
// of a shadowed binary.
func boxWritableRootsFor(projectDir string) hostexec.Roots {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return hostexec.NewRoots()
	}
	return boxWritableRoots(paths)
}
