package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// fakeLook returns a LookPath that "finds" only the named binaries.
func fakeLook(found ...string) LookPath {
	set := map[string]bool{}
	for _, f := range found {
		set[f] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestDetectAutoPrefersDocker(t *testing.T) {
	e, exe, err := Detect("auto", fakeLook("docker", "podman"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Docker {
		t.Fatalf("auto with both = %q, want docker", e)
	}
	// The ABSOLUTE path comes back with the name: discarding it is what left
	// every engine call re-reading PATH.
	if exe != "/usr/bin/docker" {
		t.Errorf("exe = %q, want the resolved absolute path", exe)
	}
}

func TestDetectAutoFallsBackToPodman(t *testing.T) {
	e, exe, err := Detect("auto", fakeLook("podman"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Podman {
		t.Fatalf("auto with only podman = %q, want podman", e)
	}
	if exe != "/usr/bin/podman" {
		t.Errorf("exe = %q, want the resolved absolute path", exe)
	}
}

// Detect fails three distinct ways -- nothing on PATH, the named engine
// missing, an unknown setting -- and each test names which one, or the wrong
// rule keeps it green.
func TestDetectAutoNoEngine(t *testing.T) {
	_, _, err := Detect("auto", fakeLook())
	if err == nil {
		t.Fatal("expected error when no engine present")
	}
	if !strings.Contains(err.Error(), "no container engine found on PATH") {
		t.Errorf("wrong rule fired: %v", err)
	}
}

func TestDetectExplicitMissing(t *testing.T) {
	_, _, err := Detect("docker", fakeLook("podman"))
	if err == nil {
		t.Fatal("expected error when explicit engine missing")
	}
	// Must name the OFFENDING engine, and must not be the unknown-setting rule.
	if !strings.Contains(err.Error(), `engine "docker" not found on PATH`) {
		t.Errorf("wrong rule fired: %v", err)
	}
}

// A lookup that refuses -- hostexec declining a docker resolved out of the
// project tree -- must NOT be stepped over the way "not installed" is: auto
// would otherwise hand back podman and hide the shadowed binary behind a
// working session. Both arms report the refusal itself.
func TestDetectSurfacesNonAbsenceLookupFailures(t *testing.T) {
	refuse := func(name string) (string, error) {
		if name == "docker" {
			return "", errors.New("declines to run docker: resolved inside /proj")
		}
		return "/usr/bin/" + name, nil
	}
	for _, setting := range []string{"auto", "docker"} {
		_, _, err := Detect(setting, refuse)
		if err == nil {
			t.Fatalf("%s: expected the refusal to surface", setting)
		}
		if !strings.Contains(err.Error(), "declines to run docker") {
			t.Errorf("%s: wrong rule fired: %v", setting, err)
		}
	}
}

func TestDetectExplicitFound(t *testing.T) {
	e, exe, err := Detect("podman", fakeLook("podman"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Podman {
		t.Fatalf("explicit podman = %q", e)
	}
	if exe != "/usr/bin/podman" {
		t.Errorf("exe = %q, want the resolved absolute path", exe)
	}
}

func TestDetectUnknown(t *testing.T) {
	// fakeLook("containerd") makes it present on PATH, so a not-found rejection
	// here would be the wrong rule -- the setting itself is what's unknown.
	_, _, err := Detect("containerd", fakeLook("containerd"))
	if err == nil {
		t.Fatal("expected error for unknown engine setting")
	}
	if !strings.Contains(err.Error(), `unknown engine "containerd"`) {
		t.Errorf("wrong rule fired: %v", err)
	}
}

func TestEmptyDefaultsToAuto(t *testing.T) {
	e, _, err := Detect("", fakeLook("docker"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Docker {
		t.Fatalf(`"" = %q, want docker`, e)
	}
}

func TestIsRootlessPodman(t *testing.T) {
	// Docker (incl. rootless Docker) is out of scope: false WITHOUT querying.
	queried := false
	r := &Runner{engine: Docker, capture: func(string, ...string) (string, error) {
		queried = true
		return "", nil
	}}
	if rootless, err := r.IsRootlessPodman(); err != nil || rootless {
		t.Fatalf("docker = (%v, %v), want (false, nil)", rootless, err)
	}
	if queried {
		t.Fatal("docker must not query the engine")
	}

	// Podman: parse the `info` rootless field, trimming whitespace.
	for out, want := range map[string]bool{"true\n": true, "false\n": false, "  true  ": true} {
		var gotArgs []string
		r := &Runner{engine: Podman, capture: func(name string, args ...string) (string, error) {
			gotArgs = append([]string{name}, args...)
			return out, nil
		}}
		got, err := r.IsRootlessPodman()
		if err != nil || got != want {
			t.Fatalf("podman info %q = (%v, %v), want %v", out, got, err, want)
		}
		if want := "podman info --format {{.Host.Security.Rootless}}"; strings.Join(gotArgs, " ") != want {
			t.Fatalf("queried %q, want %q", strings.Join(gotArgs, " "), want)
		}
	}

	// A query error propagates so the caller can stay quiet instead of guessing.
	r = &Runner{engine: Podman, capture: func(string, ...string) (string, error) {
		return "", fmt.Errorf("boom")
	}}
	if _, err := r.IsRootlessPodman(); err == nil {
		t.Fatal("expected the query error to propagate")
	}

	// Anything that is not one of the template's two answers is INCONCLUSIVE,
	// not false: the mode-select would otherwise hand a rootless engine the
	// rootful identity on an empty string or a moved field, and every one of
	// those shapes reads as "not true".
	for _, out := range []string{"", "<no value>", "Rootless: true", "yes"} {
		r := &Runner{engine: Podman, capture: func(string, ...string) (string, error) {
			return out, nil
		}}
		got, err := r.IsRootlessPodman()
		if err == nil {
			t.Errorf("podman info %q = (%v, nil), want an inconclusive error", out, got)
			continue
		}
		if !strings.Contains(err.Error(), "no usable rootless answer") {
			t.Errorf("podman info %q: err = %v, want the inconclusive-answer rule", out, err)
		}
	}
	// An engine handing back a wall of text must not become a wall of error.
	r = &Runner{engine: Podman, capture: func(string, ...string) (string, error) {
		return strings.Repeat("x", 5000), nil
	}}
	if _, err := r.IsRootlessPodman(); err == nil || len(err.Error()) > 200 {
		t.Errorf("an oversized answer must be bounded in the error: %v", err)
	}
}

// The bound is real: a child that never answers is killed and reported as a
// timeout, not left holding byre's goroutine. The netns pair runs from a
// goroutine racing a live box, so "waits forever" there means the agent parks
// at the launch gate with nothing coming.
func TestCaptureBoundedKillsAChildThatNeverAnswers(t *testing.T) {
	testtools.NeedTool(t, "sleep")
	start := time.Now()
	_, err := captureBoundedExec(50*time.Millisecond, "sleep", "60")
	if err == nil {
		t.Fatal("a child past the deadline must be an error, not a wait")
	}
	if !strings.Contains(err.Error(), "no answer within") {
		t.Errorf("the timeout must report itself, not a bare kill signal: %v", err)
	}
	if el := time.Since(start); el > 30*time.Second {
		t.Errorf("the deadline did not fire: waited %s", el)
	}
	// A child that answers in time is unaffected.
	if out, err := captureBoundedExec(time.Minute, "sleep", "0"); err != nil || out != "" {
		t.Errorf("a prompt child must pass through: %q %v", out, err)
	}
}

// A deadline kills the engine CLIENT; the container it started keeps running.
// For the netns helper that is a fail-open window -- it still holds NET_ADMIN
// over the box's netns and can still open the launch gate -- so byre names the
// helper and kills it by name whenever the call fails.
func TestHelperContainerIsNamedAndKilledOnFailure(t *testing.T) {
	pinHelperName(t, "byre-netns-cafe")
	for _, tc := range []struct {
		name string
		run  func(r *Runner) error
		kind string
	}{
		{"netns", func(r *Runner) error {
			return r.NetnsInit("img", "byre-box", "/fw", nil, false)
		}, "netns"},
		{"sockprobe", func(r *Runner) error {
			_, err := r.ProbeSockGroup("img", "/h", "/t", "")
			return err
		}, "sockprobe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			r := &Runner{engine: Docker, captureBounded: func(d time.Duration, name string, args ...string) (string, error) {
				calls = append(calls, append([]string{name}, args...))
				if args[0] == "run" {
					return "", errors.New("deadline")
				}
				return "", nil
			}}
			if err := tc.run(r); err == nil {
				t.Fatal("the failure must reach the caller")
			}
			if len(calls) != 2 {
				t.Fatalf("a failed helper must be cleaned up: calls=%v", calls)
			}
			if got := strings.Join(calls[0], " "); !strings.Contains(got, "--name byre-netns-cafe") {
				t.Errorf("the helper must be named, or it cannot be stopped: %q", got)
			}
			if got := strings.Join(calls[1], " "); got != "docker kill byre-netns-cafe" {
				t.Errorf("cleanup must kill the named helper: %q", got)
			}
		})
	}

	// A call that succeeds leaves nothing to clean up (--rm did it).
	var calls int
	r := &Runner{engine: Docker, captureBounded: func(d time.Duration, name string, args ...string) (string, error) {
		calls++
		return "989\n", nil
	}}
	if _, err := r.ProbeSockGroup("img", "/h", "/t", ""); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("a successful helper must not be killed: %d engine calls", calls)
	}
}

// Without randomness there is no name, and without a name there is no way to
// stop the container afterwards -- so byre does not start one. For the netns
// hook the caller turns that into a stopped box.
func TestHelperRefusesToStartWithoutACleanupHandle(t *testing.T) {
	orig := helperName
	helperName = func(kind string) (string, error) { return "", errors.New("no randomness") }
	t.Cleanup(func() { helperName = orig })

	called := false
	r := &Runner{engine: Docker, captureBounded: func(d time.Duration, name string, args ...string) (string, error) {
		called = true
		return "", nil
	}}
	if err := r.NetnsInit("img", "byre-box", "/fw", nil, false); err == nil {
		t.Fatal("no cleanup handle must refuse, not run blind")
	}
	if called {
		t.Error("byre must not start a container it cannot later stop")
	}
}

// The stdout cap is real: a child that floods it fails rather than becoming
// byre's memory. These calls answer with an id, a mode or a gid.
func TestCaptureBoundedCapsOutput(t *testing.T) {
	testtools.NeedTool(t, "yes")
	// captureBoundedMax is 8 MiB; `yes` fills it in well under the bound.
	_, err := captureBoundedExec(2*time.Minute, "yes", strings.Repeat("x", 4096))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unbounded child output must fail, not fill memory: %v", err)
	}
}

func TestCaptureInExecCapsOutput(t *testing.T) {
	testtools.NeedTool(t, "yes")
	_, err := captureInExec(nil, "yes", strings.Repeat("x", 4096))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unbounded captured output must fail, not fill memory: %v", err)
	}
}

func TestCaptureInExecOverflowKillsTheWholeGroup(t *testing.T) {
	testtools.NeedTool(t, "sh")
	start := time.Now()
	// The background child inherits stdout and would hold Wait open after the
	// direct shell died. Overflow must kill the whole group and return without
	// waiting for that child.
	_, err := captureInExec(nil, "sh", "-c",
		"(sleep 30) & yes x | head -c $((9 * 1024 * 1024))")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("overflow with descendant error = %v", err)
	}
	if elapsed := time.Since(start); elapsed >= waitDelay {
		t.Fatalf("overflow waited %s: a descendant kept the capture pipes open", elapsed)
	}
}

// The deadline must reach the whole process GROUP. An engine client spawns
// local helpers of its own (credential, transport), and killing the direct
// child leaves them running -- WaitDelay unwedges byre, but the descendants
// outlive it, which is the leak.
//
// Asserted directly: the descendant would touch a marker a second after the
// deadline, and the marker must never appear. The elapsed check is the second
// signal -- a group that died closes the pipes at once, so a call that instead
// takes waitDelay to return is one WaitDelay rescued rather than the kill.
func TestCaptureBoundedKillsTheWholeGroup(t *testing.T) {
	testtools.NeedTool(t, "sh")
	marker := filepath.Join(t.TempDir(), "descendant-lived")
	start := time.Now()
	_, err := captureBoundedExec(150*time.Millisecond, "sh", "-c",
		"(sleep 1; touch "+marker+") & wait")
	if err == nil {
		t.Fatal("a call that outlives its deadline must be an error")
	}
	if el := time.Since(start); el >= waitDelay {
		t.Errorf("the call took %s to return: the group was not killed, WaitDelay was", el)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, serr := os.Stat(marker); serr == nil {
		t.Error("a descendant outlived the deadline: the kill reached the child, not the group")
	}
}
