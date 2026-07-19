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

	// A marker carrying other fields is a mistyped real server. Headers is
	// asserted separately: it was the one field the extras check missed
	// (grok review, 2026-07-19) — a headers-only "closure" validated clean
	// and silently dropped the headers.
	c2 := Config{MCPs: []MCP{{Name: "!github", URL: "https://h/m"}}}
	if err := c2.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "closure marker takes only a name") {
		t.Fatalf("marker with fields: %v", err)
	}
	c2h := Config{MCPs: []MCP{{Name: "!github", Headers: map[string]string{"Authorization": "Bearer x"}}}}
	if err := c2h.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "closure marker takes only a name") {
		t.Fatalf("marker with headers only: %v", err)
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

// IPv6 url hosts are supported end-to-end via the bracket grammar (grok
// found the original refusal; Pete pulled the real fix into the arc): the
// implied endpoint comes back bracketed + canonicalized, so downstream
// "%s:%d" compositions re-parse and the firewall's v6 rules apply.
func TestMCPAcceptsIPv6URLHosts(t *testing.T) {
	m := MCP{Name: "v6", URL: "https://[2001:DB8::1]:8443/mcp"}
	if err := ValidateMCP(m); err != nil {
		t.Fatalf("IPv6 url host must validate: %v", err)
	}
	host, port, ok := m.Endpoint()
	if !ok || host != "[2001:db8::1]" || port != 8443 {
		t.Fatalf("endpoint must bracket + canonicalize: %s:%d ok=%v", host, port, ok)
	}
	// The composed entry round-trips through the egress grammar.
	if h, p, err := ParseEgress(host + ":8443"); err != nil || h != "[2001:db8::1]" || p != 8443 {
		t.Fatalf("derived entry must re-parse: %s:%d %v", h, p, err)
	}
	if err := ValidateMCP(MCP{Name: "v4", URL: "https://192.0.2.7:8443/mcp"}); err != nil {
		t.Fatalf("IPv4 literal host must stay valid: %v", err)
	}
}

// Headers: remote-only templates whose ${NAME} refs join the consumed-env
// set; the baked file carries them VERBATIM (expansion is launch-time, so
// the file stays free of byre-placed secrets).
func TestMCPHeaders(t *testing.T) {
	m := MCP{Name: "proxied", URL: "https://mcp.internal.example/mcp", Env: []string{"EXTRA"},
		Headers: map[string]string{"Authorization": "Bearer ${PROXY_TOKEN}", "X-Api-Key": "${API_KEY}", "X-Static": "plain"}}
	if err := ValidateMCP(m); err != nil {
		t.Fatalf("headers must validate: %v", err)
	}
	if got := m.HeaderEnvRefs(); strings.Join(got, ",") != "API_KEY,PROXY_TOKEN" {
		t.Fatalf("HeaderEnvRefs = %v", got)
	}
	if got := m.ConsumedEnv(); strings.Join(got, ",") != "EXTRA,API_KEY,PROXY_TOKEN" {
		t.Fatalf("ConsumedEnv = %v", got)
	}
	if got := m.HeaderNames(); strings.Join(got, ",") != "Authorization,X-Api-Key,X-Static" {
		t.Fatalf("HeaderNames = %v", got)
	}

	// Rejections: headers on a local server; a bad header name; control chars.
	if err := ValidateMCP(MCP{Name: "l", Command: []string{"srv"}, Headers: map[string]string{"X": "y"}}); err == nil ||
		!strings.Contains(err.Error(), "remote (url) servers") {
		t.Fatalf("local headers: %v", err)
	}
	if err := ValidateMCP(MCP{Name: "r", URL: "https://h/m", Headers: map[string]string{"bad name": "y"}}); err == nil ||
		!strings.Contains(err.Error(), "header name") {
		t.Fatalf("bad header name: %v", err)
	}
	if err := ValidateMCP(MCP{Name: "r", URL: "https://h/m", Headers: map[string]string{"X": "a\x1bb"}}); err == nil ||
		!strings.Contains(err.Error(), "control characters") {
		t.Fatalf("control chars in value: %v", err)
	}

	// The render carries the template text verbatim, deterministically.
	got := string(MCPConfigJSON([]MCP{{Name: "p", URL: "https://h.example/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${TOK}"}}}))
	if !strings.Contains(got, `"headers": {`) || !strings.Contains(got, `"Authorization": "Bearer ${TOK}"`) {
		t.Fatalf("headers must render verbatim: %s", got)
	}
}

// Round-10 fixes pinned: tchar field names accepted, case-variant duplicate
// header names refused, ConsumedEnv dedupes repeated explicit entries.
func TestMCPHeaderNameGrammarAndCaseDup(t *testing.T) {
	for _, name := range []string{"X_API_KEY", "2FA-Token", "X.Feature", "x-lower"} {
		m := MCP{Name: "r", URL: "https://h/m", Headers: map[string]string{name: "v"}}
		if err := ValidateMCP(m); err != nil {
			t.Errorf("tchar name %q must validate: %v", name, err)
		}
	}
	if err := ValidateMCP(MCP{Name: "r", URL: "https://h/m", Headers: map[string]string{"X Y": "v"}}); err == nil {
		t.Error("space in header name must refuse")
	}
	dup := MCP{Name: "r", URL: "https://h/m", Headers: map[string]string{"Authorization": "a", "authorization": "b"}}
	if err := ValidateMCP(dup); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Errorf("case-variant duplicate must refuse: %v", err)
	}
	if got := (MCP{Name: "x", Env: []string{"A", "A", "B"}}).ConsumedEnv(); strings.Join(got, ",") != "A,B" {
		t.Errorf("ConsumedEnv must dedupe explicit entries: %v", got)
	}
}
