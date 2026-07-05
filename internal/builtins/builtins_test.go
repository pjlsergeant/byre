package builtins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/build"
	"byre/internal/config"
	"byre/internal/gen"
	"byre/internal/project"
	"byre/internal/skills"
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
	for _, agent := range []string{"claude", "codex", "gemini"} {
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
	if len(agents) != 3 {
		t.Errorf("expected 3 agent skills (claude/codex/gemini), got %v", agents)
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

// codex installs its BINARY into ~/.codex, so it must NOT wipe that dir (doing so
// deletes the binary and leaves dangling symlinks).
func TestCodexDoesNotWipeStateDir(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "codex"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range res.BuildBlocks() {
		if b.Name != "codex" {
			continue
		}
		for _, line := range b.Dockerfile {
			if strings.Contains(line, "rm -rf /home/dev/.codex") {
				t.Fatalf("codex must NOT wipe ~/.codex (its binary lives there): %q", line)
			}
		}
	}
}

// codex's state volume + CODEX_HOME must be a DIFFERENT path from ~/.codex, where
// the installer puts the binary — otherwise the volume masks/seeds-over the
// binary (the bug). Guards the decoupling.
func TestCodexStateVolumeSeparateFromBinary(t *testing.T) {
	dest := t.TempDir()
	if err := MaterializeSkills(dest); err != nil {
		t.Fatal(err)
	}
	res, err := skills.Resolve(config.Config{Agent: "codex"}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Env()["CODEX_HOME"] == "" || res.Env()["CODEX_HOME"] == "/home/dev/.codex" {
		t.Fatalf("CODEX_HOME must be set and NOT /home/dev/.codex (the binary dir), got %q", res.Env()["CODEX_HOME"])
	}
	var found bool
	for _, v := range res.Volumes() {
		if v.Name == ".codex" {
			found = true
			if v.Target == "/home/dev/.codex" {
				t.Errorf(".codex state volume must NOT mount at /home/dev/.codex (the binary dir)")
			}
			if v.Target != res.Env()["CODEX_HOME"] {
				t.Errorf(".codex volume target %q should equal CODEX_HOME %q", v.Target, res.Env()["CODEX_HOME"])
			}
		}
	}
	if !found {
		t.Fatal("codex skill should contribute a .codex state volume")
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
