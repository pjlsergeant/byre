package builtins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestSharedAuthCompositionResolves pins the claude-shared-auth companion
// composing with the claude agent skill (ADR 0017): the machine-scoped
// identity volume, both hooks landing in the launcher's hook dirs (00- prefix
// so the firstrun hook sorts before agent-skill hooks), and the expiry brief
// reaching the agent's context.
func TestSharedAuthCompositionResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"claude-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("claude + claude-shared-auth failed to resolve: %v", err)
	}
	shipped := map[string]bool{}
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			shipped[b.Name+" "+sf.Dest] = true
		}
	}
	for _, want := range []string{
		"byre/claude-shared-auth /etc/byre/firstrun.d/00-claude-shared-auth",
		"byre/claude-shared-auth /etc/byre/env.d/50-claude-shared-auth.sh",
	} {
		if !shipped[want] {
			t.Errorf("missing shipped file %q; shipped: %v", want, shipped)
		}
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "claude-identity" && v.Role == "state" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/claude" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}
	if !strings.Contains(res.Context(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("expiry brief not in agent context")
	}
}

// The claude-shared-auth hook seeds onboarding-complete state on a FRESH
// config dir when the shared token exists (interactive Claude's wizard gates
// on .claude.json, not the env token -- host-verified 2026-07-07), and never
// touches an existing .claude.json.
func TestClaudeSharedAuthHookSeedsOnboarding(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "claude-shared-auth"), "firstrun.sh")
	run := func(base, cfg string) {
		t.Helper()
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "CLAUDE_CONFIG_DIR="+cfg)
		cmd.Stdin = nil // no TTY: the paste path must not trigger
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}

	// Token present + fresh config dir -> seeded.
	base, cfg := t.TempDir(), filepath.Join(t.TempDir(), "claude")
	if err := os.MkdirAll(filepath.Join(base, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "claude", "token"), []byte("sk-ant-oat01-x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(base, cfg)
	b, err := os.ReadFile(filepath.Join(cfg, ".claude.json"))
	if err != nil || !strings.Contains(string(b), "hasCompletedOnboarding") {
		t.Fatalf("onboarding not seeded: %v %q", err, b)
	}

	// Existing .claude.json -> untouched (Claude owns it).
	if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(base, cfg)
	if b, _ := os.ReadFile(filepath.Join(cfg, ".claude.json")); string(b) != `{"mine":true}` {
		t.Fatalf("existing .claude.json clobbered: %q", b)
	}

	// No token -> nothing seeded (per-project login must proceed untouched).
	base2, cfg2 := t.TempDir(), filepath.Join(t.TempDir(), "claude")
	run(base2, cfg2)
	if _, err := os.Stat(filepath.Join(cfg2, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("seeded onboarding without a shared token")
	}
}

// The claude-shared-auth env.sh hook is SOURCED (by the launcher and, via
// /etc/profile.d, by every login shell), so it is a PURE env-setter: it exports
// the shared token stripped of whitespace and does nothing else -- no warning,
// no prompt, no file move even when a leftover per-project login sits alongside
// the token. That remediation moved to firstrun.sh (tested below), because
// sourcing env.d into every login shell must never re-fire a prompt.
func TestClaudeSharedAuthEnvHookExportsOnly(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "claude-shared-auth"), "env.sh")
	// Source the hook the way the launcher does, then record what it exported.
	// A clean env (no inherited CLAUDE_CODE_OAUTH_TOKEN) keeps the no-token
	// cases honest when the test itself runs inside a token-authed box.
	run := func(base, cfg string) (token, output string) {
		t.Helper()
		tokenOut := filepath.Join(t.TempDir(), "token.out")
		cmd := exec.Command("bash", "-c", `. "$0"; printf '%s' "$CLAUDE_CODE_OAUTH_TOKEN" >"$1"`, hook, tokenOut)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "BYRE_IDENTITY_BASE="+base, "CLAUDE_CONFIG_DIR="+cfg)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("sourcing the hook failed: %v (%s)", err, out)
		}
		b, err := os.ReadFile(tokenOut)
		if err != nil {
			t.Fatalf("hook exited the sourcing shell: %v", err)
		}
		return string(b), string(out)
	}
	seed := func(token string) (base, cfg string) {
		t.Helper()
		base, cfg = t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "claude", "token"), []byte(token), 0o600); err != nil {
			t.Fatal(err)
		}
		return base, cfg
	}

	// Token with trailing newline -> exported stripped, silent.
	base, cfg := seed("sk-ant-oat01-x\n")
	if tok, out := run(base, cfg); tok != "sk-ant-oat01-x" || out != "" {
		t.Fatalf("clean export broken: token=%q output=%q", tok, out)
	}

	// A leftover .credentials.json must NOT make the pure env hook say anything
	// or touch the file -- that is firstrun.sh's job now.
	creds := filepath.Join(cfg, ".credentials.json")
	if err := os.WriteFile(creds, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, out := run(base, cfg); tok != "sk-ant-oat01-x" || out != "" {
		t.Fatalf("env hook must stay pure/silent with a leftover login: token=%q output=%q", tok, out)
	}
	if _, err := os.Stat(creds); err != nil {
		t.Fatalf("env hook must not touch the login: %v", err)
	}

	// No token / whitespace-only token -> nothing exported, silent.
	base2, cfg2 := t.TempDir(), t.TempDir()
	if tok, out := run(base2, cfg2); tok != "" || out != "" {
		t.Fatalf("no-token launch must stay silent: token=%q output=%q", tok, out)
	}
	base3, cfg3 := seed(" \n")
	if tok, out := run(base3, cfg3); tok != "" || out != "" {
		t.Fatalf("whitespace token must be treated as absent: token=%q output=%q", tok, out)
	}
}

// The stale-per-project-login remediation lives in firstrun.sh (EXECUTED every
// launch, self-guarded on the token), not env.sh: interactive Claude prefers a
// stored .credentials.json over the env token and stops refreshing it, so such
// a box 401s ~8h after that login (host-verified 2026-07-07). The file is
// Claude's, so it is moved only with the user's yes: interactive offers the
// move (default Y), non-interactive warns and leaves it.
func TestClaudeSharedAuthFirstrunRemediatesStaleLogin(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "claude-shared-auth"), "firstrun.sh")
	seed := func() (base, cfg string) {
		t.Helper()
		base, cfg = t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "claude", "token"), []byte("sk-ant-oat01-x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return base, cfg
	}
	// Execute firstrun.sh (it is a command hook, not sourced). stdin != nil sets
	// the BYRE_ASSUME_TTY seam so the interactive offer runs and reads the answer.
	run := func(base, cfg string, stdin *string) string {
		t.Helper()
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "CLAUDE_CONFIG_DIR="+cfg)
		if stdin != nil {
			cmd.Env = append(cmd.Env, "BYRE_ASSUME_TTY=1")
			cmd.Stdin = strings.NewReader(*stdin)
		}
		out, _ := cmd.CombinedOutput() // firstrun exits 0; ignore status
		return string(out)
	}

	// Leftover login, no TTY -> warns and leaves the file put (no user to say yes).
	base, cfg := seed()
	creds := filepath.Join(cfg, ".credentials.json")
	if err := os.WriteFile(creds, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := run(base, cfg, nil)
	if !strings.Contains(out, "401") || !strings.Contains(out, ".credentials.json") {
		t.Fatalf("warning missing or unactionable: %q", out)
	}
	if _, err := os.Stat(creds); err != nil {
		t.Fatalf("non-interactive launch must not touch the login: %v", err)
	}

	// Interactive decline ("n") -> file stays, told how to fix by hand.
	answer := "n\n"
	if out := run(base, cfg, &answer); !strings.Contains(out, "left in place") {
		t.Fatalf("declined offer should say the file was left: %q", out)
	}
	if _, err := os.Stat(creds); err != nil {
		t.Fatalf("declining the offer must leave the login: %v", err)
	}

	// Interactive accept (bare Enter = default Y) -> moved to .bak.
	answer = "\n"
	if out := run(base, cfg, &answer); !strings.Contains(out, "moved") {
		t.Fatalf("accepted offer broken: output=%q", out)
	}
	if _, err := os.Stat(creds); !os.IsNotExist(err) {
		t.Fatal("accepted offer must move the login aside")
	}
	if _, err := os.Stat(creds + ".bak"); err != nil {
		t.Fatalf("moved login must land at .bak: %v", err)
	}

	// No leftover login -> silent, no move.
	base2, cfg2 := seed()
	if out := run(base2, cfg2, nil); strings.Contains(out, "401") {
		t.Fatalf("clean box must not warn: %q", out)
	}

	// An MCP-only credentials file (mcpOAuth, no claudeAiOauth) is HEALTHY
	// state — MCP server logins on the project volume — not a stale inference
	// login: the hook must neither warn nor offer, and above all never move
	// it (that silently signs the box out of its MCP servers; found live
	// 2026-07-15 the first time MCP OAuth met shared auth).
	base3, cfg3 := seed()
	mcpOnly := filepath.Join(cfg3, ".credentials.json")
	if err := os.WriteFile(mcpOnly, []byte(`{"mcpOAuth":{"agentblocks|abc123":{"accessToken":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	answer = "\n" // even an interactive default-Y run has nothing to offer
	if out := run(base3, cfg3, &answer); strings.Contains(out, "401") || strings.Contains(out, "move it aside") {
		t.Fatalf("MCP-only credentials must not trigger the offer: %q", out)
	}
	if _, err := os.Stat(mcpOnly); err != nil {
		t.Fatalf("MCP-only credentials must never be moved: %v", err)
	}

	// Both keys present: the offer runs (the stale login is real) but the
	// collateral — MCP logins ride the same file — is disclosed first.
	base4, cfg4 := seed()
	both := filepath.Join(cfg4, ".credentials.json")
	if err := os.WriteFile(both, []byte(`{"claudeAiOauth":{},"mcpOAuth":{"x|y":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	answer = "\n"
	out = run(base4, cfg4, &answer)
	if !strings.Contains(out, "MCP server logins") || !strings.Contains(out, "/mcp") {
		t.Fatalf("both-keys move must disclose the MCP collateral: %q", out)
	}
	if _, err := os.Stat(both + ".bak"); err != nil {
		t.Fatalf("accepted both-keys offer must still move the file: %v", err)
	}
}
