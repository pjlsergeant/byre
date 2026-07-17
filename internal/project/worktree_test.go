package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// makeWorktree fabricates git's on-disk worktree layout (no git binary needed):
// a main repo with a real .git dir, a worktrees/<name> git dir with a commondir
// file, and a worktree whose .git is a pointer file. It returns the main and
// worktree paths.
func makeWorktree(t *testing.T, name string) (main, wt string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	wt = filepath.Join(root, name)
	gd := filepath.Join(main, ".git", "worktrees", name)
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// commondir is relative (as git writes it): worktrees/<name> -> ../.. = .git
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, wt
}

func TestResolveWorktreeInheritsMainIdentity(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	main, wt := makeWorktree(t, "feature")

	mainPaths, err := Resolve(main)
	if err != nil {
		t.Fatal(err)
	}
	wtPaths, err := Resolve(wt)
	if err != nil {
		t.Fatal(err)
	}

	// Identity (id/dir/config store) is inherited from the main worktree.
	if wtPaths.ID != mainPaths.ID {
		t.Fatalf("worktree id %q != main id %q (should inherit)", wtPaths.ID, mainPaths.ID)
	}
	if wtPaths.Dir != mainPaths.Dir || wtPaths.LockFile != mainPaths.LockFile {
		t.Fatalf("worktree store paths not inherited: %+v vs %+v", wtPaths, mainPaths)
	}
	if wtPaths.Canonical != mainPaths.Canonical {
		t.Fatalf("worktree Canonical %q != main %q", wtPaths.Canonical, mainPaths.Canonical)
	}

	// Per-worktree side stays local.
	if !wtPaths.IsWorktree {
		t.Fatal("IsWorktree = false for a linked worktree")
	}
	wantWork, _ := Canonicalize(wt)
	if wtPaths.WorkDir != wantWork {
		t.Fatalf("WorkDir = %q, want %q", wtPaths.WorkDir, wantWork)
	}
	if wtPaths.WorktreeID == wtPaths.ID {
		t.Fatalf("WorktreeID %q must differ from the project ID for a worktree", wtPaths.WorktreeID)
	}
	wantCommon, _ := Canonicalize(filepath.Join(main, ".git"))
	if gotCommon, _ := Canonicalize(wtPaths.CommonGitDir); gotCommon != wantCommon {
		t.Fatalf("CommonGitDir = %q, want %q", wtPaths.CommonGitDir, wantCommon)
	}

	// The main worktree itself is not flagged as one, and its local fields mirror
	// identity.
	if mainPaths.IsWorktree {
		t.Fatal("main worktree wrongly detected as a linked worktree")
	}
	if mainPaths.WorkDir != mainPaths.Canonical || mainPaths.WorktreeID != mainPaths.ID {
		t.Fatal("plain project should have WorkDir==Canonical and WorktreeID==ID")
	}
}

func TestResolveSubmoduleIsStandalone(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	root := t.TempDir()
	sub := filepath.Join(root, "super", "sub")
	// A submodule's git dir lives under .git/modules and has NO commondir file.
	gd := filepath.Join(root, "super", ".git", "modules", "sub")
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// git writes a RELATIVE pointer for submodules.
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := Resolve(sub)
	if err != nil {
		t.Fatalf("submodule resolve errored: %v", err)
	}
	if paths.IsWorktree {
		t.Fatal("submodule wrongly detected as a worktree")
	}
}

func TestResolvePlainRepoAndNonRepoStandalone(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	// Plain repo: .git is a real directory.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := Resolve(repo); err != nil || p.IsWorktree {
		t.Fatalf("plain repo: IsWorktree=%v err=%v", p.IsWorktree, err)
	}
	// Non-repo: no .git at all.
	if p, err := Resolve(t.TempDir()); err != nil || p.IsWorktree {
		t.Fatalf("non-repo: IsWorktree=%v err=%v", p.IsWorktree, err)
	}
}

func TestResolveDanglingWorktreeErrors(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	root := t.TempDir()
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	// Points into a worktrees/<name> dir that does not exist (moved main repo).
	dead := filepath.Join(root, "gone", ".git", "worktrees", "wt")
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+dead+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(wt)
	if err == nil {
		t.Fatal("expected an error for dangling worktree metadata, got nil (would silently mint a standalone identity)")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("error should mention worktree/repair, got: %v", err)
	}
}

// Security: a FORGED .git + commondir must never become an arbitrary rw host
// mount. The agent (project mounted rw) plants a project-local .git pointing
// at a worktrees/<name> dir it controls, whose commondir names an unrelated
// host dir it wants mounted — even with a consistent back-pointer. The
// structural check (gitDir must be <common>/worktrees/<name>) must reject it.
func TestDetectWorktreeRejectsForgedCommondir(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret") // the dir the attacker wants bound
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "proj")
	gd := filepath.Join(proj, "fake", "worktrees", "x") // agent-writable
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "commondir"), []byte(secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A consistent back-pointer too — the agent can forge this; the structural
	// check must still catch the escape.
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(filepath.Join(proj, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".git"), []byte("gitdir: "+gd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, ok, err := detectWorktree(proj)
	if err == nil {
		t.Fatalf("forged commondir accepted (ok=%v) — would mount %q rw", ok, info.commonGitDir)
	}
	if info.commonGitDir == secret {
		t.Fatalf("forged commondir leaked as the mount target: %q", secret)
	}
}

// A project .git that is itself a SYMLINK is never treated as a worktree
// pointer: following it (ReadFile + the reciprocal Stat) would let an agent
// point .git at genuine external worktree metadata and pass every check,
// turning it into an arbitrary rw host mount. It must resolve to standalone.
func TestDetectWorktreeRejectsSymlinkedDotGit(t *testing.T) {
	// A genuine external worktree (valid, consistent metadata).
	extMain, extWT := makeWorktree(t, "wt")
	_ = extMain
	// The victim project: its .git is a SYMLINK to the genuine worktree's .git.
	proj := t.TempDir()
	if err := os.Symlink(filepath.Join(extWT, ".git"), filepath.Join(proj, ".git")); err != nil {
		t.Fatal(err)
	}
	info, ok, err := detectWorktree(proj)
	if err != nil {
		t.Fatalf("a symlinked .git must resolve to standalone, not error: %v", err)
	}
	if ok || info.commonGitDir != "" {
		t.Fatalf("symlinked .git was treated as a worktree: ok=%v mount=%q", ok, info.commonGitDir)
	}
}

// A .git that is a FIFO (an agent can swap it in) must not block detection:
// O_NONBLOCK returns immediately and the regular-file gate resolves standalone.
// Timeout-guarded so a regression (dropping O_NONBLOCK) fails, not hangs.
func TestDetectWorktreeDotGitFifoDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".git"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, ok, err := detectWorktree(dir)
		if ok {
			err = fmt.Errorf("a FIFO .git was treated as a worktree")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("detectWorktree blocked opening a FIFO .git — O_NONBLOCK regression")
	}
}

// The reciprocal back-pointer is validated too: a structurally-plausible
// worktree whose <gitDir>/gitdir does NOT point back at <dir>/.git is rejected.
func TestDetectWorktreeRejectsBadBackPointer(t *testing.T) {
	main, wt := makeWorktree(t, "wt")
	gd := filepath.Join(main, ".git", "worktrees", "wt")
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(other+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := detectWorktree(wt); err == nil {
		t.Fatal("a worktree whose back-pointer names an unrelated file must be rejected")
	}
}

func TestParseGitdirPointer(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"gitdir: /abs/path\n", "/abs/path", true},
		{"gitdir: ../rel/path", "../rel/path", true},
		{"gitdir: /has space/x\n", "/has space/x", true},
		{"ref: refs/heads/main\n", "", false},
		{"gitdir: \n", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := parseGitdirPointer(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseGitdirPointer(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
