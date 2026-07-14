package commands

// Gated byre-level integration smoke: real docker/podman, run host-side with
//
//	BYRE_DOCKER_TESTS=1 go test ./internal/commands/ -run Integration -v
//
// The core promise checked here is the one no fake can vouch for: the
// GENERATED Dockerfile actually builds on a live engine, and the resulting
// image runs. Expect several minutes on a cold cache (debian pull + apt).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func requireEngineRunner(t *testing.T) *runner.Runner {
	t.Helper()
	if os.Getenv("BYRE_DOCKER_TESTS") != "1" {
		t.Skip("set BYRE_DOCKER_TESTS=1 to run byre integration tests")
	}
	eng, err := runner.Detect("auto", nil)
	if err != nil {
		t.Fatalf("BYRE_DOCKER_TESTS=1 but no engine: %v", err)
	}
	t.Logf("engine: %s", eng)
	return runner.New(eng)
}

// TestIntegrationGeneratedImageBuildsAndRuns assembles the default (empty
// config, no agent) build context for a fresh project, builds it for real,
// and runs the image once with the entrypoint bypassed. This is the
// generated-Dockerfile <-> real-engine contract the unit suite can only
// approximate; run it once after any change to internal/gen or the base
// core block, and before a release.
func TestIntegrationGeneratedImageBuildsAndRuns(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)

	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	t.Cleanup(func() { _ = r.ImageRemove(image) })

	if err := buildImage(r, p, rv.cfg, rv.skills, image, false); err != nil {
		t.Fatalf("generated Dockerfile failed to build: %v", err)
	}
	if ok, err := r.ImageExists(image); err != nil || !ok {
		t.Fatalf("ImageExists(%s) = (%v, %v) after build", image, ok, err)
	}

	// The image must run: entrypoint bypassed (the byre launcher would exec an
	// agent), verifying the dev user and workspace baked in at build time.
	out, err := exec.Command(string(r.Engine()), "run", "--rm",
		"--entrypoint", "id", image, "-u").CombinedOutput()
	if err != nil {
		t.Fatalf("built image failed to run: %v\n%s", err, out)
	}
	if got, want := string(out), strconv.Itoa(os.Getuid())+"\n"; got != want {
		t.Fatalf("container runs as uid %q, want %q (host uid baked at build)", got, want)
	}
}

// TestIntegrationLaunchPathAndOwnership drives a built box through the REAL
// entrypoint with the exact argv develop assembles (runParams -> RunArgs) —
// no --entrypoint bypass. These are the fresh-develop promises only a live
// engine can vouch for (manual until now):
//   - the launcher execs the command as the unprivileged dev user at the
//     host uid/gid, HOME writable, parked in /workspace;
//   - the workspace bind is the project dir BOTH ways — host files visible
//     in-box, box-written files landing host-side owned by the invoking
//     user (the ownership half of the contract);
//   - a fresh named state volume initializes owned by the baked uid (Docker
//     seeds it from the image's pre-created mount point, gen's VolumeDirs)
//     — the fresh-volume-UID case every state-volume builtin rides on.
func TestIntegrationLaunchPathAndOwnership(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	if err := builtins.EnsureStore(p.Home); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	// A plain state volume, no seed: fresh-volume ownership comes from the
	// image mount point alone.
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "statevol", Target: "/home/dev/statevol", Role: "state"},
	}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	rv := combine(cfg, res)
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}

	// Host -> box sentinel.
	if err := os.WriteFile(filepath.Join(proj, "host-sentinel"), []byte("from-host\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	params, err := runParams(p, rv, image, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	params.Command = []string{"bash", "-c", strings.Join([]string{
		`echo "ID=$(id -u):$(id -g):$(id -un)"`,
		`echo "HOME=$HOME PWD=$PWD PROJECT=$BYRE_PROJECT"`,
		`cat /workspace/host-sentinel`,
		`touch "$HOME/.home-writable"`,
		`echo from-box > /workspace/box-written`,
		`echo "VOL=$(stat -c %u:%g /home/dev/statevol)"`,
		`touch /home/dev/statevol/.vol-writable`,
	}, " && ")}
	out, err := exec.Command(string(r.Engine()), runner.RunArgs(params)...).CombinedOutput()
	if err != nil {
		t.Fatalf("launch through the real entrypoint failed: %v\n%s", err, out)
	}
	logs := string(out)
	uid, gid := os.Getuid(), os.Getgid()
	for _, want := range []string{
		fmt.Sprintf("ID=%d:%d:dev", uid, gid),
		"HOME=/home/dev PWD=/workspace PROJECT=" + p.ID,
		"from-host",
		fmt.Sprintf("VOL=%d:%d", uid, gid),
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("launch output missing %q:\n%s", want, logs)
		}
	}

	// Box -> host: the file must land AND belong to the invoking user.
	fi, err := os.Stat(filepath.Join(proj, "box-written"))
	if err != nil {
		t.Fatalf("box-written file never landed host-side: %v", err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
		t.Errorf("box-written file owned by uid %d, want invoking uid %d", st.Uid, uid)
	}
}

// TestIntegrationMachineVolumeSharedAcrossProjects: ADR 0017's shared-auth
// mechanism against a live engine — a machine-scoped state volume must
// resolve to the SAME engine volume from two different projects under one
// store, so a credential one project's box writes is live in the next
// project's box (both writing and reading as the unprivileged dev user).
func TestIntegrationMachineVolumeSharedAcrossProjects(t *testing.T) {
	r := requireEngineRunner(t)
	pA, _ := testPaths(t) // pins BYRE_HOME for the whole test
	projB := t.TempDir()
	pB, err := project.Resolve(projB)
	if err != nil {
		t.Fatal(err)
	}
	if err := pB.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if pA.ID == pB.ID {
		t.Fatalf("test projects collided on ID %q", pA.ID)
	}
	if err := builtins.EnsureStore(pA.Home); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(pA.Home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "authvol", Target: "/home/dev/.authvol", Role: "state", Scope: "machine"},
	}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	rv := combine(cfg, res)

	uid, gid := os.Getuid(), os.Getgid()
	imageA, imageB := imageTag(pA.ID, uid, gid), imageTag(pB.ID, uid, gid)
	t.Cleanup(func() { _ = r.ImageRemove(imageA); _ = r.ImageRemove(imageB) })
	if err := buildImage(r, pA, cfg, res, imageA, false); err != nil {
		t.Fatalf("project A image failed to build: %v", err)
	}
	if err := buildImage(r, pB, cfg, res, imageB, false); err != nil {
		t.Fatalf("project B image failed to build: %v", err)
	}

	paramsA, err := runParams(pA, rv, imageA, false, false)
	if err != nil {
		t.Fatal(err)
	}
	paramsB, err := runParams(pB, rv, imageB, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// The heart of the claim: same engine volume from both projects.
	if len(paramsA.Volumes) != 1 || len(paramsB.Volumes) != 1 {
		t.Fatalf("expected exactly one volume per box, got %v / %v", paramsA.Volumes, paramsB.Volumes)
	}
	volName := paramsA.Volumes[0].Name
	if got := paramsB.Volumes[0].Name; got != volName {
		t.Fatalf("machine-scoped volume resolved per-project: %q vs %q", volName, got)
	}
	_ = r.VolumeRemove(volName) // shed any leftover from an aborted prior run
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", paramsA.Name).Run()
		_ = exec.Command(string(r.Engine()), "rm", "-f", paramsB.Name).Run()
		_ = r.VolumeRemove(volName)
	})

	// Unique token so a stale volume can't satisfy the read.
	token := fmt.Sprintf("token-%d-%d", os.Getpid(), time.Now().UnixNano())
	paramsA.Command = []string{"bash", "-c", fmt.Sprintf("echo %s > /home/dev/.authvol/cred", token)}
	if out, err := exec.Command(string(r.Engine()), runner.RunArgs(paramsA)...).CombinedOutput(); err != nil {
		t.Fatalf("project A box failed to write the credential: %v\n%s", err, out)
	}
	paramsB.Command = []string{"bash", "-c", "cat /home/dev/.authvol/cred && stat -c %u /home/dev/.authvol/cred"}
	out, err := exec.Command(string(r.Engine()), runner.RunArgs(paramsB)...).CombinedOutput()
	if err != nil {
		t.Fatalf("project B box failed to read the credential: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), token) {
		t.Errorf("project B box did not see project A's credential; got:\n%s", out)
	}
	if !strings.Contains(string(out), strconv.Itoa(uid)) {
		t.Errorf("credential not owned by the dev uid %d in project B's box; got:\n%s", uid, out)
	}
}

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
	image := imageTag(pMain.ID, os.Getuid(), os.Getgid())
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, pMain, cfg, res, image, false); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}

	paramsMain, err := runParams(pMain, rv, image, false, false)
	if err != nil {
		t.Fatal(err)
	}
	paramsWt, err := runParams(pWt, rv, image, false, false)
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
	paramsWt.Command = []string{"bash", "-c",
		`for i in $(seq 50); do [ -f /home/dev/pvol/wt-token ] && break; sleep 0.2; done
cat /home/dev/pvol/wt-token && test -e /workspace/wt-only.txt && test -e /workspace/shared.txt && echo WT_OK; sleep 60`}

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
