package skills

// Grants and attributed exposure: run args, sock groups, containment
// declarations, and the env/env_docs contracts across skills.

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

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

func TestResolveSockGroupsAndContainment(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "dh", `
[runtime]
mounts = [{ host = "/var/run/docker.sock", target = "/var/run/docker.sock", mode = "rw" }]
sock_groups = ["/var/run/docker.sock"]
containment = "docker-host opens a containment hole -- skim docs/DOCKER-HOST.md"
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
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "must match an active mount target") {
		t.Fatalf("sock_groups without matching mount target must be rejected, got %v", err)
	}
}

func TestResolveRejectsRelativeSockGroup(t *testing.T) {
	dir := testHome(t)
	// sock_groups path is relative; rejected regardless of mounts.
	writeSkill(t, dir, "dh", `[runtime]
mounts = [{ host = "/h", target = "/t", mode = "rw" }]
sock_groups = ["relative"]
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("relative sock_groups path must be rejected, got %v", err)
	}
}

func TestResolveRejectsContainmentNewline(t *testing.T) {
	dir := testHome(t)
	// Literal newline inside the TOML string is invalid TOML; use the escaped
	// form so Load succeeds and validateOneLiner rejects the decoded value.
	writeSkill(t, dir, "dh", "[runtime]\ncontainment = \"hole\\nNetwork: open\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "single line") {
		t.Fatalf("containment with newline must be rejected, got %v", err)
	}
}

func TestValidateOneLinerControlChar(t *testing.T) {
	if err := validateOneLiner("hole\x01forged"); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("control char must be rejected, got %v", err)
	}
	if err := validateOneLiner("ok one-liner"); err != nil {
		t.Fatalf("valid one-liner rejected: %v", err)
	}
}

func TestResolveRejectsContainmentTooLong(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", oneLinerMaxLen+1)
	writeSkill(t, dir, "dh", "[runtime]\ncontainment = \""+long+"\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"dh"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("overlong containment must be rejected, got %v", err)
	}
}

// env_docs (consumed-env guidance): declarations resolve into sorted,
// attributed EnvDoc rows; keys are held to the env-key grammar and guidance
// to the one-liner shape, with empty guidance refused.
func TestResolveEnvDocs(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "gem", `[runtime.env_docs]
GEMINI_API_KEY = "API key from aistudio.google.com; read at launch"
ZED_TOKEN = "optional; unlocks the zed integration"
`, nil)
	writeSkill(t, dir, "other", `[runtime.env_docs]
GEMINI_API_KEY = "also consumed here"
`, nil)
	res, err := Resolve(config.Config{Skills: []string{"other", "gem"}}, catFor(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	docs := res.EnvDocs()
	if len(docs) != 3 {
		t.Fatalf("EnvDocs = %+v", docs)
	}
	// Sorted by var name then skill — two skills documenting one var is fine.
	if docs[0].Name != "GEMINI_API_KEY" || docs[0].Skill != "gem" ||
		docs[1].Name != "GEMINI_API_KEY" || docs[1].Skill != "other" ||
		docs[2].Name != "ZED_TOKEN" || !strings.Contains(docs[2].Text, "optional") {
		t.Fatalf("EnvDocs order/content: %+v", docs)
	}
}

func TestResolveRejectsBadEnvDocs(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "badkey", "[runtime.env_docs]\n\"NOT A VAR\" = \"guidance\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"badkey"}}, catFor(t, dir)); err == nil || !strings.Contains(err.Error(), "not a valid environment variable name") {
		t.Fatalf("invalid env_docs key must be rejected, got %v", err)
	}
	dir2 := testHome(t)
	writeSkill(t, dir2, "empty", "[runtime.env_docs]\nFOO = \"\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"empty"}}, catFor(t, dir2)); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty env_docs guidance must be rejected, got %v", err)
	}
	dir3 := testHome(t)
	writeSkill(t, dir3, "multiline", "[runtime.env_docs]\nFOO = \"line\\nline2\"\n", nil)
	if _, err := Resolve(config.Config{Skills: []string{"multiline"}}, catFor(t, dir3)); err == nil || !strings.Contains(err.Error(), "single line") {
		t.Fatalf("multi-line env_docs guidance must be rejected, got %v", err)
	}
}
