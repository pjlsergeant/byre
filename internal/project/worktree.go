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
	// commonGitDir is the git common dir as git records it (the structural
	// dir(dir(gitDir))), used as the bind mount TARGET — the in-box path every
	// git pointer resolves against, so it must match git's on-disk metadata and
	// is deliberately NOT symlink-resolved (see the mount discussion in
	// docs/adr/0009-worktrees-inherit-project-identity.md). The per-worktree git dir lives under it, so
	// mounting this one path makes both present.
	commonGitDir string
	// commonGitDirHost is the same directory fully symlink-resolved
	// (Canonicalize), used as the bind mount SOURCE. The target above is
	// derived from attacker-controlled .git contents and may contain symlink
	// components an agent could retarget between validation and the engine
	// resolving the source (a check-to-mount race → arbitrary rw host mount).
	// Resolving the source removes every symlink component, so there is nothing
	// left to flip — matching how WorkDir's mount source is already
	// canonicalized. ADR 0009's "same-path" constraint is about the TARGET;
	// the source host path is free to differ.
	commonGitDirHost string
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
	// Read the pointer via readMetaFile, WITHOUT following symlinks, binding
	// the content read and the regular-file gate to one opened descriptor
	// (gitInfo, reused for the reciprocal SameFile below, is that exact
	// inode). Separate Lstat / ReadFile / Stat calls would each re-resolve
	// the path, so an agent (which has /workspace rw) could swap .git for a
	// symlink to genuine external worktree metadata between them and pass
	// every check — turning it into an arbitrary rw host mount.
	data, gitInfo, err := readMetaFile(gitPath)
	if err != nil {
		// "Not a worktree pointer" shapes resolve standalone: absent (ENOENT),
		// a symlink (ELOOP from O_NOFOLLOW), non-regular (a real .git DIR of a
		// plain repo, or a FIFO an agent swapped in), or implausibly large (a
		// genuine pointer is one short line). A genuine failure (permission,
		// I/O, fd exhaustion) must surface, not silently mint a separate
		// identity and drop the worktree mount.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ELOOP) ||
			errors.Is(err, errMetaNotRegular) || errors.Is(err, errMetaTooLarge) {
			return worktreeInfo{}, false, nil
		}
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

	// From here dir is shaped like a worktree, so an unreadable target is an
	// error, not a silent standalone fallback. `commondir` both confirms it
	// (submodules lack it) and points at the common git dir. gitDir came from
	// agent-writable content, so this read (and the gitdir one below) gets the
	// same hostile-filesystem discipline as .git — a plain os.ReadFile here
	// would follow symlinks, block forever on a FIFO, and read a sparse or
	// device-backed file without bound (a host hang/OOM, not an escape: the
	// structural checks below stop the mount).
	commonData, _, err := readMetaFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return worktreeInfo{}, false, fmt.Errorf(
			"%s looks like a git worktree but its metadata is missing or unreadable (%v): "+
				"if the main repository moved, run `git worktree repair` in it", dir, err)
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
	// From here on use structCommon, NOT common, as the common git dir TARGET.
	// Both name the same directory (SameFile just proved it), but `common` is
	// the raw commondir-file content — attacker-authored — while structCommon
	// is byre's own dir(dir(gitDir)), exactly the git-recorded path the
	// same-path mount target must use for git to resolve inside the box.
	// structCommon is still NOT safe to use as the mount SOURCE: it is derived
	// from gitDir (the .git pointer, attacker-controlled) and may contain
	// symlink components an agent could retarget between the SameFile check and
	// the engine resolving the source. So the source is canonicalized below
	// (commonGitDirHost) — symlink-free, nothing left to flip — while the
	// target stays structCommon.
	backData, _, err := readMetaFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return worktreeInfo{}, false, fmt.Errorf(
			"%s looks like a git worktree but its back-pointer is missing or unreadable (%v): "+
				"run `git worktree repair`", dir, err)
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
	mainDir := structCommon
	if filepath.Base(structCommon) == ".git" {
		mainDir = filepath.Dir(structCommon)
	}
	canonMain, err := Canonicalize(mainDir)
	if err != nil {
		return worktreeInfo{}, false, err
	}
	// Symlink-resolved source for the RW bind (see commonGitDirHost). For the
	// common case (a symlink-free path) this equals structCommon, so nothing
	// changes; when it differs, docker binds the real directory and no symlink
	// component survives for an agent to retarget mid-mount.
	hostCommon, err := Canonicalize(structCommon)
	if err != nil {
		return worktreeInfo{}, false, err
	}
	return worktreeInfo{
		mainDir:          canonMain,
		commonGitDir:     structCommon,
		commonGitDirHost: hostCommon,
	}, true, nil
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

// maxMetaFileSize caps reads of git worktree metadata (.git pointer,
// commondir, gitdir). All three are single-line path files, and PATH_MAX is
// 4 KiB, so 1 MiB is generous for anything git could have written.
const maxMetaFileSize = 1 << 20

// Rejection shapes readMetaFile distinguishes (wrapped with the offending
// path). Callers pick the disposition: detectWorktree treats them as "not a
// worktree pointer" (standalone) for .git, but as loud metadata errors for
// commondir/gitdir, where the shape already committed to being a worktree.
var (
	errMetaNotRegular = errors.New("not a regular file")
	errMetaTooLarge   = errors.New("implausibly large for git worktree metadata")
)

// readMetaFile reads a git worktree metadata file under hostile-filesystem
// discipline. Every path it is used on is agent-writable or named by
// agent-writable content, so each shape below is one an agent can stage to
// hang or OOM the host-side byre process:
//
//   - O_NOFOLLOW: git writes these as plain files, never symlinks; a symlink
//     (e.g. to /dev/zero, or to genuine external metadata) fails with ELOOP.
//   - O_NONBLOCK: a FIFO opens immediately instead of blocking forever on a
//     writer that never comes (regular-file reads are unaffected).
//   - fstat on the OPENED descriptor gates to regular files, so a FIFO or
//     device that got past open is rejected before any read.
//   - the read is capped at maxMetaFileSize: os.ReadFile-style read-to-EOF
//     would balloon on a sparse file's apparent size or read /dev/zero
//     forever.
//
// The returned FileInfo is the opened inode's — valid after close, and usable
// with os.SameFile so later checks need not re-resolve the name.
func readMetaFile(path string) ([]byte, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err // fstat of an open fd failing is a genuine error
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: %w", path, errMetaNotRegular)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxMetaFileSize+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxMetaFileSize {
		return nil, nil, fmt.Errorf("%s: %w", path, errMetaTooLarge)
	}
	return data, info, nil
}
