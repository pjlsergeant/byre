package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"byre/internal/builtins"
	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/skills"
)

// resolved is the fully-loaded view of a project: the config cascade, the
// enabled skills, and — because every consumer wants them — the combined
// (config + skill) mount and volume sets, formed in one place.
type resolved struct {
	cfg     config.Config
	skills  skills.Resolved
	mounts  []config.Mount  // config mounts, then skill contributions
	volumes []config.Volume // config volumes, then skill contributions
}

// combine forms the resolved view from a loaded config and its skills — the
// single place the config+skill mount/volume union is built.
func combine(cfg config.Config, res skills.Resolved) resolved {
	return resolved{
		cfg:     cfg,
		skills:  res,
		mounts:  append(append([]config.Mount{}, cfg.Mounts...), res.Mounts()...),
		volumes: append(append([]config.Volume{}, cfg.Volumes...), res.Volumes()...),
	}
}

// validate re-checks the combined mount/volume set for target/name collisions
// across config and skills (each side is already valid on its own).
func (rv resolved) validate() error {
	if err := (config.Config{Mounts: rv.mounts, Volumes: rv.volumes}).Validate(); err != nil {
		return fmt.Errorf("config + skills: %w", err)
	}
	return nil
}

// resolve loads the config cascade and the enabled skills for a project, and
// re-validates the combined mount/volume set (config + skill contributions).
func resolve(paths project.Paths, projectDir string) (resolved, error) {
	// Materialize built-ins before loading config (templates feed the cascade)
	// and resolving skills.
	if err := builtins.MaterializeTemplates(filepath.Join(paths.Home, "templates")); err != nil {
		return resolved{}, err
	}
	skillsDir := filepath.Join(paths.Home, "skills")
	if err := builtins.MaterializeSkills(skillsDir); err != nil {
		return resolved{}, err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return resolved{}, err
	}
	res, err := skills.Resolve(cfg, skillsDir)
	if err != nil {
		return resolved{}, err
	}
	rv := combine(cfg, res)
	if err := rv.validate(); err != nil {
		return resolved{}, err
	}
	return rv, nil
}

// resolveProjectFile resolves a project-relative file, following symlinks and
// confirming containment within the project dir (so an opt-out `dockerfile`
// can't point outside via a symlink).
func resolveProjectFile(projectDir, rel string) (string, error) {
	realDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Join(realDir, rel))
	if err != nil {
		return "", fmt.Errorf("dockerfile %q: %w", rel, err)
	}
	within, err := filepath.Rel(realDir, real)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dockerfile %q escapes the project dir", rel)
	}
	return real, nil
}
