package project

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// worktreeInfo describes a detected linked git worktree: where its identity
// anchor (the main worktree) lives, and the common git dir byre must mount so
// git works inside the box.
type worktreeInfo struct {
	// mainDir is the canonical path of the main worktree — the identity anchor.
	// byre derives config/volumes/image from it so a worktree inherits the repo's
	// setup instead of re-onboarding. For a bare repo it is the git dir
	// itself (there is no working tree to anchor on).
	mainDir string
	// commonGitDir is the git common dir, exactly as git dereferences it, so it
	// can be bind-mounted at the same host path inside the box (see the mount
	// discussion in docs/adr/0009-worktrees-inherit-project-identity.md). The per-worktree git dir lives
	// under it, so mounting this one path makes both present.
	commonGitDir string
}

// detectWorktree inspects <dir>/.git to decide whether dir is a linked git
// worktree.
//
//   - (info, true, nil)  — dir is a linked worktree; inherit mainDir's identity.
//   - (_, false, nil)    — dir is a plain repo, a submodule, or not a repo at
//     all; treat it as its own standalone project (today's behaviour).
//   - (_, false, err)    — dir LOOKS like a worktree but its metadata is missing
//     (a moved main repo that has not been `git worktree repair`ed). byre refuses
//     rather than silently minting a standalone identity — a silent fallback would
//     quietly create a second, disconnected set of volumes.
//
// Detection reads git's on-disk files directly (no `git` binary dependency): a
// linked worktree's `.git` is a file `gitdir: <common>/worktrees/<name>`, and
// that dir holds a `commondir` file pointing back at the main git dir. Requiring
// BOTH the `.../worktrees/<name>` shape and a readable `commondir` excludes
// submodules, whose `.git` points at `.../modules/<name>` and has no `commondir`.
func detectWorktree(dir string) (worktreeInfo, bool, error) {
	gitPath := filepath.Join(dir, ".git")
	// Open the pointer ONCE, WITHOUT following symlinks, and bind every check
	// below (the content read AND the reciprocal fstat) to this one descriptor.
	// Separate Lstat / ReadFile / Stat calls would each re-resolve the path, so
	// an agent (which has /workspace rw) could swap .git for a symlink to
	// genuine external worktree metadata between them and pass every check —
	// turning it into an arbitrary rw host mount. O_NOFOLLOW fails (ELOOP) if
	// .git is itself a symlink; ENOENT if absent — both are standalone.
	// O_NONBLOCK so a .git swapped to a FIFO returns instead of blocking
	// forever on a writer (the gitInfo gate below then rejects it).
	gitFile, err := os.OpenFile(gitPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		// Only absence and the no-follow symlink rejection mean "standalone".
		// A genuine failure (permission, I/O, fd exhaustion) must surface, not
		// silently mint a separate identity and drop the worktree mount.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ELOOP) {
			return worktreeInfo{}, false, nil
		}
		return worktreeInfo{}, false, err
	}
	defer gitFile.Close()
	// gitInfo is this exact inode — reused for the regular-file gate and the
	// reciprocal SameFile, so nothing re-resolves the name.
	gitInfo, err := gitFile.Stat()
	if err != nil {
		return worktreeInfo{}, false, err // fstat of an open fd failing is a genuine error
	}
	// A genuine linked worktree's .git is a REGULAR FILE (the `gitdir:`
	// pointer). A real .git dir (main repo / plain repo) or anything else is
	// standalone.
	if !gitInfo.Mode().IsRegular() {
		return worktreeInfo{}, false, nil
	}

	data, err := io.ReadAll(gitFile)
	if err != nil {
		return worktreeInfo{}, false, err
	}
	gitDir, ok := parseGitdirPointer(string(data))
	if !ok {
		return worktreeInfo{}, false, nil // .git file but not a gitdir pointer; leave standalone
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	gitDir = filepath.Clean(gitDir)

	// A linked worktree's git dir is <common>/worktrees/<name>. This purely
	// textual check excludes submodules (parent dir is "modules") without
	// touching the filesystem, so a moved-and-broken worktree still trips the
	// error path below rather than being misread as standalone.
	if filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return worktreeInfo{}, false, nil
	}

	// From here dir is shaped like a worktree, so a missing target is an error,
	// not a silent standalone fallback. `commondir` both confirms it (submodules
	// lack it) and points at the common git dir.
	commonData, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return worktreeInfo{}, false, fmt.Errorf(
			"%s looks like a git worktree but its metadata is missing (%s): "+
				"if the main repository moved, run `git worktree repair` in it", dir, gitDir)
	}
	common := strings.TrimRight(string(commonData), "\r\n")
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	common = filepath.Clean(common)

	// Reject FORGED worktree metadata. `commonGitDir` becomes a same-path RW
	// host bind (runparams.go), and the agent can plant a project-local .git
	// file + commondir via its rw /workspace mount — so an unverified
	// commondir is an arbitrary-host-dir mount. A genuine linked worktree has
	// two structural invariants git guarantees, both checked here with
	// os.SameFile (inode identity — robust to symlinks and path spelling):
	//
	//  1. gitDir is exactly <common>/worktrees/<name>, i.e. the parent of
	//     gitDir's parent IS common. This is the security-critical one: to
	//     escape, the forged commondir must name a dir OUTSIDE the
	//     agent-writable tree — but gitDir (which must hold the commondir file
	//     we just read, so it is agent-writable) then cannot also sit inside
	//     that outside dir. The two requirements are mutually exclusive.
	//  2. <gitDir>/gitdir points back at <dir>/.git (git's reciprocal
	//     back-pointer). Belt-and-suspenders; matches `git worktree repair`.
	//
	// Inconsistent metadata is a loud error (never a silent standalone
	// fallback or a silent mount) — same stance as the missing-commondir case.
	//
	// Accepted residual: os.SameFile is inode identity, so a HARDLINK of a
	// genuine external worktree's .git into /workspace would pass. Unreachable
	// under byre's mounts — the agent sees only /workspace and explicit binds,
	// so it has no path to an external .git to hardlink (and hardlinks can't
	// cross filesystems); recorded so it isn't re-raised.
	structCommon := filepath.Dir(filepath.Dir(gitDir))
	sc, scErr := os.Stat(structCommon)
	cc, ccErr := os.Stat(common)
	if scErr != nil || ccErr != nil || !os.SameFile(sc, cc) {
		return worktreeInfo{}, false, fmt.Errorf(
			"%s has inconsistent git worktree metadata: commondir points at %q, "+
				"which is not the parent of the worktree git dir %q — refusing to mount it. "+
				"If the main repository moved, run `git worktree repair` in it", dir, common, gitDir)
	}
	backData, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return worktreeInfo{}, false, fmt.Errorf(
			"%s looks like a git worktree but its back-pointer is missing (%s/gitdir): "+
				"run `git worktree repair`", dir, gitDir)
	}
	back := strings.TrimRight(string(backData), "\r\n")
	bp, bpErr := os.Stat(back)
	// Compare against gitInfo — the inode we OPENED and read — not a fresh
	// os.Stat(gitPath), so a mid-check .git swap cannot satisfy this.
	if bpErr != nil || !os.SameFile(bp, gitInfo) {
		return worktreeInfo{}, false, fmt.Errorf(
			"%s git worktree back-pointer (%s/gitdir → %q) does not point back to it — "+
				"refusing to mount; run `git worktree repair`", dir, gitDir, back)
	}

	// The identity dir is the main worktree: the parent of a ".git"
	// common dir, or the common dir itself for a bare repo. Canonicalize
	// it so the id matches running byre in the main worktree directly.
	mainDir := common
	if filepath.Base(common) == ".git" {
		mainDir = filepath.Dir(common)
	}
	canonMain, err := Canonicalize(mainDir)
	if err != nil {
		return worktreeInfo{}, false, err
	}
	return worktreeInfo{mainDir: canonMain, commonGitDir: common}, true, nil
}

// parseGitdirPointer extracts the path from a `.git` file's `gitdir: <path>`
// line. The path may be absolute (worktrees) or relative (submodules); the
// caller resolves it. Only the trailing newline is trimmed — a path may
// legitimately contain spaces.
func parseGitdirPointer(content string) (string, bool) {
	line := strings.TrimRight(content, "\r\n")
	rest, ok := strings.CutPrefix(line, "gitdir: ")
	if !ok {
		return "", false
	}
	rest = strings.TrimRight(rest, "\r\n")
	if rest == "" {
		return "", false
	}
	return rest, true
}
