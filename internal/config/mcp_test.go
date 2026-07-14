package config

import (
	"strings"
	"testing"
)

func TestMCPParseAndValidate(t *testing.T) {
	c, err := Parse([]byte(`
[[mcp]]
name = "github"
command = ["github-mcp-server", "stdio"]
env = ["GITHUB_TOKEN"]

[[mcp]]
name = "linear"
url = "https://mcp.linear.app/mcp"
egress = ["auth.linear.app"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := c.ValidateLayer(); err != nil {
		t.Fatalf("layer validate: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("resolved validate: %v", err)
	}
	if len(c.MCPs) != 2 || c.MCPs[0].Name != "github" || c.MCPs[1].Name != "linear" {
		t.Fatalf("unexpected MCPs: %+v", c.MCPs)
	}
	if c.MCPs[0].Remote() || !c.MCPs[1].Remote() {
		t.Fatalf("remote discrimination wrong")
	}
	host, port, ok := c.MCPs[1].Endpoint()
	if !ok || host != "mcp.linear.app" || port != 443 {
		t.Fatalf("endpoint = %s:%d ok=%v", host, port, ok)
	}
}

func TestMCPEndpointPorts(t *testing.T) {
	cases := []struct {
		url  string
		host string
		port int
	}{
		{"https://mcp.example.com/mcp", "mcp.example.com", 443},
		{"http://mcp.example.com/mcp", "mcp.example.com", 80},
		{"https://mcp.example.com:8443/x", "mcp.example.com", 8443},
	}
	for _, tc := range cases {
		h, p, ok := (MCP{Name: "x", URL: tc.url}).Endpoint()
		if !ok || h != tc.host || p != tc.port {
			t.Errorf("%s: got %s:%d ok=%v, want %s:%d", tc.url, h, p, ok, tc.host, tc.port)
		}
	}
	if _, _, ok := (MCP{Name: "x", Command: []string{"srv"}}).Endpoint(); ok {
		t.Errorf("local declaration must have no endpoint")
	}
}

func TestMCPValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		m    MCP
		want string
	}{
		{"no name", MCP{Command: []string{"x"}}, "mcp name"},
		{"bad name chars", MCP{Name: "Bad_Name", Command: []string{"x"}}, "mcp name"},
		// The registrar namespace is structurally unreachable: no underscores.
		{"byre prefix impossible", MCP{Name: "byre__x", Command: []string{"x"}}, "mcp name"},
		{"both command and url", MCP{Name: "x", Command: []string{"s"}, URL: "https://h/m"}, "both command and url"},
		{"neither", MCP{Name: "x"}, "needs a command"},
		{"empty binary", MCP{Name: "x", Command: []string{" "}}, "must not be empty"},
		{"control char in command", MCP{Name: "x", Command: []string{"s\x1b[31m"}}, "control characters"},
		{"bad scheme", MCP{Name: "x", URL: "ftp://h/m"}, "scheme must be"},
		{"no host", MCP{Name: "x", URL: "https:///mcp"}, "missing a host"},
		{"env value smuggled", MCP{Name: "x", Command: []string{"s"}, Env: []string{"TOKEN=abc"}}, "not a valid environment variable name"},
		{"bad egress", MCP{Name: "x", Command: []string{"s"}, Egress: []string{"bad host"}}, "not a valid host"},
	}
	for _, tc := range cases {
		err := ValidateMCP(tc.m)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want contains %q", tc.name, err, tc.want)
		}
	}
}

func TestMCPLayerMarkersAndDuplicates(t *testing.T) {
	// A marker is name-only and layer-legal.
	c, err := Parse([]byte("[[mcp]]\nname = \"!github\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := c.ValidateLayer(); err != nil {
		t.Fatalf("marker should be layer-legal: %v", err)
	}
	if err := c.Validate(); err == nil {
		t.Fatalf("marker must be rejected in a resolved config")
	}

	// A marker carrying other fields is a mistyped real server.
	c2 := Config{MCPs: []MCP{{Name: "!github", URL: "https://h/m"}}}
	if err := c2.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "closure marker takes only a name") {
		t.Fatalf("marker with fields: %v", err)
	}

	// In-layer duplicate names refuse (merge would silently replace).
	c3 := Config{MCPs: []MCP{
		{Name: "github", Command: []string{"a"}},
		{Name: "github", Command: []string{"b"}},
	}}
	if err := c3.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("in-layer duplicate: %v", err)
	}
}

func TestMCPMergeReplaceByName(t *testing.T) {
	base := Config{MCPs: []MCP{{Name: "github", Command: []string{"old"}}, {Name: "linear", URL: "https://mcp.linear.app/mcp"}}}
	over := Config{MCPs: []MCP{{Name: "github", Command: []string{"new", "stdio"}}}}
	got := Merge(base, over)
	if len(got.MCPs) != 2 {
		t.Fatalf("MCPs = %+v", got.MCPs)
	}
	if got.MCPs[0].Name != "github" || got.MCPs[0].Command[0] != "new" {
		t.Fatalf("later layer must replace by name: %+v", got.MCPs[0])
	}
	if got.MCPs[1].Name != "linear" {
		t.Fatalf("unrelated entry lost: %+v", got.MCPs)
	}
}

func TestMCPMergeClosureSurvivesAndReopens(t *testing.T) {
	base := Config{MCPs: []MCP{{Name: "github", Command: []string{"srv"}}}}
	over := Config{MCPs: []MCP{{Name: "!github"}, {Name: "!linear"}}}
	got := Merge(base, over)
	if len(got.MCPs) != 0 {
		t.Fatalf("closure must remove the inherited declaration: %+v", got.MCPs)
	}
	// NOT consumed: both closures survive for the post-union subtraction —
	// including "!linear", which matched nothing in the cascade (it may
	// match a skill-declared server later).
	if len(got.MCPClosed) != 2 || got.MCPClosed[0] != "github" || got.MCPClosed[1] != "linear" {
		t.Fatalf("MCPClosed = %v", got.MCPClosed)
	}

	// A later layer's plain declaration re-opens the closure.
	reopened := Merge(got, Config{MCPs: []MCP{{Name: "github", Command: []string{"srv2"}}}})
	if len(reopened.MCPs) != 1 || reopened.MCPs[0].Command[0] != "srv2" {
		t.Fatalf("re-open failed: %+v", reopened.MCPs)
	}
	if len(reopened.MCPClosed) != 1 || reopened.MCPClosed[0] != "linear" {
		t.Fatalf("unrelated closure must survive the re-open: %v", reopened.MCPClosed)
	}

	// Within ONE layer a closure beats a plain declaration (adds fold
	// first, closures after — mirrors mergeEgress).
	sameLayer := Merge(Config{}, Config{MCPs: []MCP{{Name: "x", Command: []string{"s"}}, {Name: "!x"}}})
	if len(sameLayer.MCPs) != 0 || len(sameLayer.MCPClosed) != 1 {
		t.Fatalf("same-layer closure: MCPs=%+v closed=%v", sameLayer.MCPs, sameLayer.MCPClosed)
	}
}

func TestMCPConfigJSONDeterministicAndShaped(t *testing.T) {
	mcps := []MCP{
		{Name: "linear", URL: "https://mcp.linear.app/mcp", Env: []string{"LINEAR_KEY"}},
		{Name: "github", Command: []string{"github-mcp-server", "stdio"}, Env: []string{"GITHUB_TOKEN"}},
	}
	got := string(MCPConfigJSON(mcps))
	want := `{
  "mcpServers": {
    "github": {
      "args": [
        "stdio"
      ],
      "command": "github-mcp-server",
      "type": "stdio",
      "x_byre_env": [
        "GITHUB_TOKEN"
      ]
    },
    "linear": {
      "type": "http",
      "url": "https://mcp.linear.app/mcp",
      "x_byre_env": [
        "LINEAR_KEY"
      ]
    }
  }
}
`
	if got != want {
		t.Fatalf("mcp.json mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	// Claude-style env VALUE stanzas are deliberately absent (inheritance
	// delivers values; an unset ${VAR} would pass a literal through); names
	// ride the x_byre_env extension for scrubbed-env consumers — pin both.
	if strings.Contains(got, `"env"`) {
		t.Fatalf("mcp.json must not carry claude env stanzas: %s", got)
	}
	// The empty set is a real file, not an absent one.
	if e := string(MCPConfigJSON(nil)); e != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("empty render = %q", e)
	}
	// Argless command renders an explicit empty args list.
	one := string(MCPConfigJSON([]MCP{{Name: "x", Command: []string{"srv"}}}))
	if !strings.Contains(one, `"args": []`) {
		t.Fatalf("argless command must render empty args: %s", one)
	}
}

// A basic-auth url (user:pass@host) is ACCEPTED — a self-hosted MCP behind
// a reverse proxy is a real shape with no alternative spelling, and the
// footgun doctrine polices the agent, never the user (maintainer ruling
// 2026-07-15, reversing a review-round refusal). The secret bakes into the
// image like an [env] literal; docs + `byre mcp add` disclose it. The
// endpoint derivation must strip the userinfo from the implied egress.
func TestMCPAcceptsBasicAuthURL(t *testing.T) {
	m := MCP{Name: "proxied", URL: "https://user:pass@mcp.internal.example/mcp"}
	if err := ValidateMCP(m); err != nil {
		t.Fatalf("basic-auth url must validate: %v", err)
	}
	host, port, ok := m.Endpoint()
	if !ok || host != "mcp.internal.example" || port != 443 {
		t.Fatalf("endpoint must strip userinfo: %s:%d ok=%v", host, port, ok)
	}
}

// A url host must be expressible in the egress grammar: the host becomes an
// implied allowlist entry, and IPv6 (colon hosts) is outside that grammar —
// accepting it would bake wiring the closure/allowlist machinery can
// neither enforce nor honestly report (grok review).
func TestMCPRejectsIPv6URLHosts(t *testing.T) {
	err := ValidateMCP(MCP{Name: "v6", URL: "https://[2001:db8::1]:8443/mcp"})
	if err == nil || !strings.Contains(err.Error(), "egress grammar") {
		t.Fatalf("IPv6 url host must be rejected: %v", err)
	}
	if err := ValidateMCP(MCP{Name: "v4", URL: "https://192.0.2.7:8443/mcp"}); err != nil {
		t.Fatalf("IPv4 literal host must stay valid: %v", err)
	}
}
