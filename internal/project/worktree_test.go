package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
