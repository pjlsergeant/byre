package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeLayer writes ~/.byre/layers/<name>/layer.config.
func writeLayer(t *testing.T, home, name, content string) {
	t.Helper()
	dir := filepath.Join(LayersDir(home), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, LayerConfigName), content)
}

// The chain sits between the template and the project:
// default ⊕ template ⊕ chain(root … parent) ⊕ project. Each step follows
// the ordinary merge rules — scalars last-wins, lists union, removals apply
// against everything merged so far.
func TestLoadCascadeWithExtendsChain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeFile(t, filepath.Join(home, "default.config"),
		"base = \"debian:bookworm\"\napt = [\"git\", \"curl\"]\n")
	tmplDir := filepath.Join(home, "templates", "node")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmplDir, "template.config"),
		"base = \"node:22\"\napt = [\"build-essential\"]\n")

	// Root layer: the employer baseline. Full vocabulary — skills, env, egress.
	writeLayer(t, home, "torn",
		"apt = [\"!curl\", \"jq\"]\nskills = [\"torn-skill\"]\negress = [\"api.torn.test\"]\n[env]\nTORN = \"1\"\n")
	// Child layer overrides the template's base and extends the root.
	writeLayer(t, home, "torn-frontend",
		"extends = \"torn\"\nbase = \"node:20\"\n[env]\nTORN_FE = \"1\"\n")
	writeProjectCfg(t, proj,
		"template = \"node\"\nextends = \"torn-frontend\"\nskills = [\"proj\"]\napt = [\"!jq\"]\n")

	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Base != "node:20" {
		t.Errorf("base: chain layer should override template: got %q", cfg.Base)
	}
	// default's curl removed by torn; torn's jq removed by the project.
	if want := []string{"git", "build-essential"}; !reflect.DeepEqual(cfg.Apt, want) {
		t.Errorf("apt across chain: got %v want %v", cfg.Apt, want)
	}
	if want := []string{"torn-skill", "proj"}; !reflect.DeepEqual(cfg.Skills, want) {
		t.Errorf("skills across chain: got %v want %v", cfg.Skills, want)
	}
	if cfg.Env["TORN"] != "1" || cfg.Env["TORN_FE"] != "1" {
		t.Errorf("env from chain layers: got %v", cfg.Env)
	}
	if want := []string{"api.torn.test"}; !reflect.DeepEqual(cfg.Egress, want) {
		t.Errorf("egress from chain: got %v want %v", cfg.Egress, want)
	}
	// extends is consumed by resolution — never part of a resolved config.
	if cfg.Extends != "" {
		t.Errorf("resolved config must not carry extends, got %q", cfg.Extends)
	}
}

func TestExtendsCycleIsNamedError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeLayer(t, home, "a", "extends = \"b\"\n")
	writeLayer(t, home, "b", "extends = \"a\"\n")
	writeProjectCfg(t, proj, "extends = \"a\"\n")

	_, err := Load(proj)
	if err == nil {
		t.Fatal("extends cycle must be a hard error")
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "a -> b -> a") {
		t.Errorf("cycle error should name the loop, got: %v", err)
	}
}

func TestExtendsDanglingNamesThePathToCreate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()
	writeProjectCfg(t, proj, "extends = \"torn\"\n")

	_, err := Load(proj)
	if err == nil {
		t.Fatal("dangling extends must be a hard error")
	}
	want := LayerPath(home, "torn")
	if !strings.Contains(err.Error(), want) {
		t.Errorf("dangling error should name the exact path to create (%s), got: %v", want, err)
	}
}

// A layer file may not select a shape: template is banned even when empty.
func TestLayerFileBansTemplateKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeLayer(t, home, "torn", "template = \"\"\n")
	writeProjectCfg(t, proj, "extends = \"torn\"\n")

	_, err := Load(proj)
	if err == nil || !strings.Contains(err.Error(), "template is not allowed in a layer file") {
		t.Fatalf("template key in a layer must fail loudly, got: %v", err)
	}
}

// A distributable template may not pull in machine-local layers.
func TestTemplateBansExtendsKey(t *testing.T) {
	if _, err := ParseTemplateBody([]byte("extends = \"torn\"\n")); err == nil {
		t.Fatal("extends in template.config must be a validation error")
	}
	if _, err := ParseTemplateBody([]byte("extends = \"\"\n")); err == nil {
		t.Fatal("even an empty extends key in template.config must be a validation error")
	}
}

// default.config has no chain slot: the chain hangs off the project config.
func TestDefaultConfigBansExtends(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeFile(t, filepath.Join(home, "default.config"), "extends = \"torn\"\n")
	writeLayer(t, home, "torn", "")

	_, err := Load(proj)
	if err == nil || !strings.Contains(err.Error(), "default.config") {
		t.Fatalf("extends in default.config must fail loudly, got: %v", err)
	}
}

// A layer may not take a bundled or retired package name; a squatter dir on
// such a name is never loaded.
func TestExtendsReservedNameRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	// "codereview" is permanently retired (packages.RetiredNames) — present
	// in every catalog without needing a bundled fixture.
	writeLayer(t, home, "codereview", "apt = [\"jq\"]\n")
	writeProjectCfg(t, proj, "extends = \"codereview\"\n")

	_, err := Load(proj)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved layer name must refuse to load, got: %v", err)
	}
}

func TestValidateLayerNameGrammar(t *testing.T) {
	good := []string{"torn", "torn-frontend", "a", "0x", "very-long-but-fine"}
	for _, n := range good {
		if err := ValidateLayerName(n); err != nil {
			t.Errorf("ValidateLayerName(%q): unexpected error %v", n, err)
		}
	}
	bad := []string{"", "none", "Torn", "torn/frontend", "../evil", ".hidden", "-lead", "has space", "!torn"}
	for _, n := range bad {
		if err := ValidateLayerName(n); err == nil {
			t.Errorf("ValidateLayerName(%q): expected error", n)
		}
	}
}

// A layer's own extends value is name-checked at save (ValidateLayer), and a
// resolved config carrying extends is rejected (Validate).
func TestExtendsValidation(t *testing.T) {
	if err := (Config{Extends: "../evil"}).ValidateLayer(); err == nil {
		t.Error("bad extends name must fail ValidateLayer")
	}
	if err := (Config{Extends: "torn"}).ValidateLayer(); err != nil {
		t.Errorf("good extends rejected by ValidateLayer: %v", err)
	}
	if err := (Config{Extends: "torn"}).Validate(); err == nil {
		t.Error("extends surviving to a resolved config must fail Validate")
	}
}

// [sources] hints from a chain layer are attributed to it.
func TestChainLayerSourcesAttribution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeLayer(t, home, "torn",
		"skills = [\"torn/tooling\"]\n[sources.\"torn/tooling\"]\nuri = \"https://example.test/tooling/skill.toml\"\n")
	writeProjectCfg(t, proj, "extends = \"torn\"\n")

	cfg, err := Load(proj)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := cfg.Sources["torn/tooling"]
	if !ok {
		t.Fatalf("layer [sources] hint missing from resolved config: %v", cfg.Sources)
	}
	if h.From != "layer torn" {
		t.Errorf("hint attribution: got %q want %q", h.From, "layer torn")
	}
}

func TestListLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)

	writeLayer(t, home, "torn", "apt = [\"jq\"]\n")
	writeLayer(t, home, "torn-frontend", "extends = \"torn\"\n")
	writeLayer(t, home, "broken", "not toml [\n")
	writeLayer(t, home, "orphan", "extends = \"missing\"\n")
	writeLayer(t, home, "codereview", "") // reserved-name squatter
	// A stray file in layers/ is ignored (layers are directories).
	writeFile(t, filepath.Join(LayersDir(home), "README.txt"), "hi")

	infos, err := ListLayers(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, li := range infos {
		got[li.Name] = li.Reason
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 layer rows, got %v", got)
	}
	if got["torn"] != "" || got["torn-frontend"] != "" {
		t.Errorf("loadable layers should have no problem reason: %v", got)
	}
	if got["broken"] == "" {
		t.Error("parse-broken layer should carry a reason")
	}
	if !strings.Contains(got["orphan"], LayerPath(home, "missing")) {
		t.Errorf("dangling layer reason should name the missing path, got %q", got["orphan"])
	}
	// Reserved squatter needs a catalog to be flagged; without one the
	// name-shape checks still run.
	cat, err := catalogFor(home)
	if err != nil {
		t.Fatal(err)
	}
	infos, err = ListLayers(home, cat)
	if err != nil {
		t.Fatal(err)
	}
	for _, li := range infos {
		if li.Name == "codereview" && !strings.Contains(li.Reason, "reserved") {
			t.Errorf("reserved squatter should be flagged, got %q", li.Reason)
		}
	}
}

// ListLayers on a home with no layers dir is an empty list, not an error.
func TestListLayersMissingDir(t *testing.T) {
	infos, err := ListLayers(t.TempDir(), nil)
	if err != nil || infos != nil {
		t.Fatalf("missing layers dir: got %v, %v", infos, err)
	}
}
