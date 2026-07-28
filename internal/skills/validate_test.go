package skills

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// The intra-skill value rules must fire at the BYTES boundary -- the one
// install (Acquire's stage 2) and catalog ingest share -- and not wait for the
// develop that finally resolves the skill. Each case names the rule that
// fired: a dozen rules can reject one manifest, and the wrong one keeps a
// rejection test green.
func TestIntraSkillValueRulesRefuseAtTheBytesBoundary(t *testing.T) {
	const head = "[package]\nid = \"acme/x\"\nkind = \"skill\"\n\n"
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{
			"network_posture spelling",
			"[runtime]\nnetwork_posture = \"Deny-Default\"\n",
			[]string{"network_posture", "Deny-Default", "must match"},
		},
		{
			"netns_init relative",
			"[runtime]\nnetns_init = \"bin/firewall\"\n",
			[]string{"netns_init", "bin/firewall", "absolute"},
		},
		{
			"egress grammar",
			"[runtime]\negress = [\"github.com:not-a-port\"]\n",
			[]string{"not-a-port"},
		},
		{
			"egress_offered grammar",
			"[runtime]\negress_offered = [\"github.com:not-a-port\"]\n",
			[]string{"not-a-port"},
		},
		{
			"mcp declared twice",
			"[[mcp]]\nname = \"gh\"\ncommand = [\"x\"]\n\n[[mcp]]\nname = \"gh\"\ncommand = [\"y\"]\n",
			[]string{"mcp", "gh", "declared twice"},
		},
		{
			"claude skill source escapes",
			"[[claude_skills]]\nname = \"cs\"\nfrom = \"../../etc\"\n",
			[]string{"claude skill", "cs", "must be a relative path within the skill dir"},
		},
		{
			"sock_groups without a mount",
			"[runtime]\nsock_groups = [\"/var/run/docker.sock\"]\n",
			[]string{"sock_groups", "/var/run/docker.sock", "must match an active mount target"},
		},
		{
			"containment shape",
			"[runtime]\ncontainment = \"line one\\nline two\"\n",
			[]string{"containment", "single line"},
		},
		{
			"env_docs empty guidance",
			"[runtime]\n[runtime.env_docs]\nTOKEN = \"\"\n",
			[]string{"env_docs", "TOKEN", "must not be empty"},
		},
		{
			"build file destination relative",
			"[build.files]\n\"a.sh\" = \"relative/a\"\n",
			[]string{"file destination", "relative/a", "absolute"},
		},
		{
			"build file source escapes",
			"[build.files]\n\"../../etc/passwd\" = \"/tmp/a\"\n",
			[]string{"build file", "escapes the skill dir"},
		},
		{
			"two build files for one destination",
			"[build.files]\n\"a.sh\" = \"/usr/local/bin/tool\"\n\"b.sh\" = \"/usr/local/bin/tool\"\n",
			[]string{"both install to", "a.sh", "b.sh", "/usr/local/bin/tool"},
		},
		{
			"agent adapter outside the closed set",
			"[agent]\ncommand = \"x\"\nmcp = \"vouch\"\n",
			[]string{"[agent] mcp", "vouch", "inject"},
		},
		{
			"agent state names no contributed volume",
			"[agent]\ncommand = \"x\"\nstate = \".acme\"\n",
			[]string{"[agent].state", ".acme", "not a state volume"},
		},
		{
			"agent prefs without a state volume",
			"[agent]\ncommand = \"x\"\n[agent.prefs]\nfrom = \"~/.acme\"\nfiles = [\"theme.json\"]\n",
			[]string{"[agent.prefs]", "requires [agent].state"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePrimaryBytes([]byte(head + tc.body))
			if err == nil {
				t.Fatal("a value byre cannot run must be refused before install")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must name the rule and the offending value, missing %q: %v", want, err)
				}
			}
		})
	}
}

// The same rules on the LOAD path, which is what `byre skill validate` walks
// (validateOne -> skills.Load). Bundled skills skip catalog stage 2 entirely,
// so load is the only tier that judges them.
func TestBundledSkillValuesAreJudgedAtLoad(t *testing.T) {
	bundled := fstest.MapFS{
		"skills/broken/skill.toml": &fstest.MapFile{
			Data: []byte("description = \"b\"\n[runtime]\nnetwork_posture = \"Deny-Default\"\n"),
		},
	}
	cat, err := packages.LoadCatalog(t.TempDir(), bundled, "0.2.0", "0.2.0", packages.Stage2Hooks{Skill: ValidatePrimaryBytes})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(cat, "broken")
	if err == nil {
		t.Fatal("a bundled skill with an unrunnable value must fail to load")
	}
	for _, want := range []string{"network_posture", "Deny-Default", "must match"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the rule and the offending value, missing %q: %v", want, err)
		}
	}
}

// The other side of the extraction, pinned so nobody "finishes the job": a
// set-dependent rule must NOT migrate into the single-manifest tier. Two
// skills each declaring a network_posture are individually valid -- validate
// and install accept both, correctly -- and only the config that enables both
// is wrong, which nothing but Resolve can see. This is what makes
// `byre skill validate` a partial promise (docs/SKILLS.md).
func TestSetDependentRulesStayAtResolve(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw1", "[runtime]\nnetwork_posture = \"deny-by-default\"\n", nil)
	writeSkill(t, dir, "fw2", "[runtime]\nnetwork_posture = \"open\"\n", nil)
	cat := catFor(t, dir)

	// Each passes the tier `byre skill validate` and install walk.
	for _, n := range []string{"fw1", "fw2"} {
		if _, err := Load(cat, n); err != nil {
			t.Fatalf("skill %s is valid on its own terms: %v", n, err)
		}
	}
	// Enabling both is the error, and only the set shows it.
	_, err := Resolve(config.Config{Skills: []string{"fw1", "fw2"}}, cat)
	if err == nil {
		t.Fatal("two declared network postures must be refused")
	}
	for _, want := range []string{"both declare a network_posture", "fw1", "fw2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the rule and both claimants, missing %q: %v", want, err)
		}
	}
}

// A local skill with a bad value becomes an INVALID catalog row on the next
// scan (ADR 0029's amendment), so `byre skill list` shows it broken with its
// reason instead of healthy.
func TestBadValueSkillIsInvalidInTheCatalog(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fw", "[runtime]\nnetwork_posture = \"Deny-Default\"\n", nil)
	cat := catFor(t, dir)
	ent, ok := cat.Lookup("fw")
	if !ok {
		t.Fatal("the skill must still have a row")
	}
	if ent.Provenance != packages.ProvInvalid {
		t.Fatalf("provenance = %q, want INVALID", ent.Provenance)
	}
	if !strings.Contains(ent.Reason, "network_posture") {
		t.Errorf("the row must carry the rule that fired: %q", ent.Reason)
	}
}

// A skill whose primary PARSES but whose full load fails (here: a mount target
// that is not absolute) used to vanish -- healthy catalog row, no picker entry,
// no reason anywhere. It is a problem row now.
func TestUnloadableSkillBecomesAProblemRow(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "brokenmount",
		"[agent]\ncommand = \"x\"\n\n[[runtime.mounts]]\nhost = \"/tmp\"\ntarget = \"relative\"\n", nil)
	cat := catFor(t, dir)
	if ent, ok := cat.Lookup("brokenmount"); !ok || ent.Provenance == packages.ProvInvalid {
		t.Fatal("stage 2 does not judge mount shape; the row starts loadable")
	}

	if got := ListSkills(cat); len(got) != 0 {
		t.Errorf("an unloadable skill is still not offerable: %v", got)
	}
	ent, ok := cat.Lookup("brokenmount")
	if !ok {
		t.Fatal("the skill must still have a row")
	}
	if ent.Provenance != packages.ProvInvalid {
		t.Fatalf("provenance = %q, want INVALID", ent.Provenance)
	}
	// The reason names the rule; the identity is the row's own, not repeated.
	for _, want := range []string{"mount target", "relative"} {
		if !strings.Contains(ent.Reason, want) {
			t.Errorf("the row must carry the rule that fired, missing %q: %q", want, ent.Reason)
		}
	}
	if strings.HasPrefix(ent.Reason, "skill \"") {
		t.Errorf("the reason must not repeat the row's own identity: %q", ent.Reason)
	}
	// [agent] in the primary: the agent picker shows it disabled rather than
	// leaving the user's agent silently missing.
	if !ent.LooksLikeAgent {
		t.Error("a broken agent skill must stay visible to the agent picker")
	}
}

// MarkLoadFailures is the same pass for surfaces that read the catalog
// directly (`byre skill list`), which never call the skills package's listers.
func TestMarkLoadFailuresDemotesUnloadableSkills(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "brokenctx", "[context]\nfile = \"missing.md\"\n", nil)
	cat := catFor(t, dir)
	MarkLoadFailures(cat)
	ent, ok := cat.Lookup("brokenctx")
	if !ok {
		t.Fatal("the skill must still have a row")
	}
	if ent.Provenance != packages.ProvInvalid {
		t.Fatalf("provenance = %q, want INVALID", ent.Provenance)
	}
	if ent.Reason == "" {
		t.Error("a problem row without a reason is the silent drop again")
	}
}

// A healthy skill is untouched by the marking pass: the listers must not turn
// a working catalog into problem rows.
func TestHealthySkillsSurviveTheMarkingPass(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "fine", sampleSkill, nil)
	cat := catFor(t, dir)
	MarkLoadFailures(cat)
	ent, ok := cat.Lookup("fine")
	if !ok || ent.Provenance == packages.ProvInvalid {
		t.Fatalf("a loadable skill must stay loadable: %+v", ent)
	}
	if got := ListSkills(cat); len(got) != 1 || got[0] != "fine" {
		t.Fatalf("ListSkills = %v, want [fine]", got)
	}
}
