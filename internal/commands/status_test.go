package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

func TestRenderStatusFull(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Agent:     "claude",
		Engine:    "docker",
		Canonical: "/home/me/proj",
		Skills:    []string{"moarcode"},
		Binds: []config.Mount{
			{Host: "/data", Target: "/data", Mode: "ro"},
			{Host: "/media", Target: "/media", Mode: "rw", Disabled: true},
		},
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
		"/media -> /media  (rw, disabled)",
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

func TestNetworkLine(t *testing.T) {
	cases := []struct {
		name string
		info statusInfo
		want string
	}{
		{"default open", statusInfo{}, "open"},
		{"clean posture", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall"},
			"deny-by-default  (skill: firewall)"},
		{"project run_args degrade", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", ProjectRunArgs: true},
			"deny-by-default  (declared; raw run_args present — not guaranteed)"},
		{"raw build lines degrade", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", BuildRaw: []string{"RUN x"}},
			"deny-by-default  (declared; raw build lines present — not guaranteed)"},
		{"both degrade", statusInfo{NetPosture: "deny-by-default", NetPostureSkill: "firewall", ProjectRunArgs: true, BuildRaw: []string{"RUN x"}},
			"deny-by-default  (declared; raw run_args + raw build lines present — not guaranteed)"},
		{"unresolved skills", statusInfo{SkillErr: "boom", NetPosture: ""},
			"unknown  (skills unresolved)"},
	}
	for _, c := range cases {
		if got := networkLine(c.info); got != c.want {
			t.Errorf("%s: networkLine = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRenderStatusEgressSection(t *testing.T) {
	var buf strings.Builder
	renderStatus(&buf, statusInfo{
		Agent:           "claude",
		NetPosture:      "deny-by-default",
		NetPostureSkill: "firewall",
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "firewall", Host: "deb.debian.org", Port: 80},
			{Skill: "claude", Host: "api.anthropic.com", Port: 443}, // dup, must collapse
		},
	})
	out := buf.String()
	if !strings.Contains(out, "Egress:") {
		t.Fatalf("expected an Egress section when a posture is declared:\n%s", out)
	}
	if !strings.Contains(out, "api.anthropic.com:443  (claude)") {
		t.Errorf("egress entry not attributed to its skill:\n%s", out)
	}
	if !strings.Contains(out, "deb.debian.org:80  (firewall)") {
		t.Errorf("port-scoped base entry missing:\n%s", out)
	}
	if strings.Count(out, "api.anthropic.com:443") != 1 {
		t.Errorf("duplicate host:port must collapse to one row:\n%s", out)
	}
}

func TestRenderStatusNoEgressWithoutPosture(t *testing.T) {
	var buf strings.Builder
	// Agent skills declare egress even with no firewall; without a posture in
	// effect, status must NOT imply an allowlist is enforced.
	renderStatus(&buf, statusInfo{
		Agent:  "claude",
		Egress: []skills.EgressAllow{{Skill: "claude", Host: "api.anthropic.com", Port: 443}},
	})
	if strings.Contains(buf.String(), "Egress:") {
		t.Errorf("no Egress section when the network is open:\n%s", buf.String())
	}
}

func TestConfigEgressAttributed(t *testing.T) {
	entries := configEgress("grafana.com internal:8443 bad:99999")
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries (99999 dropped), got %+v", entries)
	}
	for _, e := range entries {
		if e.Skill != "config: FIREWALL_ALLOW" {
			t.Errorf("FIREWALL_ALLOW entry not attributed to config: %+v", e)
		}
	}
}
