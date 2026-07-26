package commands

import (
	"bytes"
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

// `byre context list` ends with the delivery verdict — the same renderer
// status uses, so the two surfaces cannot drift. With the bundled claude
// agent selected the vouch resolves to inject.
func TestContextListShowsDeliveryVerdict(t *testing.T) {
	dir, projPath, _, s, _ := mcpTestProject(t)
	out := s.Out.(*bytes.Buffer)
	mustWriteFile(t, projPath, []byte("agent = \"claude\"\n"), 0o644)
	if err := ContextAdd(s, dir, false, "house-rules", "Run the linter.\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := ContextList(s, dir); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "injected by the agent command from /etc/byre/agent-context.md") {
		t.Fatalf("delivery verdict missing:\n%s", got)
	}
}
