package builtins

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// TestOpencodeSkillPinsLoadBearingFacts pins the opencode facts unit tests
// can hold still and that are uniquely tempting to "fix" wrong: the --auto
// autonomy flag (headless asks auto-REJECT without it — never hang, but
// never proceed either), the AGENTS.md context target in the XDG config dir
// (NOT the data-dir state volume — opencode splits them), the egress set
// (models.dev is FUNCTIONAL: with it blocked the login picker silently
// degrades to API-key-only, observed live 2026-07-16), and the login hook.
func TestOpencodeSkillPinsLoadBearingFacts(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "opencode"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.AgentCommand(), "--auto") {
		t.Errorf("opencode autonomy flag missing from launch command %q", res.AgentCommand())
	}
	// The MCP adapter wiring (ADR 0033; live-verified 2026-07-17): the wrapper
	// is the launch command and the inject vouch rides with it — they flip
	// TOGETHER or not at all.
	if !strings.Contains(res.AgentCommand(), "byre-opencode-mcp-launch") {
		t.Errorf("MCP launch wrapper missing from launch command %q", res.AgentCommand())
	}
	if res.Agent.MCP != "inject" {
		t.Errorf("opencode mcp vouch = %q, want inject (live-verified 2026-07-17)", res.Agent.MCP)
	}
	if got := res.AgentContextTarget(); got != "/home/dev/.config/opencode/AGENTS.md" {
		t.Errorf("context target must be AGENTS.md in the XDG config dir, got %q", got)
	}
	egress := strings.Join(res.Egress(), " ")
	for _, h := range []string{"models.dev", "api.anthropic.com", "console.anthropic.com", "claude.ai"} {
		if !strings.Contains(egress, h) {
			t.Errorf("egress missing %s (got %q)", h, egress)
		}
	}
	// The state volume mounts at the XDG DATA dir — not ~/.opencode (the
	// binary dir) and not the config dir (the context target's home).
	var vol bool
	for _, v := range res.Volumes() {
		if v.Name == ".opencode" {
			vol = true
			if v.Target != "/home/dev/.local/share/opencode" {
				t.Errorf("state volume must mount at the XDG data dir, got %q", v.Target)
			}
		}
	}
	if !vol {
		t.Fatal("opencode skill should contribute a .opencode state volume")
	}
	var login bool
	for _, b := range res.BuildBlocks() {
		if b.Name != "opencode" && b.Name != "byre/opencode" {
			continue
		}
		for _, sf := range b.Files {
			if sf.Dest == "/etc/byre/firstrun.d/opencode-login" {
				login = true
			}
		}
	}
	if !login {
		t.Error("opencode login firstrun hook not shipped")
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "opencode"), "opencode-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "opencode auth login") {
		t.Error("login hook lost the auth-login flow")
	}
}

// The opencode login hook, driven for real with a stub `opencode` binary:
// a foreign symlinked credential is removed (anti-planting) and a fresh
// login runs; a credentialed regular file short-circuits; an empty store
// ({}) and a TRUNCATED store (an interrupted in-place write) do NOT count
// as logged in; a static provider key skips the login. The identity-link
// carve-out itself is NOT unit-testable: the trusted dir is deliberately
// the hardcoded absolute /home/dev/.byre-identity/opencode (an env seam
// there would let config [env] redefine the trusted namespace — the codex
// precedent), which only exists in a real box. Any temp-dir link is
// therefore correctly classified foreign below.
func TestOpencodeLoginHookBehavior(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode"), "opencode-login.sh")

	// Pin the WHOLE trusted-target predicate line in the hook source (full
	// conjunction, not its halves) — same rationale as the codex login-hook
	// test: the hardcoded base leaves no fixture seam, so pin the source.
	src, err := os.ReadFile(hook)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src),
		`if [ "$tdir" = "/home/dev/.byre-identity/opencode" ] && [ "$(basename "$target")" = "auth.json" ]; then`) {
		t.Error("hook must trust ONLY the full canonical path /home/dev/.byre-identity/opencode/auth.json (single && predicate)")
	}

	bin := t.TempDir()
	stamp := filepath.Join(bin, "login-attempted")
	stub := "#!/bin/sh\ntouch " + stamp + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dataHome, apiKey string) {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(),
			"PATH="+bin+":/usr/bin:/bin",
			"XDG_DATA_HOME="+dataHome,
			"ANTHROPIC_API_KEY="+apiKey,
			"OPENCODE_API_KEY=",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}
	loginAttempted := func() bool {
		_, err := os.Stat(stamp)
		return err == nil
	}
	reset := func() {
		_ = os.Remove(stamp)
	}
	credPath := func(dataHome string) string {
		dir := filepath.Join(dataHome, "opencode")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, "auth.json")
	}

	// A FOREIGN symlinked credential is removed; a fresh login runs.
	data1 := t.TempDir()
	cred1 := credPath(data1)
	planted := filepath.Join(data1, "elsewhere.json")
	if err := os.WriteFile(planted, []byte(`{"anthropic":{"type":"api","key":"planted"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(planted, cred1); err != nil {
		t.Fatal(err)
	}
	run(data1, "")
	if _, err := os.Lstat(cred1); !os.IsNotExist(err) {
		t.Fatalf("foreign symlinked credential must be removed, still present (%v)", err)
	}
	if !loginAttempted() {
		t.Fatal("removal must fall through to a fresh login; none was attempted")
	}

	// A credentialed regular file short-circuits (no login attempted)...
	reset()
	data2 := t.TempDir()
	if err := os.WriteFile(credPath(data2), []byte(`{"anthropic":{"type":"api","key":"live"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(data2, "")
	if loginAttempted() {
		t.Fatal("valid credential must short-circuit the login; one was attempted")
	}

	// ...but an EMPTY store ({} — no "type" member) does not count...
	reset()
	data3 := t.TempDir()
	if err := os.WriteFile(credPath(data3), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(data3, "")
	if !loginAttempted() {
		t.Fatal("an empty credential store must not count as logged in")
	}

	// ...and neither does a TRUNCATED store — an interrupted in-place write
	// can leave a partial file that already contains a "type" token; the
	// trailing-brace check must reject it.
	reset()
	data4 := t.TempDir()
	if err := os.WriteFile(credPath(data4), []byte(`{"anthropic":{"type":"oauth","access":"eyJtrunc`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(data4, "")
	if !loginAttempted() {
		t.Fatal("a truncated credential store must not count as logged in")
	}

	// A static provider key skips the login entirely.
	reset()
	data5 := t.TempDir()
	credPath(data5) // dir exists, no credential
	run(data5, "sk-ant-static")
	if loginAttempted() {
		t.Fatal("a static provider key must skip the login")
	}
}

// TestOpencodeSharedAuthCompositionResolves: the companion resolves
// alongside the agent, ships the 00- ordered hook (must sort before
// opencode's own login hook), and mounts the machine-scoped identity
// volume. It declares shared_auth_for (vouched 2026-07-17 — the two-box
// API-key field gate passed live, TestOpencodeSharedAuthLiveGate); that
// fact's canonical pin is the TestBuiltinSharedAuthDeclarations table in
// the skills package. The hook itself is codex-shaped and covered
// behaviorally below.
func TestOpencodeSharedAuthCompositionResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "opencode", Skills: []string{"opencode-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("opencode + opencode-shared-auth failed to resolve: %v", err)
	}
	var companion string
	var agentHooks []string
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			if !strings.HasPrefix(sf.Dest, "/etc/byre/firstrun.d/") {
				continue
			}
			switch b.Name {
			case "byre/opencode-shared-auth", "opencode-shared-auth":
				companion = path.Base(sf.Dest)
			case "byre/opencode", "opencode":
				agentHooks = append(agentHooks, path.Base(sf.Dest))
			}
		}
	}
	if companion == "" {
		t.Fatal("symlink-assert hook not shipped")
	}
	if len(agentHooks) == 0 {
		t.Fatal("opencode ships no firstrun hooks; the ordering invariant has nothing to order against")
	}
	for _, h := range agentHooks {
		if !(companion < h) {
			t.Errorf("hook ordering invariant broken: companion %q must sort before opencode's %q", companion, h)
		}
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "opencode-identity" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/opencode" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// shared_auth_for, no longer companion_for: the two-box field gate passed
	// 2026-07-17 (the vouch follows its field gate — and the keys are
	// mutually exclusive, so companion_for must be GONE).
	if !strings.Contains(string(b), `shared_auth_for = "opencode"`) || strings.Contains(string(b), `companion_for = "opencode"`) {
		t.Error("vouch shape wrong: want shared_auth_for (field gate passed 2026-07-17), without companion_for")
	}
	// The API-key-only scope must be on the record (OAuth entries are
	// unsupported and warned; they still ride the whole-file share).
	if !strings.Contains(string(b), "API-KEY LOGINS ONLY") {
		t.Error("API-key-only scope missing from the skill.toml record")
	}
}

// runOpencodeSharedAuthHook executes the symlink-assert hook at hookPath
// against a temp identity base + XDG data home (both the hook's test
// seams). The hook path is resolved once by the caller — rebuilding the
// catalog per invocation would repeat a full LoadCatalog for nothing.
func runOpencodeSharedAuthHook(t *testing.T, hookPath, identityBase, dataHome string) {
	t.Helper()
	cmd := exec.Command("bash", hookPath)
	cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+identityBase, "XDG_DATA_HOME="+dataHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
}

// The opencode symlink-assert hook's four behaviors, driven for real (the
// codex-shared-auth suite, retargeted): fresh box gets a dangling link; an
// existing per-project login is ADOPTED; a local fork is healed in favor of
// the shared credential; and the whole thing is idempotent.
func TestOpencodeSharedAuthHookBehavior(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "firstrun.sh")
	base, dataHome := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "opencode", "auth.json")
	cred := filepath.Join(dataHome, "opencode", "auth.json")

	// 1. Fresh: dangling symlink pointing at the (absent) shared credential.
	runOpencodeSharedAuthHook(t, hook, base, dataHome)
	if got, err := os.Readlink(cred); err != nil || got != shared {
		t.Fatalf("fresh run should leave a dangling link to %q, got %q (%v)", shared, got, err)
	}
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Fatalf("fresh run must not fabricate a shared credential")
	}

	// 2. Adopt: a real local login and no shared copy — the file MOVES in.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"adopted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runOpencodeSharedAuthHook(t, hook, base, dataHome)
	if b, err := os.ReadFile(shared); err != nil || string(b) != `{"adopted":true}` {
		t.Fatalf("existing login not adopted into the shared volume: %v %q", err, b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("adopted cred not re-linked: %q", got)
	}

	// 3. Heal a fork: local plain file AND shared credential — shared wins.
	if err := os.Remove(cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cred, []byte(`{"fork":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runOpencodeSharedAuthHook(t, hook, base, dataHome)
	if b, _ := os.ReadFile(shared); string(b) != `{"adopted":true}` {
		t.Fatalf("shared credential clobbered by a fork: %q", b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("fork not healed to the link: %q", got)
	}

	// 4. Idempotent: run again, nothing changes.
	runOpencodeSharedAuthHook(t, hook, base, dataHome)
	if b, _ := os.ReadFile(cred); string(b) != `{"adopted":true}` {
		t.Fatalf("idempotent re-run changed the credential: %q", b)
	}
}

// The API-key-only scope (Pete's ruling): an OAuth entry in the shared store
// draws a friendly warning and is NEVER touched; an API-key-only store is
// silent.
func TestOpencodeSharedAuthWarnsOnOAuthEntry(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "opencode-shared-auth"), "firstrun.sh")

	warns := func(authJSON string) (string, bool) {
		t.Helper()
		base, dataHome := t.TempDir(), t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "opencode"), 0o755); err != nil {
			t.Fatal(err)
		}
		shared := filepath.Join(base, "opencode", "auth.json")
		if err := os.WriteFile(shared, []byte(authJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "XDG_DATA_HOME="+dataHome)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
		s := string(out)
		before, contentSurvives := os.ReadFile(shared)
		if contentSurvives != nil || string(before) != authJSON {
			t.Fatalf("the credential must never be touched, got %q (%v)", before, contentSurvives)
		}
		return s, strings.Contains(s, "API-key logins only")
	}

	// OAuth entry (tolerate the JSON.stringify(...,2) spacing) -> warns.
	if _, w := warns(`{"anthropic": {"type": "oauth", "access": "x", "refresh": "y"}}`); !w {
		t.Fatal("an OAuth entry in the shared store must draw the API-key-only warning")
	}
	// API-key-only store -> silent.
	if _, w := warns(`{"anthropic": {"type": "api", "key": "sk-ant-live"}}`); w {
		t.Fatal("an API-key-only store must NOT warn")
	}
}
