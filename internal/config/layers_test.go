package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pjlsergeant/byre/internal/packages"
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
	// Banned by presence, empty or not. Naming the rule matters here: a dozen
	// other template rules would also reject a body, and the sibling
	// TestLayerFileBansTemplateKey has always pinned its fragment.
	const want = "extends is not allowed in template.config"
	for _, body := range []string{"extends = \"torn\"\n", "extends = \"\"\n"} {
		_, err := ParseTemplateBody([]byte(body))
		if err == nil {
			t.Fatalf("%q: want the extends rule to fire", body)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: wrong rule fired: %v, want it to name %q", body, err, want)
		}
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

// withBundledFixture points config's catalog hook at a minimal bundled FS
// (one template, "go") so reserved-name checks have a bundled alias to hit.
func withBundledFixture(t *testing.T) {
	t.Helper()
	prev := CatalogLoader
	bundled := fstest.MapFS{
		"templates/go/template.config": &fstest.MapFile{Data: []byte("# description: fixture\n")},
	}
	CatalogLoader = func(h string) (*packages.Catalog, error) {
		return packages.LoadCatalog(h, bundled, "0.2.0", "0.2.0", packages.Stage2Hooks{Template: ValidateTemplateBytes})
	}
	t.Cleanup(func() { CatalogLoader = prev })
}

// A layer may not take a BUNDLED package bare name; a squatter dir on such a
// name is never loaded. Retired names are deliberately legal (layers are a
// new namespace — nothing predates it to protect; ruled 2026-07-16).
func TestExtendsReservedNameRefused(t *testing.T) {
	withBundledFixture(t)
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeLayer(t, home, "go", "apt = [\"jq\"]\n")
	writeProjectCfg(t, proj, "extends = \"go\"\n")

	if _, err := Load(proj); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("bundled layer name must refuse to load, got: %v", err)
	}

	// "codereview" is retired (packages.RetiredNames) — legal as a layer.
	proj2 := t.TempDir()
	writeLayer(t, home, "codereview", "apt = [\"jq\"]\n")
	writeProjectCfg(t, proj2, "extends = \"codereview\"\n")
	cfg, err := Load(proj2)
	if err != nil {
		t.Fatalf("retired names must be legal layer names: %v", err)
	}
	if !slices.Contains(cfg.Apt, "jq") {
		t.Errorf("retired-named layer should contribute: %v", cfg.Apt)
	}
}

func TestValidateLayerNameGrammar(t *testing.T) {
	good := []string{"torn", "torn-frontend", "a", "0x", "very-long-but-fine"}
	for _, n := range good {
		if err := ValidateLayerName(n); err != nil {
			t.Errorf("ValidateLayerName(%q): unexpected error %v", n, err)
		}
	}
	// Each bad name names the rule that rejects it, so a shape failure cannot
	// silently stand in for the reserved-name or empty check.
	bad := map[string]string{
		"":              "layer name is empty",
		"none":          "is reserved",
		"Torn":          "want lowercase",
		"torn/frontend": "want lowercase",
		"../evil":       "want lowercase",
		".hidden":       "want lowercase",
		"-lead":         "want lowercase",
		"has space":     "want lowercase",
		"!torn":         "want lowercase",
	}
	for n, want := range bad {
		err := ValidateLayerName(n)
		if err == nil {
			t.Errorf("ValidateLayerName(%q): expected error", n)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateLayerName(%q) = %v, want it to name %q", n, err, want)
		}
	}
}

// A layer's own extends value is name-checked at save (ValidateLayer), and a
// resolved config carrying extends is rejected (Validate).
func TestExtendsValidation(t *testing.T) {
	if err := (Config{Extends: "../evil"}).ValidateLayer(); err == nil {
		t.Error("bad extends name must fail ValidateLayer")
	} else if !strings.Contains(err.Error(), "extends: layer name") {
		t.Errorf("wrong rule fired for a bad extends name: %v", err)
	}
	if err := (Config{Extends: "torn"}).ValidateLayer(); err != nil {
		t.Errorf("good extends rejected by ValidateLayer: %v", err)
	}
	if err := (Config{Extends: "torn"}).Validate(); err == nil {
		t.Error("extends surviving to a resolved config must fail Validate")
	} else if !strings.Contains(err.Error(), "only meaningful in a cascade layer") {
		t.Errorf("wrong rule fired for a resolved extends: %v", err)
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
	withBundledFixture(t)
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)

	writeLayer(t, home, "torn", "apt = [\"jq\"]\n")
	writeLayer(t, home, "torn-frontend", "extends = \"torn\"\n")
	writeLayer(t, home, "broken", "not toml [\n")
	writeLayer(t, home, "orphan", "extends = \"missing\"\n")
	writeLayer(t, home, "go", "") // bundled-name squatter
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
		if li.Name == "go" && !strings.Contains(li.Reason, "reserved") {
			t.Errorf("bundled-name squatter should be flagged, got %q", li.Reason)
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
