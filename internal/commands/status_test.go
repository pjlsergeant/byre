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

func TestRenderStatusConfigEgressShownUnenforced(t *testing.T) {
	var buf strings.Builder
	// The user's own `egress` config entries are latent grants: with no
	// posture they still print, marked unenforced (ADR 0019) — while skill
	// egress stays suppressed as noise on an open network.
	renderStatus(&buf, statusInfo{
		Agent: "claude",
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "config", Host: "grafana.com", Port: 443},
		},
	})
	out := buf.String()
	if !strings.Contains(out, "grafana.com:443") || !strings.Contains(out, "unenforced") {
		t.Errorf("config egress should print unenforced without a posture:\n%s", out)
	}
	if strings.Contains(out, "api.anthropic.com") {
		t.Errorf("skill egress should stay suppressed without a posture:\n%s", out)
	}
	// With a posture, everything prints and nothing claims unenforced.
	buf.Reset()
	renderStatus(&buf, statusInfo{
		Agent:      "claude",
		NetPosture: "deny-by-default",
		Egress: []skills.EgressAllow{
			{Skill: "claude", Host: "api.anthropic.com", Port: 443},
			{Skill: "config", Host: "grafana.com", Port: 443},
		},
	})
	out = buf.String()
	if !strings.Contains(out, "api.anthropic.com:443") || strings.Contains(out, "unenforced") {
		t.Errorf("posture on: full list, no unenforced tag:\n%s", out)
	}
}

func TestConfigEgressAttributed(t *testing.T) {
	entries := configEgress([]string{"grafana.com", "internal:8443", "bad:99999"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries (99999 dropped), got %+v", entries)
	}
	if entries[0].Host != "grafana.com" || entries[0].Port != 443 || entries[1].Port != 8443 {
		t.Errorf("entries parsed wrong: %+v", entries)
	}
	for _, e := range entries {
		if e.Skill != "config" {
			t.Errorf("egress entry not attributed to config: %+v", e)
		}
	}
}

func TestRenderStatusContainmentAndSockGroups(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Skills:    []string{"docker-host"},
		Containments: []skills.ContainmentDecl{{
			Skill: "docker-host",
			Text:  "docker-host opens a containment hole -- skim docs/docker-host.md",
		}},
		Grants: []skills.Grant{{
			Skill:      "docker-host",
			Mounts:     []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}},
			SockGroups: []string{"/var/run/docker.sock"},
		}},
	})
	out := b.String()
	for _, want := range []string{
		"Containment:", "🛑 HOLE", "docker-host opens a containment hole", "(skill: docker-host)",
		"Skill grants:", "sock group access via /var/run/docker.sock",
		"mounts /var/run/docker.sock",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
	// Network row must stay unqualified (warranty model: hole is separate).
	if !hasField(out, "Network:", "open") {
		t.Errorf("Network row should stay open/unqualified:\n%s", out)
	}
}

func TestRenderStatusMultiContainment(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, statusInfo{
		Engine:    "docker",
		Canonical: "/p",
		Containments: []skills.ContainmentDecl{
			{Skill: "docker-host", Text: "hole A"},
			{Skill: "podman-host", Text: "hole B"},
		},
	})
	out := b.String()
	if !strings.Contains(out, "hole A") || !strings.Contains(out, "hole B") {
		t.Fatalf("multi-declarer not both shown:\n%s", out)
	}
	if !strings.Contains(out, "(skill: docker-host)") || !strings.Contains(out, "(skill: podman-host)") {
		t.Fatalf("both skills must be attributed:\n%s", out)
	}
}
