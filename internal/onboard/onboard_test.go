package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fav is the common case: the stored favourite is valid, so it is also the
// effective (pre-selected) one.
func fav(v string) Favourite { return Favourite{Stored: v, Effective: v} }

func TestPickAcceptsDefaultsOnEmpty(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("\n\n\n"), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "claude" || c.SaveDefault {
		t.Fatalf("empty input should accept favourites, got %+v", c)
	}
	// Choosing what's already the default must not offer to save it as such.
	if strings.Contains(out.String(), "Save these") {
		t.Fatalf("save-as-default offered for a choice that IS the default:\n%s", out.String())
	}
}

// Retyping the favourites (rather than accepting them with Enter) is still the
// same choice — no save offer.
func TestPickRetypedDefaultsSkipSaveOffer(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("go\nclaude\n"), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "claude" || c.SaveDefault {
		t.Fatalf("retyped favourites wrong: %+v", c)
	}
	if strings.Contains(out.String(), "Save these") {
		t.Fatalf("save-as-default offered for retyped favourites:\n%s", out.String())
	}
}

// One axis differing is enough to make the offer (the save updates both
// scalars; the matching one is idempotent).
func TestPickOneAxisDifferingStillOffers(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("\ncodex\ny\n"), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "go" || c.Agent != "codex" || !c.SaveDefault {
		t.Fatalf("one-axis change should offer and save: %+v", c)
	}
	if !strings.Contains(out.String(), "Save these") {
		t.Fatalf("save offer missing for a differing choice:\n%s", out.String())
	}
}

// A STALE stored favourite (Effective dropped to "") must still get the save
// offer even when the user accepts the presented defaults: what's stored
// differs from the choice, so saving is NOT a no-op — and skipping it would
// leave the stale value to silently resurrect if its name turns valid again.
func TestPickStaleFavouriteStillOffers(t *testing.T) {
	var out bytes.Buffer
	// Stored template "old" no longer exists; the picker presents none.
	// The user accepts none + the existing agent, and answers y.
	c, err := Pick(&out, strings.NewReader("\n\ny\n"), []string{"go", "node"}, []string{"claude", "codex"},
		Favourite{Stored: "old", Effective: ""}, fav("claude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Save these") {
		t.Fatalf("save offer missing with a stale stored favourite:\n%s", out.String())
	}
	if c.Template != "" || c.Agent != "claude" || !c.SaveDefault {
		t.Fatalf("stale-favourite choice wrong: %+v", c)
	}
}

func TestPickChoosesAndSaves(t *testing.T) {
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("node\ncodex\ny\n"), []string{"go", "node"}, []string{"claude", "codex"}, fav("go"), fav("claude"))
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
	c, err := Pick(&out, strings.NewReader("rust\ngo\nclaude\n\n"), []string{"go"}, []string{"claude"}, fav("go"), fav("claude"))
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
	var out bytes.Buffer
	c, err := Pick(&out, strings.NewReader("none\nnone\n\n"), []string{"go"}, []string{"claude"}, fav(""), fav(""))
	if err != nil {
		t.Fatal(err)
	}
	if c.Template != "" || c.Agent != "" {
		t.Fatalf("none should map to empty, got %+v", c)
	}
	// With no stored favourites, none/none IS the stored state — saving would
	// be a no-op, so the offer must not appear.
	if c.SaveDefault || strings.Contains(out.String(), "Save these") {
		t.Fatalf("save offer must not appear for none/none with no favourites:\n%s", out.String())
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
	// A nested key with the same name in a [section] must not be edited.
	content := "agent = \"claude\"\n\n[env]\nagent = \"nested-should-be-ignored\"\n"
	out := setScalar(content, "agent", "codex")
	if !strings.Contains(out, `agent = "codex"`) || strings.Contains(out, `agent = "claude"`) {
		t.Fatalf("top-level agent not updated:\n%s", out)
	}
	if !strings.Contains(out, `agent = "nested-should-be-ignored"`) {
		t.Fatalf("nested key was corrupted:\n%s", out)
	}
}

func TestFavouritesReadsLiteralStrings(t *testing.T) {
	// TOML literal (single-quoted) strings are valid; the old regex reader
	// silently returned "" for them. A real parse must not.
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "default.config"), []byte("template = 'go'\nagent = 'claude'\n"), 0o644)
	tmpl, agent := Favourites(home)
	if tmpl != "go" || agent != "claude" {
		t.Fatalf("literal-string favourites misread: %q %q", tmpl, agent)
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
