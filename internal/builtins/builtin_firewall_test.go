package builtins

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestFirewallSkillResolves pins the firewall skill's contract: it declares
// the posture and the netns hook (both consumed by core), stays composable
// with an agent skill, and grants NOTHING to the box itself — no caps, no
// run_args, no mounts. The box's only firewall-related content is inert
// tooling; privileges live solely in the netns-init helper byre runs outside.
func TestFirewallSkillResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall"}}, cat)
	if err != nil {
		t.Fatalf("firewall + claude must resolve together: %v", err)
	}
	posture, by := res.NetworkPosture()
	if posture != "deny-by-default" || by != "byre/firewall" {
		t.Errorf("posture = %q by %q", posture, by)
	}
	hooks := res.NetnsInits()
	if len(hooks) != 1 || hooks[0].Path != "/usr/local/bin/byre-firewall" {
		t.Errorf("netns hooks = %+v", hooks)
	}
	for _, sk := range res.Skills {
		if sk.Name != "byre/firewall" {
			continue
		}
		rt := sk.File.Runtime
		if len(rt.Caps) != 0 || len(rt.RunArgs) != 0 || len(rt.Mounts) != 0 {
			t.Errorf("the firewall skill must grant the BOX nothing: %+v", rt)
		}
		if sk.Context == "" {
			t.Error("firewall skill should ship agent context explaining the wall")
		}
		// The gate file and the script must both ship into the image: the
		// launcher keys the wait on the former; the helper entrypoint is the latter.
		dests := map[string]bool{}
		for _, f := range sk.Files {
			dests[f.Dest] = true
		}
		for _, want := range []string{"/etc/byre/launch-gate", "/usr/local/bin/byre-firewall"} {
			if !dests[want] {
				t.Errorf("firewall skill must ship %s; files: %+v", want, sk.Files)
			}
		}
		assertCurlShipsTrustStore(t, "firewall", sk.File.Build.Apt)
	}
}

// assertCurlShipsTrustStore pins curl and ca-certificates traveling together
// in a skill's apt list: Debian's curl doesn't pull the trust store, so on a
// bare base (template = "none") HTTPS diagnostics fail TLS verification (77)
// against reachable hosts without the pair (field-QA, 2026-07-17).
func assertCurlShipsTrustStore(t *testing.T, skill string, apt []string) {
	t.Helper()
	have := map[string]bool{}
	for _, p := range apt {
		have[p] = true
	}
	if !have["curl"] || !have["ca-certificates"] {
		t.Errorf("%s skill must ship curl AND ca-certificates (apt = %v) — TLS diagnostics break on minimal bases without the pair", skill, apt)
	}
}

// TestFirewallOpenSkillResolves pins the firewall-open contract, mirroring
// the firewall's: the open-denylist posture and the netns hook (both consumed
// by core), composable with an agent skill, granting NOTHING to the box
// itself, and offering NO doors (there is no wall to open holes in). And the
// two enforcement siblings are mutually exclusive: both declare a posture,
// which resolution rejects loudly.
func TestFirewallOpenSkillResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall-open"}}, cat)
	if err != nil {
		t.Fatalf("firewall-open + claude must resolve together: %v", err)
	}
	posture, by := res.NetworkPosture()
	if posture != config.PostureOpenDenylist || by != "byre/firewall-open" {
		t.Errorf("posture = %q by %q", posture, by)
	}
	hooks := res.NetnsInits()
	if len(hooks) != 1 || hooks[0].Path != "/usr/local/bin/byre-firewall-open" {
		t.Errorf("netns hooks = %+v", hooks)
	}
	for _, sk := range res.Skills {
		if sk.Name != "byre/firewall-open" {
			continue
		}
		rt := sk.File.Runtime
		if len(rt.Caps) != 0 || len(rt.RunArgs) != 0 || len(rt.Mounts) != 0 {
			t.Errorf("the firewall-open skill must grant the BOX nothing: %+v", rt)
		}
		if len(rt.Egress) != 0 || len(rt.EgressOffered) != 0 {
			t.Errorf("no wall means nothing to open or offer: %+v", rt)
		}
		if sk.Context == "" {
			t.Error("firewall-open skill should ship agent context explaining the denylist")
		}
		dests := map[string]bool{}
		for _, f := range sk.Files {
			dests[f.Dest] = true
		}
		for _, want := range []string{"/etc/byre/launch-gate", "/usr/local/bin/byre-firewall-open"} {
			if !dests[want] {
				t.Errorf("firewall-open skill must ship %s; files: %+v", want, sk.Files)
			}
		}
		// Same diagnostic toolkit, same trust-store requirement as the
		// firewall sibling.
		assertCurlShipsTrustStore(t, "firewall-open", sk.File.Build.Apt)
	}
	if _, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall", "firewall-open"}}, cat); err == nil {
		t.Error("firewall + firewall-open must be rejected (two posture declarers)")
	}
}

// TestFirewallComposesAgentEgress pins the derived-allowlist contract
// (ADR 0020): enabling firewall + an agent opens ONLY the agent's own
// endpoints -- the skill's functional requirement. Everything else the
// firewall knows about (git hosting, apt) is OFFERED, never auto-open.
func TestFirewallComposesAgentEgress(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall"}}, cat)
	if err != nil {
		t.Fatal(err)
	}
	union := strings.Join(res.Egress(), " ")
	if !strings.Contains(union, "api.anthropic.com:443") {
		t.Errorf("agent endpoints must open with the agent; got: %s", union)
	}
	// Deny-by-default means it: git/apt must NOT be open, only offered.
	for _, closed := range []string{"github.com", "deb.debian.org"} {
		if strings.Contains(union, closed) {
			t.Errorf("%q must be offered, not auto-open; got: %s", closed, union)
		}
	}
	fw, err := skills.Load(cat, "firewall")
	if err != nil {
		t.Fatal(err)
	}
	offered := strings.Join(fw.File.Runtime.EgressOffered, " ")
	for _, want := range []string{"github.com", "deb.debian.org:80"} {
		if !strings.Contains(offered, want) {
			t.Errorf("firewall must OFFER %q; got: %s", want, offered)
		}
	}
	// The firewall skill must NOT itself carry the agent endpoints (the whole
	// point of the redesign): with claude NOT enabled, anthropic must be absent.
	fwOnly, err := skills.Resolve(config.Config{Skills: []string{"firewall"}}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(fwOnly.Egress(), " "), "anthropic") {
		t.Errorf("firewall base must not hardcode agent endpoints; got: %v", fwOnly.Egress())
	}
	// Attribution: anthropic is credited to the claude skill, not the firewall.
	for _, a := range res.EgressAllows() {
		if strings.Contains(a.Host, "anthropic") && a.Skill != "byre/claude" {
			t.Errorf("anthropic egress attributed to %q, want byre/claude", a.Skill)
		}
	}
}
