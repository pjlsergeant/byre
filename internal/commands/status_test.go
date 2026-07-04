package commands

import (
	"bytes"
	"strings"
	"testing"

	"byre/internal/config"
	"byre/internal/skills"
)

func TestRenderStatusFull(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Agent:     "claude",
		Engine:    "docker",
		Canonical: "/home/me/proj",
		Skills:    []string{"moarcode"},
		Binds:     []config.Mount{{Host: "/data", Target: "/data", Mode: "ro"}},
		Ports: []config.Port{
			{Container: 8080, Host: 8080},
			{Container: 3000}, // blank host = mirror the container port
		},
		Volumes: []config.Volume{
			{Name: "creds", Role: "state"},
			{Name: "node_modules", Role: "cache"},
		},
		RunArgs:   []string{"--cap-add=SYS_PTRACE"},
		Container: "abcdef0123456789",
	})
	out := b.String()

	for _, want := range []string{
		"Agent:", "claude",
		"Engine:", "docker",
		"/home/me/proj -> /workspace  (rw)",
		"Network:", "open",
		"Ports:", "127.0.0.1:8080 -> 8080", "127.0.0.1:3000 -> 3000",
		"/data -> /data  (ro)",
		"moarcode",
		"State vols:", "creds",
		"Cache vols:", "node_modules",
		"not introspected",
		"running (abcdef012345)", // short id
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderStatusGrantsAndRawBuild(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Skills:    []string{"shem"},
		Grants: []skills.Grant{{
			Skill:  "shem",
			Mounts: []config.Mount{{Host: "/var/run/x.sock", Target: "/run/x.sock", Mode: "rw"}},
			Caps:   []string{"SYS_PTRACE"},
		}},
		BuildRaw: []string{"RUN echo hi"},
	})
	out := b.String()
	for _, want := range []string{
		"Skill grants:", "shem:", "mounts /var/run/x.sock -> /run/x.sock (rw)", "+cap SYS_PTRACE",
		"Raw build:", "RUN echo hi", "not introspected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
}

// hasField reports whether the status output has a "Label: value" row,
// insensitive to the column padding between them (presentation, not contract).
func hasField(out, label, value string) bool {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, label) && strings.Contains(trimmed, value) {
			return true
		}
	}
	return false
}

func TestRenderStatusEmptyAndNoEngine(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "auto",
		Canonical: "/p",
		EngineErr: "no container engine found on PATH",
	})
	out := b.String()
	if !hasField(out, "Agent:", "(none)") {
		t.Errorf("missing default agent: %s", out)
	}
	if !hasField(out, "Host mounts:", "none") {
		t.Errorf("missing 'none' mounts: %s", out)
	}
	if !hasField(out, "Container:", "unknown (no engine)") {
		t.Errorf("missing no-engine container line: %s", out)
	}
}

func TestRenderStatusRootlessPodman(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{Engine: "podman", Canonical: "/p", Rootless: true})
	out := b.String()
	if !strings.Contains(out, "rootless") || !strings.Contains(out, "UNSUPPORTED") {
		t.Errorf("rootless Podman not flagged on the Engine row: %s", out)
	}
}
