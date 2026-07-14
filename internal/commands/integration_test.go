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

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
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
