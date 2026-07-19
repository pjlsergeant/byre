package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestClaudeSkillRowsEffectiveView(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{ClaudeSkills: []config.ClaudeSkill{
			{Name: "inherited", Path: "/cs/inherited"},
			{Name: "shadowed", Path: "/cs/old"},
		}},
		Skills: map[string]SkillRuntime{
			"pete/tools": {ClaudeSkills: []config.ClaudeSkill{
				{Name: "from-skill", From: "cs/from-skill"},
				{Name: "closed-skill", From: "cs/closed-skill"},
			}},
		},
	}
	cfg := config.Config{
		Skills: []string{"pete/tools"},
		ClaudeSkills: []config.ClaudeSkill{
			{Name: "own", Path: "/cs/own"},
			{Name: "shadowed", Path: "/cs/new"},
			{Name: "!closed-skill"},
			{Name: "!ghost"},
		},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, []string{"pete/tools"}, nil, inh, nil, TargetProject)
	m.listField = fClaudeSkills
	rows := m.fieldRows(fClaudeSkills)

	find := func(kind rowKind, substr string) *listRow {
		for i := range rows {
			if rows[i].kind == kind && strings.Contains(rows[i].text, substr) {
				return &rows[i]
			}
		}
		return nil
	}
	if r := find(rowInherited, "inherited"); r == nil || r.source != "default" || r.ident != "inherited" {
		t.Fatalf("inherited row wrong: %+v (rows: %+v)", r, rows)
	}
	if r := find(rowOverride, "shadowed — /cs/new"); r == nil {
		t.Fatalf("replace-by-name must render as override: %+v", rows)
	}
	if r := find(rowLocal, "own"); r == nil {
		t.Fatalf("local row missing: %+v", rows)
	}
	if r := find(rowSkill, "from-skill"); r == nil || r.ident != "from-skill" {
		t.Fatalf("skill row must be closable (ident set): %+v", rows)
	}
	if r := find(rowRemoved, "closed-skill"); r == nil || r.idx < 0 {
		t.Fatalf("skill contribution closed by this file must show removed with Restore: %+v", rows)
	}
	if r := find(rowStaleMarker, "ghost"); r == nil {
		t.Fatalf("marker matching nothing must read stale: %+v", rows)
	}

	sk := find(rowSkill, "from-skill")
	choices := m.rowChoices(fClaudeSkills, *sk)
	if len(choices) != 1 || choices[0].act != actRemoveHere {
		t.Fatalf("skill claude-skill row choices: %+v", choices)
	}
	m.removeHere(*sk)
	if out := m.assemble(); !hasClaudeSkillName(out.ClaudeSkills, "!from-skill") {
		t.Fatalf("removeHere must write the closure: %+v", out.ClaudeSkills)
	}
}

func TestClaudeSkillItemEditorCommit(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fClaudeSkills
	m = m.startItem(-1)
	m.inputs[0].SetValue("TDD-Loop") // lowercases on commit
	m.inputs[1].SetValue("~/cs/tdd-loop")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	out := m.assemble()
	if len(out.ClaudeSkills) != 1 || out.ClaudeSkills[0].Name != "tdd-loop" || out.ClaudeSkills[0].Path != "~/cs/tdd-loop" {
		t.Fatalf("assembled = %+v", out.ClaudeSkills)
	}

	// A relative path is refused with config's own message.
	m = m.startItem(-1)
	m.inputs[0].SetValue("x")
	m.inputs[1].SetValue("relative/dir")
	m = m.commitItem()
	if m.itemErr == "" || !strings.Contains(m.itemErr, "absolute or ~/") {
		t.Fatalf("relative path must refuse: %q", m.itemErr)
	}
}

// A Claude Skill edit must flip dirty — sig() has to sign m.claudeSkills or
// quitting after an add/close loses the edit without the unsaved-changes
// confirm (review finding).
func TestClaudeSkillEditsFlipDirty(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	if m.dirty() {
		t.Fatal("a freshly-opened config must not be dirty")
	}
	m.listField = fClaudeSkills
	m = m.startItem(-1)
	m.inputs[0].SetValue("tdd-loop")
	m.inputs[1].SetValue("~/cs/tdd-loop")
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if !m.dirty() {
		t.Fatal("adding a Claude Skill must mark the form dirty")
	}
}

// The Claude Skill editor warns — never gates — on a host dir the bake would
// reject (field-QA 2026-07-17, finding 4). The validator that decides is the
// bake's own (skills.ValidateClaudeSkillDir), so editor and develop cannot
// disagree; the note classifies briefly.
func TestClaudeSkillDirNoteClasses(t *testing.T) {
	if n := claudeSkillDirNote("x", ""); n != "" {
		t.Errorf("empty path is the required-check's job, got note %q", n)
	}
	if n := claudeSkillDirNote("x", "/definitely/not/a/dir"); !strings.Contains(n, "path missing") {
		t.Errorf("missing dir: got %q", n)
	}
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := claudeSkillDirNote("x", f); !strings.Contains(n, "not a directory") {
		t.Errorf("regular file: got %q", n)
	}
	empty := t.TempDir()
	if n := claudeSkillDirNote("x", empty); !strings.Contains(n, "no SKILL.md") {
		t.Errorf("dir without SKILL.md: got %q", n)
	}
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"),
		[]byte("---\nname: good-skill\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := claudeSkillDirNote("good-skill", good); n != "" {
		t.Errorf("valid dir must carry no note, got %q", n)
	}
	if n := claudeSkillDirNote("other-name", good); !strings.Contains(n, "build will fail") {
		t.Errorf("frontmatter name mismatch must warn, got %q", n)
	}
}

// Accepting a bad path stays NON-blocking (warn-only): the entry commits, the
// editor note and the list row both carry the warning, and the dirty
// SIGNATURE ignores it — a dir appearing later must not flip dirty.
func TestClaudeSkillBadPathWarnsWithoutBlocking(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fClaudeSkills
	m = m.startItem(-1)
	m.inputs[0].SetValue("qa-skill")
	m.inputs[1].SetValue("/definitely/not/a/dir")
	notes := strings.Join(m.itemNotes(), "\n")
	if !strings.Contains(notes, "path missing") || !strings.Contains(notes, "accepted anyway") {
		t.Fatalf("editor note missing the live warning: %q", notes)
	}
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("bad path must not block the commit (warn-only), got error %q", m.itemErr)
	}
	if len(m.claudeSkills) != 1 || m.claudeSkills[0].Name != "qa-skill" {
		t.Fatalf("entry not committed: %+v", m.claudeSkills)
	}
	if got := claudeSkillRowText(m.claudeSkills[0]); !strings.Contains(got, "path missing — build will fail") {
		t.Fatalf("list row must carry the warning: %q", got)
	}
	// Signature stability: the same entry with an existing vs missing dir
	// signs identically (the note is display-only).
	sigBad := m.sig()
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, "SKILL.md"),
		[]byte("---\nname: qa-skill\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2 := m
	m2.claudeSkills = []config.ClaudeSkill{{Name: "qa-skill", Path: good}}
	if claudeSkillRowText(m2.claudeSkills[0]) != claudeSkillLine(m2.claudeSkills[0]) {
		t.Fatal("valid entry must carry no row warning")
	}
	_ = sigBad // both models sign via claudeSkillLine — pinned by the substring below
	if strings.Contains(m.sig(), "build will fail") {
		t.Fatal("the warning leaked into the dirty signature")
	}
}
