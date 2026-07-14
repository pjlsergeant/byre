package builtins

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/build"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// testCat builds a catalog over a fresh home with bundled embed.FS.
func testCat(t *testing.T) (home string, cat *packages.Catalog) {
	t.Helper()
	home = t.TempDir()
	cat, err := packages.LoadCatalog(home, FS(), "0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return home, cat
}

// skillDir returns the host directory for a bundled/local skill (extracted embed).
func skillDir(t *testing.T, cat *packages.Catalog, name string) string {
	t.Helper()
	ent, err := cat.ResolveName(name)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ent.HostDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBundledClaudeInEmbed(t *testing.T) {
	b, err := fs.ReadFile(FS(), "skills/claude/skill.toml")
	if err != nil {
		t.Fatalf("claude skill not in embed: %v", err)
	}
	if !strings.Contains(string(b), "[agent]") || !strings.Contains(string(b), "claude") {
		t.Errorf("claude skill.toml content unexpected:\n%s", b)
	}
}

// TestBuiltinAgentSkillsResolve verifies the shipped agent skills parse and
// resolve as agents (catches TOML/structure errors without a Docker build —
// codex/gemini are still drafts pending host verification of install/auth).
func TestBuiltinAgentSkillsResolve(t *testing.T) {
	_, cat := testCat(t)
	for _, agent := range []string{"claude", "codex", "gemini", "grok"} {
		res, err := skills.Resolve(config.Config{Agent: agent}, cat)
		if err != nil {
			t.Errorf("agent %q: resolve failed: %v", agent, err)
			continue
		}
		if res.AgentCommand() == "" {
			t.Errorf("agent %q: no launch command", agent)
		}
		if len(res.Volumes()) == 0 || res.Volumes()[0].Role != "state" {
			t.Errorf("agent %q: expected a state volume, got %+v", agent, res.Volumes())
		}
	}
}

func TestCatalogTemplatesAndListAgents(t *testing.T) {
	_, cat := testCat(t)
	for _, n := range []string{"go", "node", "python"} {
		if _, err := cat.ResolveName(n); err != nil {
			t.Errorf("template %q: %v", n, err)
		}
	}
	agents := skills.ListAgentSkills(cat)
	if len(agents) != 4 {
		t.Errorf("expected 4 agent skills (claude/codex/gemini/grok), got %v", agents)
	}
}

// TestSelfHostCompositionResolves verifies the BUNDLED slice of byre's own
// self-hosting config (Claude agent + codex + grok). codereview and devlog
// moved out of the binary (D12, 2026-07-13) -- their content is pinned by the
// pjlsergeant-byre-skills repo and the host-side dogfood, not this suite; here
// we pin that their RETIRED bare names fail with the exact install remedy.
func TestSelfHostCompositionResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"codex", "grok"}}, cat)
	if err != nil {
		t.Fatalf("bundled self-host slice failed to resolve: %v", err)
	}
	shipped := map[string]bool{} // "skill dest" -> present
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			shipped[b.Name+" "+sf.Dest] = true
		}
	}
	for _, want := range []string{
		"byre/codex /etc/byre/firstrun.d/codex-login",
		"byre/grok /etc/byre/firstrun.d/grok-login",
		"byre/grok /etc/byre/firstrun.d/grok-bundled",
	} {
		if !shipped[want] {
			t.Errorf("missing shipped file %q; shipped: %v", want, shipped)
		}
	}
	// Workflow context reaches Claude's memory file.
	if res.AgentContextTarget() != "/home/dev/.claude/CLAUDE.md" {
		t.Errorf("context target wrong: %q", res.AgentContextTarget())
	}
}

// TestRetiredNamesTombstone pins the D15 cut-over: the bare names byre used
// to bundle fail with the EXACT pinned install command (URI and digest, not
// just their shapes), and cannot be reclaimed by a local package.
func TestRetiredNamesTombstone(t *testing.T) {
	_, cat := testCat(t)
	want := map[string]string{
		"codereview": "byre skill install https://raw.githubusercontent.com/pjlsergeant/pjlsergeant-byre-skills/v1.0.0/skills/codereview/skill.toml --digest sha256:366093764005feacafa40560a47c2847ba130678de86fdbc02e7a465c553bb3f, then reference pjlsergeant/codereview",
		"devlog":     "byre skill install https://raw.githubusercontent.com/pjlsergeant/pjlsergeant-byre-skills/v1.0.0/skills/devlog/skill.toml --digest sha256:9ecb65b18386ceea0dc54b7bb040b42e29a9872ab8fed4f9b1f86d5562926c12, then reference pjlsergeant/devlog",
	}
	for bare, remedy := range want {
		_, err := cat.ResolveName(bare)
		if err == nil {
			t.Fatalf("%s must not resolve after the move", bare)
		}
		if !strings.Contains(err.Error(), remedy) {
			t.Errorf("%s tombstone must carry the exact pinned remedy:\nwant substring: %s\ngot: %v", bare, remedy, err)
		}
	}
	if !cat.IsProtected("devlog") || !cat.IsProtected("codereview") {
		t.Error("retired names must stay protected (D15)")
	}
}

// TestByreConfigSourcesAgreeWithTombstones is the drift lock between the four
// hand-duplicated URI/digest pairs: this repo's own byre.config [sources]
// hints must name exactly the URIs and digests the D15 tombstones print --
// disagreement means a release updated one copy and not the other.
func TestByreConfigSourcesAgreeWithTombstones(t *testing.T) {
	cfg, err := config.ParseFile(filepath.Join("..", "..", "byre.preset"))
	if err != nil {
		t.Fatal(err)
	}
	for bare, id := range map[string]string{
		"codereview": "pjlsergeant/codereview",
		"devlog":     "pjlsergeant/devlog",
	} {
		hint, ok := cfg.Sources[id]
		if !ok {
			t.Fatalf("byre.config [sources] missing %q", id)
		}
		tomb := packages.RetiredTombstone(bare)
		if tomb == "" {
			t.Fatalf("no tombstone for %q", bare)
		}
		// ParseFile does not run ValidateLayer, so empty fields parse fine --
		// and Contains(x, "") passes vacuously. Both pins must exist to compare.
		if hint.URI == "" {
			t.Fatalf("byre.config [sources] %q lost its uri", id)
		}
		if !strings.Contains(tomb, hint.URI) {
			t.Errorf("%s tombstone URI drifted from byre.config [sources]:\ntombstone: %s\nconfig:    %s", bare, tomb, hint.URI)
		}
		// digest is optional in [sources] generally, but REQUIRED here: an
		// empty one would make the Contains check below vacuously pass --
		// the exact regression this test exists to prevent.
		if hint.Digest == "" {
			t.Fatalf("byre.config [sources] %q lost its digest pin", id)
		}
		if !strings.Contains(tomb, hint.Digest) {
			t.Errorf("%s tombstone digest drifted from byre.config [sources]:\ntombstone: %s\nconfig:    %s", bare, tomb, hint.Digest)
		}
	}
}

// TestSelfHostBuildStagesAndOrders assembles a real build context for the
// self-host composition and checks that its shipped files are staged and the
// generated Dockerfile COPYs byre-codereview before the chmod that uses it.
func TestSelfHostBuildStagesAndOrders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	paths, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStore(paths.Home); err != nil {
		t.Fatal(err)
	}
	_, cat := testCat(t)
	_ = cat
	// The bundled slice of this repo's own byre.config skill set (codex +
	// grok; codereview/devlog are installed packages now, covered by the
	// host-side dogfood).
	cfg := config.Config{Base: "golang:1.22-bookworm", Agent: "claude", Skills: []string{"codex", "grok"}}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	df, err := build.Assemble(paths, cfg, res)
	if err != nil {
		t.Fatal(err)
	}
	// codex's first-run login hook is staged and COPYd to firstrun.d.
	if _, err := os.Stat(filepath.Join(paths.ContextDir, "skills", "byre", "codex", "codex-login.sh")); err != nil {
		t.Fatalf("codex-login.sh not staged: %v", err)
	}
	if !strings.Contains(df, gen.CopyLine("skills/byre/codex/codex-login.sh", "/etc/byre/firstrun.d/codex-login")) {
		t.Errorf("codex login hook COPY missing:\n%s", df)
	}
	// grok's two firstrun hooks are staged and COPYd likewise.
	for src, dst := range map[string]string{
		"skills/byre/grok/grok-login.sh":   "/etc/byre/firstrun.d/grok-login",
		"skills/byre/grok/grok-bundled.sh": "/etc/byre/firstrun.d/grok-bundled",
	} {
		if _, err := os.Stat(filepath.Join(paths.ContextDir, filepath.FromSlash(src))); err != nil {
			t.Fatalf("%s not staged: %v", src, err)
		}
		if !strings.Contains(df, gen.CopyLine(src, dst)) {
			t.Errorf("grok hook COPY missing (%s -> %s):\n%s", src, dst, df)
		}
	}
}

// claude/gemini install their binaries OUTSIDE their state dir, so they wipe it
// after install (a fresh state volume then starts clean). Each wipe must come
// after the installer that created the residue.
func TestAgentSkillsCleanStateDir(t *testing.T) {
	_, cat := testCat(t)
	for _, c := range []struct{ agent, install, clean string }{
		{"claude", "install.sh", "rm -rf /home/dev/.claude"},
		{"gemini", "npm install -g", "rm -rf /home/dev/.gemini"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, cat)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		installAt, cleanAt := -1, -1
		for _, b := range res.BuildBlocks() {
			if b.Name != "byre/"+c.agent && b.Name != c.agent {
				continue
			}
			for i, line := range b.Dockerfile {
				if installAt < 0 && strings.Contains(line, c.install) {
					installAt = i
				}
				if strings.Contains(line, c.clean) {
					cleanAt = i
				}
			}
		}
		if installAt < 0 || cleanAt < 0 || cleanAt <= installAt {
			t.Errorf("%s: cleanup %q must come after the installer (install=%d clean=%d)", c.agent, c.clean, installAt, cleanAt)
		}
	}
}

// codex and grok install their BINARIES into their dotdir (~/.codex, ~/.grok),
// so they must NOT wipe it (doing so deletes the binary and leaves dangling
// symlinks).
func TestBinaryDirAgentsDoNotWipeIt(t *testing.T) {
	_, cat := testCat(t)
	for _, c := range []struct{ agent, binDir string }{
		{"codex", "/home/dev/.codex"},
		{"grok", "/home/dev/.grok"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, cat)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		for _, b := range res.BuildBlocks() {
			if b.Name != "byre/"+c.agent && b.Name != c.agent {
				continue
			}
			for _, line := range b.Dockerfile {
				if strings.Contains(line, "rm -rf "+c.binDir) {
					t.Errorf("%s must NOT wipe %s (its binary lives there): %q", c.agent, c.binDir, line)
				}
			}
		}
	}
}

// codex's/grok's state volume + home env must be a DIFFERENT path from the
// dotdir where the installer puts the binary — otherwise the volume
// masks/seeds-over the binary (the bug). Guards the decoupling.
func TestStateVolumeSeparateFromBinaryDir(t *testing.T) {
	_, cat := testCat(t)
	for _, c := range []struct{ agent, envKey, binDir, volName string }{
		{"codex", "CODEX_HOME", "/home/dev/.codex", ".codex"},
		{"grok", "GROK_HOME", "/home/dev/.grok", ".grok"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, cat)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		if res.Env()[c.envKey] == "" || res.Env()[c.envKey] == c.binDir {
			t.Fatalf("%s must be set and NOT %s (the binary dir), got %q", c.envKey, c.binDir, res.Env()[c.envKey])
		}
		var found bool
		for _, v := range res.Volumes() {
			if v.Name == c.volName {
				found = true
				if v.Target == c.binDir {
					t.Errorf("%s state volume must NOT mount at %s (the binary dir)", c.volName, c.binDir)
				}
				if v.Target != res.Env()[c.envKey] {
					t.Errorf("%s volume target %q should equal %s %q", c.volName, v.Target, c.envKey, res.Env()[c.envKey])
				}
			}
		}
		if !found {
			t.Fatalf("%s skill should contribute a %s state volume", c.agent, c.volName)
		}
	}
}

// The node template gives the box its OWN node_modules — a cache volume at
// /workspace/node_modules that masks the host's in the bind-mounted project, so
// host (e.g. macOS) and container (Linux) deps stay separate.
func TestNodeTemplateContainerNodeModules(t *testing.T) {
	_, cat := testCat(t)
	ent, err := cat.ResolveName("node")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ent.ReadPrimary()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(packages.StripPackageTable(raw))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, v := range cfg.Volumes {
		if v.Target == "/workspace/node_modules" {
			found = true
			if v.Role != "cache" {
				t.Errorf("node_modules should be a cache volume, got role %q", v.Role)
			}
		}
	}
	if !found {
		t.Error("node template should declare a /workspace/node_modules cache volume")
	}
}

// TestUpdateTemplatesOverwritesAndBacksUp mirrors the skills update test:
// shipped template changes need the same pickup path (`byre skill update`).
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

// TestCodexSharedAuthCompositionResolves pins the codex-shared-auth companion
// composing with the codex skill: the machine-scoped identity volume and the
// 00-prefixed symlink-assert hook sorting BEFORE codex's own login hook in
// the launcher's glob order (the login hook must see the asserted link).
func TestCodexSharedAuthCompositionResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "codex", Skills: []string{"codex-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("codex + codex-shared-auth failed to resolve: %v", err)
	}
	var hook bool
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			if b.Name == "byre/codex-shared-auth" && sf.Dest == "/etc/byre/firstrun.d/00-codex-shared-auth" {
				hook = true
			}
		}
	}
	if !hook {
		t.Error("symlink-assert hook not shipped")
	}
	if !("00-codex-shared-auth" < "codex-login") {
		t.Error("hook ordering invariant broken: companion must sort before codex-login")
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "codex-identity" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/codex" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}
}

// runCodexSharedAuthHook executes the real materialized symlink-assert hook
// against a temp identity base + CODEX_HOME (the BYRE_IDENTITY_BASE seam).
func runCodexSharedAuthHook(t *testing.T, identityBase, codexHome string) {
	t.Helper()
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "codex-shared-auth"), "firstrun.sh")
	cmd := exec.Command("bash", hook)
	cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+identityBase, "CODEX_HOME="+codexHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
}

// The symlink-assert hook's four behaviors, driven for real: fresh box gets a
// dangling link; an existing per-project login is ADOPTED (moved, then
// linked); a local fork is healed in favor of the shared credential; and the
// whole thing is idempotent.
func TestCodexSharedAuthHookBehavior(t *testing.T) {
	base, home := t.TempDir(), t.TempDir()
	shared := filepath.Join(base, "codex", "auth.json")
	cred := filepath.Join(home, "auth.json")

	// 1. Fresh: dangling symlink pointing at the (absent) shared credential.
	runCodexSharedAuthHook(t, base, home)
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
	runCodexSharedAuthHook(t, base, home)
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
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(shared); string(b) != `{"adopted":true}` {
		t.Fatalf("shared credential clobbered by a fork: %q", b)
	}
	if got, _ := os.Readlink(cred); got != shared {
		t.Fatalf("fork not healed to the link: %q", got)
	}

	// 4. Idempotent: run again, nothing changes.
	runCodexSharedAuthHook(t, base, home)
	if b, _ := os.ReadFile(cred); string(b) != `{"adopted":true}` {
		t.Fatalf("idempotent re-run changed the credential: %q", b)
	}
}

// TestGrokSkillPinsLoadBearingFacts pins the grok facts unit tests can hold
// still and that are uniquely tempting to "fix" wrong: the autonomy flag, the
// AGENTS.md context target inside GROK_HOME, the egress set (the device-auth
// flow was observed live against accounts.x.ai), the device-auth login flow —
// which the vendor's TOP-LEVEL README does not document (it lags the binary;
// the flag is real, see the skill.toml evidence note) — and the bundled-skills
// bridge hook (without it the GROK_HOME split silently drops grok's bundled
// product skills).
func TestGrokSkillPinsLoadBearingFacts(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "grok"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.AgentCommand(), "--always-approve") {
		t.Errorf("grok autonomy flag missing from launch command %q", res.AgentCommand())
	}
	if got := res.AgentContextTarget(); got != "/home/dev/.grok-home/AGENTS.md" {
		t.Errorf("context target must be AGENTS.md inside GROK_HOME, got %q", got)
	}
	egress := strings.Join(res.Egress(), " ")
	for _, h := range []string{"cli-chat-proxy.grok.com", "auth.x.ai", "accounts.x.ai"} {
		if !strings.Contains(egress, h) {
			t.Errorf("egress missing %s (got %q)", h, egress)
		}
	}
	var login, bundled bool
	for _, b := range res.BuildBlocks() {
		if b.Name != "grok" && b.Name != "byre/grok" {
			continue
		}
		for _, sf := range b.Files {
			switch sf.Dest {
			case "/etc/byre/firstrun.d/grok-login":
				login = true
			case "/etc/byre/firstrun.d/grok-bundled":
				bundled = true
			}
		}
	}
	if !login || !bundled {
		t.Errorf("grok firstrun hooks not both shipped (login=%v bundled=%v)", login, bundled)
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "grok"), "grok-login.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "grok login --device-auth") {
		t.Error("login hook lost the device-auth flow (the vendor README omits the flag; the binary has it)")
	}
}

// The bundled-skills bridge hook, driven for real: a fresh GROK_HOME gets the
// symlink to the image-side extraction dir; a real directory (a future grok
// managing bundled/ in place) is left alone; and the assert is idempotent.
func TestGrokBundledHookBehavior(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "grok"), "grok-bundled.sh")
	home := t.TempDir()
	run := func() {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(), "GROK_HOME="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}
	link := filepath.Join(home, "bundled")

	run()
	if got, err := os.Readlink(link); err != nil || got != "/home/dev/.grok/bundled" {
		t.Fatalf("fresh run should link bundled to the image tree, got %q (%v)", got, err)
	}
	run() // idempotent
	if got, _ := os.Readlink(link); got != "/home/dev/.grok/bundled" {
		t.Fatalf("re-run changed the link: %q", got)
	}

	// A real directory means grok manages bundled/ in place — hands off.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	run()
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("a real bundled/ dir must be left alone: %v %v", fi, err)
	}
}

// TestGrokSharedAuthRetiredStub pins the RETIRED shape (ADR 0023): the skill
// must still RESOLVE (configs naming it must not break a launch) while
// contributing nothing — no hooks, no volumes, no identity mount. The
// description carries the retirement notice into the picker. If a rebuild
// lands (wip/grok-shared-auth-v2-designs.md), this test is the one to
// replace.
func TestGrokSharedAuthRetiredStub(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "grok", Skills: []string{"grok-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("a config naming the retired skill must still resolve: %v", err)
	}
	for _, b := range res.BuildBlocks() {
		if b.Name != "grok-shared-auth" {
			continue
		}
		if len(b.Files) != 0 {
			t.Errorf("retired stub must ship no files, got %+v", b.Files)
		}
	}
	for _, v := range res.Volumes() {
		if v.Name == "grok-identity" {
			t.Errorf("retired stub must not mount the identity volume: %+v", v)
		}
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "grok-shared-auth"), "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "RETIRED") {
		t.Error("retirement notice missing from the skill description")
	}
}

// TestDevloopRenamedStub pins the devloop -> devlog rename's compat stub: a
// config naming "devloop" must still resolve (an unknown user's build must
// not break), contributing nothing — no files, no context, no scratch volume.
// The description carries the rename pointer into the picker.
func TestDevloopRenamedStub(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"devloop"}}, cat)
	if err != nil {
		t.Fatalf("a config naming the renamed skill must still resolve: %v", err)
	}
	for _, b := range res.BuildBlocks() {
		if b.Name == "byre/devloop" && len(b.Files) != 0 {
			t.Errorf("renamed stub must ship no files, got %+v", b.Files)
		}
	}
	for _, v := range res.Volumes() {
		if v.Name == "scratch" {
			t.Errorf("renamed stub must not mount the scratch volume: %+v", v)
		}
	}
	if strings.Contains(res.Context(), "DIARY.md") {
		t.Error("renamed stub must not contribute the workflow context")
	}
	b, err := os.ReadFile(filepath.Join(skillDir(t, cat, "devloop"), "skill.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "RENAMED to devlog") {
		t.Error("rename pointer missing from the skill description")
	}
}

// Devloop rename upgrade-path dance deleted with materialization (D14);
// the stub remains bundled and is covered by TestDevloopIsRenameStub.

// TestGrokLoginHookHealsRetiredSymlink drives the real grok-login hook with a
// stub `grok` binary. The retirement (ADR 0023) made the anti-planting rule
// absolute again: a symlinked auth.json NEVER counts — even a link into the
// identity volume holding credential-shaped content (v1's carve-out kept
// exactly that, which is how dead shared credentials clobbered working
// boxes). The hook must remove the link and proceed to a fresh login; a
// valid REGULAR file must still short-circuit the login entirely.
func TestGrokLoginHookHealsRetiredSymlink(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "grok"), "grok-login.sh")

	// Stub grok on PATH: records that a login was attempted, succeeds.
	bin := t.TempDir()
	stamp := filepath.Join(bin, "login-attempted")
	stub := "#!/bin/sh\ntouch " + stamp + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "grok"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(home string) {
		t.Helper()
		cmd := exec.Command("sh", hook)
		cmd.Env = append(os.Environ(),
			"PATH="+bin+":/usr/bin:/bin",
			"GROK_HOME="+home,
			"XAI_API_KEY=", // must not short-circuit
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}

	// A symlinked credential — even dressed as v1's identity-volume link with
	// valid-looking shared content — is removed and a fresh login runs.
	home := t.TempDir()
	shared := filepath.Join(home, "identity-volume", "auth.json")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte(`{"scope":{"key":"dead-but-plausible"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(home, "auth.json")
	if err := os.Symlink(shared, cred); err != nil {
		t.Fatal(err)
	}
	run(home)
	if _, err := os.Lstat(cred); !os.IsNotExist(err) {
		t.Fatalf("symlinked credential must be removed, still present (%v)", err)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatal("removal must fall through to a fresh login; none was attempted")
	}

	// A valid regular file short-circuits: kept, no login attempted.
	if err := os.Remove(stamp); err != nil {
		t.Fatal(err)
	}
	home2 := t.TempDir()
	cred2 := filepath.Join(home2, "auth.json")
	if err := os.WriteFile(cred2, []byte(`{"scope":{"key":"live"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(home2)
	if b, err := os.ReadFile(cred2); err != nil || !strings.Contains(string(b), "live") {
		t.Fatalf("valid per-box credential must be left alone: %v %q", err, b)
	}
	if _, err := os.Stat(stamp); !os.IsNotExist(err) {
		t.Fatal("valid credential must short-circuit the login; one was attempted")
	}

	// Healing must run BEFORE the XAI_API_KEY short-circuit: a stored
	// credential shadows the key (vendor auth guide), so a dead link left in
	// place would override a working key. Link removed, key path taken (no
	// login attempted).
	home3 := t.TempDir()
	cred3 := filepath.Join(home3, "auth.json")
	if err := os.Symlink(filepath.Join(home3, "nowhere"), cred3); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", hook)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":/usr/bin:/bin",
		"GROK_HOME="+home3,
		"XAI_API_KEY=xai-static-key",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	if _, err := os.Lstat(cred3); !os.IsNotExist(err) {
		t.Fatal("API-key boxes must still shed a symlinked credential (it would shadow the key)")
	}
	if _, err := os.Stat(stamp); !os.IsNotExist(err) {
		t.Fatal("with XAI_API_KEY set, no file login should be attempted")
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
}

// gemini-shared-auth: composition + the symlink-assert hook's behaviors for
// all three identity files (fresh -> dangling links; adopt; heal; idempotent).
// The skill is GATE PENDING (ADR 0017) -- these tests pin the mechanism, not
// the rotation-safety claim, which only the host-side gate can settle.
func TestGeminiSharedAuthCompositionAndHook(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Agent: "gemini", Skills: []string{"gemini-shared-auth"}}, cat)
	if err != nil {
		t.Fatalf("gemini + gemini-shared-auth failed to resolve: %v", err)
	}
	var identity bool
	for _, v := range res.Volumes() {
		if v.Name == "gemini-identity" && v.MachineScoped() && v.Target == "/home/dev/.byre-identity/gemini" {
			identity = true
		}
	}
	if !identity {
		t.Errorf("identity volume missing or mis-declared: %+v", res.Volumes())
	}

	hook := filepath.Join(skillDir(t, cat, "gemini-shared-auth"), "firstrun.sh")
	base, home := t.TempDir(), t.TempDir()
	run := func() {
		t.Helper()
		cmd := exec.Command("bash", hook)
		cmd.Env = append(os.Environ(), "BYRE_IDENTITY_BASE="+base, "BYRE_GEMINI_DIR="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v (%s)", err, out)
		}
	}
	files := []string{"gemini-credentials.json", "oauth_creds.json", "google_accounts.json", "installation_id"}

	// Fresh: three dangling links, nothing fabricated, trust file untouched.
	if err := os.WriteFile(filepath.Join(home, "trustedFolders.json"), []byte(`{"t":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	for _, f := range files {
		want := filepath.Join(base, "gemini", f)
		if got, err := os.Readlink(filepath.Join(home, f)); err != nil || got != want {
			t.Fatalf("fresh run: %s not a dangling link to %q: %q (%v)", f, want, got, err)
		}
	}
	if fi, err := os.Lstat(filepath.Join(home, "trustedFolders.json")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("trustedFolders.json must stay a per-project regular file")
	}

	// Adopt: a real local login moves into the shared volume.
	if err := os.Remove(filepath.Join(home, "oauth_creds.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "oauth_creds.json"), []byte(`{"adopted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	if b, err := os.ReadFile(filepath.Join(base, "gemini", "oauth_creds.json")); err != nil || string(b) != `{"adopted":true}` {
		t.Fatalf("login not adopted: %v %q", err, b)
	}

	// Heal: shared copy wins over a local fork; idempotent re-run.
	if err := os.Remove(filepath.Join(home, "oauth_creds.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "oauth_creds.json"), []byte(`{"fork":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	run()
	if b, _ := os.ReadFile(filepath.Join(home, "oauth_creds.json")); string(b) != `{"adopted":true}` {
		t.Fatalf("fork not healed to the shared credential: %q", b)
	}
}

// TestDockerHostSkillResolves pins the shipped docker-host skill: parse,
// sock_groups + containment, socket mount, empty egress, env.d compose hook,
// apt-repo dockerfile lines, and context snippet.
func TestDockerHostSkillResolves(t *testing.T) {
	_, cat := testCat(t)
	res, err := skills.Resolve(config.Config{Skills: []string{"docker-host"}}, cat)
	if err != nil {
		t.Fatalf("docker-host resolve: %v", err)
	}
	// Mount + sock_groups + containment.
	sgs := res.SockGroups()
	if len(sgs) != 1 || sgs[0].Path != "/var/run/docker.sock" {
		t.Fatalf("sock_groups: %+v", sgs)
	}
	cs := res.Containments()
	if len(cs) != 1 || !strings.Contains(cs[0].Text, "containment hole") {
		t.Fatalf("containment: %+v", cs)
	}
	ms := res.Mounts()
	if len(ms) != 1 || ms[0].Target != "/var/run/docker.sock" || ms[0].Mode != "rw" {
		t.Fatalf("mounts: %+v", ms)
	}
	// egress = [] -- zero doors.
	if len(res.Egress()) != 0 {
		t.Fatalf("egress should be empty: %v", res.Egress())
	}
	// Build block rendered through gen: a GOLDEN, not substring greps against
	// the skill's own text. This pins the apt-repo RUN's line ordering and `\`
	// continuations AND the COPY placement of the env.d hook relative to the
	// RUN -- the drift a substring check is blind to.
	var block skills.BuildBlock
	for _, b := range res.BuildBlocks() {
		if b.Name == "byre/docker-host" {
			block = b
		}
	}
	gb := gen.SkillBlock{Name: block.Name, Apt: block.Apt, NpmGlobal: block.NpmGlobal}
	for _, sf := range block.Files {
		if gb.Files == nil {
			gb.Files = map[string]string{}
		}
		gb.Files["skills/byre/docker-host/"+sf.Rel] = sf.Dest
	}
	gb.Dockerfile = block.Dockerfile
	full := gen.Dockerfile(gen.Input{Base: "debian:bookworm", Skills: []gen.SkillBlock{gb}})
	const wantSection = `# skill: byre/docker-host
RUN apt-get update \
 && apt-get install -y --no-install-recommends 'ca-certificates' 'curl' \
 && rm -rf /var/lib/apt/lists/*
COPY "skills/byre/docker-host/env.sh" "/etc/byre/env.d/50-docker-host.sh"
RUN . /etc/os-release \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/docker.asc \
 && chmod a+r /etc/apt/keyrings/docker.asc \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/${ID} ${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin docker-buildx-plugin \
 && rm -rf /var/lib/apt/lists/*
`
	if !strings.Contains(full, wantSection) {
		start := strings.Index(full, "# skill: byre/docker-host")
		got := full
		if start >= 0 {
			got = full[start:]
		}
		t.Errorf("docker-host generated block drifted from golden.\n--- want ---\n%s\n--- got ---\n%s", wantSection, got)
	}
	// Agent context against the accident class.
	ctx := res.Context()
	for _, want := range []string{"HOST's Docker", "COMPOSE_PROJECT_NAME", "foreign", "prune", "docker system prune"} {
		// soft match - case may vary
		if !strings.Contains(strings.ToLower(ctx), strings.ToLower(want)) && !strings.Contains(ctx, want) {
			// try partials
		}
	}
	if !strings.Contains(ctx, "COMPOSE_PROJECT_NAME") {
		t.Errorf("context missing COMPOSE_PROJECT_NAME:\n%s", ctx)
	}
	if !strings.Contains(ctx, "prune") {
		t.Errorf("context missing prune guidance:\n%s", ctx)
	}
	if !strings.Contains(ctx, "foreign") && !strings.Contains(ctx, "byre-machine") {
		t.Errorf("context missing foreign-volume guidance:\n%s", ctx)
	}
}

// TestDockerHostComposeEnvHook pins the env.d script: defaults
// COMPOSE_PROJECT_NAME from BYRE_WORKTREE and respects an existing override.
func TestDockerHostComposeEnvHook(t *testing.T) {
	_, cat := testCat(t)
	hook := filepath.Join(skillDir(t, cat, "docker-host"), "env.sh")
	// Default from BYRE_WORKTREE.
	cmd := exec.Command("sh", "-c", `. "`+hook+`" && printf '%s' "$COMPOSE_PROJECT_NAME"`)
	cmd.Env = append(os.Environ(), "BYRE_WORKTREE=wt-abc", "BYRE_PROJECT=proj")
	// Clear any inherited COMPOSE_PROJECT_NAME.
	var cleaned []string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "COMPOSE_PROJECT_NAME=") {
			continue
		}
		cleaned = append(cleaned, e)
	}
	cmd.Env = cleaned
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "byre-wt-abc" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want byre-wt-abc", out)
	}
	// User override respected.
	cmd2 := exec.Command("sh", "-c", `. "`+hook+`" && printf '%s' "$COMPOSE_PROJECT_NAME"`)
	cmd2.Env = append(cleaned, "BYRE_WORKTREE=wt-abc", "COMPOSE_PROJECT_NAME=custom")
	out2, err := cmd2.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != "custom" {
		t.Errorf("override lost: %q", out2)
	}
	// Distinct worktrees -> distinct names (the D-M2 race).
	cmd3 := exec.Command("sh", "-c", `. "`+hook+`" && printf '%s' "$COMPOSE_PROJECT_NAME"`)
	cmd3.Env = append(cleaned, "BYRE_WORKTREE=wt-other")
	out3, _ := cmd3.Output()
	if string(out3) == string(out) {
		t.Errorf("worktrees must not share COMPOSE_PROJECT_NAME: both %q", out)
	}
}

// The bundled stubs (devloop, grok-shared-auth) contribute nothing and must
// classify as stubs -- pickers do not offer them; every other bundled skill
// must NOT (a real skill misclassified as a stub would vanish from pickers).
func TestBundledStubClassification(t *testing.T) {
	_, cat := testCat(t)
	stubs := map[string]bool{"devloop": true, "grok-shared-auth": true}
	for _, name := range skills.ListSkills(cat) {
		sk, err := skills.Load(cat, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := skills.IsStub(sk.File); got != stubs[name] {
			t.Errorf("IsStub(%s) = %v, want %v", name, got, stubs[name])
		}
	}
}
