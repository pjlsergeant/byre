package config

import (
	"strings"
	"testing"
)

func TestClaudeSkillParseAndValidate(t *testing.T) {
	c, err := Parse([]byte(`
[[claude_skills]]
name = "tdd-loop"
path = "~/claude-skills/tdd-loop"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(c.ClaudeSkills) != 1 || c.ClaudeSkills[0].Name != "tdd-loop" || c.ClaudeSkills[0].Path != "~/claude-skills/tdd-loop" {
		t.Fatalf("ClaudeSkills = %+v", c.ClaudeSkills)
	}
}

func TestClaudeSkillValidationRejects(t *testing.T) {
	cases := []struct {
		cs        ClaudeSkill
		fromSkill bool
		want      string
	}{
		{ClaudeSkill{Name: "Bad_Name", Path: "/x"}, false, "must be lowercase"},
		{ClaudeSkill{Name: "ok", Path: ""}, false, "needs `path`"},
		{ClaudeSkill{Name: "ok", Path: "relative/dir"}, false, "must be absolute or ~/"},
		{ClaudeSkill{Name: "ok", Path: "/x", From: "y"}, false, "`from` is skill.toml vocabulary"},
		{ClaudeSkill{Name: "ok", From: ""}, true, "needs `from`"},
		{ClaudeSkill{Name: "ok", From: "../escape"}, true, "within the skill dir"},
		{ClaudeSkill{Name: "ok", From: "/abs"}, true, "within the skill dir"},
		{ClaudeSkill{Name: "ok", From: "x", Path: "/y"}, true, "`path` is config vocabulary"},
	}
	for _, tc := range cases {
		err := ValidateClaudeSkill(tc.cs, tc.fromSkill)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateClaudeSkill(%+v, fromSkill=%v) = %v; want %q", tc.cs, tc.fromSkill, err, tc.want)
		}
	}
	// Both homes accept their own well-formed shape.
	if err := ValidateClaudeSkill(ClaudeSkill{Name: "ok", Path: "/abs/dir"}, false); err != nil {
		t.Errorf("config home: %v", err)
	}
	if err := ValidateClaudeSkill(ClaudeSkill{Name: "ok", From: "skills/ok"}, true); err != nil {
		t.Errorf("skill home: %v", err)
	}
}

func TestClaudeSkillLayerMarkersAndDuplicates(t *testing.T) {
	// A marker is name-only and layer-legal.
	c, err := Parse([]byte("[[claude_skills]]\nname = \"!tdd-loop\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := c.ValidateLayer(); err != nil {
		t.Fatalf("marker should be layer-legal: %v", err)
	}
	if err := c.Validate(); err == nil {
		t.Fatalf("marker must be rejected in a resolved config")
	}

	// A marker carrying other fields is a mistyped real declaration.
	c2 := Config{ClaudeSkills: []ClaudeSkill{{Name: "!tdd-loop", Path: "/x"}}}
	if err := c2.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "closure marker takes only a name") {
		t.Fatalf("marker with fields: %v", err)
	}

	// In-layer duplicate names refuse (merge would silently replace).
	c3 := Config{ClaudeSkills: []ClaudeSkill{
		{Name: "tdd-loop", Path: "/a"},
		{Name: "tdd-loop", Path: "/b"},
	}}
	if err := c3.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("in-layer duplicate: %v", err)
	}
}

func TestClaudeSkillMergeReplaceByName(t *testing.T) {
	base := Config{ClaudeSkills: []ClaudeSkill{{Name: "tdd-loop", Path: "/old"}, {Name: "review", Path: "/r"}}}
	over := Config{ClaudeSkills: []ClaudeSkill{{Name: "tdd-loop", Path: "/new"}}}
	got := Merge(base, over)
	if len(got.ClaudeSkills) != 2 {
		t.Fatalf("ClaudeSkills = %+v", got.ClaudeSkills)
	}
	if got.ClaudeSkills[0].Name != "tdd-loop" || got.ClaudeSkills[0].Path != "/new" {
		t.Fatalf("later layer must replace by name: %+v", got.ClaudeSkills[0])
	}
	if got.ClaudeSkills[1].Name != "review" {
		t.Fatalf("unrelated entry lost: %+v", got.ClaudeSkills)
	}
}

func TestClaudeSkillMergeClosureSurvivesAndReopens(t *testing.T) {
	base := Config{ClaudeSkills: []ClaudeSkill{{Name: "tdd-loop", Path: "/t"}}}
	over := Config{ClaudeSkills: []ClaudeSkill{{Name: "!tdd-loop"}, {Name: "!review"}}}
	got := Merge(base, over)
	if len(got.ClaudeSkills) != 0 {
		t.Fatalf("closure must remove the declaration: %+v", got.ClaudeSkills)
	}
	// Closures survive the merge (they subtract skill contributions later).
	if len(got.ClaudeSkillsClosed) != 2 {
		t.Fatalf("ClaudeSkillsClosed = %v", got.ClaudeSkillsClosed)
	}
	// A later layer's plain declaration re-opens the closure.
	reopened := Merge(got, Config{ClaudeSkills: []ClaudeSkill{{Name: "tdd-loop", Path: "/again"}}})
	if len(reopened.ClaudeSkills) != 1 || reopened.ClaudeSkills[0].Path != "/again" {
		t.Fatalf("reopen: %+v", reopened.ClaudeSkills)
	}
	for _, c := range reopened.ClaudeSkillsClosed {
		if c == "tdd-loop" {
			t.Fatalf("reopened name must leave the closed set: %v", reopened.ClaudeSkillsClosed)
		}
	}
}
