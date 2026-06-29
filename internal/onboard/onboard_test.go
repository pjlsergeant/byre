package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickAcceptsDefaultsOnEmpty(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("\n\n\n"), []string{"go", "node"}, []string{"claude", "codex"}, "go", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "claude" || c.SaveDefault {
		t.Fatalf("empty input should accept favourites, got %+v", c)
	}
}

func TestPickChoosesAndSaves(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("node\ncodex\ny\n"), []string{"go", "node"}, []string{"claude", "codex"}, "go", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "node" || c.Agent != "codex" || !c.SaveDefault {
		t.Fatalf("explicit choices wrong: %+v", c)
	}
}

func TestAskAxisPromptsOneAxis(t *testing.T) {
	var out bytes.Buffer
	// Empty input accepts the favourite.
	v, err := AskAxis(&out, strings.NewReader("\n"), "Template", []string{"go", "node"}, "node")
	if err != nil {
		t.Fatal(err)
	}
	if v != "node" {
		t.Fatalf("empty should accept favourite, got %q", v)
	}
	// Explicit "none" returns "".
	v, err = AskAxis(&out, strings.NewReader("none\n"), "Template", []string{"go", "node"}, "node")
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("none should be empty, got %q", v)
	}
}

func TestPickReprompsOnInvalid(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("rust\ngo\nclaude\n\n"), []string{"go"}, []string{"claude"}, "go", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" {
		t.Fatalf("should reprompt past invalid, got %+v", c)
	}
	if !strings.Contains(out.String(), "not one of") {
		t.Errorf("expected an invalid-choice message: %s", out.String())
	}
}

func TestPickNone(t *testing.T) {
	c, err := Pick(&bytes.Buffer{}, strings.NewReader("none\nnone\n\n"), []string{"go"}, []string{"claude"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "" || c.Agent != "" {
		t.Fatalf("none should map to empty, got %+v", c)
	}
}

func TestWriteProjectConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store", "byre.config") // parent created by WriteProjectConfig
	if err := WriteProjectConfig(path, "go", "claude"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, `template = "go"`) || !strings.Contains(s, `agent = "claude"`) {
		t.Fatalf("byre.config content: %s", s)
	}
	// Refuses to overwrite.
	if err := WriteProjectConfig(path, "node", "codex"); err == nil {
		t.Fatal("should refuse to overwrite an existing byre.config")
	}
}

func TestWriteProjectConfigOmitsNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")
	if err := WriteProjectConfig(path, "", "claude"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "template") {
		t.Errorf("empty template should be omitted: %s", b)
	}
}

func TestSaveDefaultPreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte("base = \"debian:bookworm\"\nagent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveDefault(home, "go", "codex"); err != nil {
		t.Fatal(err)
	}
	tmpl, agent := Favourites(home)
	if tmpl != "go" || agent != "codex" {
		t.Fatalf("favourites not updated: %q %q", tmpl, agent)
	}
	b, _ := os.ReadFile(filepath.Join(home, "default.config"))
	if !strings.Contains(string(b), `base = "debian:bookworm"`) {
		t.Errorf("should preserve base: %s", b)
	}
}

func TestScalarEditingIsTopLevelOnly(t *testing.T) {
	// A nested key with the same name in a [section] must not be read or edited.
	content := "agent = \"claude\"\n\n[env]\nagent = \"nested-should-be-ignored\"\n"
	if got := getScalar(content, "agent"); got != "claude" {
		t.Fatalf("getScalar read a nested key: %q", got)
	}
	out := setScalar(content, "agent", "codex")
	if getScalar(out, "agent") != "codex" {
		t.Fatalf("top-level agent not updated:\n%s", out)
	}
	if !strings.Contains(out, `agent = "nested-should-be-ignored"`) {
		t.Fatalf("nested key was corrupted:\n%s", out)
	}
}

func TestSaveDefaultCreatesWhenAbsent(t *testing.T) {
	home := t.TempDir()
	if err := SaveDefault(home, "node", "claude"); err != nil {
		t.Fatal(err)
	}
	tmpl, agent := Favourites(home)
	if tmpl != "node" || agent != "claude" {
		t.Fatalf("favourites = %q %q", tmpl, agent)
	}
}

func TestSaveDefaultRemovesOnEmpty(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "default.config"), []byte("template = \"go\"\nagent = \"claude\"\n"), 0o644)
	if err := SaveDefault(home, "", "claude"); err != nil { // none template
		t.Fatal(err)
	}
	if tmpl, _ := Favourites(home); tmpl != "" {
		t.Fatalf("empty template should be removed, got %q", tmpl)
	}
}

func TestListTemplates(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"go", "python"} {
		td := filepath.Join(dir, n)
		os.MkdirAll(td, 0o755)
		os.WriteFile(filepath.Join(td, "template.config"), []byte("base = \"x\"\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "empty"), 0o755) // no template.config -> excluded
	got := ListTemplates(dir)
	if len(got) != 2 || got[0] != "go" || got[1] != "python" {
		t.Fatalf("ListTemplates = %v", got)
	}
}
