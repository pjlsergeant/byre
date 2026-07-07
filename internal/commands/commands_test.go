package commands

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
)

func TestVolumeName(t *testing.T) {
	const id = "proj-abc123"
	if got := volumeName(id, "cache"); got != "byre-"+id+"-cache" {
		t.Errorf("volumeName = %q, want byre-%s-cache", got, id)
	}
}

func TestMachineVolumeName(t *testing.T) {
	if got := machineVolumeName(501, "claude-identity"); got != "byre-machine-u501-claude-identity" {
		t.Errorf("machineVolumeName = %q", got)
	}
	// scopedVolumeName resolves the SAME Docker name from two different
	// projects for a machine-scoped volume (the point of the scope, ADR 0017),
	// and the project-scoped name otherwise.
	mv := config.Volume{Name: "claude-identity", Role: "state", Target: "/x", Scope: "machine"}
	a := scopedVolumeName("proj-a-111111", 501, mv)
	b := scopedVolumeName("proj-b-222222", 501, mv)
	if a != b || a != "byre-machine-u501-claude-identity" {
		t.Errorf("machine-scoped names differ across projects: %q vs %q", a, b)
	}
	pv := config.Volume{Name: ".claude", Role: "state", Target: "/x"}
	if got := scopedVolumeName("proj-a-111111", 501, pv); got != "byre-proj-a-111111-.claude" {
		t.Errorf("project-scoped name = %q", got)
	}
	// Two USERS on one machine resolve different names (uid-qualified).
	if machineVolumeName(501, "x") == machineVolumeName(502, "x") {
		t.Error("uid must qualify machine volume names")
	}
}

func TestWarnRootlessPodman(t *testing.T) {
	cases := []struct {
		name string
		c    *fakeRunner
		warn bool
	}{
		{"rootless warns", &fakeRunner{rootless: true}, true},
		{"rootful is quiet", &fakeRunner{rootless: false}, false},
		{"detection error is quiet", &fakeRunner{rootlessErr: errors.New("boom")}, false},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		warnRootlessPodman(&buf, tc.c)
		if got := strings.Contains(buf.String(), "rootless Podman detected"); got != tc.warn {
			t.Errorf("%s: warned=%v, want %v (out=%q)", tc.name, got, tc.warn, buf.String())
		}
	}
}

func TestDockerfilePrintsWithoutTouchingContext(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	s, out, _ := testStreams("", false)
	if err := Dockerfile(s, proj); err != nil {
		t.Fatal(err)
	}

	// Printed bytes must equal the generator output.
	want := gen.Dockerfile(gen.Input{})
	if out.String() != want {
		t.Fatalf("printed output != generator output:\n%s", out.String())
	}

	// `byre dockerfile` is informational and side-effect-free: it must NOT write
	// the Dockerfile or restage the context (that races a concurrent develop
	// build sharing the context dir — the reason it renders instead of assembling).
	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Dockerfile); !os.IsNotExist(err) {
		t.Fatalf("byre dockerfile persisted to disk (should be side-effect-free): %v", err)
	}
}

func TestDockerfileHonorsByreHomeAndCollision(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-claim this id with a different recorded path -> collision.
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, _, _ := testStreams("", false)
	if err := Dockerfile(s, proj); err == nil {
		t.Fatal("expected collision error from Dockerfile, got nil")
	}
}

func TestShellArgQuoting(t *testing.T) {
	cases := map[string]string{
		"plain":                         "plain",
		"type=bind,source=/a,target=/b": "type=bind,source=/a,target=/b", // = and , stay bare
		"127.0.0.1:8080:8080":           "127.0.0.1:8080:8080",
		"has space":                     "'has space'",
		"a'b":                           `'a'\''b'`,
		"":                              "''",
	}
	for in, want := range cases {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %q, want %q", in, got, want)
		}
	}
}
