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

func TestDetectAutoNoEngine(t *testing.T) {
	if _, err := Detect("auto", fakeLook()); err == nil {
		t.Fatal("expected error when no engine present")
	}
}

func TestDetectExplicitMissing(t *testing.T) {
	if _, err := Detect("docker", fakeLook("podman")); err == nil {
		t.Fatal("expected error when explicit engine missing")
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
	_, err := Detect("containerd", fakeLook("containerd"))
	if err == nil {
		t.Fatal("expected error for unknown engine setting")
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

func TestRunnerEngineAccessor(t *testing.T) {
	if got := New(Docker).Engine(); got != Docker {
		t.Fatalf("Engine() = %q", got)
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
}
