package commands

import (
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// resolved is the fully-loaded view of a project: the config cascade, the
// enabled skills, and — because every consumer wants them — the combined
// (config + skill) mount and volume sets, formed in one place.
type resolved struct {
	cfg     config.Config
	skills  skills.Resolved
	cat     *packages.Catalog
	mounts  []config.Mount  // config mounts, then skill contributions
	volumes []config.Volume // config volumes, then skill contributions
	// mcps is the effective declared MCP set (config ∪ skills, minus config
	// closures — skills.MCPSet); mcpErr is its cross-source duplicate reject,
	// carried so validate() can surface it (combine stays error-free).
	mcps   []skills.MCPDecl
	mcpErr error
}

// combine forms the resolved view from a loaded config and its skills — the
// single place the config+skill mount/volume union and the effective MCP set
// are built.
func combine(cfg config.Config, res skills.Resolved) resolved {
	mcps, mcpErr := skills.MCPSet(cfg, res)
	return resolved{
		cfg:     cfg,
		skills:  res,
		mounts:  append(append([]config.Mount{}, cfg.Mounts...), res.Mounts()...),
		volumes: append(append([]config.Volume{}, cfg.Volumes...), res.Volumes()...),
		mcps:    mcps,
		mcpErr:  mcpErr,
	}
}

// validate re-checks the combined mount/volume set for target/name collisions
// across config and skills (each side is already valid on its own), and the
// cross-source MCP name collisions MCPSet rejected.
func (rv resolved) validate() error {
	if err := (config.Config{Mounts: rv.mounts, Volumes: rv.volumes}).Validate(); err != nil {
		return fmt.Errorf("config + skills: %w", err)
	}
	if rv.mcpErr != nil {
		return fmt.Errorf("config + skills: %w", rv.mcpErr)
	}
	return nil
}

// resolve loads the config cascade and the enabled skills for a project, and
// re-validates the combined mount/volume set (config + skill contributions).
// notices receives store-ensure human lines (mirror regen, LEGACY) — pass the
// caller's s.Err; the once-per-process gate in builtins keeps develop's
// earlier noticed call from doubling. nil = silent (tests).
func resolve(paths project.Paths, projectDir string, notices io.Writer) (resolved, error) {
	if err := builtins.EnsureStoreOut(paths.Home, notices); err != nil {
		return resolved{}, err
	}
	cat, err := builtins.LoadCatalogRaw(paths.Home)
	if err != nil {
		return resolved{}, err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return resolved{}, err
	}
	res, err := skills.Resolve(cfg, cat)
	if err != nil {
		return resolved{}, err
	}
	rv := combine(cfg, res)
	rv.cat = cat
	if err := rv.validate(); err != nil {
		return resolved{}, err
	}
	return rv, nil
}
