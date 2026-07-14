package builtins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// The codex MCP adapter is a shell wrapper deriving `-c` overrides from the
// canonical mcp.json. This drives the REAL script against the REAL
// renderer's output (the two halves of the contract), with a stub codex
// capturing argv — so a format change in either half fails here, not in a
// live box. Skips where bash or jq is unavailable (the image always has
// both; CI runners do too).
func TestCodexMCPLaunchWrapperDerivesFlags(t *testing.T) {
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s unavailable", bin)
		}
	}
	dir := t.TempDir()

	// A stub codex that records its argv, one per line.
	argvFile := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + argvFile + "\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	mcpJSON := config.MCPConfigJSON([]config.MCP{
		{Name: "github", Command: []string{"gh-mcp", "stdio"}, Env: []string{"GITHUB_TOKEN"}},
		{Name: "linear", URL: "https://mcp.linear.app/mcp"},
	})
	mcpPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(mcpPath, mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join("skills", "codex", "codex-mcp-launch.sh")
	cmd := exec.Command("bash", script, "--dangerously-bypass-approvals-and-sandbox")
	cmd.Env = append(os.Environ(),
		"BYRE_MCP_CONFIG="+mcpPath,
		"PATH="+dir+":"+os.Getenv("PATH"),
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
		"--dangerously-bypass-approvals-and-sandbox",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv mismatch:\n got %q\nwant %q", got, want)
	}
}

// The empty declared set must exec codex with the passthrough args ONLY —
// zero -c flags (and no bash unbound-variable trip on the empty array).
func TestCodexMCPLaunchWrapperEmptySet(t *testing.T) {
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s unavailable", bin)
		}
	}
	dir := t.TempDir()
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
	cmd.Env = append(os.Environ(), "BYRE_MCP_CONFIG="+mcpPath, "PATH="+dir+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper failed on the empty set: %v\n%s", err, out)
	}
	argv, _ := os.ReadFile(argvFile)
	if strings.TrimRight(string(argv), "\n") != "--flag" {
		t.Fatalf("empty set must pass through args only: %q", argv)
	}
}
