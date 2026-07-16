package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func mkClaudeSkillCarrier(name string, decls ...ClaudeSkillDecl) Skill {
	return Skill{Name: name, ClaudeSkills: decls}
}

func csDecl(skill, name, src string) ClaudeSkillDecl {
	return ClaudeSkillDecl{Skill: skill, CS: config.ClaudeSkill{Name: name, From: "x"}, SrcDir: src}
}

func TestClaudeSkillSetUnionAndAttribution(t *testing.T) {
	cfg := config.Config{ClaudeSkills: []config.ClaudeSkill{{Name: "tdd-loop", Path: "~/cs/tdd-loop"}}}
	r := Resolved{Skills: []Skill{mkClaudeSkillCarrier("pete/tools", csDecl("pete/tools", "review-loop", "/resolved/review-loop"))}}
	set, err := ClaudeSkillSet(cfg, r)
	if err != nil {
		t.Fatalf("ClaudeSkillSet: %v", err)
	}
	if len(set) != 2 {
		t.Fatalf("set = %+v", set)
	}
	if set[0].Skill != ClaudeSkillsFromConfig || set[0].CS.Name != "tdd-loop" || set[0].SrcDir != "" {
		t.Fatalf("config attribution: %+v", set[0])
	}
	if set[1].Skill != "pete/tools" || set[1].CS.Name != "review-loop" || set[1].SrcDir != "/resolved/review-loop" {
		t.Fatalf("skill attribution: %+v", set[1])
	}
}

func TestClaudeSkillSetDuplicateHardReject(t *testing.T) {
	cfg := config.Config{ClaudeSkills: []config.ClaudeSkill{{Name: "review-loop", Path: "/a"}}}
	r := Resolved{Skills: []Skill{mkClaudeSkillCarrier("pete/tools", csDecl("pete/tools", "review-loop", "/b"))}}
	_, err := ClaudeSkillSet(cfg, r)
	if err == nil || !strings.Contains(err.Error(), "declared by both the config and skill \"pete/tools\"") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "!review-loop") {
		t.Fatalf("remedy must name the closure: %v", err)
	}
}

func TestClaudeSkillSetClosureSubtractsAfterSkillUnion(t *testing.T) {
	cfg := config.Config{ClaudeSkillsClosed: []string{"review-loop"}}
	r := Resolved{Skills: []Skill{mkClaudeSkillCarrier("pete/tools",
		csDecl("pete/tools", "review-loop", "/r"),
		csDecl("pete/tools", "tdd-loop", "/t"),
	)}}
	set, err := ClaudeSkillSet(cfg, r)
	if err != nil {
		t.Fatalf("ClaudeSkillSet: %v", err)
	}
	if len(set) != 1 || set[0].CS.Name != "tdd-loop" {
		t.Fatalf("closure must subtract the skill contribution: %+v", set)
	}
	// A closed name doesn't collide either — closing is the documented remedy.
	cfg2 := config.Config{
		ClaudeSkills:       []config.ClaudeSkill{{Name: "review-loop", Path: "/mine"}},
		ClaudeSkillsClosed: []string{"review-loop"},
	}
	if set, err := ClaudeSkillSet(cfg2, r); err != nil || len(set) != 1 {
		t.Fatalf("closed name must not collide: set=%+v err=%v", set, err)
	}
}

// writeClaudeSkill lays down a well-formed Claude Skill dir.
func writeClaudeSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if frontmatter == "" {
		frontmatter = "---\nname: " + name + "\ndescription: Use when testing byre.\n---\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(frontmatter+"Body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateClaudeSkillDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tdd-loop")
	writeClaudeSkill(t, dir, "tdd-loop", "")
	if err := os.WriteFile(filepath.Join(dir, "support.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClaudeSkillDir(dir, "tdd-loop"); err != nil {
		t.Fatalf("well-formed skill: %v", err)
	}

	// Multiline (folded) description is real YAML and must pass.
	folded := filepath.Join(t.TempDir(), "folded")
	writeClaudeSkill(t, folded, "folded", "---\nname: folded\ndescription: >-\n  Use when testing byre\n  across multiple lines.\n---\n")
	if err := ValidateClaudeSkillDir(folded, "folded"); err != nil {
		t.Fatalf("folded description: %v", err)
	}

	// CRLF frontmatter fences parse too.
	crlf := filepath.Join(t.TempDir(), "crlf")
	writeClaudeSkill(t, crlf, "crlf", "---\r\nname: crlf\r\ndescription: Use when testing byre.\r\n---\r\n")
	if err := ValidateClaudeSkillDir(crlf, "crlf"); err != nil {
		t.Fatalf("crlf: %v", err)
	}
}

func TestValidateClaudeSkillDirRejects(t *testing.T) {
	base := t.TempDir()

	missing := filepath.Join(base, "missing")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClaudeSkillDir(missing, "missing"); err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Fatalf("missing SKILL.md: %v", err)
	}

	if err := ValidateClaudeSkillDir(filepath.Join(base, "nodir"), "nodir"); err == nil {
		t.Fatalf("missing dir must refuse")
	}

	mismatch := filepath.Join(base, "mismatch")
	writeClaudeSkill(t, mismatch, "other-name", "")
	if err := ValidateClaudeSkillDir(mismatch, "mismatch"); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("name mismatch: %v", err)
	}

	nodesc := filepath.Join(base, "nodesc")
	writeClaudeSkill(t, nodesc, "nodesc", "---\nname: nodesc\n---\n")
	if err := ValidateClaudeSkillDir(nodesc, "nodesc"); err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("empty description: %v", err)
	}

	unclosed := filepath.Join(base, "unclosed")
	writeClaudeSkill(t, unclosed, "unclosed", "---\nname: unclosed\ndescription: x\n")
	if err := ValidateClaudeSkillDir(unclosed, "unclosed"); err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Fatalf("unclosed frontmatter: %v", err)
	}

	nofm := filepath.Join(base, "nofm")
	writeClaudeSkill(t, nofm, "nofm", "just a body\n")
	if err := ValidateClaudeSkillDir(nofm, "nofm"); err == nil || !strings.Contains(err.Error(), "must open with") {
		t.Fatalf("no frontmatter: %v", err)
	}

	badyaml := filepath.Join(base, "badyaml")
	writeClaudeSkill(t, badyaml, "badyaml", "---\nname: [unclosed\ndescription: x\n---\n")
	if err := ValidateClaudeSkillDir(badyaml, "badyaml"); err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Fatalf("bad yaml: %v", err)
	}

	linked := filepath.Join(base, "linked")
	writeClaudeSkill(t, linked, "linked", "")
	if err := os.Symlink("/etc/passwd", filepath.Join(linked, "steal")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClaudeSkillDir(linked, "linked"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: %v", err)
	}

	crowded := filepath.Join(base, "crowded")
	writeClaudeSkill(t, crowded, "crowded", "")
	for i := 0; i <= MaxClaudeSkillFiles; i++ {
		if err := os.WriteFile(filepath.Join(crowded, "f"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateClaudeSkillDir(crowded, "crowded"); err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("file cap: %v", err)
	}
}

func TestResolveClaudeSkillContribution(t *testing.T) {
	home := testHome(t)
	writeSkill(t, home, "carrier", `
description = "carries a claude skill"
[[claude_skills]]
name = "review-loop"
from = "cs/review-loop"
`, map[string]string{"cs/review-loop/SKILL.md": "---\nname: review-loop\ndescription: d\n---\n"})
	cat := catFor(t, home)
	res, err := Resolve(config.Config{Skills: []string{"carrier"}}, cat)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	sk := res.Skills[len(res.Skills)-1]
	if len(sk.ClaudeSkills) != 1 || sk.ClaudeSkills[0].CS.Name != "review-loop" {
		t.Fatalf("ClaudeSkills = %+v", sk.ClaudeSkills)
	}
	if sk.ClaudeSkills[0].SrcDir == "" || !strings.HasSuffix(sk.ClaudeSkills[0].SrcDir, filepath.Join("cs", "review-loop")) {
		t.Fatalf("SrcDir = %q", sk.ClaudeSkills[0].SrcDir)
	}

	// Escapes refuse at resolve.
	writeSkill(t, home, "escaper", `
description = "escapes"
[[claude_skills]]
name = "oops"
from = "../outside"
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"escaper"}}, catFor(t, home)); err == nil || !strings.Contains(err.Error(), "within the skill dir") {
		t.Fatalf("escape: %v", err)
	}

	// A typo'd vouch enum refuses.
	writeSkill(t, home, "badvouch", `
description = "agent with bad vouch"
[agent]
command = "x"
claude_skills = "add-dir"
`, nil)
	if _, err := Resolve(config.Config{Skills: []string{"badvouch"}}, catFor(t, home)); err == nil || !strings.Contains(err.Error(), "claude_skills \"add-dir\" invalid") {
		t.Fatalf("vouch typo: %v", err)
	}
}
