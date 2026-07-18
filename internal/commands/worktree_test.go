package commands

import (
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

// initRepo makes a real git repo with one commit (git is available on dev
// hosts; the host-side probes and the tests' own fixtures use it).
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

// The pre-create engine gate: no engine → refuse, nothing created. Doubly
// load-bearing now — creation itself runs in the box.
func TestWorktreeRefusesWithoutEngine(t *testing.T) {
	repo := initRepo(t)
	// A PATH with git (needed for the toplevel/registration probes) but no engine.
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

// worktreeCreate assembles the create step correctly: image built first (under
// the setup lock), then the create container with the repo's three paths and
// the resolved identity — and the target dir made host-side as the mount point.
func TestWorktreeCreateAssemblesContainer(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "wt")
	f := &fakeRunner{}
	if err := worktreeCreate(f, discardStreams(), paths, repo, "feat", target); err != nil {
		t.Fatalf("worktreeCreate: %v", err)
	}
	if len(f.builds) != 1 {
		t.Fatalf("want 1 image build, got %v", f.builds)
	}
	if len(f.worktreeAdds) != 1 {
		t.Fatalf("want 1 WorktreeAdd, got %v", f.worktreeAdds)
	}
	// Build strictly before the create container (it runs from that image).
	if f.ops[0] != "build "+f.builds[0] || !strings.HasPrefix(f.ops[len(f.ops)-1], "worktreeadd ") {
		t.Errorf("ops order: %v", f.ops)
	}
	rec := f.worktreeAdds[0]
	gd, _ := filepath.EvalSymlinks(filepath.Join(paths.Canonical, ".git"))
	for _, want := range []string{
		f.builds[0] + " ", // the just-built image
		" byre-wtadd-",    // a create name, never a session name
		gd + "->" + filepath.Join(paths.Canonical, ".git") + " ", // common dir: resolved source -> recorded target
		" " + paths.Canonical + " ",                              // main tree
		" " + target + " ",                                       // target
		" feat",                                                  // the branch
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("WorktreeAdd record missing %q: %s", want, rec)
		}
	}
	// Rootful engine: the host identity rides the create container.
	if id := f.worktreeIdents[0]; id.UID != os.Getuid() || id.GID != os.Getgid() || id.KeepID {
		t.Errorf("create identity = %+v, want host identity", id)
	}
	// The target was made host-side, empty — the box does the git.
	entries, rerr := os.ReadDir(target)
	if rerr != nil || len(entries) != 0 {
		t.Errorf("target should be an empty mount-point dir: err=%v entries=%d", rerr, len(entries))
	}
}

// Run from a LINKED worktree, the create step still anchors on the main tree:
// its canonical path and its validated common git dir (project.Resolve's),
// never the current worktree's.
func TestWorktreeCreateFromLinkedWorktree(t *testing.T) {
	repo := initRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "other", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(linked)
	if err != nil {
		t.Fatal(err)
	}
	if !paths.IsWorktree {
		t.Fatal("fixture: linked dir not detected as a worktree")
	}
	target := filepath.Join(t.TempDir(), "wt")
	f := &fakeRunner{}
	if err := worktreeCreate(f, discardStreams(), paths, linked, "feat", target); err != nil {
		t.Fatalf("worktreeCreate: %v", err)
	}
	rec := f.worktreeAdds[0]
	if !strings.Contains(rec, paths.CommonGitDirHost+"->"+paths.CommonGitDir+" ") {
		t.Errorf("common dir should be the resolver's validated pair, got: %s", rec)
	}
	if !strings.Contains(rec, " "+paths.Canonical+" ") || strings.Contains(rec, " "+paths.WorkDir+" "+target) {
		t.Errorf("main-tree mount should be the MAIN tree %s, got: %s", paths.Canonical, rec)
	}
}

// The host-side flow issues NO mutating git command: an agent-plantable hook or
// filter must never run on the host, and nothing gets registered by the host.
// (The 2026-07-18 sandbox-escape class — now closed structurally: the host
// never runs git against the repo except bounded read-only probes.)
func TestWorktreeCreateRunsNoAgentCodeOnHost(t *testing.T) {
	repo := initRepo(t)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// A tracked file with a smudge filter, and hooks — all the agent's to write
	// (repo + common git dir are rw from the box).
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
	for proof, hook := range map[string]string{
		hookProof:   "post-checkout",
		refTxnProof: "reference-transaction",
	} {
		if err := os.WriteFile(filepath.Join(hookDir, hook),
			[]byte("#!/bin/sh\ntouch "+proof+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "wt")
	f := &fakeRunner{}
	if err := worktreeCreate(f, discardStreams(), paths, repo, "hostile", target); err != nil {
		t.Fatalf("worktreeCreate: %v", err)
	}

	for proof, what := range map[string]string{
		hookProof:   "post-checkout hook",
		smudgeProof: "smudge filter",
		refTxnProof: "reference-transaction hook",
	} {
		if _, err := os.Stat(proof); err == nil {
			t.Fatalf("%s executed on the host — sandbox escape", what)
		}
	}
	// Nothing registered host-side: the registration is the box's job.
	if reg, err := worktreeRegistered(paths.Canonical, target); err != nil || reg {
		t.Errorf("host-side registration happened (reg=%v err=%v) — the host must not run git worktree add", reg, err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Error("host wrote into the target — only the box may")
	}
}

// A failed create removes exactly the empty mount-point dir byre made (never a
// recursive delete), so a retry isn't refused by the exists check.
func TestWorktreeCreateFailureRemovesEmptyDir(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "wt")
	f := &fakeRunner{worktreeAddErr: os.ErrDeadlineExceeded}
	err = worktreeCreate(f, discardStreams(), paths, repo, "feat", target)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("want create failure, got %v", err)
	}
	if _, serr := os.Stat(target); serr == nil {
		t.Error("empty mount-point dir left behind — a retry would be refused")
	}
}

// The target-dir Mkdir is the create's ownership token (codex 2026-07-19): a
// dir that appears between Worktree's exists-check and the create — another
// invocation's mount point — is a refusal BEFORE any container runs, and the
// dir is left alone (it isn't ours to remove).
func TestWorktreeCreateRefusesConcurrentTargetDir(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "wt")
	if err := os.Mkdir(target, 0o755); err != nil { // the other invocation's token
		t.Fatal(err)
	}
	f := &fakeRunner{}
	err = worktreeCreate(f, discardStreams(), paths, repo, "feat", target)
	if err == nil || !strings.Contains(err.Error(), "another `byre worktree`") {
		t.Fatalf("want the concurrent-create refusal, got %v", err)
	}
	if len(f.worktreeAdds) != 0 {
		t.Error("a create container ran despite losing the target-dir token")
	}
	if _, serr := os.Stat(target); serr != nil {
		t.Error("the other invocation's mount-point dir was removed")
	}
}

// Interrupted-create recognition, both halves: a registered worktree whose dir
// exists resumes via develop; a registered one whose dir is GONE gets the
// targeted prune remedy. Checked before the engine gate, so no engine needed.
func TestWorktreeRecognizesExistingRegistration(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("BYRE_HOME", t.TempDir())
	target := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "--no-checkout", "-b", "feat", target).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	// Dir present + registered → resume with develop, not "already exists".
	err := Worktree(discardStreams(), repo, "feat", target, false)
	if err == nil || !strings.Contains(err.Error(), "byre develop") {
		t.Fatalf("want a resume-with-develop refusal, got: %v", err)
	}

	// Dir gone but still registered → the targeted stale-registration remedy.
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	err = Worktree(discardStreams(), repo, "feat", target, false)
	if err == nil || !strings.Contains(err.Error(), "worktree prune") {
		t.Fatalf("want a prune remedy for the stale registration, got: %v", err)
	}
}

// A plain existing directory (no registration) keeps the plain refusal.
func TestWorktreeRefusesExistingUnregisteredTarget(t *testing.T) {
	repo := initRepo(t)
	t.Setenv("BYRE_HOME", t.TempDir())
	target := t.TempDir()
	err := Worktree(discardStreams(), repo, "feat", target, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want the exists refusal, got: %v", err)
	}
}

// A main tree whose .git is not a directory (separate git dir) is refused with
// the manual route rather than mis-mounted.
func TestWorktreeCommonGitDirRefusesGitfileMain(t *testing.T) {
	dir := t.TempDir()
	realGit := filepath.Join(t.TempDir(), "gitdir")
	if err := os.MkdirAll(realGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+realGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canon, _ := project.Canonicalize(dir)
	_, _, err := worktreeCommonGitDir(project.Paths{Canonical: canon})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want a gitfile refusal, got %v", err)
	}
}
