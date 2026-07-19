package skills

// Network posture, netns init, and egress declarations: single-declarer
// postures, the netns hook contract, and the egress grammar/attribution.

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestResolveNetworkPostureAndNetnsInit(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"deny-by-default\"\nnetns_init = \"/usr/local/bin/byre-firewall\"\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	posture, by := res.NetworkPosture()
	if posture != "deny-by-default" || by != "fw" {
		t.Errorf("posture = %q by %q, want deny-by-default by fw", posture, by)
	}
	hooks := res.NetnsInits()
	if len(hooks) != 1 || hooks[0].Skill != "fw" || hooks[0].Path != "/usr/local/bin/byre-firewall" {
		t.Errorf("netns hooks = %+v", hooks)
	}
	// A netns_init is a root-privileged hook — it must surface as a grant.
	grants := res.Grants()
	if len(grants) != 1 || grants[0].NetnsInit != "/usr/local/bin/byre-firewall" {
		t.Errorf("netns_init must be an attributed grant: %+v", grants)
	}
}

func TestResolveNoPostureMeansOpen(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "plain", "[build]\napt = [\"jq\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"plain"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if posture, by := res.NetworkPosture(); posture != "" || by != "" {
		t.Errorf("no skill declares a posture; got %q by %q", posture, by)
	}
}

func TestResolveRejectsConflictingPostures(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw1", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	writeSkill(t, dir, "fw2", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"fw1", "fw2"}}, catFor(t, dir))
	if err == nil || !strings.Contains(err.Error(), "both declare a network_posture") {
		t.Fatalf("two skills declaring a posture must be rejected (even identical: each claims the stance), got %v", err)
	}
	for _, want := range []string{"fw1", "fw2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestResolveRejectsMalformedPosture(t *testing.T) {
	dir := testHome(t)
	// Status prints the posture verbatim; a spoofing label must be rejected.
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"open  (all good)\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("posture with spaces/parens must be rejected, got %v", err)
	}
}

func TestResolveRejectsRelativeNetnsInit(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw", "[runtime]\nnetns_init = \"bin/fw\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "must be an absolute image path") {
		t.Fatalf("relative netns_init must be rejected, got %v", err)
	}
}

func TestResolveRejectsTwoNetnsInits(t *testing.T) {
	dir := testHome(t)
	// The launch gate is opened by the hook's own script, so a second hook
	// could run after the agent was already released — refuse the ambiguity
	// (same stance as two posture declarations).
	writeSkill(t, dir, "fw1", "[runtime]\nnetns_init = \"/usr/local/bin/fw1\"\n", nil)
	writeSkill(t, dir, "fw2", "[runtime]\nnetns_init = \"/usr/local/bin/fw2\"\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"fw1", "fw2"}}, catFor(t, dir))
	if err == nil || !strings.Contains(err.Error(), "both declare a netns_init") {
		t.Fatalf("two skills declaring a netns_init must be rejected, got %v", err)
	}
	for _, want := range []string{"fw1", "fw2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestEgressUnionAndAttribution(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "claude", "[runtime]\negress = [\"api.anthropic.com\", \"claude.ai\"]\n", nil)
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"deny-by-default\"\negress = [\"github.com\", \"deb.debian.org:80\", \"api.anthropic.com\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"claude", "fw"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// Union: normalized host:port, deduped (api.anthropic.com appears in both),
	// port defaulted to 443, explicit :80 preserved, first-seen order.
	got := res.Egress()
	want := []string{"api.anthropic.com:443", "claude.ai:443", "github.com:443", "deb.debian.org:80"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Egress() = %v, want %v", got, want)
	}
	// Attribution keeps the per-skill duplicate (both declared anthropic) so
	// status can show who asked for what.
	allows := res.EgressAllows()
	var fromClaude, fromFw int
	for _, a := range allows {
		switch a.Skill {
		case "claude":
			fromClaude++
		case "fw":
			fromFw++
		}
	}
	if fromClaude != 2 || fromFw != 3 {
		t.Errorf("attribution counts: claude=%d fw=%d; allows=%+v", fromClaude, fromFw, allows)
	}
}

func TestEgressRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"api.anthropic.com:99999", "has space.com", "host:notaport:443", "bad;host"} {
		dir := t.TempDir()
		writeSkill(t, dir, "fw", "[runtime]\negress = [\""+bad+"\"]\n", nil)
		if _, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), `egress "`+bad+`"`) {
			t.Errorf("egress %q must be rejected by the egress grammar, got %v", bad, err)
		}
	}
}

func TestEgressPortDefaultsTo443(t *testing.T) {
	if h, p, err := parseEgress("api.anthropic.com"); err != nil || h != "api.anthropic.com" || p != 443 {
		t.Fatalf("parseEgress default = (%q,%d,%v), want (api.anthropic.com,443,nil)", h, p, err)
	}
	if h, p, err := parseEgress("deb.debian.org:80"); err != nil || h != "deb.debian.org" || p != 80 {
		t.Fatalf("parseEgress explicit = (%q,%d,%v)", h, p, err)
	}
}
