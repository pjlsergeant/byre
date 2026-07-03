package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// worktreeInfo describes a detected linked git worktree: where its identity
// anchor (the main worktree) lives, and the common git dir byre must mount so
// git works inside the box.
type worktreeInfo struct {
	// familyDir is the canonical path of the main worktree — the identity anchor.
	// byre derives config/volumes/image from it so a worktree inherits the repo's
	// setup instead of re-onboarding. For a bare-repo family it is the git dir
	// itself (there is no working tree to anchor on).
	familyDir string
	// commonGitDir is the git common dir, exactly as git dereferences it, so it
	// can be bind-mounted at the same host path inside the box (see the mount
	// discussion in docs/agent-volume-sharing.md). The per-worktree git dir lives
	// under it, so mounting this one path makes both present.
	commonGitDir string
}

// detectWorktree inspects <dir>/.git to decide whether dir is a linked git
// worktree.
//
//   - (info, true, nil)  — dir is a linked worktree; inherit familyDir's identity.
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
	fi, err := os.Lstat(gitPath)
	if err != nil {
		return worktreeInfo{}, false, nil // no .git — not a repo; standalone
	}
	if fi.IsDir() {
		return worktreeInfo{}, false, nil // a real .git dir — main worktree / plain repo
	}

	data, err := os.ReadFile(gitPath)
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

	// The family (identity) dir is the main worktree: the parent of a ".git"
	// common dir, or the common dir itself for a bare-repo family. Canonicalize
	// it so the id matches running byre in the main worktree directly.
	familyDir := common
	if filepath.Base(common) == ".git" {
		familyDir = filepath.Dir(common)
	}
	canonFamily, err := Canonicalize(familyDir)
	if err != nil {
		return worktreeInfo{}, false, err
	}
	return worktreeInfo{familyDir: canonFamily, commonGitDir: common}, true, nil
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
