package commands

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestIsGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if isGitRepo(t.TempDir()) {
		t.Error("empty dir reported as a git repo")
	}
	if !isGitRepo(initRepo(t)) {
		t.Error("real repo not reported as a git repo")
	}
}
