package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// refHit is one config that references (or cannot be proven not to reference)
// a candidate package ID.
type refHit struct {
	Where   string // human location: "project my-app" / "default.config"
	Path    string
	Guarded bool // unparsable config counted as a hit (the guarded path)
}

// scanReferences is the conservative reference extractor: syntactic
// per-layer collection (skills, agent, template, ! markers) canonicalized
// through the alias table -- never a full effective resolution, because the
// configs that matter most (dangling refs, INVALID packages) are exactly the
// ones fail-fast resolution dies on. A config that cannot be parsed well
// enough to PROVE it does not reference the candidate counts as a hit.
// Scope: default.config, every named layer under ~/.byre/layers/, and every
// project config under ~/.byre/projects/ -- the whole stored cascade.
// A local file walk and catalog lookups; no engine calls. (Templates are
// shape and reference no packages -- the template KEY itself is the
// only template reference to follow.)
func scanReferences(home string, cat *packages.Catalog, id string) []refHit {
	var hits []refHit
	check := func(where, path string) {
		st, err := os.Stat(path)
		if err != nil || st.IsDir() {
			return // no config here = provably no reference
		}
		cfg, err := config.ParseFile(path)
		if err != nil {
			hits = append(hits, refHit{Where: where, Path: path, Guarded: true})
			return
		}
		if configReferences(cat, cfg, id) {
			hits = append(hits, refHit{Where: where, Path: path})
		}
	}
	subdirs := func(dir string) []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		return names
	}

	check("default.config", filepath.Join(home, "default.config"))
	for _, n := range subdirs(config.LayersDir(home)) {
		check("layer "+n, filepath.Join(config.LayersDir(home), n, config.LayerConfigName))
	}
	for _, n := range subdirs(filepath.Join(home, "projects")) {
		check("project "+n, filepath.Join(home, "projects", n, "byre.config"))
	}
	return hits
}

// configReferences reports whether one parsed layer names id in any package
// position, aliases expanded, ! markers included (a removal marker is still a
// reference -- it names the package).
func configReferences(cat *packages.Catalog, cfg config.Config, id string) bool {
	names := append([]string{}, cfg.Skills...)
	if cfg.Agent != "" {
		names = append(names, cfg.Agent)
	}
	if cfg.Template != "" {
		names = append(names, cfg.Template)
	}
	for _, n := range names {
		n = strings.TrimPrefix(strings.TrimSpace(n), "!")
		if n == "" || n == config.NoneLabel {
			continue
		}
		if cat.ExpandAlias(n) == id {
			return true
		}
	}
	return false
}

// renderRefHits is the shared "these boxes are affected" block for install
// replacement/activation and uninstall prompts.
func renderRefHits(hits []refHit) string {
	var b strings.Builder
	for _, h := range hits {
		if h.Guarded {
			fmt.Fprintf(&b, "  %s  (could not parse %s -- counted as a reference)\n", h.Where, h.Path)
		} else {
			fmt.Fprintf(&b, "  %s\n", h.Where)
		}
	}
	return b.String()
}
