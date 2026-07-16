package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// writeTestClaudeSkill lays down a minimal well-formed Claude Skill dir and
// returns its path.
func writeTestClaudeSkill(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nname: " + name + "\ndescription: Use when testing byre.\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestClaudeSkillAddDerivesNameAndValidates(t *testing.T) {
	dir, projPath, _, s, errw := mcpTestProject(t)
	src := writeTestClaudeSkill(t, "tdd-loop")

	// Name derived from the frontmatter; declaration lands in the project layer.
	if err := ClaudeSkillAdd(s, dir, false, "", src); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg, err := config.ParseFile(projPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ClaudeSkills) != 1 || cfg.ClaudeSkills[0].Name != "tdd-loop" || cfg.ClaudeSkills[0].Path != src {
		t.Fatalf("declaration = %+v", cfg.ClaudeSkills)
	}
	if !strings.Contains(errw.String(), "added claude skill tdd-loop") {
		t.Errorf("add not reported: %s", errw)
	}

	// add-or-update: same name replaces in place.
	if err := ClaudeSkillAdd(s, dir, false, "tdd-loop", src); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	cfg, _ = config.ParseFile(projPath)
	if len(cfg.ClaudeSkills) != 1 {
		t.Fatalf("re-add duplicated: %+v", cfg.ClaudeSkills)
	}

	// A --name that disagrees with the frontmatter fails the pair check.
	if err := ClaudeSkillAdd(s, dir, false, "other-name", src); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("mismatch: %v", err)
	}

	// A dir that isn't a skill refuses with the reason.
	if err := ClaudeSkillAdd(s, dir, false, "", t.TempDir()); err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Fatalf("non-skill: %v", err)
	}
}

func TestClaudeSkillAddGlobalAndReopen(t *testing.T) {
	dir, projPath, globalPath, s, errw := mcpTestProject(t)
	src := writeTestClaudeSkill(t, "review-loop")

	if err := ClaudeSkillAdd(s, dir, true, "", src); err != nil {
		t.Fatalf("global add: %v", err)
	}
	g, err := config.ParseFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.ClaudeSkills) != 1 || g.ClaudeSkills[0].Name != "review-loop" {
		t.Fatalf("global declaration = %+v", g.ClaudeSkills)
	}

	// A project-layer closure is re-opened by a project add.
	if err := os.WriteFile(projPath, []byte("[[claude_skills]]\nname = \"!review-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if err := ClaudeSkillAdd(s, dir, false, "", src); err != nil {
		t.Fatalf("reopen add: %v", err)
	}
	p, _ := config.ParseFile(projPath)
	if len(p.ClaudeSkills) != 1 || p.ClaudeSkills[0].Name != "review-loop" {
		t.Fatalf("closure not superseded: %+v", p.ClaudeSkills)
	}
	if !strings.Contains(errw.String(), "re-opens it") {
		t.Errorf("reopen not reported: %s", errw)
	}
}

func TestClaudeSkillRemoveClosureSmart(t *testing.T) {
	dir, projPath, globalPath, s, errw := mcpTestProject(t)
	src := writeTestClaudeSkill(t, "tdd-loop")

	// Declared in the project layer only: remove deletes the block, no closure.
	if err := ClaudeSkillAdd(s, dir, false, "", src); err != nil {
		t.Fatal(err)
	}
	if err := ClaudeSkillRemove(s, dir, false, "tdd-loop"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	p, _ := config.ParseFile(projPath)
	if len(p.ClaudeSkills) != 0 {
		t.Fatalf("block not deleted: %+v", p.ClaudeSkills)
	}

	// Declared below (global layer): remove writes the closure.
	if err := ClaudeSkillAdd(s, dir, true, "", src); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if err := ClaudeSkillRemove(s, dir, false, "tdd-loop"); err != nil {
		t.Fatalf("remove inherited: %v", err)
	}
	p, _ = config.ParseFile(projPath)
	if len(p.ClaudeSkills) != 1 || p.ClaudeSkills[0].Name != "!tdd-loop" {
		t.Fatalf("closure not written: %+v", p.ClaudeSkills)
	}
	if !strings.Contains(errw.String(), "closed claude skill tdd-loop") {
		t.Errorf("closure not reported: %s", errw)
	}

	// Already closed: idempotent, not an error.
	errw.Reset()
	if err := ClaudeSkillRemove(s, dir, false, "tdd-loop"); err != nil {
		t.Fatalf("re-remove: %v", err)
	}
	if !strings.Contains(errw.String(), "already closed") {
		t.Errorf("idempotence not reported: %s", errw)
	}

	// Nowhere at all: an error.
	if err := ClaudeSkillRemove(s, dir, false, "ghost"); err == nil || !strings.Contains(err.Error(), "nothing to remove") {
		t.Fatalf("ghost: %v", err)
	}
	_ = globalPath
}

func TestClaudeSkillListRendersEffectiveSet(t *testing.T) {
	dir, _, _, s, _ := mcpTestProject(t)
	src := writeTestClaudeSkill(t, "tdd-loop")
	out := s.Out.(interface{ String() string })

	if err := ClaudeSkillList(s, dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no Claude Skills declared") {
		t.Errorf("empty list: %s", out.String())
	}

	if err := ClaudeSkillAdd(s, dir, false, "", src); err != nil {
		t.Fatal(err)
	}
	if err := ClaudeSkillList(s, dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tdd-loop — "+src+"  (config)") {
		t.Errorf("list row missing: %s", out.String())
	}
	if !strings.Contains(out.String(), "no agent selected") {
		t.Errorf("agentless delivery line missing: %s", out.String())
	}
}
