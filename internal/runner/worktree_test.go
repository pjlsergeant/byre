package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The create container is minimal and hermetic: its own entrypoint (never the
// launcher), the box identity, exactly the three repo mounts, and no session
// labels — a running create step must never be mistaken for a live session.
func TestWorktreeAddArgs(t *testing.T) {
	id := Identity{UID: 501, GID: 20}
	args := worktreeAddArgs("img", "byre-wtadd-x", id,
		"/repo/.git", "/repo/.git", "/repo", "/wt", "feat")

	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{
		" run --rm ",
		" --name byre-wtadd-x ",
		" --entrypoint sh ",
		" --network none ", // local git only; repo hooks run here — no egress
		" -u 501:20 ",
		" -e BYRE_WT_MAIN=/repo ",
		" -e BYRE_WT_TARGET=/wt ",
		" -e BYRE_WT_BRANCH=feat ",
		" --mount type=bind,source=/repo,target=/repo ",
		" --mount type=bind,source=/repo/.git,target=/repo/.git ",
		" --mount type=bind,source=/wt,target=/wt ",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q:\n%s", want, joined)
		}
	}
	if mounts := strings.Count(joined, "--mount"); mounts != 3 {
		t.Errorf("want exactly 3 mounts, got %d", mounts)
	}
	if strings.Contains(joined, "--label") {
		t.Error("create container must carry no labels (it is not a session)")
	}
	if strings.Contains(joined, "--userns") {
		t.Error("no userns flag for a non-keep-id identity")
	}
	// The image comes right before the script (sh -c <script>): nothing after
	// the mounts may be misparsed as extra grants.
	if args[len(args)-3] != "img" || args[len(args)-2] != "-c" || args[len(args)-1] != worktreeAddScript {
		t.Errorf("argv tail should be [image -c script], got %v", args[len(args)-3:len(args)-1])
	}
}

// Under rootless Podman the create step must run in the same userns mapping as
// the box, or the files it writes land owned by an unmapped id.
func TestWorktreeAddArgsKeepID(t *testing.T) {
	id := Identity{UID: 1000, GID: 1000, KeepID: true}
	args := worktreeAddArgs("img", "n", id, "/c", "/c", "/m", "/t", "b")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--userns=keep-id:uid=1000,gid=1000") {
		t.Errorf("keep-id identity must ride the create container: %s", joined)
	}
}

// --- script behavior, against real git (the script IS the creation logic now;
// these are the DWIM tests the old host-side createWorktree had) ---

// initWtRepo makes a real repo with one commit for script tests.
func initWtRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
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

// runWtScript runs worktreeAddScript as the create container does: sh -c, the
// inputs as env — with the target dir pre-created empty, exactly like the host
// side's mkdir-then-mount.
func runWtScript(t *testing.T, main, target, branch string) (string, error) {
	t.Helper()
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", worktreeAddScript)
	cmd.Env = append(os.Environ(),
		"BYRE_WT_MAIN="+main,
		"BYRE_WT_TARGET="+target,
		"BYRE_WT_BRANCH="+branch,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// adminDir resolves the new worktree's git admin dir (where the marker lands).
func adminDir(t *testing.T, target string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", target, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatalf("rev-parse --absolute-git-dir: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestWorktreeAddScriptNewBranch(t *testing.T) {
	repo := initWtRepo(t)
	target := filepath.Join(t.TempDir(), "wt")
	out, err := runWtScript(t, repo, target, "feat")
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	// Registered, --no-checkout (only .git present), branch created, marker set.
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatalf("worktree not registered: %v", err)
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 1 {
		t.Errorf("want a bare .git pointer only (--no-checkout), got %d entries", len(entries))
	}
	if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", "refs/heads/feat").Run(); err != nil {
		t.Error("branch feat was not created")
	}
	if _, err := os.Stat(filepath.Join(adminDir(t, target), "byre-needs-checkout")); err != nil {
		t.Errorf("pending-checkout marker not written: %v", err)
	}
}

func TestWorktreeAddScriptExistingBranch(t *testing.T) {
	repo := initWtRepo(t)
	if out, err := exec.Command("git", "-C", repo, "branch", "existing").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}
	target := filepath.Join(t.TempDir(), "wt")
	out, err := runWtScript(t, repo, target, "existing")
	if err != nil {
		t.Fatalf("script on existing branch: %v\n%s", err, out)
	}
	head, err := exec.Command("git", "-C", target, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != "existing" {
		t.Errorf("worktree HEAD = %q err=%v, want existing", strings.TrimSpace(string(head)), err)
	}
}

// The DWIM footgun: a name existing only as a remote branch must be checked
// out tracking the remote, not forked as a divergent local branch off HEAD.
func TestWorktreeAddScriptRemoteBranch(t *testing.T) {
	repo := initWtRepo(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--bare", origin)
	run("-C", repo, "remote", "add", "origin", origin)
	run("-C", repo, "push", "-q", "origin", "HEAD:refs/heads/remotefeat")
	run("-C", repo, "fetch", "-q", "origin")

	target := filepath.Join(root, "wt")
	out, err := runWtScript(t, repo, target, "remotefeat")
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	up, err := exec.Command("git", "-C", target, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Output()
	if err != nil || strings.TrimSpace(string(up)) != "origin/remotefeat" {
		t.Errorf("worktree should track origin/remotefeat, upstream=%q err=%v", strings.TrimSpace(string(up)), err)
	}
}

// A failed add leaves no half-registered worktree behind (git rolls its own
// partial state back; the script adds no cleanup of its own there).
func TestWorktreeAddScriptFailureCleansUp(t *testing.T) {
	repo := initWtRepo(t)
	// Checking out the branch the main tree already has checked out makes the
	// add itself fail ("already checked out").
	head, err := exec.Command("git", "-C", repo, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "wt")
	out, serr := runWtScript(t, repo, target, strings.TrimSpace(string(head)))
	if serr == nil {
		t.Fatalf("script should fail adding the checked-out branch\n%s", out)
	}
	list, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(list), target) {
		t.Errorf("failed add left the worktree registered:\n%s", list)
	}
}

// The codex-found race (2026-07-19): a create that collides with an EXISTING
// registration for the target must not destroy it. The failed-add path runs no
// `worktree remove` of its own — removing there would delete a concurrent
// invocation's (or any pre-existing) worktree at the same path.
func TestWorktreeAddScriptFailedAddPreservesExistingRegistration(t *testing.T) {
	repo := initWtRepo(t)
	target := filepath.Join(t.TempDir(), "wt")
	// Someone already holds the target: a registered worktree with its dir.
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "--no-checkout", "-b", "first", target).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	out, serr := runWtScript(t, repo, target, "second")
	if serr == nil {
		t.Fatalf("script should fail adding over an existing registration\n%s", out)
	}
	list, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), "worktree "+target) {
		t.Errorf("the existing registration was destroyed by the failed add:\n%s", list)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Errorf("the existing worktree's .git was removed: %v", err)
	}
}

// A box image without git gets a loud, actionable message — never a silently
// missing worktree.
func TestWorktreeAddScriptNoGit(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	bin := t.TempDir()
	if err := os.Symlink(shPath, filepath.Join(bin, "sh")); err != nil {
		t.Skip("cannot symlink sh")
	}
	cmd := exec.Command(filepath.Join(bin, "sh"), "-c", worktreeAddScript)
	cmd.Env = []string{"PATH=" + bin, "BYRE_WT_MAIN=/x", "BYRE_WT_TARGET=/y", "BYRE_WT_BRANCH=b"}
	out, serr := cmd.CombinedOutput()
	if serr == nil {
		t.Fatalf("script should fail without git\n%s", out)
	}
	if !strings.Contains(string(out), "no git") {
		t.Errorf("want an actionable no-git message, got:\n%s", out)
	}
}
