package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/project"
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
	if ok, err := branchExists(repo, "feat"); err != nil || !ok {
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
	if ok, err := branchExists(repo, "remotefeat"); err != nil || ok {
		t.Fatal("precondition: no local remotefeat expected")
	}
	if ok, err := remoteBranchExists(repo, "remotefeat"); err != nil || !ok {
		t.Fatalf("remote-only branch not detected as existing (would fork a divergent branch): ok=%v err=%v", ok, err)
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

// The sandbox-escape regression (2026-07-18): the host-side worktree add must
// run NOTHING agent-controlled. An agent with repo write access can plant a
// post-checkout hook and a smudge filter that git would otherwise run AS THE
// HOST USER during checkout — host code execution from inside the box.
func TestCreateWorktreeRunsNoAgentCodeOnHost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// A tracked file with a smudge filter, and a post-checkout hook — both the
	// agent's to write (repo + common git dir are rw from the box).
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("* filter=pwn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-C", repo, "-c", "user.email=a@b.c", "-c", "user.name=x", "add", "-A")
	run("-C", repo, "-c", "user.email=a@b.c", "-c", "user.name=x", "commit", "-q", "-m", "payload")

	hookProof := filepath.Join(t.TempDir(), "hook-ran")
	smudgeProof := filepath.Join(t.TempDir(), "smudge-ran")
	refTxnProof := filepath.Join(t.TempDir(), "reftxn-ran")
	run("-C", repo, "config", "filter.pwn.smudge", "sh -c 'touch "+smudgeProof+"; cat'")
	hookDir := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "post-checkout"),
		[]byte("#!/bin/sh\ntouch "+hookProof+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// reference-transaction fires during worktree add's ref updates EVEN under
	// --no-checkout (git 2.39.5) — so it proves the empty core.hooksPath is
	// load-bearing, not the post-checkout hook (which --no-checkout alone stops).
	// This test would fail if createWorktree dropped the hooksPath override.
	if err := os.WriteFile(filepath.Join(hookDir, "reference-transaction"),
		[]byte("#!/bin/sh\ntouch "+refTxnProof+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-hostile")
	t.Cleanup(func() { os.RemoveAll(target) })
	if err := createWorktree(io.Discard, repo, "hostile", target); err != nil {
		t.Fatalf("createWorktree: %v", err)
	}

	if _, err := os.Stat(hookProof); err == nil {
		t.Fatal("post-checkout hook executed on the host — sandbox escape")
	}
	if _, err := os.Stat(smudgeProof); err == nil {
		t.Fatal("smudge filter executed on the host — sandbox escape")
	}
	if _, err := os.Stat(refTxnProof); err == nil {
		t.Fatal("reference-transaction hook executed on the host — the empty core.hooksPath is not doing its job")
	}
	// --no-checkout leaves the working tree empty (populated in-box later).
	if _, err := os.Stat(filepath.Join(target, "tracked.txt")); err == nil {
		t.Fatal("working tree was checked out on the host (want --no-checkout)")
	}
	// The pending-checkout marker is present so the launcher populates it. The
	// admin dir is resolved (the marker is written under the symlink-resolved
	// common dir), so resolve before checking.
	gitdir, err := exec.Command("git", "-C", target, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	adminDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(gitdir)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(adminDir, needsCheckoutMarker)); err != nil {
		t.Fatalf("pending-checkout marker not written: %v", err)
	}
}

// The marker write must not follow a symlink: the admin dir is agent-writable
// (common git dir bound rw), so a concurrent box could pre-plant the marker
// name as a symlink and a naive write would clobber its target as the host
// user (codex review). O_EXCL|O_NOFOLLOW through an os.Root refuses it.
func TestMarkNeedsCheckoutRefusesSymlink(t *testing.T) {
	repo := initRepo(t)
	target := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-sym")
	t.Cleanup(func() {
		os.RemoveAll(target)
		_ = exec.Command("git", "-C", repo, "worktree", "prune").Run()
	})
	// Create the worktree by hand (so we control the marker-write timing), then
	// pre-plant the marker name as a symlink to a victim file.
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "--no-checkout", "-b", "sym", target).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	t.Setenv("BYRE_HOME", t.TempDir())
	wt, err := project.Resolve(target)
	if err != nil || wt.CommonGitDirHost == "" {
		t.Fatalf("resolving worktree: %v (common=%q)", err, wt.CommonGitDirHost)
	}
	adminOut, err := exec.Command("git", "-C", target, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	adminName := filepath.Base(strings.TrimSpace(string(adminOut)))
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-plant the marker NAME as a symlink at the exact path the write targets.
	if err := os.Symlink(victim, filepath.Join(wt.CommonGitDirHost, "worktrees", adminName, needsCheckoutMarker)); err != nil {
		t.Fatal(err)
	}
	if err := markNeedsCheckout(wt.CommonGitDirHost, target); err == nil {
		t.Fatal("markNeedsCheckout followed a pre-planted symlink instead of refusing")
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("victim file was clobbered through the symlink: %q", b)
	}
}

// The pre-create engine gate: no engine → refuse, nothing created.
func TestWorktreeRefusesWithoutEngine(t *testing.T) {
	repo := initRepo(t)
	// A PATH with git (needed for the toplevel/branch probes) but no engine.
	bin := t.TempDir()
	if p, err := exec.LookPath("git"); err == nil {
		if err := os.Symlink(p, filepath.Join(bin, "git")); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("BYRE_HOME", t.TempDir())
	target := filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-noeng")
	err := Worktree(discardStreams(), repo, "noeng", target, false)
	if err == nil || !strings.Contains(err.Error(), "needs a container engine") {
		t.Fatalf("want engine refusal, got %v", err)
	}
	if _, serr := os.Stat(target); serr == nil {
		t.Fatal("worktree was created despite the engine refusal")
	}
}

// codex rounds 2-3: no component of the admin path BELOW the common git dir
// (worktrees, <name>, or the marker entry) may be a followed symlink that
// escapes the common dir — anchoring os.Root on the common dir refuses each.
func TestWriteCheckoutMarkerRefusesEscapingComponents(t *testing.T) {
	// Sanity: a real admin dir under the common dir gets its marker.
	common := t.TempDir()
	if err := os.MkdirAll(filepath.Join(common, "worktrees", "name"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckoutMarker(common, "name"); err != nil {
		t.Fatalf("writeCheckoutMarker on a real dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(common, "worktrees", "name", needsCheckoutMarker)); err != nil {
		t.Fatalf("marker not written to the real admin dir: %v", err)
	}

	// Each escaping-symlink swap BELOW the common dir must be refused with no
	// file created in the attacker dir: the leaf (<name>), AND the intermediate
	// (worktrees) — the level codex's round-3 finding escalated to.
	for _, tc := range []struct {
		name string
		mk   func(t *testing.T, common, evil string)
	}{
		{"leaf <name> swapped", func(t *testing.T, common, evil string) {
			if err := os.MkdirAll(filepath.Join(common, "worktrees"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(evil, filepath.Join(common, "worktrees", "wt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"intermediate worktrees swapped", func(t *testing.T, common, evil string) {
			if err := os.Symlink(evil, filepath.Join(common, "worktrees")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common := t.TempDir()
			evil := t.TempDir()
			tc.mk(t, common, evil)
			if err := writeCheckoutMarker(common, "wt"); err == nil {
				t.Fatal("write followed a symlink escaping the common git dir")
			}
			if _, err := os.Stat(filepath.Join(evil, needsCheckoutMarker)); err == nil {
				t.Fatal("marker created in the attacker directory")
			}
		})
	}
}
