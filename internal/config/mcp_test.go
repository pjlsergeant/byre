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
		{"credentials in url", MCP{Name: "x", URL: "https://token@h.example/mcp"}, "must not carry credentials"},
		{"userinfo pair in url", MCP{Name: "x", URL: "https://user:pass@h.example/mcp"}, "must not carry credentials"},
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
		{Name: "linear", URL: "https://mcp.linear.app/mcp", Env: []string{"IGNORED"}},
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
      "type": "stdio"
    },
    "linear": {
      "type": "http",
      "url": "https://mcp.linear.app/mcp"
    }
  }
}
`
	if got != want {
		t.Fatalf("mcp.json mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	// Env stanzas are deliberately absent (inheritance delivers values; an
	// unset ${VAR} would pass a literal through) — pin that.
	if strings.Contains(got, "env") {
		t.Fatalf("mcp.json must not carry env stanzas: %s", got)
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
