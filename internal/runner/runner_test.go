package runner

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
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
	e, err := Detect("auto", fakeLook("docker", "podman"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Docker {
		t.Fatalf("auto with both = %q, want docker", e)
	}
}

func TestDetectAutoFallsBackToPodman(t *testing.T) {
	e, err := Detect("auto", fakeLook("podman"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Podman {
		t.Fatalf("auto with only podman = %q, want podman", e)
	}
}

// Detect fails three distinct ways -- nothing on PATH, the named engine
// missing, an unknown setting -- and each test names which one, or the wrong
// rule keeps it green.
func TestDetectAutoNoEngine(t *testing.T) {
	_, err := Detect("auto", fakeLook())
	if err == nil {
		t.Fatal("expected error when no engine present")
	}
	if !strings.Contains(err.Error(), "no container engine found on PATH") {
		t.Errorf("wrong rule fired: %v", err)
	}
}

func TestDetectExplicitMissing(t *testing.T) {
	_, err := Detect("docker", fakeLook("podman"))
	if err == nil {
		t.Fatal("expected error when explicit engine missing")
	}
	// Must name the OFFENDING engine, and must not be the unknown-setting rule.
	if !strings.Contains(err.Error(), `engine "docker" not found on PATH`) {
		t.Errorf("wrong rule fired: %v", err)
	}
}

func TestDetectExplicitFound(t *testing.T) {
	e, err := Detect("podman", fakeLook("podman"))
	if err != nil {
		t.Fatal(err)
	}
	if e != Podman {
		t.Fatalf("explicit podman = %q", e)
	}
}

func TestDetectUnknown(t *testing.T) {
	// fakeLook("containerd") makes it present on PATH, so a not-found rejection
	// here would be the wrong rule -- the setting itself is what's unknown.
	_, err := Detect("containerd", fakeLook("containerd"))
	if err == nil {
		t.Fatal("expected error for unknown engine setting")
	}
	if !strings.Contains(err.Error(), `unknown engine "containerd"`) {
		t.Errorf("wrong rule fired: %v", err)
	}
}

func TestEmptyDefaultsToAuto(t *testing.T) {
	e, err := Detect("", fakeLook("docker"))
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
