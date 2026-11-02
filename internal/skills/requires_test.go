package skills

import (
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// requires is a dependency the user enables, never byre: a skill whose
// requirement is not in the enabled set refuses at resolve, naming the
// skill, the requirement, and the key that fixes it.
func TestResolveRefusesUnmetRequires(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "wrapper", "requires = [\"fake\"]\n", nil)
	writeSkill(t, dir, "fake", fakeAgentSkill, nil)
	writeSkill(t, dir, "sample", sampleSkill, nil)
	cat := catFor(t, dir)

	res, err := Resolve(config.Config{Skills: []string{"wrapper", "sample"}}, cat)
	if err == nil {
		t.Fatalf("an unmet requirement must refuse, resolved %v", res.Names())
	}
	for _, want := range []string{`"wrapper" requires "fake"`, "not enabled", "skills = [...]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q: %v", want, err)
		}
	}
}

// Met through either enabling spelling -- the skills list or the agent key --
// the requirement resolves, and nothing is added to the set.
func TestResolveMetRequires(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "wrapper", "requires = [\"fake\"]\n", nil)
	writeSkill(t, dir, "fake", fakeAgentSkill, nil)
	cat := catFor(t, dir)
	for _, cfg := range []config.Config{
		{Skills: []string{"wrapper", "fake"}},
		{Skills: []string{"wrapper"}, Agent: "fake"},
	} {
		res, err := Resolve(cfg, cat)
		if err != nil {
			t.Fatalf("%+v: %v", cfg, err)
		}
		if got := strings.Join(res.Names(), ","); got != "wrapper,fake" {
			t.Fatalf("%+v: resolved %q, want exactly the enabled pair", cfg, got)
		}
	}
}

// A skill may not require itself: refused at load, which validate and develop
// both run. (Resolve repeats the check after alias expansion, for the alias
// spelling of a bundled skill's own name.)
func TestRequiresSelfRefused(t *testing.T) {
	dir := testHome(t)
	writeSkill(t, dir, "loop", "requires = [\"loop\"]\n", nil)
	cat := catFor(t, dir)
	if _, err := Load(cat, "loop"); err == nil || !strings.Contains(err.Error(), "requires itself") {
		t.Fatalf("a self-requirement must refuse at load naming the rule, got %v", err)
	}
	if _, err := Resolve(config.Config{Skills: []string{"loop"}}, cat); err == nil || !strings.Contains(err.Error(), "requires itself") {
		t.Fatalf("a self-requirement must refuse at resolve naming the rule, got %v", err)
	}
}

// A [package] manifest states its own id, so the bytes boundary that install
// and catalog ingest share refuses the self-requirement too.
func TestRequiresSelfRefusedAtTheBytesBoundary(t *testing.T) {
	raw := []byte("requires = [\"pete/tool\"]\n\n[package]\nid = \"pete/tool\"\nversion = \"1.0.0\"\nkind = \"skill\"\n")
	if err := ValidatePrimaryBytes(raw); err == nil || !strings.Contains(err.Error(), "requires itself") {
		t.Fatalf("a self-requirement must refuse at the bytes boundary naming the rule, got %v", err)
	}
}

// requires entries are skill names: the id grammar holds and a duplicate is
// a typo. An unknown name is NOT a manifest error -- only the catalog knows.
func TestRequiresGrammar(t *testing.T) {
	for body, want := range map[string]string{
		"requires = [\"!codex\"]\n":           "must not start with '!'",
		"requires = [\"none\"]\n":             "reserved",
		"requires = [\"a/b/c\"]\n":            "at most one '/'",
		"requires = [\"codex\", \"codex\"]\n": "listed twice",
	} {
		_, err := ParsePrimaryBytes([]byte(body))
		if err == nil || !strings.Contains(err.Error(), "requires") || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want a requires refusal naming %q, got %v", body, want, err)
		}
	}
	if _, err := ParsePrimaryBytes([]byte("requires = [\"no-such-skill\", \"someone/else\"]\n")); err != nil {
		t.Fatalf("an unknown name is not a manifest error: %v", err)
	}
}
