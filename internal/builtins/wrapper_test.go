package builtins

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/testtools"
)

// writeTestContext lays down a small known agent-context file and returns
// its path — every wrapper invocation points BYRE_AGENT_CONTEXT at it so the
// suite box's REAL /etc/byre/agent-context.md never leaks into assertions.
func writeTestContext(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "agent-context.md")
	if err := os.WriteFile(p, []byte("test context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The codex MCP adapter is a shell wrapper deriving `-c` overrides from the
// canonical mcp.json. This drives the REAL script against the REAL
// renderer's output (the two halves of the contract), with a stub codex
// capturing argv — so a format change in either half fails here, not in a
// live box. Needs bash and jq, which the image always has and CI runners do
// too -- so their absence is a skip here and a failure in CI.
func TestCodexMCPLaunchWrapperDerivesFlags(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)

	// A stub codex that records its argv, one per line.
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	mcpJSON := config.MCPConfigJSON([]config.MCP{
		{Name: "github", Command: []string{"gh-mcp", "stdio"}, Env: []string{"GITHUB_TOKEN"}},
		{Name: "linear", URL: "https://mcp.linear.app/mcp"},
		{Name: "proxied", URL: "https://mcp.internal.example/mcp", Headers: map[string]string{
			"authorization": "Bearer ${PROXY_TOKEN}", // bearer tier (lowercase spelling: HTTP names are case-insensitive)
			"X-Api-Key":     "${API_KEY}",            // pure-ref tier: env_http_headers
			"X-Tenant":      "acme-${TENANT}",        // mixed: expanded at launch
			"X-Unset":       "keep-${NEVER_SET_VAR}", // unset ref stays literal (claude parity)
		}},
	})
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join("skills", "codex", "codex-mcp-launch.sh")
	cmd := exec.Command("bash", script, "--dangerously-bypass-approvals-and-sandbox")
	cmd.Env = append(os.Environ(),
		"BYRE_MCP_CONFIG="+mcpPath,
		"BYRE_AGENT_CONTEXT="+ctxPath,
		"BYRE_SESSION_CONTEXT=\n\nsession note", // launcher exports it leading-separated
		"PATH="+dir+":"+os.Getenv("PATH"),
		"PROXY_TOKEN=sekrit", "API_KEY=alsosekrit", "TENANT=corp",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("stub codex never ran: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(argv), "\n"), "\n")
	want := []string{
		"-c", `mcp_servers.github.command="gh-mcp"`,
		"-c", `mcp_servers.github.args=["stdio"]`,
		"-c", `mcp_servers.github.env_vars=["GITHUB_TOKEN"]`,
		"-c", `mcp_servers.linear.url="https://mcp.linear.app/mcp"`,
		"-c", `mcp_servers.proxied.url="https://mcp.internal.example/mcp"`,
		"-c", `mcp_servers.proxied.bearer_token_env_var="PROXY_TOKEN"`,
		"-c", `mcp_servers.proxied.env_http_headers={"X-Api-Key" = "API_KEY"}`,
		"-c", `mcp_servers.proxied.http_headers={"X-Tenant" = "acme-corp", "X-Unset" = "keep-${NEVER_SET_VAR}"}`,
		// One argv element; the line-per-arg stub splits its newlines.
		"-c", "developer_instructions=test context", "", "session note",
		"--dangerously-bypass-approvals-and-sandbox",
	}
	// The leak check runs BEFORE the byte-exact compare: after an exact match
	// against a want with no secret in it, a leak is impossible, so ordered
	// the other way this assertion could never fire -- and a leak should
	// report as a leak, not as an argv mismatch.
	if strings.Contains(string(argv), "sekrit") {
		t.Fatalf("by-name tiers must keep token values off the argv:\n%s", argv)
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv mismatch:\n got %q\nwant %q", got, want)
	}
}

// The opencode MCP adapter builds an OPENCODE_CONFIG_CONTENT from the same
// canonical mcp.json. Drives the REAL script against the REAL renderer output
// with a stub opencode capturing the env — so a format change in either half
// fails here, not in a live box. opencode's schema differs from codex's:
// combined `command` array, {type:"local"|"remote"}, remote headers expanded
// to literal values (no by-name tier), local env inherited (no `environment`).
func TestOpencodeMCPLaunchWrapperBuildsConfig(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	// A stub opencode that records OPENCODE_CONFIG_CONTENT (empty marker if unset).
	envFile := filepath.Join(dir, "env")
	stub := "#!/bin/sh\nprintf '%s' \"${OPENCODE_CONFIG_CONTENT-<<UNSET>>}\" > " + envFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := config.MCPConfigJSON([]config.MCP{
		{Name: "github", Command: []string{"gh-mcp", "stdio"}, Env: []string{"GITHUB_TOKEN"}},
		{Name: "linear", URL: "https://mcp.linear.app/mcp"},
		{Name: "proxied", URL: "https://mcp.internal.example/mcp", Headers: map[string]string{
			"authorization": "Bearer ${PROXY_TOKEN}", // expanded to a literal value
			"X-Tenant":      "acme-${TENANT}",        // mixed: expanded at launch
			"X-Unset":       "keep-${NEVER_SET_VAR}", // unset ref stays literal
		}},
	})
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("skills", "opencode", "opencode-mcp-launch.sh")
	cmd := exec.Command("bash", script, "--auto")
	cmd.Env = append(os.Environ(),
		"BYRE_MCP_CONFIG="+mcpPath,
		"BYRE_AGENT_CONTEXT="+ctxPath,
		"BYRE_SESSION_CONTEXT=",
		"PATH="+dir+":"+os.Getenv("PATH"),
		"OPENCODE_CONFIG_CONTENT=", // unset in the box; the wrapper starts from {}
		"PROXY_TOKEN=sekrit", "TENANT=corp",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("stub opencode never ran: %v", err)
	}
	var got struct {
		MCP map[string]struct {
			Type    string            `json:"type"`
			Command []string          `json:"command"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcp"`
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\n%s", err, raw)
	}
	// stdio -> local with a COMBINED command array; no `environment` (box env inherited).
	gh := got.MCP["github"]
	if gh.Type != "local" || strings.Join(gh.Command, " ") != "gh-mcp stdio" {
		t.Fatalf("github: want local [gh-mcp stdio], got %q %v", gh.Type, gh.Command)
	}
	// remote with headers expanded to literal values at launch.
	px := got.MCP["proxied"]
	if px.Type != "remote" || px.URL != "https://mcp.internal.example/mcp" {
		t.Fatalf("proxied: want remote url, got %q %q", px.Type, px.URL)
	}
	if px.Headers["authorization"] != "Bearer sekrit" || px.Headers["X-Tenant"] != "acme-corp" {
		t.Fatalf("proxied headers not expanded: %v", px.Headers)
	}
	if px.Headers["X-Unset"] != "keep-${NEVER_SET_VAR}" {
		t.Fatalf("unset ref must stay literal: %q", px.Headers["X-Unset"])
	}
	if got.MCP["linear"].Type != "remote" {
		t.Fatalf("linear: want remote, got %q", got.MCP["linear"].Type)
	}
	if len(got.Instructions) != 1 || got.Instructions[0] != ctxPath {
		t.Fatalf("instructions must carry the baked context path: %v", got.Instructions)
	}
}

// A pre-existing OPENCODE_CONFIG_CONTENT is preserved and byre's servers
// deep-merge ON TOP (additive), not clobbered.
func TestOpencodeMCPLaunchWrapperMergesExisting(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	envFile := filepath.Join(dir, "env")
	stub := "#!/bin/sh\nprintf '%s' \"$OPENCODE_CONFIG_CONTENT\" > " + envFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, config.MCPConfigJSON([]config.MCP{
		{Name: "github", Command: []string{"gh-mcp"}},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "opencode", "opencode-mcp-launch.sh"), "--auto")
	cmd.Env = append(os.Environ(),
		"BYRE_MCP_CONFIG="+mcpPath,
		"BYRE_AGENT_CONTEXT="+ctxPath,
		"BYRE_SESSION_CONTEXT=", "PATH="+dir+":"+os.Getenv("PATH"),
		// Deliberately REVERSE-sorted user paths: a sorting dedupe (jq's
		// unique) would scramble them and interleave
		// byre's entry; order must be user's-as-written, byre's appended.
		`OPENCODE_CONFIG_CONTENT={"theme":"nord","instructions":["/z/mine.md","/a/mine.md"],"mcp":{"user-srv":{"type":"local","command":["mine"]}}}`,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	var got struct {
		Theme        string                    `json:"theme"`
		MCP          map[string]map[string]any `json:"mcp"`
		Instructions []string                  `json:"instructions"`
	}
	raw, _ := os.ReadFile(envFile)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, raw)
	}
	if got.Theme != "nord" {
		t.Fatalf("user config must survive the merge: theme=%q", got.Theme)
	}
	if _, ok := got.MCP["user-srv"]; !ok {
		t.Fatalf("user's own mcp server must survive: %v", got.MCP)
	}
	if _, ok := got.MCP["github"]; !ok {
		t.Fatalf("byre's injected server must be present: %v", got.MCP)
	}
	if strings.Join(got.Instructions, " ") != "/z/mine.md /a/mine.md "+ctxPath {
		t.Fatalf("instructions must keep the user's order and append byre's: %v", got.Instructions)
	}
}

// The empty declared MCP set must add zero mcp -c flags (and no bash
// unbound-variable trip on the empty array); the context injection is
// UNCONDITIONAL (the baked file always exists), so exactly one -c remains.
func TestCodexMCPLaunchWrapperEmptySet(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, config.MCPConfigJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "codex", "codex-mcp-launch.sh"), "--flag")
	cmd.Env = append(os.Environ(), "BYRE_MCP_CONFIG="+mcpPath, "BYRE_AGENT_CONTEXT="+ctxPath, "BYRE_SESSION_CONTEXT=", "PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed on the empty set: %v\n%s", err, out)
	}
	argv, _ := os.ReadFile(argvFile)
	if got := strings.TrimRight(string(argv), "\n"); got != "-c\ndeveloper_instructions=test context\n--flag" {
		t.Fatalf("empty MCP set: want context -c + passthrough only, got %q", got)
	}
}

// The empty declared MCP set contributes no `mcp` key — but the context
// injection is UNCONDITIONAL (ADR 0046), so OPENCODE_CONFIG_CONTENT is now
// always set, carrying exactly the instructions entry.
func TestOpencodeMCPLaunchWrapperEmptySet(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	envFile := filepath.Join(dir, "env")
	stub := "#!/bin/sh\nprintf '%s' \"${OPENCODE_CONFIG_CONTENT-<<UNSET>>}\" > " + envFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, config.MCPConfigJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "opencode", "opencode-mcp-launch.sh"), "--auto")
	cmd.Env = append(os.Environ(), "BYRE_MCP_CONFIG="+mcpPath, "BYRE_AGENT_CONTEXT="+ctxPath, "BYRE_SESSION_CONTEXT=", "PATH="+dir+":"+os.Getenv("PATH"), "OPENCODE_CONFIG_CONTENT=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed on the empty set: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(envFile)
	var got struct {
		MCP          map[string]any `json:"mcp"`
		Instructions []string       `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, raw)
	}
	if got.MCP != nil {
		t.Fatalf("empty MCP set must not emit an mcp key: %s", raw)
	}
	if len(got.Instructions) != 1 || got.Instructions[0] != ctxPath {
		t.Fatalf("instructions must carry the baked context path: %v", got.Instructions)
	}
}

// The grok launch adapter injects baked context + session additions as ONE
// --append-system-prompt argv element, and caps the value under Linux's
// per-argument exec limit (~128 KiB) with a loud disclosure instead of
// killing the exec (MAX_ARG_STRLEN binds far
// under byre's uncapped-but-tiered context bounds).
func TestGrokLaunchWrapperInjectsAndCaps(t *testing.T) {
	testtools.NeedTool(t, "bash")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "grok"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(env ...string) []string {
		t.Helper()
		cmd := exec.Command("bash", filepath.Join("skills", "grok", "grok-launch.sh"), "--always-approve")
		cmd.Env = append(append([]string{}, os.Environ()...), append(env, "PATH="+dir+":"+os.Getenv("PATH"))...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("wrapper failed: %v\n%s", err, out)
		}
		raw, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatalf("stub grok never ran: %v", err)
		}
		return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	}

	// Baked + session as one argv element, session leading-separated.
	got := run("BYRE_AGENT_CONTEXT="+ctxPath, "BYRE_SESSION_CONTEXT=\n\nsession note")
	if len(got) != 3 || got[0] != "--append-system-prompt" || got[1] != "test context\n\nsession note" || got[2] != "--always-approve" {
		t.Fatalf("argv = %q", got)
	}

	// A context past the cap truncates WITH the disclosure, and still execs.
	big := filepath.Join(dir, "big.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 150_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	got = run("BYRE_AGENT_CONTEXT="+big, "BYRE_SESSION_CONTEXT=")
	if len(got) != 3 {
		t.Fatalf("argv = %d elements", len(got))
	}
	if len(got[1]) > 101_000 || !strings.Contains(got[1], "truncated at this agent's argv limit") {
		t.Fatalf("cap/disclosure wrong: len=%d tail=%q", len(got[1]), got[1][len(got[1])-120:])
	}

	// Nothing to inject: plain exec.
	missing := filepath.Join(dir, "absent.md")
	got = run("BYRE_AGENT_CONTEXT="+missing, "BYRE_SESSION_CONTEXT=")
	if len(got) != 1 || got[0] != "--always-approve" {
		t.Fatalf("empty context must exec plain: %q", got)
	}
}

// The codex wrapper shares the argv cap (same MAX_ARG_STRLEN exposure).
func TestCodexLaunchWrapperCapsContext(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, config.MCPConfigJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 150_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "codex", "codex-mcp-launch.sh"), "--flag")
	cmd.Env = append(os.Environ(), "BYRE_MCP_CONFIG="+mcpPath, "BYRE_AGENT_CONTEXT="+big, "BYRE_SESSION_CONTEXT=", "PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(argvFile)
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(args) != 3 || args[0] != "-c" {
		t.Fatalf("argv = %q", args)
	}
	if len(args[1]) > 101_100 || !strings.Contains(args[1], "truncated at this agent's argv limit") {
		t.Fatalf("cap/disclosure wrong: len=%d", len(args[1]))
	}
}

// A user's non-array `instructions` (bare string) coerces instead of
// bricking the launch on a jq type error.
func TestOpencodeLaunchWrapperCoercesStringInstructions(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	envFile := filepath.Join(dir, "env")
	stub := "#!/bin/sh\nprintf '%s' \"$OPENCODE_CONFIG_CONTENT\" > " + envFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, config.MCPConfigJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "opencode", "opencode-mcp-launch.sh"), "--auto")
	cmd.Env = append(os.Environ(),
		"BYRE_MCP_CONFIG="+mcpPath, "BYRE_AGENT_CONTEXT="+ctxPath, "BYRE_SESSION_CONTEXT=",
		"PATH="+dir+":"+os.Getenv("PATH"),
		`OPENCODE_CONFIG_CONTENT={"instructions":"/single.md"}`,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper must not brick on a string instructions value: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(envFile)
	var got struct {
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, raw)
	}
	if strings.Join(got.Instructions, " ") != "/single.md "+ctxPath {
		t.Fatalf("string must coerce to a one-element array, byre appended: %v", got.Instructions)
	}
}

// The cap measures BYTES: multi-byte prose under 100k characters but over
// the wire limit must still truncate (${#} counts characters
// and let UTF-8 slip past to a dead exec). And the session additions append
// AFTER the disclosure, never silently dropped by the truncation.
func TestGrokLaunchWrapperCapIsByteAccurateAndKeepsSession(t *testing.T) {
	testtools.NeedTool(t, "bash")
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "grok"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// 70k é = 140k bytes but only 70k characters.
	big := filepath.Join(dir, "utf8.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("é", 70_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "grok", "grok-launch.sh"), "--always-approve")
	cmd.Env = append(os.Environ(),
		"BYRE_AGENT_CONTEXT="+big, "BYRE_SESSION_CONTEXT=\n\nsession survives",
		"PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(argvFile)
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(args) != 3 {
		t.Fatalf("argv = %d elements", len(args))
	}
	if len(args[1]) > 102_000 {
		t.Fatalf("byte cap missed UTF-8 content: %d bytes", len(args[1]))
	}
	if !strings.Contains(args[1], "truncated at this agent's argv limit") {
		t.Fatalf("disclosure missing")
	}
	if !strings.HasSuffix(args[1], "session survives") {
		t.Fatalf("session additions must survive truncation, tail = %q", args[1][len(args[1])-80:])
	}
	// The truncation lands on a codepoint boundary (a raw
	// head -c splits UTF-8 and hands the CLI an invalid string).
	if !utf8.ValidString(args[1]) {
		t.Fatalf("truncated prompt is not valid UTF-8")
	}

	// Absurd corner: a session larger than the whole budget truncates too —
	// degrade, never a dead exec — and the total stays under the wire limit.
	cmd = exec.Command("bash", filepath.Join("skills", "grok", "grok-launch.sh"), "--always-approve")
	cmd.Env = append(os.Environ(),
		"BYRE_AGENT_CONTEXT="+big,
		"BYRE_SESSION_CONTEXT=\n\n"+strings.Repeat("s", 120_000),
		"PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed on oversized session: %v\n%s", err, out)
	}
	raw, _ = os.ReadFile(argvFile)
	args = strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(args[1]) > 103_000 {
		t.Fatalf("oversized session busted the budget: %d bytes", len(args[1]))
	}
	if !strings.Contains(args[1], "session context truncated") {
		t.Fatalf("session truncation must disclose itself")
	}
}

// The grok skill must chmod its wrapper: bundled extraction writes 0644, and
// a non-executable /usr/local/bin/byre-grok-launch is Permission denied at
// exec — a bricked box (the codex/opencode skills carry the
// same line).
func TestGrokSkillChmodsLaunchWrapper(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("skills", "grok", "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "chmod +x /usr/local/bin/byre-grok-launch") {
		t.Fatal("grok skill.toml must chmod the launch wrapper")
	}
}

// The codex wrapper shares the byte-cap + session-survival algorithm; its
// own fixture so a codex-only edit can't regress it.
func TestCodexLaunchWrapperCapIsByteAccurateAndKeepsSession(t *testing.T) {
	testtools.NeedTool(t, "bash", "jq")
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, config.MCPConfigJSON(nil), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "utf8.md")
	if err := os.WriteFile(big, []byte(strings.Repeat("é", 70_000)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join("skills", "codex", "codex-mcp-launch.sh"), "--flag")
	cmd.Env = append(os.Environ(),
		"BYRE_MCP_CONFIG="+mcpPath, "BYRE_AGENT_CONTEXT="+big,
		"BYRE_SESSION_CONTEXT=\n\nsession survives", "PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(argvFile)
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(args) != 3 || args[0] != "-c" {
		t.Fatalf("argv = %q…", args[0])
	}
	if len(args[1]) > 102_100 {
		t.Fatalf("byte cap missed UTF-8 content: %d bytes", len(args[1]))
	}
	if !strings.Contains(args[1], "truncated at this agent's argv limit") || !strings.HasSuffix(args[1], "session survives") {
		t.Fatalf("disclosure/session wrong, tail = %q", args[1][len(args[1])-80:])
	}
	if !utf8.ValidString(args[1]) {
		t.Fatalf("truncated prompt is not valid UTF-8")
	}
}

// The claude launch adapter merges the baked context + the launcher's
// per-session additions into ONE file passed via a single
// --append-system-prompt-file. The Claude CLI REJECTS --append-system-prompt
// alongside --append-system-prompt-file — and an EMPTY second flag slips its
// check, so a two-flag command line only dies on boxes with session additions
// (--self-edit, a firewall): exactly the gap byre shipped until 2026-07-29.
// So this drives the REAL resolved agent command (not a hand-copied argv)
// with a stub claude, and pins the exclusivity on the argv that actually
// reaches the CLI — with the session var non-empty AND empty.
func TestClaudeLaunchWrapperMergesContextIntoOneFlag(t *testing.T) {
	testtools.NeedTool(t, "bash")
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctxPath := writeTestContext(t, dir)
	// The real wrapper goes on PATH under its in-box name, so the resolved
	// command string runs verbatim.
	wrapper, err := os.ReadFile(filepath.Join("skills", "claude", "claude-launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "byre-claude-launch"), wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\0' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(session, tmpdir string, extraEnv ...string) []string {
		t.Helper()
		cmd := exec.Command("bash", "-c", res.AgentCommand())
		cmd.Env = append(os.Environ(),
			"BYRE_AGENT_CONTEXT="+ctxPath,
			"BYRE_SESSION_CONTEXT="+session,
			"TMPDIR="+tmpdir,
			"PATH="+dir+":"+os.Getenv("PATH"),
		)
		cmd.Env = append(cmd.Env, extraEnv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("launch failed: %v\n%s", err, out)
		}
		raw, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatalf("stub claude never ran: %v", err)
		}
		return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	}

	// readInjected enforces the CLI's exclusivity — the two flags must never
	// BOTH reach claude — then returns the injected file's path and content.
	readInjected := func(argv []string) (string, string) {
		t.Helper()
		file, plain := "", false
		for i, a := range argv {
			switch a {
			case "--append-system-prompt-file":
				if file != "" {
					t.Fatalf("--append-system-prompt-file passed twice: %q", argv)
				}
				if i+1 >= len(argv) {
					t.Fatalf("--append-system-prompt-file missing its value: %q", argv)
				}
				file = argv[i+1]
			case "--append-system-prompt":
				plain = true
			}
		}
		if plain && file != "" {
			t.Fatalf("claude rejects --append-system-prompt alongside --append-system-prompt-file; both reached the CLI: %q", argv)
		}
		if file == "" {
			t.Fatalf("no --append-system-prompt-file on the argv: %q", argv)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("injected file unreadable: %v", err)
		}
		return file, string(b)
	}

	// Session additions present (the --self-edit / firewall case that killed
	// the two-flag form): one file, baked + session merged (the launcher
	// exports the var leading-separated, so no separator logic here).
	argv := run("\n\nsession note", dir)
	if _, got := readInjected(argv); got != "test context\n\n\nsession note" {
		t.Errorf("merged content wrong: %q", got)
	}
	joined := strings.Join(argv, "\x00")
	for _, want := range []string{"--dangerously-skip-permissions", "--mcp-config\x00/etc/byre/mcp.json", "--add-dir\x00/etc/byre/claude-skills"} {
		if !strings.Contains(joined, want) {
			t.Errorf("pass-through flag lost: %q (argv %q)", strings.ReplaceAll(want, "\x00", " "), argv)
		}
	}

	// Empty session — the case whose falsy slip HID the bug in live boxes.
	if _, got := readInjected(run("", dir)); got != "test context\n" {
		t.Errorf("baked-only content wrong: %q", got)
	}

	// A plant at the predictable merge path must never route byre's write
	// into another file: the compose lands by rename, which replaces the
	// plant instead of following it. Refusal is the contract — the victim
	// stays byte-identical and the launch still injects correctly.
	mergePath := filepath.Join(dir, "byre-agent-context.md")
	victim := filepath.Join(dir, "victim.md")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(mergePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, mergePath); err != nil {
		t.Fatal(err)
	}
	if _, got := readInjected(run("\n\nsession note", dir)); got != "test context\n\n\nsession note" {
		t.Errorf("merged content wrong after a planted symlink: %q", got)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "precious" {
		t.Errorf("planted symlink routed the merge into its target: %q (%v)", b, err)
	}

	// Compose failure (here: an unusable TMPDIR) must DEGRADE to injecting
	// the baked file, never hand claude a dead merge path or block launch.
	if file, got := readInjected(run("\n\nsession note", filepath.Join(dir, "no-such-dir"))); file != ctxPath || got != "test context\n" {
		t.Errorf("failed compose must fall back to the baked file, injected %q content %q", file, got)
	}

	// Exec leaves no one to clean up after claude exits, so the wrapper must
	// reuse ONE launch-owned path — repeated launches (container restarts)
	// must not accumulate context copies in TMPDIR, and a failed compose
	// must not leak its intermediate mktemp file either.
	merges, err := filepath.Glob(filepath.Join(dir, "byre-agent-context*"))
	if err != nil || len(merges) != 1 {
		t.Errorf("launches must reuse one merge file, got %v (%v)", merges, err)
	}

	// The other arm of the same containment: a DIRECTORY planted at the
	// merge path. The rename refuses it, so the launch degrades to the baked
	// file and writes nothing into the plant (its removal below fails if
	// anything landed there).
	if err := os.Remove(mergePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mergePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if file, got := readInjected(run("\n\nsession note", dir)); file != ctxPath || got != "test context\n" {
		t.Errorf("directory plant must degrade to the baked file, injected %q content %q", file, got)
	}
	if err := os.Remove(mergePath); err != nil {
		t.Errorf("something was written into the planted directory: %v", err)
	}
	if merges, err := filepath.Glob(filepath.Join(dir, "byre-agent-context*")); err != nil || len(merges) != 0 {
		t.Errorf("the refused rename leaked its mktemp intermediate: %v (%v)", merges, err)
	}

	// The race the -d probe can LOSE: the directory lands between the probe
	// and the mv. POSIX mv then reports success — it moved the temp file
	// INTO the plant — so only the post-mv -f probe notices the fixed name
	// is not a file. An mv shim makes the race deterministic: it plants the
	// directory, then runs the real mv. The launch must degrade to the
	// baked file, never hand claude the directory path.
	shim := "#!/bin/sh\n" +
		"if [ -n \"${BYRE_TEST_PLANT:-}\" ]; then rm -rf \"$BYRE_TEST_PLANT\"; mkdir -p \"$BYRE_TEST_PLANT\"; fi\n" +
		"exec /bin/mv \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mv"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if file, got := readInjected(run("\n\nsession note", dir, "BYRE_TEST_PLANT="+mergePath)); file != ctxPath || got != "test context\n" {
		t.Errorf("lost probe race must degrade to the baked file, injected %q content %q", file, got)
	}
}
