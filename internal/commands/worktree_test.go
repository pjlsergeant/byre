package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/project"
)

func TestWorktreeLeaf(t *testing.T) {
	if got := worktreeLeaf("/home/me/dev/byre", "feature"); got != "byre-feature" {
		t.Errorf("worktreeLeaf = %q, want byre-feature", got)
	}
	// Branch slashes are flattened so the worktree stays a single dir under base.
	if got := worktreeLeaf("/home/me/dev/byre", "fix/bug"); got != "byre-fix-bug" {
		t.Errorf("slash flattening: got %q, want byre-fix-bug", got)
	}
}

// worktreeParent resolves the three worktree_base states: unset (refuse),
// "sibling" (beside the repo), and a path (under it).
func TestWorktreeParent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	canon, _ := project.Canonicalize(repo)
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	set := func(v string) {
		if v == "" {
			os.Remove(filepath.Join(home, "default.config"))
			return
		}
		if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("worktree_base = \""+v+"\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	set("") // unset -> refuse (empty parent)
	if p, err := worktreeParent(repo, canon); err != nil || p != "" {
		t.Fatalf("unset: parent=%q err=%v, want empty", p, err)
	}
	set("sibling") // beside the repo
	if p, err := worktreeParent(repo, canon); err != nil || p != filepath.Dir(canon) {
		t.Fatalf("sibling: parent=%q err=%v, want %q", p, err, filepath.Dir(canon))
	}
	base := t.TempDir() // an explicit base path
	set(base)
	if p, err := worktreeParent(repo, canon); err != nil || p != base {
		t.Fatalf("path: parent=%q err=%v, want %q", p, err, base)
	}
}

// With neither --path nor a configured worktree_base, byre refuses rather than
// guessing a location (least surprise — no directories created unbidden).
func TestWorktreeRefusesWithoutLocation(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("BYRE_HOME", t.TempDir()) // empty ~/.byre -> no worktree_base
	err := Worktree(discardStreams(), repo, "feat", "", false)
	if err == nil {
		t.Fatal("expected refusal without --path or worktree_base")
	}
	if !strings.Contains(err.Error(), "byre config") || !strings.Contains(err.Error(), "--path") {
		t.Errorf("error should name both remedies (byre config / --path): %v", err)
	}
	// And it must refuse BEFORE creating anything.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-feat")); statErr == nil {
		t.Error("a worktree was created despite the refusal")
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

	if err := createWorktree(io.Discard, repo, "feat", target); err != nil {
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
		t.Errorf("worktree project dir %q != repo %q", p.Canonical, canonRepo)
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
	if err := createWorktree(io.Discard, repo, "existing", target); err != nil {
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
	if err := createWorktree(io.Discard, repo, "remotefeat", target); err != nil {
		t.Fatalf("createWorktree: %v", err)
	}
	// The worktree tracks the remote branch.
	up, err := exec.Command("git", "-C", target, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Output()
	if err != nil || strings.TrimSpace(string(up)) != "origin/remotefeat" {
		t.Errorf("worktree should track origin/remotefeat, upstream=%q err=%v", strings.TrimSpace(string(up)), err)
	}
}
