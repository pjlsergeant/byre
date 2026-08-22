package config

// cascadefile.go walks the cascade as FILES rather than as one merged
// config: default.config, the selected template, the extends chain root-first,
// then the project config. Two things need that view — attribution ("which
// layer declared this?") and file-local tables, which by design never reach a
// merged Config at all.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// CascadeFile is one raw cascade layer as the FILE that carries it, in merge
// order (later wins).
type CascadeFile struct {
	// Label is the attribution spelling every surface prints: "default",
	// "template:<name>", "layer:<name>", "project".
	Label string
	// Path is the physical file. Empty for a bundled template, which is read
	// from embedded bytes and has no host path.
	Path string
	// Raw is the file's bytes — what a consumer parses a FILE-LOCAL table out
	// of. The merged Config cannot answer for such a table: merging it is
	// exactly what must not happen.
	Raw []byte
	// Cfg is the parsed layer, unmerged.
	Cfg Config
}

// CascadeFiles returns the cascade layers of projectDir as files, in merge
// order. A sublayer that fails to load simply drops out (a caller gets fewer
// labels; develop still fails loudly on a genuinely broken cascade); only an
// unreadable or invalid PROJECT layer errors, since every caller is about to
// speak for its content.
func CascadeFiles(projectDir string) ([]CascadeFile, error) {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return nil, err
	}
	projPath := filepath.Join(paths.Dir, ProjectConfigName)
	projRaw, projCfg, err := readCascadeFile(projPath, false)
	if err != nil {
		return nil, err
	}

	var out []CascadeFile
	defPath := filepath.Join(paths.Home, "default.config")
	if raw, cfg, derr := readCascadeFile(defPath, true); derr == nil {
		out = append(out, CascadeFile{Label: "default", Path: defPath, Raw: raw, Cfg: cfg})
	}
	// The catalog error is not a droppable sublayer: without a catalog the
	// template and every extends layer silently vanish from the walk, and a
	// surface speaking for the cascade (credential rows, compat warnings)
	// would describe a cascade that is not this project's. Fail the way
	// Load fails.
	cat, err := catalogFor(paths.Home)
	if err != nil {
		return nil, fmt.Errorf("cannot build the package catalog for the cascade walk: %w", err)
	}
	if t := FromNone(projCfg.Template); t != "" && cat != nil {
		if f, terr := templateCascadeFile(cat, t); terr == nil {
			out = append(out, f)
		}
	}
	if chain, cerr := LoadExtendsChain(paths.Home, cat, projCfg.Extends); cerr == nil {
		for _, nl := range chain {
			path := LayerPath(paths.Home, nl.Name)
			// The chain walk already parsed this file; re-read it only for the
			// bytes. An unreadable file here (raced deletion) costs the
			// file-local tables, not the layer.
			raw, _ := hostopen.PlainReadFile(path, hostopen.StoreOwned)
			out = append(out, CascadeFile{Label: "layer:" + nl.Name, Path: path, Raw: raw, Cfg: nl.Config})
		}
	}
	return append(out, CascadeFile{Label: "project", Path: projPath, Raw: projRaw, Cfg: projCfg}), nil
}

// attribution names a cascade file the way a refusal should: the label a user
// recognizes, and the path to edit when there is one (a bundled template has
// none).
func (f CascadeFile) attribution() string {
	if f.Path == "" {
		return f.Label
	}
	return f.Label + " (" + f.Path + ")"
}

// readCascadeFile is loadLayer plus the raw bytes: one bounded, fd-judged
// read, held to the per-layer rules. A missing file is an empty layer, as
// everywhere else in the cascade.
func readCascadeFile(path string, follow bool) ([]byte, Config, error) {
	raw, err := hostopen.ReadFileBounded(path, follow, MaxConfigBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Config{}, nil
		}
		return nil, Config{}, err
	}
	c, err := Parse(raw)
	if err != nil {
		return nil, Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.ValidateLayer(); err != nil {
		return nil, Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return raw, c, nil
}

// templateCascadeFile loads the selected template as a file. A bundled
// template has no host path — its bytes come from embed.FS.
func templateCascadeFile(cat *packages.Catalog, name string) (CascadeFile, error) {
	ent, err := cat.ResolveName(name)
	if err != nil {
		return CascadeFile{}, err
	}
	if ent.Kind != packages.KindTemplate {
		return CascadeFile{}, fmt.Errorf("package %q is a %s, not a template", ent.ID, ent.Kind)
	}
	raw, err := ent.ReadPrimary()
	if err != nil {
		return CascadeFile{}, err
	}
	cfg, err := ParseTemplateBody(raw)
	if err != nil {
		return CascadeFile{}, err
	}
	var path string
	if ent.Dir != "" {
		path = filepath.Join(ent.Dir, ent.Primary)
	}
	return CascadeFile{Label: "template:" + name, Path: path, Raw: raw, Cfg: cfg}, nil
}
