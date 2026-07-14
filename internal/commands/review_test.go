package commands

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

func grantTexts(lines []grantLine) string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return strings.Join(out, "\n")
}

func TestGrantSummaryMarksDisabledMounts(t *testing.T) {
	got := grantTexts(grantSummary(config.Config{Mounts: []config.Mount{
		{Host: "/a", Target: "/a", Mode: "rw"},
		{Host: "/b", Target: "/b", Mode: "rw", Disabled: true},
	}}))
	if !strings.Contains(got, "/a->/a(rw)") {
		t.Errorf("active mount missing: %q", got)
	}
	// Adopting a disabled mount plants an entry one flip away from a grant:
	// the reviewer must see it, marked, not have it hidden.
	if !strings.Contains(got, "/b->/b(rw, disabled)") {
		t.Errorf("disabled mount should be shown marked: %q", got)
	}
}

// The summary's charter (nothing smuggled unseen) covers every Grant class:
// machine-scoped volumes — the shared-credential shape, and the only grant
// that crosses project scope — plus ports and egress.
func TestGrantSummaryFlagsMachineVolumesPortsEgress(t *testing.T) {
	lines := grantSummary(config.Config{
		Volumes: []config.Volume{
			{Name: "claude-identity", Role: "state", Target: "/x", Scope: "machine"},
			{Name: "cache", Role: "cache", Target: "/c"}, // per-project: quiet
		},
		Ports: []config.Port{{Container: 3000}, {Container: 8080, Host: 80, Interface: "0.0.0.0"}, {Container: 9999, Remove: true}},
	})
	got := grantTexts(lines)
	if !strings.Contains(got, `machine-scoped volume "claude-identity"`) || !strings.Contains(got, "every project on this machine") {
		t.Errorf("machine-scoped volume must be flagged loudly: %q", got)
	}
	if strings.Contains(got, `"cache"`) {
		t.Errorf("per-project volumes are the sandbox model, not a grant: %q", got)
	}
	var cross bool
	for _, l := range lines {
		if strings.Contains(l.Text, "claude-identity") && l.CrossProject {
			cross = true
		}
	}
	if !cross {
		t.Error("the machine-volume line must carry the cross-project emphasis flag")
	}
	if !strings.Contains(got, "binds host ports: 127.0.0.1:3000->3000, 0.0.0.0:80->8080") {
		t.Errorf("ports must be summarized (removal markers skipped): %q", got)
	}
	if strings.Contains(got, "9999") {
		t.Errorf("a removal marker grants nothing: %q", got)
	}
}

// Egress is summarized with its honest posture status, and never hidden even
// when the cascade can't be expanded.
func TestEgressGrantLineStatus(t *testing.T) {
	if got := grantTexts(egressGrantLine([]string{"a.com", "b.com:8443"}, "restricted", "firewall", true)); !strings.Contains(got, "live — skill \"firewall\" sets posture \"restricted\"") {
		t.Errorf("posture-live phrasing: %q", got)
	}
	if got := grantTexts(egressGrantLine([]string{"a.com"}, "", "", true)); !strings.Contains(got, "inert now") {
		t.Errorf("no-posture phrasing: %q", got)
	}
	// open-denylist leaves the network open: allowlist entries are inert
	// there, and the review must not dress them up as live grants (ADR 0030).
	if got := grantTexts(egressGrantLine([]string{"a.com"}, config.PostureOpenDenylist, "firewall-open", true)); !strings.Contains(got, "inert") || strings.Contains(got, "live — skill") {
		t.Errorf("open-denylist phrasing must read inert: %q", got)
	}
	if got := grantTexts(egressGrantLine([]string{"a.com"}, "", "", false)); !strings.Contains(got, "under a restrictive network posture") {
		t.Errorf("unknown-posture fallback phrasing: %q", got)
	}
	if lines := egressGrantLine(nil, "p", "s", true); lines != nil {
		t.Errorf("no entries — no line: %v", lines)
	}
}

func TestSkillGrantSummaryContainmentTopSorted(t *testing.T) {
	var sf skills.File
	sf.Runtime.Containment = "docker-host opens a containment hole -- skim docs"
	sf.Runtime.Mounts = []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}}
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	sf.Volumes = []config.Volume{{Name: "id", Role: "state", Target: "/x", Scope: "machine"}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	lines := skillGrantSummary(res)
	if len(lines) < 2 {
		t.Fatalf("expected containment + other grants: %+v", lines)
	}
	if !lines[0].Containment || !strings.Contains(lines[0].Text, "containment hole") {
		t.Fatalf("containment must be first: %+v", lines[0])
	}
	// After full sort, containment still tops cross-project.
	mixed := append([]grantLine{{Text: "plain"}, {Text: "machine", CrossProject: true}}, lines...)
	sorted := sortGrantLines(mixed)
	if !sorted[0].Containment {
		t.Fatalf("sortGrantLines containment first: %+v", sorted)
	}
	if !sorted[1].CrossProject {
		t.Fatalf("cross-project second: %+v", sorted)
	}
}
