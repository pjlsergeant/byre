package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/project"
)

func TestDefaultWorktreePath(t *testing.T) {
	got := defaultWorktreePath("/home/me/dev/byre", "feature")
	if want := "/home/me/dev/byre-feature"; got != want {
		t.Errorf("defaultWorktreePath = %q, want %q", got, want)
	}
	// Branch slashes are flattened so the worktree stays a single sibling dir.
	got = defaultWorktreePath("/home/me/dev/byre", "fix/bug")
	if want := "/home/me/dev/byre-fix-bug"; got != want {
		t.Errorf("slash flattening: got %q, want %q", got, want)
	}
}

// initRepo makes a real git repo with one commit (git is available on dev hosts;
// the createWorktree path shells out to it).
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"-C", dir, "init", "-q"},
		{"-C", dir, "-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCreateWorktreeNewBranch(t *testing.T) {
	repo := initRepo(t)
	target := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-feat")
	t.Cleanup(func() { os.RemoveAll(target) })

	if err := createWorktree(repo, "feat", target); err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	// The target is now a real linked worktree that byre detects and inherits from.
	t.Setenv("BYRE_HOME", t.TempDir())
	p, err := project.Resolve(target)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsWorktree {
		t.Fatal("created dir is not detected as a linked worktree")
	}
	canonRepo, _ := project.Canonicalize(repo)
	if p.Canonical != canonRepo {
		t.Errorf("worktree family %q != repo %q", p.Canonical, canonRepo)
	}
	// The new branch exists.
	if !branchExists(repo, "feat") {
		t.Error("expected branch 'feat' to be created")
	}
}

func TestCreateWorktreeExistingBranch(t *testing.T) {
	repo := initRepo(t)
	if out, err := exec.Command("git", "-C", repo, "branch", "existing").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	target := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-existing")
	t.Cleanup(func() { os.RemoveAll(target) })

	// Should check out the existing branch (not fail trying to -b create it).
	if err := createWorktree(repo, "existing", target); err != nil {
		t.Fatalf("createWorktree on existing branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Errorf("worktree .git not present: %v", err)
	}
}

func TestGitToplevel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, ok := gitToplevel(t.TempDir()); ok {
		t.Error("empty dir reported as a git repo")
	}
	repo := initRepo(t)
	// From a SUBDIRECTORY, toplevel must resolve to the repo root — otherwise the
	// default worktree path would anchor inside the repo instead of beside it.
	sub := filepath.Join(repo, "pkg", "inner")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	top, ok := gitToplevel(sub)
	if !ok {
		t.Fatal("subdir of a repo not recognized as inside a repo")
	}
	canonRepo, _ := project.Canonicalize(repo)
	canonTop, _ := project.Canonicalize(top)
	if canonTop != canonRepo {
		t.Errorf("toplevel from subdir = %q, want repo root %q", canonTop, canonRepo)
	}
}

// TestCreateWorktreeRemoteBranch covers the DWIM footgun: a name that exists only
// as a remote branch must be checked out (tracking the remote), NOT forked as a
// new local branch off HEAD.
func TestCreateWorktreeRemoteBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "main")
	run := func(args ...string) {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--bare", origin)
	run("init", "-q", repo)
	run("-C", repo, "-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "--allow-empty", "-m", "init")
	run("-C", repo, "remote", "add", "origin", origin)
	run("-C", repo, "push", "-q", "origin", "HEAD:refs/heads/remotefeat")
	run("-C", repo, "fetch", "-q", "origin")

	// No LOCAL remotefeat, but a remote one exists -> should be detected as existing.
	if branchExists(repo, "remotefeat") {
		t.Fatal("precondition: no local remotefeat expected")
	}
	if !branchOrRemoteExists(repo, "remotefeat") {
		t.Fatal("remote-only branch not detected as existing (would fork a divergent branch)")
	}
	target := filepath.Join(root, "wt")
	if err := createWorktree(repo, "remotefeat", target); err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	// The worktree tracks the remote branch.
	up, err := exec.Command("git", "-C", target, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Output()
	if err != nil || strings.TrimSpace(string(up)) != "origin/remotefeat" {
		t.Errorf("worktree should track origin/remotefeat, upstream=%q err=%v", strings.TrimSpace(string(up)), err)
	}
}
