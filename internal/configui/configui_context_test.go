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

// The item editor SHOWS the stored prose read-only (maintainer call,
// 2026-07-25: reading the instructions must not require launching $EDITOR;
// only editing does). Long prose truncates with a tail count.
func TestContextItemViewShowsProse(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{Contexts: []config.ContextDecl{
		{Name: "house-rules", Text: "Run the linter.\nNever force-push.\n"},
	}}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fContext
	m = m.startItem(0)

	v := m.viewItem()
	for _, want := range []string{"Run the linter.", "Never force-push."} {
		if !strings.Contains(v, want) {
			t.Fatalf("prose not shown in item view (%q missing):\n%s", want, v)
		}
	}

	// Long prose truncates with a tail count instead of flooding the form.
	long := strings.Repeat("line\n", 40)
	m.itemProse = long
	v = m.viewItem()
	if !strings.Contains(v, "+28 more lines") {
		t.Fatalf("long prose should truncate with a tail count:\n%s", v)
	}

	// File mode shows no prose block.
	m.itemMode = 1
	if v := m.viewItem(); strings.Contains(v, "│") {
		t.Fatalf("file mode must not render a prose block:\n%s", v)
	}
}

// An invalid name warns WHILE TYPING, not first at commit (maintainer
// review: "check first" only failed on enter). The check runs after the
// auto-lowercase transform, so a merely-uppercase name draws no warning —
// save fixes it silently. Shared by the named-decl editors; asserted here
// for context and spot-checked for MCP.
func TestContextItemNameWarnsLive(t *testing.T) {
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, Inherited{}, nil, TargetProject)
	m.listField = fContext
	m = m.startItem(-1)

	m.inputs[0].SetValue("check first")
	if v := m.viewItem(); !strings.Contains(v, "won't save") {
		t.Fatalf("invalid name must warn live:\n%s", v)
	}
	m.inputs[0].SetValue("Check-First")
	if v := m.viewItem(); strings.Contains(v, "won't save") {
		t.Fatalf("a name the auto-lowercase fixes must not warn:\n%s", v)
	}
	m.inputs[0].SetValue("check-first")
	if v := m.viewItem(); strings.Contains(v, "won't save") {
		t.Fatalf("valid name must not warn:\n%s", v)
	}
	m.inputs[0].SetValue("")
	if v := m.viewItem(); strings.Contains(v, "won't save") {
		t.Fatalf("empty (still typing) must not warn:\n%s", v)
	}

	m.listField = fMCP
	m = m.startItem(-1)
	m.inputs[0].SetValue("bad name")
	if v := m.viewItem(); !strings.Contains(v, "won't save") {
		t.Fatalf("MCP editor must share the live warning:\n%s", v)
	}
}

// Opening an INHERITED instructions row shows the full prose in its menu —
// the user can't edit another layer's snippet from this file, but reading
// it never requires leaving the screen (maintainer call, 2026-07-25).
func TestContextInheritedRowMenuShowsFullProse(t *testing.T) {
	inh := Inherited{
		HasLower: true,
		Default: config.Config{Contexts: []config.ContextDecl{
			{Name: "house-rules", Text: "Run the linter.\nNever force-push.\nAsk before deploys.\n"},
		}},
	}
	m := newModel("t", "/tmp/x", config.Config{}, nil, nil, nil, nil, inh, nil, TargetProject)
	m.listField = fContext
	rows := m.fieldRows(fContext)
	if len(rows) != 1 || rows[0].kind != rowInherited {
		t.Fatalf("rows = %+v", rows)
	}
	m.menuRow = rows[0]

	v := m.viewMenu()
	for _, want := range []string{"Never force-push.", "Ask before deploys.", "Override here"} {
		if !strings.Contains(v, want) {
			t.Fatalf("inherited menu must show the full prose and actions (%q missing):\n%s", want, v)
		}
	}
}
