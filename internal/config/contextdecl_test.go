package config

import (
	"strings"
	"testing"
)

func TestContextDeclParseAndValidate(t *testing.T) {
	c, err := Parse([]byte(`
[[context]]
name = "house-rules"
text = """
Always run the linter before committing.
"""

[[context]]
name = "team-conventions"
file = "~/notes/agent-conventions.md"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(c.Contexts) != 2 || c.Contexts[0].Name != "house-rules" || c.Contexts[1].File != "~/notes/agent-conventions.md" {
		t.Fatalf("Contexts = %+v", c.Contexts)
	}
	if !strings.Contains(c.Contexts[0].Text, "linter") {
		t.Fatalf("inline text lost: %+v", c.Contexts[0])
	}
}

func TestContextDeclValidationRejects(t *testing.T) {
	cases := []struct {
		cd   ContextDecl
		want string
	}{
		{ContextDecl{Name: "Bad_Name", Text: "x"}, "must be lowercase"},
		{ContextDecl{Name: "ok"}, "needs text"},
		{ContextDecl{Name: "ok", Text: "x", File: "/f"}, "both text and file"},
		{ContextDecl{Name: "ok", File: "relative/notes.md"}, "must be absolute or ~/"},
	}
	for _, tc := range cases {
		err := ValidateContextDecl(tc.cd)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateContextDecl(%+v) = %v; want %q", tc.cd, err, tc.want)
		}
	}
	for _, ok := range []ContextDecl{
		{Name: "ok", Text: "inline prose"},
		{Name: "ok", File: "~/notes.md"},
		{Name: "ok", File: "/abs/notes.md"},
	} {
		if err := ValidateContextDecl(ok); err != nil {
			t.Errorf("ValidateContextDecl(%+v) = %v; want nil", ok, err)
		}
	}
}

func TestContextDeclLayerMarkersAndDuplicates(t *testing.T) {
	// A marker is name-only and layer-legal.
	c, err := Parse([]byte("[[context]]\nname = \"!house-rules\"\n"))
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
	c2 := Config{Contexts: []ContextDecl{{Name: "!house-rules", Text: "x"}}}
	if err := c2.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "closure marker takes only a name") {
		t.Fatalf("marker with fields: %v", err)
	}

	// In-layer duplicate names refuse (merge would silently replace).
	c3 := Config{Contexts: []ContextDecl{
		{Name: "house-rules", Text: "a"},
		{Name: "house-rules", Text: "b"},
	}}
	if err := c3.ValidateLayer(); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("in-layer duplicate: %v", err)
	}
}

func TestContextDeclMergeReplaceByName(t *testing.T) {
	base := Config{Contexts: []ContextDecl{{Name: "house-rules", Text: "old"}, {Name: "tone", Text: "t"}}}
	over := Config{Contexts: []ContextDecl{{Name: "house-rules", Text: "new"}}}
	got := Merge(base, over)
	if len(got.Contexts) != 2 {
		t.Fatalf("Contexts = %+v", got.Contexts)
	}
	// The replacement speaks at the REPLACING layer's position — a later
	// layer's prose lands after what it didn't replace, so cascade
	// precedence and rendered order agree.
	if got.Contexts[1].Name != "house-rules" || got.Contexts[1].Text != "new" {
		t.Fatalf("replacement must take the replacing layer's position: %+v", got.Contexts)
	}
	if got.Contexts[0].Name != "tone" {
		t.Fatalf("unrelated entry lost: %+v", got.Contexts)
	}
}

func TestContextDeclMergeClosureRemovesAndReopens(t *testing.T) {
	base := Config{Contexts: []ContextDecl{{Name: "house-rules", Text: "x"}}}
	over := Config{Contexts: []ContextDecl{{Name: "!house-rules"}}}
	got := Merge(base, over)
	if len(got.Contexts) != 0 {
		t.Fatalf("closure must remove the declaration: %+v", got.Contexts)
	}
	// A later layer's plain declaration re-opens the closure.
	reopened := Merge(got, Config{Contexts: []ContextDecl{{Name: "house-rules", Text: "again"}}})
	if len(reopened.Contexts) != 1 || reopened.Contexts[0].Text != "again" {
		t.Fatalf("reopen: %+v", reopened.Contexts)
	}
}

func TestContextDeclAllowedInTemplateBody(t *testing.T) {
	// Standing instructions are content, not composition: a template may
	// carry them (inline text is the portable form).
	if _, err := ParseTemplateBody([]byte("[[context]]\nname = \"go-style\"\ntext = \"Table-driven tests.\"\n")); err != nil {
		t.Fatalf("template body with [[context]]: %v", err)
	}
}

// Prose renders verbatim in the config UI, so control characters beyond
// newline and tab are refused at validation — an ESC sequence in an
// inherited layer's text could forge the surrounding terminal UI when its
// row is opened (the mcpPrintable stance; codex pre-ship review).
func TestContextDeclRejectsControlCharacters(t *testing.T) {
	bad := ContextDecl{Name: "sneaky", Text: "look normal\x1b[2Kthen forge the line\n"}
	if err := ValidateContextDecl(bad); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("err = %v, want the control-character refusal", err)
	}
	ok := ContextDecl{Name: "fine", Text: "newlines\nand\ttabs are prose\n"}
	if err := ValidateContextDecl(ok); err != nil {
		t.Fatalf("newline/tab prose refused: %v", err)
	}
}
