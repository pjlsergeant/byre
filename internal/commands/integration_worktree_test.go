package commands

// Gated integration coverage for worktree sessions against a live
// engine: concurrent siblings, in-box creation, and metadata-write
// isolation. Run with BYRE_DOCKER_TESTS=1 (see integration_test.go).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"time"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestIntegrationConcurrentWorktreeSessions: worktree support against a live
// engine (ADR 0009) — a linked worktree resolves to the same project (shared
// image, shared project-scoped volumes) but its own box: distinct container
// names so two sessions run SIMULTANEOUSLY, each seeing its own checkout at
// /workspace, with a project volume live in both.
func TestIntegrationConcurrentWorktreeSessions(t *testing.T) {
	r := requireEngineRunner(t)
	pMain, proj := testPaths(t)
	if err := builtins.EnsureStore(pMain.Home); err != nil {
		t.Fatal(err)
	}

	// A real repo with a real linked worktree (worktree resolution reads
	// git's own metadata; no fake can stand in for it).
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = proj
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(proj, "shared.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "seed")
	wtDir := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "-b", "session-b", wtDir)
	// Untracked sentinel: exists ONLY in the worktree checkout.
	if err := os.WriteFile(filepath.Join(wtDir, "wt-only.txt"), []byte("wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pWt, err := project.Resolve(wtDir)
	if err != nil {
		t.Fatal(err)
	}
	if !pWt.IsWorktree {
		t.Fatalf("worktree dir %s did not resolve as a worktree", wtDir)
	}
	if pWt.ID != pMain.ID {
		t.Fatalf("worktree resolved to its own project ID %q, want main's %q (ADR 0009)", pWt.ID, pMain.ID)
	}

	cat, err := builtins.LoadCatalogRaw(pMain.Home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "pvol", Target: "/home/dev/pvol", Role: "state"},
	}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	rv := combine(cfg, res)

	// One image, shared by both sessions (imageTag keys on the project ID).
	ident := testIdentity(t, r)
	image := imageTag(pMain.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, pMain, cfg, res, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}

	paramsMain, err := runParams(pMain, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	paramsWt, err := runParams(pWt, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	if paramsMain.Name == paramsWt.Name {
		t.Fatalf("main and worktree boxes share container name %q — concurrent sessions impossible", paramsMain.Name)
	}
	if paramsMain.Volumes[0].Name != paramsWt.Volumes[0].Name {
		t.Fatalf("project volume not shared into the worktree box: %q vs %q",
			paramsMain.Volumes[0].Name, paramsWt.Volumes[0].Name)
	}
	_ = r.VolumeRemove(paramsMain.Volumes[0].Name)
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", paramsMain.Name).Run()
		_ = exec.Command(string(r.Engine()), "rm", "-f", paramsWt.Name).Run()
		_ = r.VolumeRemove(paramsMain.Volumes[0].Name)
	})

	// Box A (main): drop a token in the shared volume, assert the worktree
	// sentinel is NOT in its checkout, park.
	token := fmt.Sprintf("token-%d-%d", os.Getpid(), time.Now().UnixNano())
	paramsMain.Command = []string{"bash", "-c", fmt.Sprintf(
		"echo %s > /home/dev/pvol/wt-token && test ! -e /workspace/wt-only.txt && echo MAIN_OK; sleep 60", token)}
	// Box B (worktree): wait for A's token through the shared volume, assert
	// its own checkout, park. Both boxes RUNNING at once is the claim.
	//
	// The last two tests are ADR 0009's same-path binds, checked through
	// git's own metadata (no git binary in the default image needed): the
	// worktree's /workspace/.git is a pointer full of absolute HOST paths —
	// it only resolves in-box because runParams bind-mounts the common git
	// dir and the worktree at those exact host paths. If either bind is
	// dropped, the pointer target (or the same-path view of the checkout)
	// vanishes and the probe fails.
	paramsWt.Command = []string{"bash", "-c", fmt.Sprintf(
		`for i in $(seq 50); do [ -f /home/dev/pvol/wt-token ] && break; sleep 0.2; done
cat /home/dev/pvol/wt-token && test -e /workspace/wt-only.txt && test -e /workspace/shared.txt &&
gd=$(cut -d' ' -f2 /workspace/.git) && test -d "$gd" &&
test -f %q/wt-only.txt && echo WT_OK; sleep 60`, wtDir)}

	detach := func(p runner.RunParams) {
		t.Helper()
		args := runner.RunArgs(p)
		args = append([]string{args[0], "-d"}, args[1:]...)
		if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
			t.Fatalf("start %s: %v\n%s", p.Name, err, out)
		}
	}
	detach(paramsMain)
	detach(paramsWt)
	waitRunning(t, r, paramsMain.Name)
	waitRunning(t, r, paramsWt.Name)
	waitForLog(t, r, paramsMain.Name, "MAIN_OK")
	waitForLog(t, r, paramsWt.Name, "WT_OK")

	// Both sessions live at the same instant.
	for _, name := range []string{paramsMain.Name, paramsWt.Name} {
		out, _ := exec.Command(string(r.Engine()), "inspect", "-f", "{{.State.Running}}", name).CombinedOutput()
		if strings.TrimSpace(string(out)) != "true" {
			t.Errorf("box %s not running during the concurrent window", name)
		}
	}
	if logs := containerLogs(t, r, paramsWt.Name); !strings.Contains(logs, token) {
		t.Errorf("worktree box never saw main box's token through the shared volume:\n%s", logs)
	}
}

// TestIntegrationWorktreeCreateInBox: staged worktree creation against a live
// engine (ADR 0009 — every mutating git operation on the repo runs in a box).
// worktreeCreate builds the project image (git baked in via the config
// cascade, as a user would) and runs the one-shot creation container; the
// promises only a live engine can vouch for: the same-path mounts land the
// registration, marker, and branch exactly where the host and the next
// session expect them, owned by the invoking user — with the target left
// --no-checkout. Plus the no-git-image path: a loud failure, nothing
// registered, no half-created state.
func TestIntegrationWorktreeCreateInBox(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	// git must be IN the box for creation now; ride the cascade like a user.
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte("apt = [\"git\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = proj
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(proj, "tracked.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "seed")

	ident := testIdentity(t, r)
	t.Cleanup(func() { _ = r.ImageRemove(imageTag(p.ID, ident.UID, ident.GID)) })
	target := filepath.Join(t.TempDir(), "wt")
	if err := worktreeCreate(r, discardStreams(), p, proj, "boxed", target); err != nil {
		t.Fatalf("worktreeCreate: %v", err)
	}

	// Registered, and --no-checkout: the target holds ONLY the .git pointer
	// (population is the first session's job).
	if reg, err := worktreeRegistered(p.Canonical, target); err != nil || !reg {
		t.Fatalf("worktree not registered host-side: reg=%v err=%v", reg, err)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".git" {
		t.Fatalf("target should hold only .git (no checkout), got %v", entries)
	}
	// The branch was created in the box and is visible to host git.
	if err := exec.Command("git", "-C", p.Canonical, "rev-parse", "--verify", "-q", "refs/heads/boxed").Run(); err != nil {
		t.Fatal("branch 'boxed' not created")
	}
	// The pending-checkout marker is where the launcher will look.
	gitdir, err := exec.Command("git", "-C", target, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	adminDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(gitdir)))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(adminDir, "byre-needs-checkout")
	fi, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("pending-checkout marker not written: %v", err)
	}
	// Ownership: the box identity maps back to the invoking user (rootful
	// bake, or keep-id under rootless Podman), so in-box writes land ours.
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != uint32(os.Getuid()) {
		t.Fatalf("marker owned by uid %d, want %d", st.Uid, os.Getuid())
	}

	// A git-less image: loud failure, nothing registered, no target debris —
	// exercised at the WorktreeAdd layer so it needs no second image build.
	commonTarget, commonHost, err := worktreeCommonGitDir(p)
	if err != nil {
		t.Fatal(err)
	}
	target2 := filepath.Join(t.TempDir(), "wt-nogit")
	if err := os.MkdirAll(target2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.WorktreeAdd("busybox", "byre-inttest-wtadd-nogit", ident,
		commonHost, commonTarget, p.Canonical, target2, "nogit"); err == nil {
		t.Fatal("WorktreeAdd on a git-less image should fail")
	}
	if reg, err := worktreeRegistered(p.Canonical, target2); err != nil || reg {
		t.Fatalf("git-less create must register nothing: reg=%v err=%v", reg, err)
	}
	if entries, _ := os.ReadDir(target2); len(entries) != 0 {
		t.Fatalf("git-less create left debris in the target: %v", entries)
	}
}

// TestIntegrationWorktreeCreateIsolatesMetadataWrites is the direct isolation
// test for the whole in-box-creation change (ADR 0009): every git side effect
// during `worktree add` resolves in the CONTAINER's filesystem namespace, so a
// metadata indirection pointing outside the three mounts never reaches the host
// path it names.
//
// The observable indirection: a new branch's reflog leaf
// (.git/logs/refs/heads/<branch>), pre-planted as a symlink to an absolute path
// that on the host holds a sentinel. Run host-side, `git worktree add -b`
// FOLLOWS that symlink and appends the branch-creation entry to the host file
// (an escape). Run in the box, the same absolute path resolves in the
// container (a /tmp git creates fresh and writes ephemerally), so the host
// sentinel is untouched — while the branch, registration, and marker still
// land correctly in the mounted common dir.
func TestIntegrationWorktreeCreateIsolatesMetadataWrites(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	if err := os.WriteFile(filepath.Join(p.Home, "default.config"), []byte("apt = [\"git\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = proj
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "seed")

	// The sentinel lives at an absolute path OUTSIDE the three mounts, in the
	// host's /tmp (container-writable there, but a DIFFERENT /tmp). A host-side
	// add would append the reflog here; an in-box add must not.
	victim := filepath.Join("/tmp", fmt.Sprintf("byre-wtiso-%d-%d", os.Getpid(), time.Now().UnixNano()))
	const sentinel = "SENTINEL-untouched\n"
	if err := os.WriteFile(victim, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(victim) })
	// Pre-plant the branch's reflog leaf as a symlink to the sentinel. The
	// parent (.git/logs/refs/heads) must exist for git to open the leaf.
	logDir := filepath.Join(proj, ".git", "logs", "refs", "heads")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(logDir, "boxed")); err != nil {
		t.Fatal(err)
	}

	ident := testIdentity(t, r)
	t.Cleanup(func() { _ = r.ImageRemove(imageTag(p.ID, ident.UID, ident.GID)) })
	target := filepath.Join(t.TempDir(), "wt")
	if err := worktreeCreate(r, discardStreams(), p, proj, "boxed", target); err != nil {
		t.Fatalf("worktreeCreate: %v", err)
	}

	// The isolation claim: the host sentinel is byte-for-byte unchanged — the
	// reflog write resolved in the container, never at this host path.
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("host sentinel was modified — a metadata write escaped the box:\n%q", got)
	}
	// And the real metadata still landed: registered, branch created, marker set.
	if reg, err := worktreeRegistered(p.Canonical, target); err != nil || !reg {
		t.Fatalf("worktree not registered: reg=%v err=%v", reg, err)
	}
	if err := exec.Command("git", "-C", p.Canonical, "rev-parse", "--verify", "-q", "refs/heads/boxed").Run(); err != nil {
		t.Fatal("branch 'boxed' not created in the mounted common dir")
	}
	gitdir, err := exec.Command("git", "-C", target, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	adminDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(gitdir)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(adminDir, "byre-needs-checkout")); err != nil {
		t.Fatalf("pending-checkout marker not written: %v", err)
	}
}
