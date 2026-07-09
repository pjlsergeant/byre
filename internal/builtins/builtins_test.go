package builtins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/build"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

func TestMaterializeWritesClaudeSkill(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(dest, "claude", "skill.toml")
	b, err := os.ReadFile(toml)
	if err != nil {
		t.Fatalf("claude skill not materialized: %v", err)
	}
	if !strings.Contains(string(b), "[agent]") || !strings.Contains(string(b), "claude") {
		t.Errorf("claude skill.toml content unexpected:\n%s", b)
	}
}

// TestBuiltinAgentSkillsResolve verifies the shipped agent skills parse and
// resolve as agents (catches TOML/structure errors without a Docker build —
// codex/gemini are still drafts pending host verification of install/auth).
func TestBuiltinAgentSkillsResolve(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex", "gemini", "grok"} {
		res, err := skills.Resolve(config.Config{Agent: agent}, dest)
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

func TestMaterializeTemplatesAndListAgents(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeTemplates(filepath.Join(dest, "templates")); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"go", "node", "python"} {
		if _, err := os.Stat(filepath.Join(dest, "templates", n, "template.config")); err != nil {
			t.Errorf("template %q not materialized: %v", n, err)
		}
	}
	if err := MaterializeSkills(filepath.Join(dest, "skills")); err != nil {
		t.Fatal(err)
	}
	agents := skills.ListAgentSkills(filepath.Join(dest, "skills"))
	if len(agents) != 4 {
		t.Errorf("expected 4 agent skills (claude/codex/gemini/grok), got %v", agents)
	}
}

func TestMaterializeDoesNotClobber(t *testing.T) {
	dest := t.TempDir()
	// Pre-create a user-edited claude skill.
	claudeDir := filepath.Join(dest, "claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(claudeDir, "skill.toml")
	if err := os.WriteFile(custom, []byte("# my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(custom)
	if string(b) != "# my edit\n" {
		t.Errorf("Materialize clobbered an existing skill: %q", b)
	}
}

// TestSelfHostCompositionResolves verifies byre's own self-hosting config
// (Claude agent + codex + devloop) resolves end-to-end: devloop ships the
// byre-codereview script, the workflow context reaches the agent's memory file,
// and codex's reviewer apt dep is present.
func TestSelfHostCompositionResolves(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"codex", "devloop"}}, dest)
	if err != nil {
		t.Fatalf("self-host composition failed to resolve: %v", err)
	}
	// devloop ships byre-codereview and its firstrun hook; codex ships its
	// first-run login hook into the launcher's firstrun.d.
	shipped := map[string]bool{} // "skill dest" -> present
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			shipped[b.Name+" "+sf.Dest] = true
		}
	}
	for _, want := range []string{
		"devloop /usr/local/bin/byre-codereview",
		"devloop /etc/byre/firstrun.d/devloop",
		"devloop /usr/local/lib/byre-devloop-lib.sh", // shared hardening lib both scripts source
		"codex /etc/byre/firstrun.d/codex-login",
	} {
		if !shipped[want] {
			t.Errorf("missing shipped file %q; shipped: %v", want, shipped)
		}
	}
	// devloop contributes the persistent scratch volume and advertises it.
	var scratchVol bool
	for _, v := range res.Volumes() {
		if v.Name == "scratch" && v.Role == "state" && v.Target == "/home/dev/scratch" {
			scratchVol = true
		}
	}
	if !scratchVol {
		t.Errorf("devloop did not contribute the scratch state volume: %+v", res.Volumes())
	}
	if got := res.Env()["BYRE_SCRATCH"]; got != "/home/dev/scratch" {
		t.Errorf("BYRE_SCRATCH = %q, want /home/dev/scratch", got)
	}
	// Workflow context reaches Claude's memory file.
	if res.AgentContextTarget() != "/home/dev/.claude/CLAUDE.md" {
		t.Errorf("context target wrong: %q", res.AgentContextTarget())
	}
	if !strings.Contains(res.Context(), "byre-codereview") {
		t.Errorf("devloop workflow context not present in agent context")
	}
	// codex contributes the reviewer binary install (its build block is present).
	var codexBlock bool
	for _, b := range res.BuildBlocks() {
		if b.Name == "codex" {
			codexBlock = true
		}
	}
	if !codexBlock {
		t.Errorf("codex skill block missing from composition")
	}
}

// TestDevloopBuildStagesAndOrders assembles a real build context for the
// devloop composition and checks that its shipped files are staged and the
// generated Dockerfile COPYs byre-codereview before the chmod that uses it.
func TestDevloopBuildStagesAndOrders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	paths, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(paths.Home, "skills")
	if err := MaterializeSkills(skillsDir); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Base: "golang:1.22-bookworm", Agent: "claude", Skills: []string{"codex", "devloop"}}
	res, err := skills.Resolve(cfg, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	df, err := build.Assemble(paths, cfg, res)
	if err != nil {
		t.Fatal(err)
	}
	// The script is staged into the build context.
	if _, err := os.Stat(filepath.Join(paths.ContextDir, "skills", "devloop", "codereview.sh")); err != nil {
		t.Fatalf("codereview.sh not staged: %v", err)
	}
	// COPY of byre-codereview must precede the chmod that makes it executable.
	cp := strings.Index(df, gen.CopyLine("skills/devloop/codereview.sh", "/usr/local/bin/byre-codereview"))
	chmod := strings.Index(df, "chmod +x /usr/local/bin/byre-codereview")
	if cp < 0 || chmod < 0 || cp > chmod {
		t.Fatalf("COPY must precede chmod (copy=%d chmod=%d):\n%s", cp, chmod, df)
	}
	// codex's first-run login hook is staged and COPYd to firstrun.d.
	if _, err := os.Stat(filepath.Join(paths.ContextDir, "skills", "codex", "codex-login.sh")); err != nil {
		t.Fatalf("codex-login.sh not staged: %v", err)
	}
	if !strings.Contains(df, gen.CopyLine("skills/codex/codex-login.sh", "/etc/byre/firstrun.d/codex-login")) {
		t.Errorf("codex login hook COPY missing:\n%s", df)
	}
}

func TestUpdateSkillsOverwritesAndBacksUp(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	// User-edit the codex skill.
	codexToml := filepath.Join(dest, "codex", "skill.toml")
	if err := os.WriteFile(codexToml, []byte("# my local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateSkills(dest)
	if err != nil {
		t.Fatal(err)
	}
	// codex should be updated (it differs); the edited copy backed up.
	var codexBak string
	for _, u := range updated {
		if u.Name == "codex" {
			codexBak = u.Backup
		}
	}
	if codexBak == "" {
		t.Fatalf("codex should have been updated with a backup, got %+v", updated)
	}
	// The reported backup actually holds the edited content.
	if b, _ := os.ReadFile(filepath.Join(codexBak, "skill.toml")); string(b) != "# my local edit\n" {
		t.Errorf("reported backup %s does not contain the edit: %q", codexBak, b)
	}
	if b, _ := os.ReadFile(codexToml); string(b) == "# my local edit\n" {
		t.Errorf("codex skill.toml was not overwritten with the shipped version")
	}
	// The edit was preserved in an append-only backup slot (skills.bak/codex.*).
	if n := countBackups(t, dest, "codex"); n != 1 {
		t.Errorf("want 1 codex backup, got %d", n)
	}
}

// countBackups counts skills.bak/<name>.* backup dirs.
func countBackups(t *testing.T, dest, name string) int {
	t.Helper()
	entries, err := os.ReadDir(dest + ".bak")
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), name+".") {
			n++
		}
	}
	return n
}

// Each update of a differing copy keeps its OWN backup — backups are append-only
// and never deleted, so distinct successive edits are all recoverable.
func TestUpdateSkillsBackupsAppendOnly(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	codexToml := filepath.Join(dest, "codex", "skill.toml")
	edits := []string{"# edit one\n", "# edit two\n"}
	for i, edit := range edits {
		if err := os.WriteFile(codexToml, []byte(edit), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := UpdateSkills(dest); err != nil {
			t.Fatal(err)
		}
		if n := countBackups(t, dest, "codex"); n != i+1 {
			t.Fatalf("after %d edits, want %d backups, got %d", i+1, i+1, n)
		}
	}
	// Both distinct edits are recoverable from the (separate) backups.
	entries, _ := os.ReadDir(dest + ".bak")
	found := map[string]bool{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "codex.") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dest+".bak", e.Name(), "skill.toml"))
		found[string(b)] = true
	}
	for _, edit := range edits {
		if !found[edit] {
			t.Errorf("edit %q was not preserved in any backup; have %v", edit, found)
		}
	}
}

func TestUpdateSkillsIdempotent(t *testing.T) {
	dest := t.TempDir()
	if _, err := UpdateSkills(dest); err != nil { // fresh install
		t.Fatal(err)
	}
	updated, err := UpdateSkills(dest) // second run: nothing changed
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 0 {
		t.Errorf("a second update with no changes should report nothing, got %v", updated)
	}
}

// claude/gemini install their binaries OUTSIDE their state dir, so they wipe it
// after install (a fresh state volume then starts clean). Each wipe must come
// after the installer that created the residue.
func TestAgentSkillsCleanStateDir(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ agent, install, clean string }{
		{"claude", "install.sh", "rm -rf /home/dev/.claude"},
		{"gemini", "npm install -g", "rm -rf /home/dev/.gemini"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, dest)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		installAt, cleanAt := -1, -1
		for _, b := range res.BuildBlocks() {
			if b.Name != c.agent {
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
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ agent, binDir string }{
		{"codex", "/home/dev/.codex"},
		{"grok", "/home/dev/.grok"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, dest)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		for _, b := range res.BuildBlocks() {
			if b.Name != c.agent {
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
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ agent, envKey, binDir, volName string }{
		{"codex", "CODEX_HOME", "/home/dev/.codex", ".codex"},
		{"grok", "GROK_HOME", "/home/dev/.grok", ".grok"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, dest)
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
	dest := t.TempDir()
	if err := MaterializeTemplates(dest); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseFile(filepath.Join(dest, "node", "template.config"))
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
func TestUpdateTemplatesOverwritesAndBacksUp(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeTemplates(dest); err != nil {
		t.Fatal(err)
	}
	goTmpl := filepath.Join(dest, "go", "template.config")
	if err := os.WriteFile(goTmpl, []byte("# my local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := UpdateTemplates(dest)
	if err != nil {
		t.Fatal(err)
	}
	var goChange *Change
	for i := range changes {
		if changes[i].Name == "go" {
			goChange = &changes[i]
		}
	}
	if goChange == nil {
		t.Fatalf("edited go template not updated: %+v", changes)
	}
	// The shipped content is restored...
	b, _ := os.ReadFile(goTmpl)
	if string(b) == "# my local edit\n" {
		t.Error("update did not overwrite the edited template")
	}
	// ...and the prior copy is preserved where the change says.
	if goChange.Backup == "" {
		t.Fatal("a differing copy must be backed up")
	}
	prior, err := os.ReadFile(filepath.Join(goChange.Backup, "template.config"))
	if err != nil || string(prior) != "# my local edit\n" {
		t.Errorf("prior copy not preserved at %s: %q err=%v", goChange.Backup, prior, err)
	}
	// A second update is a no-op.
	again, err := UpdateTemplates(dest)
	if err != nil || len(again) != 0 {
		t.Errorf("second update should change nothing: %+v err=%v", again, err)
	}
}

// TestFirewallSkillResolves pins the firewall skill's contract: it declares
// the posture and the netns hook (both consumed by core), stays composable
// with an agent skill, and grants NOTHING to the box itself — no caps, no
// run_args, no mounts. The box's only firewall-related content is inert
// tooling; privileges live solely in the netns-init helper byre runs outside.
func TestFirewallSkillResolves(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall"}}, dest)
	if err != nil {
		t.Fatalf("firewall + claude must resolve together: %v", err)
	}
	posture, by := res.NetworkPosture()
	if posture != "deny-by-default" || by != "firewall" {
		t.Errorf("posture = %q by %q", posture, by)
	}
	hooks := res.NetnsInits()
	if len(hooks) != 1 || hooks[0].Path != "/usr/local/bin/byre-firewall" {
		t.Errorf("netns hooks = %+v", hooks)
	}
	for _, sk := range res.Skills {
		if sk.Name != "firewall" {
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
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"firewall"}}, dest)
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
	fw, err := skills.Load(dest, "firewall")
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
	fwOnly, err := skills.Resolve(config.Config{Skills: []string{"firewall"}}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(fwOnly.Egress(), " "), "anthropic") {
		t.Errorf("firewall base must not hardcode agent endpoints; got: %v", fwOnly.Egress())
	}
	// Attribution: anthropic is credited to the claude skill, not the firewall.
	for _, a := range res.EgressAllows() {
		if strings.Contains(a.Host, "anthropic") && a.Skill != "claude" {
			t.Errorf("anthropic egress attributed to %q, want claude", a.Skill)
		}
	}
}

// TestSharedAuthCompositionResolves pins the claude-shared-auth companion
// composing with the claude agent skill (ADR 0017): the machine-scoped
// identity volume, both hooks landing in the launcher's hook dirs (00- prefix
// so the firstrun hook sorts before agent-skill hooks), and the expiry brief
// reaching the agent's context.
func TestSharedAuthCompositionResolves(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "claude", Skills: []string{"claude-shared-auth"}}, dest)
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
		"claude-shared-auth /etc/byre/firstrun.d/00-claude-shared-auth",
		"claude-shared-auth /etc/byre/env.d/50-claude-shared-auth.sh",
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
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "codex", Skills: []string{"codex-shared-auth"}}, dest)
	if err != nil {
		t.Fatalf("codex + codex-shared-auth failed to resolve: %v", err)
	}
	var hook bool
	for _, b := range res.BuildBlocks() {
		for _, sf := range b.Files {
			if b.Name == "codex-shared-auth" && sf.Dest == "/etc/byre/firstrun.d/00-codex-shared-auth" {
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
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dest, "codex-shared-auth", "firstrun.sh")
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

// The claude-shared-auth hook seeds onboarding-complete state on a FRESH
// config dir when the shared token exists (interactive Claude's wizard gates
// on .claude.json, not the env token -- host-verified 2026-07-07), and never
// touches an existing .claude.json.
func TestClaudeSharedAuthHookSeedsOnboarding(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dest, "claude-shared-auth", "firstrun.sh")
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

// The claude-shared-auth env hook is SOURCED by the launcher (it must never
// exit) and exports the shared token stripped of whitespace. When a leftover
// per-project login sits alongside the token it warns on stderr: interactive
// Claude prefers the stored credential and stops refreshing it, so such a box
// 401s ~8h after that login (host-verified 2026-07-07). The file is Claude's,
// so the hook only moves it aside with the user's yes: an interactive launch
// offers the move (default Y), a non-interactive one warns and leaves it.
func TestClaudeSharedAuthEnvHookExportsAndWarns(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dest, "claude-shared-auth", "env.sh")
	// Source the hook the way the launcher does, then record what it exported.
	// A clean env (no inherited CLAUDE_CODE_OAUTH_TOKEN) keeps the no-token
	// cases honest when the test itself runs inside a token-authed box.
	// stdin == nil sources the hook the non-interactive way the test always
	// has; a non-nil stdin also sets the BYRE_ASSUME_TTY test seam so the
	// offer path runs and reads the scripted answer.
	runWith := func(base, cfg string, stdin *string) (token, output string) {
		t.Helper()
		tokenOut := filepath.Join(t.TempDir(), "token.out")
		cmd := exec.Command("bash", "-c", `. "$0"; printf '%s' "$CLAUDE_CODE_OAUTH_TOKEN" >"$1"`, hook, tokenOut)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "BYRE_IDENTITY_BASE="+base, "CLAUDE_CONFIG_DIR="+cfg)
		if stdin != nil {
			cmd.Env = append(cmd.Env, "BYRE_ASSUME_TTY=1")
			cmd.Stdin = strings.NewReader(*stdin)
		}
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
	run := func(base, cfg string) (token, output string) {
		t.Helper()
		return runWith(base, cfg, nil)
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

	// Token with trailing newline, no leftover login -> exported stripped, silent.
	base, cfg := seed("sk-ant-oat01-x\n")
	if tok, out := run(base, cfg); tok != "sk-ant-oat01-x" || out != "" {
		t.Fatalf("clean export broken: token=%q output=%q", tok, out)
	}

	// Leftover .credentials.json alongside the token, no TTY -> still
	// exported, warns, and the file stays put (no user to say yes).
	creds := filepath.Join(cfg, ".credentials.json")
	if err := os.WriteFile(creds, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, out := run(base, cfg)
	if tok != "sk-ant-oat01-x" {
		t.Fatalf("leftover login must not block the export: token=%q", tok)
	}
	if !strings.Contains(out, "401") || !strings.Contains(out, ".credentials.json") {
		t.Fatalf("warning missing or unactionable: %q", out)
	}
	if _, err := os.Stat(creds); err != nil {
		t.Fatalf("non-interactive launch must not touch the login: %v", err)
	}

	// Interactive decline ("n") -> file stays put, told how to fix by hand.
	answer := "n\n"
	if _, out := runWith(base, cfg, &answer); !strings.Contains(out, "left in place") {
		t.Fatalf("declined offer should say the file was left: %q", out)
	}
	if _, err := os.Stat(creds); err != nil {
		t.Fatalf("declining the offer must leave the login: %v", err)
	}

	// Interactive accept (bare Enter = default Y) -> moved to .bak, exported.
	answer = "\n"
	tok, out = runWith(base, cfg, &answer)
	if tok != "sk-ant-oat01-x" || !strings.Contains(out, "moved") {
		t.Fatalf("accepted offer broken: token=%q output=%q", tok, out)
	}
	if _, err := os.Stat(creds); !os.IsNotExist(err) {
		t.Fatal("accepted offer must move the login aside")
	}
	if _, err := os.Stat(creds + ".bak"); err != nil {
		t.Fatalf("moved login must land at .bak: %v", err)
	}

	// No token file -> nothing exported, no warning even with a leftover login.
	base2, cfg2 := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg2, ".credentials.json"), []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, out := run(base2, cfg2); tok != "" || out != "" {
		t.Fatalf("no-token launch must stay silent: token=%q output=%q", tok, out)
	}

	// Whitespace-only token file -> treated as absent: no export, no warning.
	base3, cfg3 := seed(" \n")
	if err := os.WriteFile(filepath.Join(cfg3, ".credentials.json"), []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, out := run(base3, cfg3); tok != "" || out != "" {
		t.Fatalf("whitespace token must be treated as absent: token=%q output=%q", tok, out)
	}
}

// gemini-shared-auth: composition + the symlink-assert hook's behaviors for
// all three identity files (fresh -> dangling links; adopt; heal; idempotent).
// The skill is GATE PENDING (ADR 0017) -- these tests pin the mechanism, not
// the rotation-safety claim, which only the host-side gate can settle.
func TestGeminiSharedAuthCompositionAndHook(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "gemini", Skills: []string{"gemini-shared-auth"}}, dest)
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

	hook := filepath.Join(dest, "gemini-shared-auth", "firstrun.sh")
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
