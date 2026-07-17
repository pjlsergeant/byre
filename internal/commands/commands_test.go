package commands

import (
	"bytes"
	"errors"
	"fmt"
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
		{"rootless without keep-id warns", &fakeRunner{rootless: true}, true},
		{"rootless with keep-id is quiet (supported path)", &fakeRunner{rootless: true, keepID: true}, false},
		{"keep-id probe error warns", &fakeRunner{rootless: true, keepIDErr: errors.New("boom")}, true},
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

// The identity mode-select (ADR 0032): rootful engines get the host identity
// quietly; rootless Podman with keep-id mapping support gets the generic
// keep-id identity, announced; rootless Podman without it keeps the old
// refusal (BYRE_ALLOW_ROOTLESS_PODMAN=1 proceeds on the host identity with the
// warning retained); a detection error stays a quiet rootful proceed.
func TestResolveIdentity(t *testing.T) {
	var buf bytes.Buffer

	ident, err := resolveIdentity(&buf, &fakeRunner{rootless: true, keepID: true})
	if err != nil {
		t.Fatalf("supported rootless Podman must proceed: %v", err)
	}
	if ident.UID != genericUID || ident.GID != genericGID || !ident.KeepID {
		t.Errorf("supported rootless Podman must select the generic keep-id identity, got %+v", ident)
	}
	if !strings.Contains(buf.String(), "--userns=keep-id:uid=1000,gid=1000") {
		t.Errorf("keep-id mode must be announced with its mapping: %q", buf.String())
	}

	buf.Reset()
	if _, err := resolveIdentity(&buf, &fakeRunner{rootless: true}); err == nil {
		t.Fatal("rootless Podman without keep-id must be refused without the override")
	} else if !strings.Contains(err.Error(), "BYRE_ALLOW_ROOTLESS_PODMAN") {
		t.Errorf("refusal should name the override: %v", err)
	}
	if _, err := resolveIdentity(&buf, &fakeRunner{rootless: true, keepIDErr: errors.New("boom")}); err == nil {
		t.Fatal("an unverifiable keep-id support claim must refuse, not guess")
	}

	t.Setenv("BYRE_ALLOW_ROOTLESS_PODMAN", "1")
	buf.Reset()
	ident, err = resolveIdentity(&buf, &fakeRunner{rootless: true})
	if err != nil {
		t.Fatalf("override must proceed: %v", err)
	}
	if ident.KeepID || ident.UID != os.Getuid() {
		t.Errorf("override proceeds on the HOST identity, got %+v", ident)
	}
	if !strings.Contains(buf.String(), "rootless Podman detected") {
		t.Errorf("override must keep the detailed warning: %q", buf.String())
	}

	t.Setenv("BYRE_ALLOW_ROOTLESS_PODMAN", "")
	buf.Reset()
	for name, f := range map[string]*fakeRunner{
		"detection error": {rootlessErr: errors.New("boom")},
		"rootful":         {},
	} {
		ident, err := resolveIdentity(&buf, f)
		if err != nil {
			t.Fatalf("%s must not refuse: %v", name, err)
		}
		if ident.KeepID || ident.UID != os.Getuid() || ident.GID != os.Getgid() {
			t.Errorf("%s must select the host identity, got %+v", name, ident)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("quiet cases must stay quiet: %q", buf.String())
	}
}

// imageTagCandidates gates the generic keep-id tag on the ENGINE being
// rootless Podman: on a shared rootful daemon a forget/rehome must never
// capture a real uid-1000 user's image.
func TestImageTagCandidates(t *testing.T) {
	got := imageTagCandidates(&fakeRunner{}, "proj", 501, 501)
	want := []string{"byre-proj-u501-g501", "byre-proj"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rootful candidates = %v, want %v", got, want)
	}
	got = imageTagCandidates(&fakeRunner{rootless: true, keepID: true}, "proj", 501, 501)
	want = []string{"byre-proj-u1000-g1000", "byre-proj-u501-g501", "byre-proj"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("keep-id candidates = %v, want %v", got, want)
	}
	// The generic tag rides ROOTLESSNESS, not the version gate: a downgraded
	// (or version-unprobeable) rootless podman must still surface a keep-id
	// image for forget/rehome, or it stays orphaned.
	got = imageTagCandidates(&fakeRunner{rootless: true}, "proj", 501, 501)
	want = []string{"byre-proj-u501-g501", "byre-proj-u1000-g1000", "byre-proj"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("downgraded-rootless candidates = %v, want %v", got, want)
	}
	// A host uid that IS the generic id must not duplicate the tag.
	got = imageTagCandidates(&fakeRunner{rootless: true, keepID: true}, "proj", 1000, 1000)
	want = []string{"byre-proj-u1000-g1000", "byre-proj"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("uid-1000 keep-id candidates = %v, want %v", got, want)
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

// The inspection commands are documented side-effect-free: run against a
// project byre has never seen, they must not enroll it — no
// ~/.byre/projects/<id>/ directory, no path record. (The store mirror under
// the byre home is machine-level and byre-owned; project enrollment is the
// contract under test.)
func TestInspectionCommandsDoNotEnrollProject(t *testing.T) {
	cases := []struct {
		name    string
		run     func(Streams, string) error
		wantErr bool // ejectfirewall errors ("nothing to eject") — the contract holds anyway
	}{
		{"dockerfile", Dockerfile, false},
		{"dockerrun", DockerRun, false},
		{"ejectfirewall", EjectFirewall, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BYRE_HOME", t.TempDir())
			proj := t.TempDir()
			s, _, _ := testStreams("", false)
			err := tc.run(s, proj)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			paths, perr := project.Resolve(proj)
			if perr != nil {
				t.Fatal(perr)
			}
			if _, serr := os.Stat(paths.Dir); !os.IsNotExist(serr) {
				t.Fatalf("byre %s enrolled the project (created %s): %v", tc.name, paths.Dir, serr)
			}
		})
	}
}

// No write, no enrollment: flows that CAN write but end without writing must
// also leave a never-seen project un-enrolled.
func TestNoWriteFlowsDoNotEnrollProject(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, proj string)
	}{
		{"preset apply declined", func(t *testing.T, proj string) {
			shipPreset(t, proj, PresetName, "agent = \"none\"\n")
			s, _, _ := testStreams("n\n", true)
			if err := PresetApply(s, proj, ""); err != nil {
				t.Fatal(err)
			}
		}},
		{"mcp remove nothing", func(t *testing.T, proj string) {
			s, _, _ := testStreams("", false)
			if err := MCPRemove(s, proj, false, "ghost"); err == nil {
				t.Fatal("expected nothing-to-remove error")
			}
		}},
		{"claude-skill remove nothing", func(t *testing.T, proj string) {
			s, _, _ := testStreams("", false)
			if err := ClaudeSkillRemove(s, proj, false, "ghost"); err == nil {
				t.Fatal("expected nothing-to-remove error")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BYRE_HOME", t.TempDir())
			proj := t.TempDir()
			tc.run(t, proj)
			paths, err := project.Resolve(proj)
			if err != nil {
				t.Fatal(err)
			}
			if _, serr := os.Stat(paths.Dir); !os.IsNotExist(serr) {
				t.Fatalf("%s enrolled the project (created %s)", tc.name, paths.Dir)
			}
		})
	}
}

// The write-side counterpart: a landed layer-verb write enrolls WITH the path
// record (dir alone would defeat the collision check).
func TestMCPAddEnrollsWithPathRecord(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	s, _, _ := testStreams("", false)
	if err := MCPAdd(s, proj, false, "srv", []string{"https://example.com/mcp"}, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.PathRecord); err != nil {
		t.Fatalf("mcp add landed a write but did not enroll (path record): %v", err)
	}
}

// Read-only views share the loud id-collision stance: on a collision they
// would render ANOTHER project's config as this one's — refuse instead.
func TestReadOnlyViewsFailLoudlyOnCollision(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, _ := testStreams("", false)
	for name, run := range map[string]func() error{
		"status":            func() error { return Status(s, proj, false) },
		"mcp list":          func() error { return MCPList(s, proj) },
		"claude-skill list": func() error { return ClaudeSkillList(s, proj) },
		"preset inspect":    func() error { return PresetInspect(s, proj, "") },
		"mcp remove":        func() error { return MCPRemove(s, proj, false, "ghost") },
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), "collision") {
			t.Errorf("%s on a collided id: err = %v, want collision error", name, err)
		}
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
