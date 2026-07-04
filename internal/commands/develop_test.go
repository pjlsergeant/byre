package commands

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/skills"
)

// liveWorkdir marks a session live for the project's worktree label — the
// label develop's fast path and run-race re-check query.
func liveWorkdir(p project.Paths, ids ...string) map[string][]string {
	return map[string][]string{workdirKey + "=" + p.WorktreeID: ids}
}

// exitError produces a real *exec.ExitError with the given exit code, so tests
// exercise develop's status mapping against the type docker's CLI failure
// actually returns.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected sh to exit %d", code)
	}
	return err
}

func TestDevelopRefusesWhenSessionLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: liveWorkdir(p, "abcdef0123456789")}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitRefused {
		t.Fatalf("expected ExitError{%d}, got %v", ExitRefused, err)
	}
	if len(f.builds) != 0 || len(f.runs) != 0 {
		t.Fatalf("must not build or run when a session is live: builds=%v runs=%v", f.builds, f.runs)
	}
}

func TestDevelopBuildsSeedsThenRuns(t *testing.T) {
	p, _ := testPaths(t)
	seedSrc := t.TempDir()
	cfg := config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude", Seed: &config.Seed{Host: seedSrc}},
	}}
	f := &fakeRunner{}
	if err := develop(f, discardStreams(), p, combine(cfg, skills.Resolved{}), false); err != nil {
		t.Fatal(err)
	}
	image := ImageTag(p.ID, os.Getuid(), os.Getgid())
	if len(f.builds) != 1 || f.builds[0] != image {
		t.Fatalf("expected one cached build of %s, got %v", image, f.builds)
	}
	if len(f.seeded) != 1 || f.seeded[0] != VolumeName(p.ID, ".claude") {
		t.Fatalf("expected the state volume seeded, got %v", f.seeded)
	}
	if len(f.runs) != 1 {
		t.Fatalf("expected one run, got %v", f.runs)
	}
	// Build, then seed, then run — seeding uses the image just built, and the
	// interactive run must come after setup completes.
	ops := strings.Join(f.ops, " | ")
	bi, si, ri := strings.Index(ops, "build"), strings.Index(ops, "seed"), strings.Index(ops, "run")
	if !(bi >= 0 && bi < si && si < ri) {
		t.Fatalf("expected build < seed < run, got ops %v", f.ops)
	}
	// The run argv is the assembled `run ...` for this project's image.
	argv := strings.Join(f.runs[0], " ")
	if !strings.HasPrefix(argv, "run ") || !strings.Contains(argv, image) {
		t.Fatalf("run argv doesn't run the built image: %s", argv)
	}
	if !strings.Contains(argv, "--name byre-"+p.WorktreeID) {
		t.Fatalf("run argv missing the session container name: %s", argv)
	}
}

func TestDevelopRunRaceReportsRefusal(t *testing.T) {
	p, _ := testPaths(t)
	// Nothing live at the fast path, run fails, and the re-check finds the
	// winner's container: a concurrent develop won the container-name race.
	f := &fakeRunner{
		runErr:     exitError(t, 125),
		liveSecond: liveWorkdir(p, "cafebabe0000"),
	}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitRefused {
		t.Fatalf("expected ExitError{%d} after losing the run race, got %v", ExitRefused, err)
	}
}

func TestDevelopAgentExitCodePassesThrough(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{runErr: exitError(t, 7)}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("expected the agent's own exit 7 passed through, got %v", err)
	}
}

func TestDevelopEngineFailureStaysByreError(t *testing.T) {
	p, _ := testPaths(t)
	// Docker reserves 125-127 for engine-level failures; with no session live at
	// the re-check, that must surface as a byre error, not the agent's status.
	f := &fakeRunner{runErr: exitError(t, 126)}
	err := develop(f, discardStreams(), p, combine(config.Config{}, skills.Resolved{}), false)
	var exitErr ExitError
	if err == nil || errors.As(err, &exitErr) {
		t.Fatalf("engine failure must stay an ordinary error, got %v", err)
	}
}

func TestDevelopSelfEditNotesAndMount(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	s, _, stderr := testStreams("", false)
	if err := develop(f, s, p, combine(config.Config{}, skills.Resolved{}), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "self-edit on") {
		t.Errorf("expected the self-edit note on stderr: %s", stderr.String())
	}
	if argv := strings.Join(f.runs[0], " "); !strings.Contains(argv, "target="+selfEditTarget) {
		t.Errorf("run argv missing the self-edit mount: %s", argv)
	}
}

func TestRebuildBuildsNoCache(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	var out bytes.Buffer
	if err := rebuild(&out, f, p, config.Config{}, skills.Resolved{}); err != nil {
		t.Fatal(err)
	}
	image := ImageTag(p.ID, os.Getuid(), os.Getgid())
	if len(f.builds) != 1 || f.builds[0] != image+" nocache" {
		t.Fatalf("expected one --no-cache build of %s, got %v", image, f.builds)
	}
}
