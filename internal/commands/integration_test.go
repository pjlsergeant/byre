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
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
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

	// Login shells keep the image's ENV PATH (QA pass-2: a go-template box had
	// no `go` in `byre shell` — /etc/profile resets PATH; byre-env.sh restores
	// it from the baked /etc/byre/image-path). The core block's own
	// /home/dev/.local/bin entry is the sentinel: /etc/profile drops it, so
	// its survival in `bash -l` proves capture + restore end to end.
	out, err = exec.Command(string(r.Engine()), "run", "--rm",
		"--entrypoint", "bash", image, "-lc",
		`test -f /etc/byre/image-path && printf '%s' "$PATH"`).CombinedOutput()
	if err != nil {
		t.Fatalf("login-shell PATH probe failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "/home/dev/.local/bin") {
		t.Fatalf("login shell lost the image ENV PATH (no /home/dev/.local/bin): %s", out)
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
