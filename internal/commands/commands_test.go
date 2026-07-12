package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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

// develop refuses under rootless Podman: the launch is known to create
// wrong-owned files (baked-UID model vs userns remap), so it must not
// complete silently. BYRE_ALLOW_ROOTLESS_PODMAN=1 overrides with the warning
// retained; a detection error stays a quiet proceed (never refuse on a guess).
func TestRequireRootfulEngine(t *testing.T) {
	var buf bytes.Buffer
	if err := requireRootfulEngine(&buf, &fakeRunner{rootless: true}); err == nil {
		t.Fatal("rootless Podman must be refused without the override")
	} else if !strings.Contains(err.Error(), "BYRE_ALLOW_ROOTLESS_PODMAN") {
		t.Errorf("refusal should name the override: %v", err)
	}

	t.Setenv("BYRE_ALLOW_ROOTLESS_PODMAN", "1")
	buf.Reset()
	if err := requireRootfulEngine(&buf, &fakeRunner{rootless: true}); err != nil {
		t.Fatalf("override must proceed: %v", err)
	}
	if !strings.Contains(buf.String(), "rootless Podman detected") {
		t.Errorf("override must keep the detailed warning: %q", buf.String())
	}

	t.Setenv("BYRE_ALLOW_ROOTLESS_PODMAN", "")
	buf.Reset()
	if err := requireRootfulEngine(&buf, &fakeRunner{rootlessErr: errors.New("boom")}); err != nil {
		t.Fatalf("a detection error must not refuse: %v", err)
	}
	if err := requireRootfulEngine(&buf, &fakeRunner{}); err != nil {
		t.Fatalf("rootful must proceed: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("quiet cases must stay quiet: %q", buf.String())
	}
}

func TestDockerfilePrintsWithoutTouchingContext(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	s, out, _ := testStreams("", false)
	if err := Dockerfile(s, proj); err != nil {
		t.Fatal(err)
	}

	// Printed bytes must equal the generator output. AgentContext is always on:
	// the chassis paragraph (the /inbox fact) makes the context non-empty on
	// every box.
	want := gen.Dockerfile(gen.Input{AgentContext: true})
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

// writeStoreConfig writes the project's host-side byre.config directly.
func writeStoreConfig(t *testing.T, proj, content string) {
	t.Helper()
	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Dir, config.ProjectConfigName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The eject surfaces (ADR 0019): a firewalled project's dockerfile output
// explains its launch gate, dockerrun warns on stderr, and ejectfirewall
// prints the standalone sidecar with the resolved allowlist.
func TestEjectSurfacesFirewalled(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	writeStoreConfig(t, proj, "skills = [\"firewall\"]\negress = [\"grafana.com\", \"internal:8443\"]\n")

	s, out, _ := testStreams("", false)
	if err := Dockerfile(s, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "expects byre's launch-time firewall") {
		t.Errorf("firewalled dockerfile output missing the gate comment:\n%s", out.String()[:200])
	}

	s2, out2, err2 := testStreams("", false)
	if err := DockerRun(s2, proj); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2.String(), "launch gate") {
		t.Error("the dockerrun note must not pollute stdout")
	}
	if !strings.Contains(err2.String(), "ejectfirewall") {
		t.Errorf("firewalled dockerrun missing the stderr note: %q", err2.String())
	}

	s3, out3, _ := testStreams("", false)
	if err := EjectFirewall(s3, proj); err != nil {
		t.Fatal(err)
	}
	script := out3.String()
	for _, want := range []string{
		"#!/bin/sh",
		"--cap-add NET_ADMIN",
		"--entrypoint /usr/local/bin/byre-firewall",
		"--net \"container:$BOX\"",
		"grafana.com:443",
		"internal:8443",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("eject script missing %q:\n%s", want, script)
		}
	}
}

// Without a firewall: dockerfile output is untouched (pinned byte-identical
// elsewhere), dockerrun stays quiet, ejectfirewall refuses.
func TestEjectSurfacesUnfirewalled(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	s, _, errBuf := testStreams("", false)
	if err := DockerRun(s, proj); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errBuf.String(), "ejectfirewall") {
		t.Errorf("unfirewalled dockerrun should not warn: %q", errBuf.String())
	}
	s2, _, _ := testStreams("", false)
	if err := EjectFirewall(s2, proj); err == nil {
		t.Fatal("ejectfirewall without a firewall skill should refuse")
	}
}
