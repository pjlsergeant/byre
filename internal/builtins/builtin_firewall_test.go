package builtins

import (
	"os"
	"path/filepath"
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
// against reachable hosts without the pair.
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
	if _, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall", "firewall-open"}}, cat); err == nil || !strings.Contains(err.Error(), "both declare a network_posture") {
		t.Errorf("firewall + firewall-open must be rejected by the two-posture rule, got: %v", err)
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

// Under deny-by-default the OUTPUT policy is DROP with per-(ip, port) TCP
// accepts, so ICMP leaves for nothing: ping and traceroute time out for an
// allowlisted host exactly as they do for a blocked one. Shipping them arms
// the agent with a probe that cannot tell the two apart and reads a working
// door as shut, so this skill's diagnostics are the port-scoped ones. The
// firewall-open sibling keeps them: its policy stays ACCEPT.
func TestFirewallShipsNoICMPDiagnostics(t *testing.T) {
	_, cat := testCat(t)
	fw, err := skills.Load(cat, "firewall")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range fw.File.Build.Apt {
		if p == "iputils-ping" || p == "traceroute" {
			t.Errorf("the deny-by-default skill must not ship %q: ICMP is dropped to every destination, so it cannot diagnose the wall (apt = %v)", p, fw.File.Build.Apt)
		}
	}
}

// Both netns helpers word-split unquoted input -- the egress/denylist entries,
// getent's answers, resolv.conf -- and a bracketed IPv6 entry
// ("[2001:db8::1]:443") is a glob pattern, so pathname expansion can turn a
// rule for a host into a rule for whatever files happen to sit in the CWD.
// The assert is on the script text because the loops it protects cannot run
// in a unit test (they need iptables, a netns, and the launch gate).
// ORDER is the assertion, not presence: a set -f anywhere in the file leaves
// a split above it unprotected, and moving the entry loop is exactly the edit
// that would do it.
func TestNetnsHelpersDisablePathnameExpansion(t *testing.T) {
	_, cat := testCat(t)
	for _, tc := range []struct{ skill, script, split string }{
		{"firewall", "firewall.sh", `for e in $(echo "${BYRE_EGRESS`},
		{"firewall-open", "firewall-open.sh", `for e in $(echo "${BYRE_EGRESS_DENY`},
	} {
		b, err := os.ReadFile(filepath.Join(skillDir(t, cat, tc.skill), tc.script))
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		noglob := strings.Index(body, "\nset -f\n")
		if noglob < 0 {
			t.Errorf("%s must disable globbing (set -f) before it word-splits its entries", tc.script)
			continue
		}
		split := strings.Index(body, tc.split)
		if split < 0 {
			t.Errorf("%s: no entry-splitting loop found (%q) -- the guard below has nothing to pin", tc.script, tc.split)
			continue
		}
		if noglob > split {
			t.Errorf("%s: set -f (offset %d) must come BEFORE the entry word-split (offset %d), or the split runs with globbing on", tc.script, noglob, split)
		}
	}
}
