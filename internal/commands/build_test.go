package commands

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/lock"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

func plainResolved() resolved {
	return combine(merged(config.Config{}), skills.Resolved{})
}

// assertNoSession pins what separates a build from develop: nothing seeded,
// no container, no launch record, no engine record.
func assertNoSession(t *testing.T, f *fakeRunner, p project.Paths) {
	t.Helper()
	if len(f.creates) != 0 || len(f.starts) != 0 || len(f.startIOs) != 0 {
		t.Errorf("a build creates and starts nothing: creates %v, starts %v", f.creates, f.starts)
	}
	if len(f.created) != 0 || len(f.seeded) != 0 || len(f.literals) != 0 || len(f.fileSeed) != 0 || len(f.probes) != 0 {
		t.Errorf("a build seeds and probes nothing: created %v, seeded %v, literals %v, files %v, probes %v",
			f.created, f.seeded, f.literals, f.fileSeed, f.probes)
	}
	for _, path := range []string{launchesDir(p), engineRecordPath(p)} {
		if ok, err := hostopen.ExistsNoFollow(path); err != nil || ok {
			t.Errorf("a build writes no session record, but %s exists (err %v)", path, err)
		}
	}
}

// The build runs while the setup lock is held (a lock attempt from inside the
// engine's Build finds it taken), stdout is exactly the tag, and nothing of a
// session happens.
func TestBuildBuildsUnderTheLockAndPrintsOnlyTheTag(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{}
	lockHeld := false
	f.buildHook = func() {
		l, ok, err := lock.TryAcquire(p.LockFile)
		if err != nil {
			t.Errorf("lock probe: %v", err)
			return
		}
		if ok {
			_ = l.Release()
			return
		}
		lockHeld = true
	}
	s, out, _ := testStreams("", false)
	if err := buildImage(f, s, p, plainResolved()); err != nil {
		t.Fatal(err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	if !lockHeld {
		t.Error("the image must be built under the setup lock")
	}
	if len(f.builds) != 1 || f.builds[0] != image {
		t.Errorf("want one cached build of %s, got %v", image, f.builds)
	}
	if out.String() != image+"\n" {
		t.Errorf("stdout must be the tag and nothing else, got %q", out.String())
	}
	assertNoSession(t, f, p)
}

// A live session of this worktree does not refuse a build: the build shares
// only the context dir and the tag, which the setup lock serializes.
func TestBuildRunsWhileASessionIsLive(t *testing.T) {
	p, _ := testPaths(t)
	f := &fakeRunner{live: map[string][]string{workdirLabel(p): {"abc123"}}}
	s, out, stderr := testStreams("", false)
	if err := buildImage(f, s, p, plainResolved()); err != nil {
		t.Fatalf("a build must not refuse a live session: %v", err)
	}
	image := imageTag(p.ID, os.Getuid(), os.Getgid())
	if len(f.builds) != 1 || out.String() != image+"\n" {
		t.Errorf("want the build and the tag, got builds %v, stdout %q", f.builds, out.String())
	}
	if strings.Contains(stderr.String(), "abc123") {
		t.Errorf("a build reports no live session: %s", stderr.String())
	}
}

// Another setup holding the lock makes the build wait, not fail: nothing is
// built until it is released.
func TestBuildWaitsForTheSetupLock(t *testing.T) {
	p, _ := testPaths(t)
	lk, err := lock.Acquire(p.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	s, out, stderr := testStreams("", false)
	notice := lockWaitWriter(stderr)
	s.Err = notice
	done := make(chan error, 1)
	go func() { done <- buildImage(f, s, p, plainResolved()) }()
	select {
	case <-notice.waited:
	case <-time.After(30 * time.Second):
		_ = lk.Release()
		<-done
		t.Fatal("the build never queued on the held setup lock")
	}
	select {
	case err := <-done:
		t.Fatalf("the build returned while another setup held the lock: %v", err)
	default:
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(f.builds) != 1 || out.String() != imageTag(p.ID, os.Getuid(), os.Getgid())+"\n" {
		t.Errorf("want the build once the lock was free, got builds %v, stdout %q", f.builds, out.String())
	}
}

// What is built is the configuration read under the lock: a save that moved
// the engine while the build waited is refused before anything is built.
func TestBuildRereadsTheConfigUnderTheLock(t *testing.T) {
	p, _ := testPaths(t)
	rv := plainResolved()
	rv.reread = func() (resolved, error) {
		return combine(merged(config.Config{Engine: "podman"}), skills.Resolved{}), nil
	}
	f := &fakeRunner{}
	s, out, _ := testStreams("", false)
	err := buildImage(f, s, p, rv)
	if err == nil || !strings.Contains(err.Error(), "configured engine changed") || !strings.Contains(err.Error(), "while build waited") {
		t.Fatalf("want the engine-changed refusal naming build, got %v", err)
	}
	if len(f.builds) != 0 || out.Len() != 0 {
		t.Errorf("nothing may be built or printed: builds %v, stdout %q", f.builds, out.String())
	}
}

// A failed engine build is a byre failure: the engine's error comes back as a
// plain error (main's banner, exit 1), and no tag is printed.
func TestBuildFailureReturnsTheEngineError(t *testing.T) {
	p, _ := testPaths(t)
	engineErr := errors.New("exit status 1")
	f := &fakeRunner{buildErr: engineErr}
	s, out, _ := testStreams("", false)
	err := buildImage(f, s, p, plainResolved())
	if !errors.Is(err, engineErr) {
		t.Fatalf("want the engine's build error back, got %v", err)
	}
	var exitErr ExitError
	if errors.As(err, &exitErr) {
		t.Errorf("a failed build is a byre failure with a banner, not an ExitError: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("a failed build prints no tag, got %q", out.String())
	}
	assertNoSession(t, f, p)
}

// An unconfigured project is refused unless both --template and --agent are
// given, before anything is enrolled -- the run rule, since a build has no
// place for the first-run questions.
func TestBuildRefusesAnUnconfiguredProject(t *testing.T) {
	for _, o := range []BuildOptions{{}, {Agent: "claude"}, {Template: "none"}} {
		t.Setenv("BYRE_HOME", t.TempDir())
		proj := t.TempDir()
		s, out, stderr := testStreams("", true)
		err := Build(s, proj, o)
		var exitErr ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 1 {
			t.Fatalf("%+v: want ExitError{1}, got %v", o, err)
		}
		if !strings.Contains(stderr.String(), "not configured here") || !strings.Contains(stderr.String(), "--template <t> and --agent <a>") {
			t.Errorf("%+v: want the not-configured refusal with its remedy, got %q", o, stderr.String())
		}
		if out.Len() != 0 {
			t.Errorf("%+v: a refusal prints no tag, got %q", o, out.String())
		}
		p, err := project.Resolve(proj)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := hostopen.ExistsNoFollow(p.Dir); err != nil || ok {
			t.Errorf("%+v: a refused build must enroll nothing, but %s exists (err %v)", o, p.Dir, err)
		}
	}
}
