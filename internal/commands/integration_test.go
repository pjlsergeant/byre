package commands

// Gated byre-level integration smoke: real docker/podman, run host-side with
//
//	BYRE_DOCKER_TESTS=1 go test ./internal/commands/ -run Integration -v
//
// The core promise checked here is the one no fake can vouch for: the
// GENERATED Dockerfile actually builds on a live engine, and the resulting
// image runs. Expect several minutes on a cold cache (debian pull + apt).

import (
	"bytes"
	"fmt"
	"io"
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
	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/tuitest"
)

func requireEngineRunner(t *testing.T) *runner.Runner {
	t.Helper()
	if os.Getenv("BYRE_DOCKER_TESTS") != "1" {
		t.Skip("set BYRE_DOCKER_TESTS=1 to run byre integration tests")
	}
	// BYRE_TEST_ENGINE pins the suite to one engine ("docker"/"podman") on a
	// host that has both — Detect's auto prefers docker, which would silently
	// leave the podman paths (rootless keep-id included) unexercised.
	setting := os.Getenv("BYRE_TEST_ENGINE")
	if setting == "" {
		setting = "auto"
	}
	eng, err := runner.Detect(setting, nil)
	if err != nil {
		t.Fatalf("BYRE_DOCKER_TESTS=1 but no engine (BYRE_TEST_ENGINE=%q): %v", setting, err)
	}
	t.Logf("engine: %s", eng)
	return runner.New(eng)
}

// testIdentity resolves the identity develop would use on r — host identity
// on rootful engines, the generic keep-id identity on rootless Podman — so
// the gated tests build, run, and assert in the same mode a real session
// would. Skips where develop itself would refuse (rootless without keep-id).
func testIdentity(t *testing.T, r *runner.Runner) runner.Identity {
	t.Helper()
	ident, err := resolveIdentity(io.Discard, r)
	if err != nil {
		t.Skipf("engine unsupported for sessions: %v", err)
	}
	if ident.KeepID {
		t.Logf("keep-id identity: %d:%d", ident.UID, ident.GID)
	}
	return ident
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
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })

	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
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
	if got, want := string(out), strconv.Itoa(ident.UID)+"\n"; got != want {
		t.Fatalf("container runs as uid %q, want %q (the identity baked at build)", got, want)
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
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}

	// Host -> box sentinel.
	if err := os.WriteFile(filepath.Join(proj, "host-sentinel"), []byte("from-host\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	params, err := runParams(p, rv, image, false, false, ident)
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
	// In-box facts are the IDENTITY's (generic 1000 under keep-id); the
	// host-side ownership assertion below stays the INVOKER's uid — mapping
	// the two together is exactly what the mode promises.
	for _, want := range []string{
		fmt.Sprintf("ID=%d:%d:dev", ident.UID, ident.GID),
		"HOME=/home/dev PWD=/workspace PROJECT=" + p.ID,
		"from-host",
		fmt.Sprintf("VOL=%d:%d", ident.UID, ident.GID),
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
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		t.Errorf("box-written file owned by uid %d, want invoking uid %d", st.Uid, os.Getuid())
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

	ident := testIdentity(t, r)
	imageA, imageB := imageTag(pA.ID, ident.UID, ident.GID), imageTag(pB.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(imageA); _ = r.ImageRemove(imageB) })
	if err := buildImage(r, pA, cfg, res, imageA, false, ident); err != nil {
		t.Fatalf("project A image failed to build: %v", err)
	}
	if err := buildImage(r, pB, cfg, res, imageB, false, ident); err != nil {
		t.Fatalf("project B image failed to build: %v", err)
	}

	paramsA, err := runParams(pA, rv, imageA, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	paramsB, err := runParams(pB, rv, imageB, false, false, ident)
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
	paramsB.Command = []string{"bash", "-c", `cat /home/dev/.authvol/cred && echo "CRED_OWNER=$(stat -c %u /home/dev/.authvol/cred)"`}
	out, err := exec.Command(string(r.Engine()), runner.RunArgs(paramsB)...).CombinedOutput()
	if err != nil {
		t.Fatalf("project B box failed to read the credential: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), token) {
		t.Errorf("project B box did not see project A's credential; got:\n%s", out)
	}
	// Labeled, newline-terminated: bare digits could hide inside the token.
	if !strings.Contains(string(out), fmt.Sprintf("CRED_OWNER=%d\n", ident.UID)) {
		t.Errorf("credential not owned by the dev uid %d in project B's box; got:\n%s", ident.UID, out)
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

// TestIntegrationRootlessPodmanKeepID: the rootless-Podman keep-id path (ADR
// 0032) against a live rootless engine — the exact promises the unit fakes
// can't vouch for:
//   - resolveIdentity selects the generic keep-id identity;
//   - the generic-uid image builds and the box (real entrypoint, develop's own
//     argv) runs as dev at uid 1000 under --userns=keep-id;
//   - box-written workspace files land host-side owned by the INVOKING user
//     (the whole point of the mapping);
//   - a seeded state volume is written under the box's mapping: the box sees
//     the seeded file owned by dev (1000) and can write next to it.
//
// Skips when podman is absent, rootful, or too old for the mapping — it needs
// a rootless-podman host (the inttest VM qualifies), not the docker CI engine.
func TestIntegrationRootlessPodmanKeepID(t *testing.T) {
	if os.Getenv("BYRE_DOCKER_TESTS") != "1" {
		t.Skip("set BYRE_DOCKER_TESTS=1 to run byre integration tests")
	}
	eng, err := runner.Detect("podman", nil)
	if err != nil {
		t.Skip("podman not installed")
	}
	r := runner.New(eng)
	if rootless, rerr := r.IsRootlessPodman(); rerr != nil || !rootless {
		t.Skipf("podman is not rootless here (rootless=%v err=%v)", rootless, rerr)
	}

	p, proj := testPaths(t)
	if err := builtins.EnsureStore(p.Home); err != nil {
		t.Fatal(err)
	}
	cat, err := builtins.LoadCatalogRaw(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	seedSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(seedSrc, "seeded-cred"), []byte("seeded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Volumes: []config.Volume{
		{Name: "statevol", Target: "/home/dev/statevol", Role: "state", Seed: &config.Seed{Host: seedSrc}},
	}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	rv := combine(cfg, res)

	var identOut strings.Builder
	ident, err := resolveIdentity(&identOut, r)
	if err != nil {
		// Not a skip: a rootless engine that fails the mode-select on a
		// keep-id-capable host is the bug this test exists to catch. But a
		// genuinely old podman can't run the path at all.
		if ok, _ := r.SupportsKeepIDMapping(); !ok {
			t.Skipf("podman too old for keep-id mapping: %v", err)
		}
		t.Fatalf("resolveIdentity refused on a keep-id-capable rootless engine: %v", err)
	}
	if !ident.KeepID || ident.UID != genericUID {
		t.Fatalf("mode-select picked %+v, want the generic keep-id identity", ident)
	}

	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, cfg, res, image, false, ident); err != nil {
		t.Fatalf("generic-uid image failed to build: %v", err)
	}

	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})

	// Seed exactly like develop does — under the box's identity/mapping.
	if err := seedVolumes(r, io.Discard, p, image, rv.volumes, ident); err != nil {
		t.Fatalf("seeding under keep-id failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(proj, "host-sentinel"), []byte("from-host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"bash", "-c", strings.Join([]string{
		`echo "ID=$(id -u):$(id -g):$(id -un)"`,
		`cat /workspace/host-sentinel`,
		`echo from-box > /workspace/box-written`,
		`echo "SEED=$(cat /home/dev/statevol/seeded-cred) SEED_OWNER=$(stat -c %u:%g /home/dev/statevol/seeded-cred)"`,
		`touch /home/dev/statevol/.vol-writable`,
	}, " && ")}
	out, err := exec.Command(string(r.Engine()), runner.RunArgs(params)...).CombinedOutput()
	if err != nil {
		t.Fatalf("keep-id launch through the real entrypoint failed: %v\n%s", err, out)
	}
	logs := string(out)
	for _, want := range []string{
		fmt.Sprintf("ID=%d:%d:dev", genericUID, genericGID),
		"from-host",
		fmt.Sprintf("SEED=seeded SEED_OWNER=%d:%d", genericUID, genericGID),
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("keep-id launch output missing %q:\n%s", want, logs)
		}
	}

	// The ownership half of the contract: the box's write lands host-side
	// owned by the INVOKING user, not a subuid.
	fi, err := os.Stat(filepath.Join(proj, "box-written"))
	if err != nil {
		t.Fatalf("box-written file never landed host-side: %v", err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		t.Errorf("box-written file owned by uid %d, want invoking uid %d", st.Uid, os.Getuid())
	}
}

// TestIntegrationMCPConfigBaked proves the [[mcp]] contract end to end in a
// REAL image (ADR 0033): the canonical /etc/byre/mcp.json is COPY'd into
// every build — the declared set (headers templates verbatim, x_byre_env
// names, closures already subtracted) byte-identical to the renderer's
// output, and the empty set as a real file — so the injection adapters'
// static flags can rely on the path unconditionally.
func TestIntegrationMCPConfigBaked(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	if err := os.WriteFile(filepath.Join(p.Dir, config.ProjectConfigName), []byte(`
[[mcp]]
name = "github"
command = ["github-mcp-server", "stdio"]
env = ["GITHUB_TOKEN"]

[[mcp]]
name = "proxied"
url = "https://mcp.internal.example/mcp"

[mcp.headers]
Authorization = "Bearer ${PROXY_TOKEN}"

[[mcp]]
name = "!dropped"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("build: %v", err)
	}

	out, err := exec.Command(string(r.Engine()), "run", "--rm",
		"--entrypoint", "cat", image, gen.MCPConfigPath).CombinedOutput()
	if err != nil {
		t.Fatalf("reading %s from the image: %v\n%s", gen.MCPConfigPath, err, out)
	}
	want := config.MCPConfigJSON(skills.MCPList(rv.mcps))
	if string(out) != string(want) {
		t.Fatalf("baked mcp.json != renderer output:\n--- image ---\n%s--- want ---\n%s", out, want)
	}
	for _, must := range []string{`"github"`, `"Bearer ${PROXY_TOKEN}"`, `"x_byre_env"`} {
		if !strings.Contains(string(out), must) {
			t.Fatalf("baked file missing %s:\n%s", must, out)
		}
	}
	if strings.Contains(string(out), "dropped") {
		t.Fatalf("closure marker leaked into the bake:\n%s", out)
	}

	// The EMPTY set is a real file too — the always-baked half of the
	// contract that lets agent commands carry their flags unconditionally.
	p2, proj2 := testPaths(t)
	rv2, err := resolve(p2, proj2, nil)
	if err != nil {
		t.Fatal(err)
	}
	image2 := imageTag(p2.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image2) })
	if err := buildImage(r, p2, rv2.cfg, rv2.skills, image2, false, ident); err != nil {
		t.Fatalf("empty-set build: %v", err)
	}
	out2, err := exec.Command(string(r.Engine()), "run", "--rm",
		"--entrypoint", "cat", image2, gen.MCPConfigPath).CombinedOutput()
	if err != nil {
		t.Fatalf("empty-set image must still carry the file: %v\n%s", err, out2)
	}
	if string(out2) != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("empty-set bake = %q", out2)
	}
}

// TestIntegrationDeliverTransport lands real files in a LIVE box's /inbox —
// the transport scripts (inboxCheck, fileClaim) executing through a real
// engine exec, the seam no unit fake can vouch for (deliver_test's fake
// reimplements the claim loop). Pins ADR 0021's promises end to end: the
// box is discovered by label from cwd, the landed path comes back on
// stdout, the bytes arrive exactly, and re-delivering the same name claims
// -2 instead of clobbering.
func TestIntegrationDeliverTransport(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}

	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"sleep", "120"}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	args := runner.RunArgs(params)
	args = append([]string{args[0], "-d"}, args[1:]...)
	if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
		t.Fatalf("box failed to start: %v\n%s", err, out)
	}

	src := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(src, []byte("hello from the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Through deliverWith itself — the production wiring (engine adapters,
	// callerScoped probe, uid guard) is part of what this test vouches for.
	// No clipboard, no picker: a single owned session resolves without both.
	deliverOnce := func() string {
		var out, errw strings.Builder
		s := Streams{Out: &out, Err: &errw, In: strings.NewReader("")}
		if _, err := deliverWith(s, proj, deliver.Options{}, deliver.PathSources([]string{src}), []sessionRunner{r}, os.Getuid(), nil, nil); err != nil {
			t.Fatalf("deliver failed: %v\nstderr: %s", err, errw.String())
		}
		return out.String()
	}

	if got := deliverOnce(); got != "/inbox/hello.txt\n" {
		t.Fatalf("landed path = %q, want /inbox/hello.txt", got)
	}
	ids, err := r.RunningContainersByLabel(labelKey + "=" + p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("session discovery = (%v, %v), want exactly one box", ids, err)
	}
	content, err := r.ExecInput(ids[0], ident.UID, ident.GID, nil, "cat", "/inbox/hello.txt")
	if err != nil {
		t.Fatalf("reading the landed file: %v", err)
	}
	if content != "hello from the host\n" {
		t.Fatalf("landed content = %q", content)
	}

	// Same name again: the ln-EEXIST claim loop must uniquify, not clobber.
	if got := deliverOnce(); got != "/inbox/hello-2.txt\n" {
		t.Fatalf("second landed path = %q, want /inbox/hello-2.txt", got)
	}
}

// TestIntegrationDeliverLoopbackSSH is the no-fakes version of the remote
// loop: REAL ssh to this machine's own sshd, the REAL sshExec seam, and a
// byre binary built from this tree answering on the far side — quoting
// through an actual remote shell (the binary's path carries a space on
// purpose), exit codes propagating through an actual sshd, the tar riding an
// actual no-pty channel. What stays untestable here is only the
// other-machine-ness (macOS sshd's sparse PATH, network latency).
//
// It provisions loopback ssh by MUTATING ~/.ssh (an ephemeral key appended
// to authorized_keys, a Host alias appended to config; both restored on
// cleanup), so it gates on BYRE_SSH_LOOP_TESTS=1 on top of the docker gate —
// set by byre-inttest for the sacrificial VM, never by a developer's default
// run on a real machine.
// provisionLoopbackSSH makes `ssh byre-test-loopback` reach this machine's
// own sshd with an ephemeral key: skips without BYRE_SSH_LOOP_TESTS=1 (it
// edits ~/.ssh — sacrificial machines only; every mutation restores the
// exact prior bytes on cleanup), then probes. A second alias,
// byre-test-loopback-down, points at a refused port for 255-path tests.
func provisionLoopbackSSH(t *testing.T) {
	t.Helper()
	if os.Getenv("BYRE_SSH_LOOP_TESTS") != "1" {
		t.Skip("set BYRE_SSH_LOOP_TESTS=1 to run loopback-ssh tests (they edit ~/.ssh — sacrificial machines only)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	key := filepath.Join(tmp, "loop-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	appendRestoring(t, filepath.Join(sshDir, "authorized_keys"), string(pub))
	appendRestoring(t, filepath.Join(sshDir, "config"), fmt.Sprintf(`
Host byre-test-loopback byre-test-loopback-down
  HostName 127.0.0.1
  IdentityFile %s
  IdentitiesOnly yes
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  BatchMode yes
  ConnectTimeout 5
Host byre-test-loopback-down
  Port 9
`, key))
	if out, err := exec.Command("ssh", "byre-test-loopback", "true").CombinedOutput(); err != nil {
		t.Fatalf("loopback ssh probe failed (is sshd running here?): %v\n%s", err, out)
	}
}

// startTestBox builds the project's image and starts a sleep box, with all
// cleanups registered; it returns the running container id.
func startTestBox(t *testing.T, r *runner.Runner, p project.Paths, proj string, ident runner.Identity) string {
	t.Helper()
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}
	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"sleep", "120"}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	args := runner.RunArgs(params)
	args = append([]string{args[0], "-d"}, args[1:]...)
	if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
		t.Fatalf("box failed to start: %v\n%s", err, out)
	}
	ids, err := r.RunningContainersByLabel(labelKey + "=" + p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("session discovery for %s = (%v, %v)", p.ID, ids, err)
	}
	return ids[0]
}

func TestIntegrationDeliverLoopbackSSH(t *testing.T) {
	r := requireEngineRunner(t)
	provisionLoopbackSSH(t)
	tmp := t.TempDir()

	// The remote byre is BUILT from this tree — both ends of the wire run
	// the code under test — and lands in a directory with a space, so the
	// argv the remote shell evaluates actually exercises the quoting.
	binDir := filepath.Join(tmp, "remote bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "byre")
	build := exec.Command("go", "build", "-o", bin, "./cmd/byre")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building byre: %v\n%s", err, out)
	}

	// A live box, exactly as the fake-ssh loop test runs one.
	p, proj := testPaths(t)
	ident := testIdentity(t, r)
	ids := []string{startTestBox(t, r, p, proj, ident)}

	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "bug", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(srcDir, "bug", "notes.txt"), []byte("notes\n"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "bug", "sub", "deep.txt"), []byte("deep\n"), 0o644)
	top := filepath.Join(srcDir, "top.txt")
	os.WriteFile(top, []byte("top\n"), 0o644)
	sources := deliver.PathSources([]string{filepath.Join(srcDir, "bug"), top})

	target, isSSH, err := deliver.ParseSSHTarget("ssh://byre-test-loopback")
	if err != nil || !isSSH {
		t.Fatalf("target = (%v, %v)", isSSH, err)
	}

	// The full two-leg flow over real ssh: enumeration first (the picker
	// pins OUR box, so a leftover box on the machine can't misroute the
	// test), then the tar leg into it.
	var out, errw strings.Builder
	cfg := deliver.Config{Out: &out, Err: &errw, Pick: func(ss []deliver.Session) (deliver.Session, bool, error) {
		for _, s := range ss {
			if s.ID == ids[0] {
				return s, true, nil
			}
		}
		return deliver.Session{}, false, fmt.Errorf("our box %s missing from the remote list: %+v", ids[0], ss)
	}}
	landed, err := deliver.RunRemote(cfg, deliver.Options{RemoteByre: bin, NoClip: true}, target, sources, sshExec, false)
	if err != nil {
		t.Fatalf("loopback delivery failed: %v\nstderr: %s", err, errw.String())
	}
	if strings.Join(landed, " ") != "/inbox/bug /inbox/top.txt" {
		t.Fatalf("landed = %v\nstderr: %s", landed, errw.String())
	}
	for path, want := range map[string]string{
		"/inbox/bug/notes.txt":    "notes\n",
		"/inbox/bug/sub/deep.txt": "deep\n",
		"/inbox/top.txt":          "top\n",
	} {
		got, err := r.ExecInput(ids[0], ident.UID, ident.GID, nil, "cat", path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	// Exit-code translation through a REAL sshd: a missing remote binary is
	// the shell's 127, and byre must say "PATH", not "exit status".
	_, err = deliver.RunRemote(cfg, deliver.Options{RemoteByre: "/nonexistent/byre", NoClip: true}, target, sources, sshExec, false)
	if err == nil || !strings.Contains(err.Error(), "ssh PATH") {
		t.Fatalf("127 translation: err = %v", err)
	}

	// And ssh's own 255 (a refused port) must blame the transport.
	down, _, err := deliver.ParseSSHTarget("ssh://byre-test-loopback-down")
	if err != nil {
		t.Fatal(err)
	}
	_, err = deliver.RunRemote(cfg, deliver.Options{RemoteByre: bin, NoClip: true}, down, sources, sshExec, false)
	if err == nil || !strings.Contains(err.Error(), "ssh to") {
		t.Fatalf("255 translation: err = %v", err)
	}
}

// TestIntegrationTUIPickerDeliver drives the real binary's TTY picker in a
// tmux pane against two live boxes: `byre deliver <file>` from a neutral
// cwd finds both, the picker renders, the test steers the cursor to the
// second project's row, and the delivery lands where the pick said. (TUI
// tier of the harness design; lives HERE because everything sharing docker
// or loopback-ssh state stays in this one serial test binary — the
// serialization rule the design records.)
func TestIntegrationTUIPickerDeliver(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	ident := testIdentity(t, r)
	p1, proj1 := testPaths(t)
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	id1 := startTestBox(t, r, p1, proj1, ident)
	id2 := startTestBox(t, r, p2, proj2, ident)

	src := filepath.Join(t.TempDir(), "picked.txt")
	if err := os.WriteFile(src, []byte("picked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := tuitest.Start(t, tuitest.Opts{}, tuitest.Binary(t), "deliver", src)
	s.WaitFor("deliver to which box?")
	steerPickTo(t, s, p2.ID)
	s.Keys("Enter")
	s.WaitFor("/inbox/picked.txt")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}

	got, err := r.ExecInput(id2, ident.UID, ident.GID, nil, "cat", "/inbox/picked.txt")
	if err != nil || got != "picked\n" {
		t.Fatalf("picked box content = (%q, %v)", got, err)
	}
	if out, err := r.ExecInput(id1, ident.UID, ident.GID, nil, "sh", "-c", "ls /inbox"); err == nil && strings.Contains(out, "picked.txt") {
		t.Fatalf("the delivery ALSO landed in the unpicked box: %q", out)
	}
}

// steerPickTo moves the picker's highlight onto the row containing target.
// After each Down it waits for the highlight to actually MOVE before reading
// it again — sampling a stale frame would double-step past the target. Row
// order isn't promised, and a leftover box on the machine must not break
// the pick.
func steerPickTo(t *testing.T, s *tuitest.Session, target string) {
	t.Helper()
	highlighted := func() string {
		for _, l := range strings.Split(s.CaptureNow(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "> ") {
				return l
			}
		}
		return ""
	}
	row := highlighted()
	for i := 0; i < 10 && !strings.Contains(row, target); i++ {
		s.Keys("Down")
		moved := row
		for j := 0; j < 40 && moved == row; j++ {
			time.Sleep(50 * time.Millisecond)
			moved = highlighted()
		}
		if moved == row {
			break // bottom of the list: the cursor stops moving
		}
		row = moved
	}
	if !strings.Contains(row, target) {
		t.Fatalf("never reached %s's row:\n%s", target, s.CaptureNow())
	}
}

// TestIntegrationTUIPickerOnDevTTY pins ssh's contract, adopted for byre's
// prompts (ADR 0038's resolved question): stdin carries the payload (a
// pipe), and the picker still appears — read from /dev/tty, rendered to
// stderr — while the piped bytes become the delivery.
func TestIntegrationTUIPickerOnDevTTY(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	ident := testIdentity(t, r)
	p1, proj1 := testPaths(t)
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	id1 := startTestBox(t, r, p1, proj1, ident)
	id2 := startTestBox(t, r, p2, proj2, ident)

	bin := tuitest.Binary(t)
	s := tuitest.Start(t, tuitest.Opts{}, "sh", "-c",
		fmt.Sprintf("printf 'hello from a pipe' | '%s' deliver - --name piped.txt", bin))
	s.WaitFor("deliver to which box?")
	steerPickTo(t, s, p2.ID)
	s.Keys("Enter")
	s.WaitFor("/inbox/piped.txt")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	got, err := r.ExecInput(id2, ident.UID, ident.GID, nil, "cat", "/inbox/piped.txt")
	if err != nil || got != "hello from a pipe" {
		t.Fatalf("picked box content = (%q, %v)", got, err)
	}
	if out, err := r.ExecInput(id1, ident.UID, ident.GID, nil, "sh", "-c", "ls /inbox"); err == nil && strings.Contains(out, "piped.txt") {
		t.Fatalf("the delivery ALSO landed in the unpicked box: %q", out)
	}
}

// TestIntegrationTUIPickerCancel pins the picker's other exit: q abandons
// the delivery cleanly — the cancel notice, exit 0, and nothing lands
// anywhere.
func TestIntegrationTUIPickerCancel(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	ident := testIdentity(t, r)
	p1, proj1 := testPaths(t)
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	id1 := startTestBox(t, r, p1, proj1, ident)
	id2 := startTestBox(t, r, p2, proj2, ident)

	src := filepath.Join(t.TempDir(), "unwanted.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := tuitest.Start(t, tuitest.Opts{}, tuitest.Binary(t), "deliver", src)
	s.WaitFor("deliver to which box?")
	s.Keys("q")
	s.WaitFor("cancelled — nothing delivered")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("cancel should exit 0, got %d\n%s", st, s.CaptureNow())
	}
	for _, id := range []string{id1, id2} {
		if out, err := r.ExecInput(id, ident.UID, ident.GID, nil, "sh", "-c", "ls /inbox"); err == nil && strings.Contains(out, "unwanted.txt") {
			t.Fatalf("cancelled delivery still landed in %s: %q", id, out)
		}
	}
}

// TestIntegrationTUIMeterFinalState delivers a >256 KiB payload over real
// loopback ssh with a TTY, and asserts the FINAL terminal state only: the
// meter resolved to a sent-total, the remote's notes sit on their own
// lines, the landed path printed, exit 0. Mid-transfer meter observation is
// deliberately not claimed (design: it races an unthrottled loopback
// transfer; the guard's byte ordering stays pinned by the unit tests).
func TestIntegrationTUIMeterFinalState(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	provisionLoopbackSSH(t)
	ident := testIdentity(t, r)
	p, proj := testPaths(t)
	id := startTestBox(t, r, p, proj, ident)

	big := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := tuitest.Binary(t)
	// --box pins the target (enumeration is tier 1's claim; a leftover box
	// must not turn the sole-box auto-pick into a picker here).
	s := tuitest.Start(t, tuitest.Opts{}, bin,
		"deliver", "ssh://byre-test-loopback", big, "--remote-byre", bin, "--box", id)
	s.WaitFor("/inbox/big.bin")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	final := s.CaptureNow()
	if !strings.Contains(final, "byre: sent ") {
		t.Fatalf("meter never resolved to a sent-total:\n%s", final)
	}
	if !strings.Contains(final, "byre: delivered 1 file") {
		t.Fatalf("remote note missing from the final terminal:\n%s", final)
	}

	got, err := r.ExecInput(id, ident.UID, ident.GID, nil, "sh", "-c", "wc -c < /inbox/big.bin")
	if err != nil || strings.TrimSpace(got) != "1048576" {
		t.Fatalf("delivered size = (%q, %v)", got, err)
	}
}

// appendRestoring appends text to path (creating it 0600 if absent) and
// restores the exact prior state — original bytes, or absence — on cleanup.
func appendRestoring(t *testing.T, path, text string) {
	t.Helper()
	orig, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	next := append([]byte{}, orig...)
	// A last line without its newline (legal in authorized_keys) must not
	// concatenate with the appended text into one invalid entry.
	if len(next) > 0 && next[len(next)-1] != '\n' {
		next = append(next, '\n')
	}
	next = append(next, []byte(text)...)
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// This restores SHARED credential state (~/.ssh): a failed restore
		// is a loud test failure, never a silent leftover authorization.
		if existed {
			if err := os.WriteFile(path, orig, 0o600); err != nil {
				t.Errorf("restoring %s: %v", path, err)
			}
		} else if err := os.Remove(path); err != nil {
			t.Errorf("removing %s: %v", path, err)
		}
	})
}

// TestIntegrationDeliverRemoteLoop runs remote delivery end to end with the
// ssh binary as the ONLY fake: deliver.RunRemote packs real files through
// the production planner, the "ssh" hop hands the stream straight to
// commands.Deliver in tar mode (dispatch, --proto handshake, deliverConfig
// wiring), and the archive unpacks through the REAL transport scripts into
// a live box — claims, interior mkdirs, uniquify and all (ADR 0037).
func TestIntegrationDeliverRemoteLoop(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}
	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"sleep", "120"}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	args := runner.RunArgs(params)
	args = append([]string{args[0], "-d"}, args[1:]...)
	if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
		t.Fatalf("box failed to start: %v\n%s", err, out)
	}

	// The enumeration leg against the live engine: one row, protocol-shaped,
	// naming this box.
	var listOut, listErr strings.Builder
	ls := Streams{Out: &listOut, Err: &listErr, In: strings.NewReader("")}
	lcfg, err := deliverConfig(ls, proj, []sessionRunner{r}, os.Getuid(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := deliver.Boxes(lcfg, deliver.Options{})
	if err != nil || partial {
		t.Fatalf("Boxes = partial %v, err %v\nstderr: %s", partial, err, listErr.String())
	}
	rows, err := deliver.ParseBoxes(listOut.String())
	if err != nil {
		t.Fatalf("the live listing broke its own grammar: %v\n%s", err, listOut.String())
	}
	if len(rows) != 1 || rows[0].Project != p.ID {
		t.Fatalf("rows = %+v, want exactly this box (project %s)", rows, p.ID)
	}

	// Sources: a tree and a top-level file.
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "bug", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(srcDir, "bug", "notes.txt"), []byte("notes\n"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "bug", "sub", "deep.txt"), []byte("deep\n"), 0o644)
	top := filepath.Join(srcDir, "top.txt")
	os.WriteFile(top, []byte("top\n"), 0o644)

	// The ssh hop: assert the frozen argv shape, then run the remote side
	// for real — commands.Deliver dispatching tar mode into the live box.
	sshLoop := func(tgt deliver.SSHTarget, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
		want := []string{"byre", "deliver", "--proto", "1", "--box", rows[0].ID, "--no-clip", "--tar", "-"}
		if strings.Join(argv, " ") != strings.Join(want, " ") {
			t.Fatalf("remote argv = %v, want %v", argv, want)
		}
		s2 := Streams{Out: stdout, Err: stderr, In: stdin}
		return Deliver(s2, proj, deliver.Options{Tar: true, Proto: 1, Box: rows[0].ID, NoClip: true}, []string{"-"})
	}
	deliverRemoteOnce := func() []string {
		var out, errw strings.Builder
		cfg := deliver.Config{Out: &out, Err: &errw}
		landed, err := deliver.RunRemote(cfg, deliver.Options{Box: rows[0].ID, NoClip: true},
			deliver.SSHTarget{Host: "loop"}, deliver.PathSources([]string{filepath.Join(srcDir, "bug"), top}), sshLoop, false)
		if err != nil {
			t.Fatalf("remote delivery failed: %v\nstderr: %s", err, errw.String())
		}
		return landed
	}

	landed := deliverRemoteOnce()
	if strings.Join(landed, " ") != "/inbox/bug /inbox/top.txt" {
		t.Fatalf("landed = %v", landed)
	}
	ids, err := r.RunningContainersByLabel(labelKey + "=" + p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("session discovery = (%v, %v)", ids, err)
	}
	for path, want := range map[string]string{
		"/inbox/bug/notes.txt":    "notes\n",
		"/inbox/bug/sub/deep.txt": "deep\n",
		"/inbox/top.txt":          "top\n",
	} {
		got, err := r.ExecInput(ids[0], ident.UID, ident.GID, nil, "cat", path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	// Again: every top-level name claims -2, nothing clobbers.
	if again := deliverRemoteOnce(); strings.Join(again, " ") != "/inbox/bug-2 /inbox/top-2.txt" {
		t.Fatalf("second landed = %v", again)
	}
}
