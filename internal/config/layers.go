// Named layers: user-authored cascade layers at
// ~/.byre/layers/<name>/layer.config, chained through the scalar `extends`
// key. Any layer file — and the project config, which is the chain's leaf —
// may name at most ONE parent; byre walks to the root and merges root-first,
// so the cascade is
//
//	default ⊕ template ⊕ chain(root … parent) ⊕ project
//
// Layers are plain files, not packages: no [package] table, no version, no
// install verbs. They carry the full config vocabulary except `template`
// (shape selection has exactly one owner, the project config) and are
// resolved live at every develop — editing a layer changes every project
// that extends it on its next develop.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/pjlsergeant/byre/internal/packages"
)

// LayerConfigName is the fixed per-layer config filename (directory form, so
// a layer may carry payload files beside it for `files = {...}`).
const LayerConfigName = "layer.config"

// LayersDir is where named layers live under a byre home.
func LayersDir(home string) string { return filepath.Join(home, "layers") }

// LayerPath is the on-disk path of a named layer's config file.
// Callers must ValidateLayerName first — the name becomes a path element.
func LayerPath(home, name string) string {
	return filepath.Join(LayersDir(home), name, LayerConfigName)
}

// layerNameRe is the package-ID segment grammar: layers aren't packages, but
// their names live in the same mental namespace (extends values, dir names),
// so they follow the same shape. Single segment only — no owner/name.
var layerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateLayerName checks a layer name against the grammar. It is also the
// path-traversal gate: every LayerPath caller runs it first, so a name can
// never carry a separator or "..".
func ValidateLayerName(name string) error {
	if name == "" {
		return errors.New("layer name is empty")
	}
	if name == NoneLabel {
		return fmt.Errorf("layer name %q is reserved (config sentinel)", name)
	}
	if !layerNameRe.MatchString(name) {
		return fmt.Errorf("layer name %q: want lowercase [a-z0-9-], starting alphanumeric, max 64 chars", name)
	}
	return nil
}

// ReservedLayerName reports why a layer may not take this name ("" = free):
// bundled and retired package bare names are off-limits so a layer can never
// look like the template or skill of the same name. Checked at `byre layer
// new` AND on every chain walk — a hand-dropped squatter dir is never loaded.
func ReservedLayerName(cat *packages.Catalog, name string) string {
	if cat == nil {
		return ""
	}
	return cat.ProtectedReason(name)
}

// NamedLayer is one loaded chain layer.
type NamedLayer struct {
	Name   string
	Config Config
}

// ParseLayerBody is the stage-2 layer check used by the chain walk and
// `byre layer validate`: ban `template` (even empty — shape selection has
// exactly one owner, the project config), strict-parse as Config,
// ValidateLayer. `extends` is the only pointer key a layer may carry.
func ParseLayerBody(raw []byte) (Config, error) {
	if err := rejectLayerKeys(raw); err != nil {
		return Config{}, err
	}
	c, err := Parse(raw)
	if err != nil {
		return Config{}, err
	}
	if err := c.ValidateLayer(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// rejectLayerKeys bans the `template` KEY in a layer file when present at
// all — even empty (same stance as rejectTemplateComposition).
func rejectLayerKeys(body []byte) error {
	var probe struct {
		Template string `toml:"template"`
	}
	md, err := toml.Decode(string(body), &probe)
	if err != nil {
		// Let Parse surface the real syntax error.
		return nil
	}
	if md.IsDefined("template") {
		return fmt.Errorf("shape selection belongs to the project config (template is not allowed in a layer file)")
	}
	return nil
}

// LoadExtendsChain walks an `extends` pointer to its root and returns the
// chain ROOT-FIRST (merge order). leafExtends is the extends value of the
// chain's leaf (the project config, or the layer under validation); "" means
// no chain. Cycles and dangling parents are hard errors: the cycle error
// names the loop, the dangling error names the exact path to create.
func LoadExtendsChain(home string, cat *packages.Catalog, leafExtends string) ([]NamedLayer, error) {
	var chain []NamedLayer
	seen := map[string]bool{}
	var walked []string // leaf-parent-first, for the cycle message
	for name := leafExtends; name != ""; {
		if err := ValidateLayerName(name); err != nil {
			return nil, fmt.Errorf("extends: %w", err)
		}
		if reason := ReservedLayerName(cat, name); reason != "" {
			return nil, fmt.Errorf("extends: layer name %q is reserved (%s); a layer dir squatting on it is never loaded", name, reason)
		}
		if seen[name] {
			return nil, fmt.Errorf("extends cycle: %s -> %s", strings.Join(walked, " -> "), name)
		}
		seen[name] = true
		walked = append(walked, name)
		path := LayerPath(home, name)
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("layer %q not found — create %s", name, path)
		}
		if err != nil {
			return nil, err
		}
		c, err := ParseLayerBody(raw)
		if err != nil {
			return nil, fmt.Errorf("layer %q (%s): %w", name, path, err)
		}
		// The walk goes leafward -> rootward; merge order is root-first.
		chain = append([]NamedLayer{{Name: name, Config: c}}, chain...)
		name = c.Extends
	}
	return chain, nil
}

// ChainNames renders a loaded chain root-first ("torn -> torn-frontend"),
// the shape every report prints.
func ChainNames(chain []NamedLayer) []string {
	names := make([]string, len(chain))
	for i, nl := range chain {
		names[i] = nl.Name
	}
	return names
}

// LayerInfo is one row of ListLayers: loadable layers and broken ones
// (parse errors, reserved-name squatters, dangling extends). Mirrors the
// package catalog's list-with-reason shape without being one — layers are
// plain files.
type LayerInfo struct {
	Name    string
	Extends string // the layer's own parent pointer ("" = chain root)
	Reason  string // "" = loadable; otherwise why the layer is never loaded
}

// ListLayers scans home's layers dir, sorted by name (ReadDir order). A
// missing dir is an empty list.
func ListLayers(home string, cat *packages.Catalog) ([]LayerInfo, error) {
	entries, err := os.ReadDir(LayersDir(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []LayerInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		li := LayerInfo{Name: name, Reason: layerProblem(home, cat, name)}
		// The parent pointer, for display: best-effort even when the chain
		// above is broken (the reason already says why).
		if raw, err := os.ReadFile(LayerPath(home, name)); err == nil {
			if c, err := ParseLayerBody(raw); err == nil {
				li.Extends = c.Extends
			}
		}
		out = append(out, li)
	}
	return out, nil
}

// LoadableLayers returns every loadable named layer's raw config keyed by
// name (parent pointers included) — the config editor's provenance input.
// Broken layers are simply absent; `byre layer list` names them and why.
func LoadableLayers(home string, cat *packages.Catalog) (map[string]Config, error) {
	infos, err := ListLayers(home, cat)
	if err != nil {
		return nil, err
	}
	out := map[string]Config{}
	for _, li := range infos {
		if li.Reason != "" {
			continue
		}
		raw, err := os.ReadFile(LayerPath(home, li.Name))
		if err != nil {
			continue
		}
		if c, err := ParseLayerBody(raw); err == nil {
			out[li.Name] = c
		}
	}
	return out, nil
}

// layerProblem returns why a layer dir can't be loaded ("" = loadable):
// bad or reserved name, missing/broken layer.config, or a broken chain
// above it (cycle, dangling or invalid parent).
func layerProblem(home string, cat *packages.Catalog, name string) string {
	if err := ValidateLayerName(name); err != nil {
		return err.Error()
	}
	if reason := ReservedLayerName(cat, name); reason != "" {
		return fmt.Sprintf("name is reserved (%s); never loaded", reason)
	}
	if _, err := LoadExtendsChain(home, cat, name); err != nil {
		return err.Error()
	}
	return ""
}
