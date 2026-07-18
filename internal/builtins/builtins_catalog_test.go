package builtins

import (
	"io/fs"
	"os"
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
	for _, agent := range []string{"claude", "codex", "gemini", "grok", "opencode"} {
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
	want := []string{"claude", "codex", "gemini", "grok", "opencode"}
	got := map[string]bool{}
	for _, a := range agents {
		got[a] = true
	}
	if len(agents) != len(want) {
		t.Errorf("expected %d agent skills %v, got %v", len(want), want, agents)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("agent skill %q missing from ListAgentSkills: %v", w, agents)
		}
	}
}

// TestSelfHostCompositionResolves verifies the BUNDLED slice of byre's own
// self-hosting config (Claude agent + codex + grok). codereview and devlog
// moved out of the binary (2026-07-13, ADR 0029) -- their content is pinned
// by the pjlsergeant-byre-skills repo and the host-side dogfood, not this
// suite; here we pin that their RETIRED bare names fail with the exact
// install remedy.
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

// TestRetiredNamesTombstone pins the retired-name cut-over (ADR 0029): the
// bare names byre used to bundle fail with the EXACT pinned install command
// (URI and digest, not just their shapes), and cannot be reclaimed by a
// local package.
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
		t.Error("retired names must stay protected")
	}
}

// TestByreConfigSourcesAgreeWithTombstones is the drift lock between the four
// hand-duplicated URI/digest pairs: this repo's own byre.config [sources]
// hints must name exactly the URIs and digests the retired-name tombstones
// print -- disagreement means a release updated one copy and not the other.
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
		{"opencode", "opencode.ai/install", "rm -rf /home/dev/.local/share/opencode"},
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

// codex, grok, and opencode install their BINARIES into their dotdir
// (~/.codex, ~/.grok, ~/.opencode), so they must NOT wipe it (doing so
// deletes the binary and leaves dangling symlinks).
func TestBinaryDirAgentsDoNotWipeIt(t *testing.T) {
	_, cat := testCat(t)
	for _, c := range []struct{ agent, binDir string }{
		{"codex", "/home/dev/.codex"},
		{"grok", "/home/dev/.grok"},
		{"opencode", "/home/dev/.opencode"},
	} {
		res, err := skills.Resolve(config.Config{Agent: c.agent}, cat)
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		var found bool
		for _, b := range res.BuildBlocks() {
			if b.Name != "byre/"+c.agent && b.Name != c.agent {
				continue
			}
			found = true
			for _, line := range b.Dockerfile {
				if strings.Contains(line, "rm -rf "+c.binDir) {
					t.Errorf("%s must NOT wipe %s (its binary lives there): %q", c.agent, c.binDir, line)
				}
			}
		}
		if !found {
			t.Fatalf("%s: no build block named byre/%s or %s — the negative assertion above checked nothing", c.agent, c.agent, c.agent)
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

// Devloop rename upgrade-path dance deleted with materialization (ADR 0029);
// the stub remains bundled and is covered by TestDevloopRenamedStub.

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

// The bundled stubs (devloop, grok-shared-auth) contribute nothing and must
// classify as stubs -- pickers do not offer them; every other bundled skill
// must NOT (a real skill misclassified as a stub would vanish from pickers).
func TestBundledStubClassification(t *testing.T) {
	_, cat := testCat(t)
	stubs := map[string]bool{"devloop": true}
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
