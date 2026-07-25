package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestContextRowsEffectiveView(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{Contexts: []config.ContextDecl{
			{Name: "house-rules", Text: "Always lint.\n"},
			{Name: "shadowed", Text: "old prose"},
		}},
	}
	cfg := config.Config{
		Contexts: []config.ContextDecl{
			{Name: "own", Text: "Local prose.\nSecond line.\n"},
			{Name: "shadowed", File: "~/notes/new.md"},
			{Name: "!house-rules"},
		},
	}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, inh, nil, TargetProject)
	m.listField = fContext
	rows := m.fieldRows(fContext)

	find := func(kind rowKind, substr string) *listRow {
		for i := range rows {
			if rows[i].kind == kind && strings.Contains(rows[i].text, substr) {
				return &rows[i]
			}
		}
		return nil
	}
	if r := find(rowLocal, `own — "Local prose." +1 lines`); r == nil {
		t.Fatalf("local row with prose summary missing: %+v", rows)
	}
	if r := find(rowOverride, "shadowed — file: ~/notes/new.md"); r == nil {
		t.Fatalf("replace-by-name must render as override: %+v", rows)
	}
	if r := find(rowRemoved, "house-rules"); r == nil {
		t.Fatalf("closed inherited snippet must show removed with Restore: %+v", rows)
	}
}

// The item editor's prose round-trip: ^e writes the draft to a temp file,
// $EDITOR edits it, editorClosedMsg routes back into the item editor (not
// the whole-file reload), and commit stores the declaration.
func TestContextItemProseEditorRoundTrip(t *testing.T) {
	m := newModel("t", filepath.Join(t.TempDir(), "byre.config"), config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fContext
	m = m.startItem(-1)
	m.inputs[0].SetValue("house-rules")

	// Simulate the $EDITOR leg: the draft file exists (what ^e writes), the
	// user edits it, the editor closes.
	f, err := os.CreateTemp(t.TempDir(), "prose-*.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("Run the linter.\nNever force-push.\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	m.prosePath = f.Name()
	m = m.onProseEditorClosed(nil)
	if m.prosePath != "" {
		t.Fatal("prosePath must clear after the round-trip")
	}
	if !strings.Contains(m.itemProse, "Never force-push") {
		t.Fatalf("prose draft not loaded: %q", m.itemProse)
	}

	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if len(m.contexts) != 1 || m.contexts[0].Name != "house-rules" || !strings.Contains(m.contexts[0].Text, "linter") {
		t.Fatalf("declaration not stored: %+v", m.contexts)
	}

	// And the whole thing lands in the file via Save, as multiline prose.
	out := m.assemble()
	if err := Save(m.filePath, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(m.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "text = '''") {
		t.Fatalf("prose should land as a multiline literal:\n%s", raw)
	}
	back, err := config.ParseFile(m.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Contexts) != 1 || back.Contexts[0].Text != "Run the linter.\nNever force-push.\n" {
		t.Fatalf("round-trip: %+v", back.Contexts)
	}
}

// Committing an inline-text declaration with no prose is refused with the
// ^e pointer, not accepted empty (ValidateContextDecl would reject it
// anyway, but the message should teach the flow).
func TestContextItemEmptyProseRefused(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fContext
	m = m.startItem(-1)
	m.inputs[0].SetValue("empty")
	m = m.commitItem()
	if !strings.Contains(m.itemErr, "^e") {
		t.Fatalf("empty prose must point at ^e: %q", m.itemErr)
	}
}

// A file-backed declaration commits from the path input (mode 1); no prose
// needed.
func TestContextItemFileMode(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fContext
	m = m.startItem(-1)
	m.inputs[0].SetValue("conventions")
	m.inputs[1].SetValue("~/notes/conventions.md")
	m.itemMode = 1
	m = m.commitItem()
	if m.itemErr != "" {
		t.Fatalf("commit: %s", m.itemErr)
	}
	if len(m.contexts) != 1 || m.contexts[0].File != "~/notes/conventions.md" || m.contexts[0].Text != "" {
		t.Fatalf("file-backed declaration wrong: %+v", m.contexts)
	}
}

// The dirty signature tracks prose content, not just the summary line: a
// deep edit (same first line) must flip it.
func TestContextProseEditFlipsDirtySignature(t *testing.T) {
	cfg := config.Config{Contexts: []config.ContextDecl{{Name: "n", Text: "Same first line.\nold tail\n"}}}
	m := newModel("t", "/tmp/x", cfg, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	before := m.sig()
	m.contexts[0].Text = "Same first line.\nnew tail\n"
	if m.sig() == before {
		t.Fatal("prose change must flip the dirty signature")
	}
}
