package commands

// Gated byre-level integration smoke: real docker/podman, run host-side with
//
//	BYRE_DOCKER_TESTS=1 go test ./internal/commands/ -run Integration -v
//
// The core promise checked here is the one no fake can vouch for: the
// GENERATED Dockerfile actually builds on a live engine, and the resulting
// image runs. Expect several minutes on a cold cache (debian pull + apt).

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/pjlsergeant/byre/internal/runner"
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
