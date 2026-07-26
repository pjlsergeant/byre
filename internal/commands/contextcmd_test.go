package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

func TestContextAddInlineAndUpdate(t *testing.T) {
	dir, projPath, _, s, errw := mcpTestProject(t)

	if err := ContextAdd(s, dir, false, "house-rules", "Run the linter.\n", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg, err := config.ParseFile(projPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Contexts) != 1 || cfg.Contexts[0].Name != "house-rules" || cfg.Contexts[0].Text != "Run the linter.\n" {
		t.Fatalf("declaration = %+v", cfg.Contexts)
	}
	if !strings.Contains(errw.String(), "added context house-rules") {
		t.Errorf("add not reported: %s", errw)
	}

	// add-or-update by name.
	if err := ContextAdd(s, dir, false, "house-rules", "New text.\n", ""); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	cfg, _ = config.ParseFile(projPath)
	if len(cfg.Contexts) != 1 || cfg.Contexts[0].Text != "New text.\n" {
		t.Fatalf("update: %+v", cfg.Contexts)
	}
}

// With neither --text nor --file the verb opens $EDITOR (the git-commit
// shape), seeded with the current inline text.
func TestContextAddEditorFlow(t *testing.T) {
	dir, projPath, _, s, _ := mcpTestProject(t)

	orig := editProse
	defer func() { editProse = orig }()
	var seen string
	editProse = func(seed string) (string, error) {
		seen = seed
		return seed + "From the editor.\n", nil
	}

	if err := ContextAdd(s, dir, false, "notes", "", ""); err != nil {
		t.Fatalf("editor add: %v", err)
	}
	if seen != "" {
		t.Fatalf("new declaration must seed an empty editor, got %q", seen)
	}
	if err := ContextAdd(s, dir, false, "notes", "", ""); err != nil {
		t.Fatalf("editor re-edit: %v", err)
	}
	if !strings.Contains(seen, "From the editor.") {
		t.Fatalf("re-edit must seed the stored text, got %q", seen)
	}
	cfg, _ := config.ParseFile(projPath)
	if len(cfg.Contexts) != 1 || strings.Count(cfg.Contexts[0].Text, "From the editor.") != 2 {
		t.Fatalf("editor text not stored: %+v", cfg.Contexts)
	}

	// Emptying the buffer is a refusal pointing at remove, not a silent
	// empty declaration.
	editProse = func(string) (string, error) { return "\n", nil }
	if err := ContextAdd(s, dir, false, "notes", "", ""); err == nil || !strings.Contains(err.Error(), "byre context remove") {
		t.Fatalf("empty editor buffer: err = %v", err)
	}
}

func TestContextAddExclusiveFlagsAndFileAnchor(t *testing.T) {
	dir, projPath, _, s, _ := mcpTestProject(t)
	if err := ContextAdd(s, dir, false, "x", "text", "/f"); err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("both flags: err = %v", err)
	}
	// A ~ spelling is stored as typed (expands at bake).
	if err := ContextAdd(s, dir, false, "conventions", "", "~/notes/conv.md"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.ParseFile(projPath)
	if cfg.Contexts[0].File != "~/notes/conv.md" {
		t.Fatalf("file = %+v", cfg.Contexts)
	}
}

// remove is closure-smart via the shared lifecycle: removing a declaration
// that only exists in a lower layer writes the `!name` closure.
func TestContextRemoveWritesClosureForInherited(t *testing.T) {
	dir, projPath, defaultCfg, s, errw := mcpTestProject(t)
	mustWriteFile(t, defaultCfg, []byte("[[context]]\nname = \"house-rules\"\ntext = \"global prose\"\n"), 0o644)

	if err := ContextRemove(s, dir, false, "house-rules"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	cfg, err := config.ParseFile(projPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Contexts) != 1 || cfg.Contexts[0].Name != "!house-rules" {
		t.Fatalf("closure not written: %+v", cfg.Contexts)
	}
	if !strings.Contains(errw.String(), "closed context house-rules") {
		t.Errorf("closure not reported: %s", errw)
	}
}

func TestContextList(t *testing.T) {
	dir, _, _, s, _ := mcpTestProject(t)
	out := s.Out.(*bytes.Buffer)

	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no standing instructions declared") {
		t.Fatalf("empty list: %s", out)
	}
	out.Reset()

	if err := ContextAdd(s, dir, false, "house-rules", "Run the linter.\nNever force-push.\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := ContextAdd(s, dir, false, "conventions", "", "~/notes/conv.md"); err != nil {
		t.Fatal(err)
	}
	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `house-rules  "Run the linter."  (+1 more lines)`) {
		t.Fatalf("inline line wrong:\n%s", got)
	}
	if !strings.Contains(got, "conventions  (file: ~/notes/conv.md)") {
		t.Fatalf("file line wrong:\n%s", got)
	}
}

// The empty-editor refusal's remove hint only fits an EXISTING declaration —
// for a new name it pointed at a remove that would itself fail (QA finding
// 2026-07-25, dispatched 2026-07-26).
func TestContextAddEditorNewNameEmptyNoRemoveHint(t *testing.T) {
	dir, _, _, s, _ := mcpTestProject(t)
	orig := editProse
	defer func() { editProse = orig }()
	editProse = func(string) (string, error) { return "\n", nil }
	err := ContextAdd(s, dir, false, "fresh", "", "")
	if err == nil || !strings.Contains(err.Error(), "nothing added") {
		t.Fatalf("new-name empty buffer: err = %v", err)
	}
	if strings.Contains(err.Error(), "byre context remove") {
		t.Fatalf("hint names a remove that would fail: %v", err)
	}
}

// An editor round-trip that returns the seed unchanged says "unchanged" and
// writes nothing — "updated … joins at the next develop" claimed a write
// that didn't happen (the configui ^q class; QA finding 2026-07-25).
func TestContextAddEditorNoopSaysUnchanged(t *testing.T) {
	dir, projPath, _, s, errw := mcpTestProject(t)
	if err := ContextAdd(s, dir, false, "notes", "Prose.\n", ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(projPath)
	if err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	orig := editProse
	defer func() { editProse = orig }()
	editProse = func(seed string) (string, error) { return seed, nil }
	if err := ContextAdd(s, dir, false, "notes", "", ""); err != nil {
		t.Fatalf("no-op round trip must not error: %v", err)
	}
	if !strings.Contains(errw.String(), "context notes unchanged") {
		t.Fatalf("no-op must say unchanged: %s", errw)
	}
	if strings.Contains(errw.String(), "updated") || strings.Contains(errw.String(), "joins the agent's memory") {
		t.Fatalf("no-op must not claim a write: %s", errw)
	}
	after, _ := os.ReadFile(projPath)
	if !bytes.Equal(before, after) {
		t.Fatal("no-op round trip touched the file")
	}
}

// Adding a --file whose path doesn't exist yet is accepted (it can be
// created before the next develop) but never silently — the Claude Skills
// screen warns for the identical shape (QA finding 2026-07-25).
func TestContextAddMissingFileWarns(t *testing.T) {
	dir, _, _, s, errw := mcpTestProject(t)
	missing := filepath.Join(t.TempDir(), "not-yet.md")
	if err := ContextAdd(s, dir, false, "gone", "", missing); err != nil {
		t.Fatalf("a missing file must still be accepted: %v", err)
	}
	if !strings.Contains(errw.String(), "does not exist yet") {
		t.Fatalf("missing file not warned: %s", errw)
	}
	errw.Reset()
	present := filepath.Join(t.TempDir(), "here.md")
	mustWriteFile(t, present, []byte("x"), 0o644)
	if err := ContextAdd(s, dir, false, "here", "", present); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errw.String(), "does not exist") {
		t.Fatalf("existing file must not warn: %s", errw)
	}
}

// list attributes every row to the layer that speaks it and shows the
// cascade's shadows — overridden lower declarations and closures (Pete's
// ruling 2026-07-26: list is the "where did my snippet go?" surface).
func TestContextListAttributionAndShadows(t *testing.T) {
	dir, _, defaultCfg, s, _ := mcpTestProject(t)
	out := s.Out.(*bytes.Buffer)
	mustWriteFile(t, defaultCfg, []byte(`[[context]]
name = "lint"
text = "global lint"

[[context]]
name = "house"
text = "global house"
`), 0o644)
	if err := ContextAdd(s, dir, false, "lint", "project lint wins", ""); err != nil {
		t.Fatal(err)
	}
	if err := ContextAdd(s, dir, false, "own", "project only", ""); err != nil {
		t.Fatal(err)
	}
	if err := ContextRemove(s, dir, false, "house"); err != nil {
		t.Fatal(err)
	}
	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`lint  "project lint wins"  (project — overrides default)`,
		`own  "project only"  (project)`,
		"house  — removed by project  (was default)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"global lint"`) {
		t.Errorf("an overridden lower text must not render as effective:\n%s", got)
	}
}

// A closure a HIGHER layer re-opens is SPENT: it must not render as a
// shadow, and the re-opening declaration overrides nothing (the closure
// already consumed what sat below it). A first-seen scan got both wrong
// (codex review 2026-07-26): default→x, layer→!x, project→x showed a
// removed-row AND "overrides default" simultaneously with the shipping row.
func TestContextListSpentClosureNeitherShadowsNorOverrides(t *testing.T) {
	dir, projPath, defaultCfg, s, _ := mcpTestProject(t)
	out := s.Out.(*bytes.Buffer)
	home := filepath.Dir(defaultCfg)
	mustWriteFile(t, defaultCfg, []byte("[[context]]\nname = \"x\"\ntext = \"global x\"\n"), 0o644)
	if err := os.MkdirAll(filepath.Join(home, "layers", "l1"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(home, "layers", "l1", "layer.config"),
		[]byte("[[context]]\nname = \"!x\"\n"), 0o644)
	mustWriteFile(t, projPath, []byte("extends = \"l1\"\n\n[[context]]\nname = \"x\"\ntext = \"project x again\"\n"), 0o644)

	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `x  "project x again"  (project)`) {
		t.Errorf("re-opened row must attribute to project alone:\n%s", got)
	}
	if strings.Contains(got, "overrides") {
		t.Errorf("a re-open across a spent closure overrides nothing:\n%s", got)
	}
	if strings.Contains(got, "removed by") {
		t.Errorf("a spent closure must not render as a shadow:\n%s", got)
	}

	// And when the closure is the FINAL word (project drops its re-add), the
	// shadow names the closing layer and what it consumed.
	out.Reset()
	mustWriteFile(t, projPath, []byte("extends = \"l1\"\n"), 0o644)
	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "x  — removed by layer:l1  (was default)") {
		t.Errorf("final closure must attribute closer and victim:\n%s", got)
	}
}

// Within ONE layer, declarations apply before closures whatever the TOML
// order (mergeNamedDecls's split) — a layer carrying both `x` and `!x`
// ships nothing, and list must say so (codex review round 2).
func TestContextListSameLayerClosureWins(t *testing.T) {
	dir, projPath, defaultCfg, s, _ := mcpTestProject(t)
	out := s.Out.(*bytes.Buffer)
	mustWriteFile(t, defaultCfg, []byte("[[context]]\nname = \"x\"\ntext = \"global x\"\n"), 0o644)
	// Marker FIRST, declaration second: sequential replay would end open.
	mustWriteFile(t, projPath, []byte("[[context]]\nname = \"!x\"\n\n[[context]]\nname = \"x\"\ntext = \"same layer\"\n"), 0o644)
	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "x  — removed by project") {
		t.Errorf("same-layer closure must win regardless of order:\n%s", got)
	}
	// Resolution agrees (probed live): nothing ships, so no effective row.
	if strings.Contains(got, `"same layer"`) || strings.Contains(got, `"global x"`) {
		t.Errorf("no effective row may render beside the shadow:\n%s", got)
	}
}

// Duplicate `!x` markers in one layer are layer-valid; the second must not
// erase the first's victim attribution (codex review round 3).
func TestContextListDuplicateClosureKeepsVictim(t *testing.T) {
	dir, projPath, defaultCfg, s, _ := mcpTestProject(t)
	out := s.Out.(*bytes.Buffer)
	mustWriteFile(t, defaultCfg, []byte("[[context]]\nname = \"x\"\ntext = \"global x\"\n"), 0o644)
	mustWriteFile(t, projPath, []byte("[[context]]\nname = \"!x\"\n\n[[context]]\nname = \"!x\"\n"), 0o644)
	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "x  — removed by project  (was default)") {
		t.Errorf("duplicate marker erased the victim:\n%s", got)
	}
}
