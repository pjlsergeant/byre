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
	// claudeSkills / claudeSkillsErr: the same pair for the effective Claude
	// Skill set (skills.ClaudeSkillSet).
	claudeSkills    []skills.ClaudeSkillDecl
	claudeSkillsErr error
	// otherEngines are session runners for installed engines OTHER than the
	// configured one, so develop can enforce single-session across an engine
	// switch (ADR 0004). Set by Develop; nil in combine() (and thus in unit
	// tests, which drive develop directly) means "no cross-engine check".
	otherEngines []sessionRunner
}

// combine forms the resolved view from a loaded config and its skills — the
// single place the config+skill mount/volume union and the effective MCP set
// are built.
func combine(cfg config.Config, res skills.Resolved) resolved {
	mcps, mcpErr := skills.MCPSet(cfg, res)
	claudeSkills, claudeSkillsErr := skills.ClaudeSkillSet(cfg, res)
	return resolved{
		cfg:             cfg,
		skills:          res,
		mounts:          append(append([]config.Mount{}, cfg.Mounts...), res.Mounts()...),
		volumes:         append(append([]config.Volume{}, cfg.Volumes...), res.Volumes()...),
		mcps:            mcps,
		mcpErr:          mcpErr,
		claudeSkills:    claudeSkills,
		claudeSkillsErr: claudeSkillsErr,
	}
}

// validate re-checks the combined mount/volume set for target/name collisions
// across config and skills (each side is already valid on its own), and the
// cross-source MCP name collisions MCPSet rejected. The attributed scan runs
// first: a cross-source collision names WHO declared each side — the flat
// invariant check can't (it sees one list), and "collides with mount X" is a
// riddle when one X is yours and the other rode in with a skill.
func (rv resolved) validate() error {
	if err := rv.attributedCollisions(); err != nil {
		return fmt.Errorf("config + skills: %w", err)
	}
	if err := (config.Config{Mounts: rv.mounts, Volumes: rv.volumes}).Validate(); err != nil {
		return fmt.Errorf("config + skills: %w", err)
	}
	if rv.mcpErr != nil {
		return fmt.Errorf("config + skills: %w", rv.mcpErr)
	}
	if rv.claudeSkillsErr != nil {
		return fmt.Errorf("config + skills: %w", rv.claudeSkillsErr)
	}
	return nil
}

// attributedCollisions mirrors Validate's mount/volume uniqueness invariants
// (targets unique across both kinds, volume names unique) over the combined
// set WITH provenance labels, so the error can say which source owns each
// side. Anything it misses still lands on the flat Validate behind it.
func (rv resolved) attributedCollisions() error {
	targets := map[string]string{} // container target -> "config's mount /x" etc.
	names := map[string]string{}   // volume name -> claimant
	claim := func(m map[string]string, key, who string) error {
		if prev := m[key]; prev != "" {
			return fmt.Errorf("%s collides with %s (skill grants ride the skill — remove the skill or your own entry)", who, prev)
		}
		m[key] = who
		return nil
	}
	walk := func(source string, mounts []config.Mount, volumes []config.Volume) error {
		for _, m := range mounts {
			if err := claim(targets, m.Target, fmt.Sprintf("%s's mount %s", source, m.Target)); err != nil {
				return err
			}
		}
		for _, v := range volumes {
			if err := claim(names, v.Name, fmt.Sprintf("%s's volume %s", source, v.Name)); err != nil {
				return err
			}
			if err := claim(targets, v.Target, fmt.Sprintf("%s's volume %s (target %s)", source, v.Name, v.Target)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("config", rv.cfg.Mounts, rv.cfg.Volumes); err != nil {
		return err
	}
	for _, sk := range rv.skills.Skills {
		if err := walk("skill "+sk.Name, sk.File.Runtime.Mounts, sk.File.Volumes); err != nil {
			return err
		}
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
