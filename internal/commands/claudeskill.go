package commands

// `byre claude-skill` — sugar over the [[claude_skills]] config vocabulary.
// add and remove edit ONE cascade layer (the project store config, or with
// global the machine default.config) through the same parse/validate/atomic-
// write path as the interactive editor; list renders the EFFECTIVE set
// through status's own renderers so the two surfaces cannot drift. All
// host-side: nothing here touches the project tree.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// claudeSkillVerbs plugs the [[claude_skills]] vocabulary into the shared
// layer-edit lifecycle (nameddecl.go).
var claudeSkillVerbs = declVerbs[config.ClaudeSkill]{
	kind:   "claude skill",
	name:   func(cs config.ClaudeSkill) string { return cs.Name },
	marker: func(name string) config.ClaudeSkill { return config.ClaudeSkill{Name: name} },
	list:   func(c *config.Config) *[]config.ClaudeSkill { return &c.ClaudeSkills },
	effectiveHas: func(effective config.Config, res skills.Resolved, name string) (bool, error) {
		set, err := skills.ClaudeSkillSet(effective, res)
		if err != nil {
			return false, err
		}
		for _, d := range set {
			if d.CS.Name == name {
				return true, nil
			}
		}
		return false, nil
	},
}

// ClaudeSkillAdd implements `byre claude-skill add <dir> [--name <name>]`:
// validate the directory as a Claude Skill and add-or-update the declaration
// in the target layer, re-opening a matching `!name` closure. The name
// defaults to the SKILL.md frontmatter name (the two must match anyway —
// passing --name only makes a mismatch fail here instead of at develop).
func ClaudeSkillAdd(s Streams, projectDir string, global bool, name, dir string) error {
	stored := dir
	// A bare relative path is CWD-dependent; anchor it now so the stored
	// declaration means the same dir tomorrow. `~` spellings are kept as
	// typed (they expand at bake, so the config stays home-relative).
	if stored != "~" && !strings.HasPrefix(stored, "~/") && !filepath.IsAbs(stored) {
		abs, err := filepath.Abs(stored)
		if err != nil {
			return err
		}
		stored = abs
	}
	expanded, err := expandClaudeSkillPath(stored)
	if err != nil {
		return err
	}
	if name == "" {
		// The frontmatter is the identity; deriving it here keeps the happy
		// path one argument. ValidateClaudeSkillDir re-checks the pair below.
		if name, err = skills.ClaudeSkillDirName(expanded); err != nil {
			return err
		}
	}
	if !config.ValidClaudeSkillName(name) {
		return fmt.Errorf("claude-skill add: %q is not a valid claude skill name (lowercase [a-z0-9-])", name)
	}
	if err := skills.ValidateClaudeSkillDir(expanded, name); err != nil {
		return err
	}
	cs := config.ClaudeSkill{Name: name, Path: stored}
	if err := config.ValidateClaudeSkill(cs, false); err != nil {
		return err
	}
	if err := addNamedDecl(s, projectDir, global, claudeSkillVerbs, name, cs); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: the directory bakes into the image at the next develop — edits to %s apply on rebuild\n", dir)
	fmt.Fprintln(s.Err, "byre: `byre status` shows the effective set and delivery.")
	return nil
}

// ClaudeSkillRemove implements `byre claude-skill remove <name>` — the shared
// closure-smart contract (see removeNamedDecl for the full taxonomy and
// ruling trail).
func ClaudeSkillRemove(s Streams, projectDir string, global bool, name string) error {
	name = strings.TrimPrefix(name, "!") // tolerate a pasted closure spelling
	if !config.ValidClaudeSkillName(name) {
		return fmt.Errorf("claude-skill remove: %q is not a valid claude skill name", name)
	}
	return removeNamedDecl(s, projectDir, global, claudeSkillVerbs, name)
}

// ClaudeSkillList implements `byre claude-skill list`: the effective declared
// set, rendered by the SAME functions status uses, so this view can never
// tell a different story.
func ClaudeSkillList(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Read-only, but collision-checked like status: never render another
	// project's declared set as this one's.
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}
	info := statusInfo{
		Agent:              cfg.Agent,
		ClaudeSkillsClosed: cfg.ClaudeSkillsClosed,
	}
	info.ClaudeSkills, _ = skills.ClaudeSkillSet(cfg, skills.Resolved{})
	if serr := builtins.EnsureStoreOut(paths.Home, s.Err); serr != nil {
		info.SkillErr = serr.Error()
	} else if cat, _ := builtins.LoadCatalogRaw(paths.Home); cat == nil {
		info.SkillErr = "catalog unavailable"
	} else if res, rerr := skills.Resolve(cfg, cat); rerr != nil {
		info.SkillErr = rerr.Error()
	} else {
		rv := combine(cfg, res)
		if verr := rv.validate(); verr != nil {
			info.SkillErr = verr.Error()
		} else {
			info.ClaudeSkills = rv.claudeSkills
			if res.Agent != nil {
				info.AgentClaudeSkills = res.Agent.ClaudeSkills
			}
		}
	}

	if len(info.ClaudeSkills) == 0 && len(info.ClaudeSkillsClosed) == 0 {
		fmt.Fprintln(s.Out, "no Claude Skills declared  (add one: byre claude-skill add <dir>)")
		return nil
	}
	for _, d := range info.ClaudeSkills {
		fmt.Fprintln(s.Out, claudeSkillStatusLine(d))
	}
	if len(info.ClaudeSkills) > 0 {
		fmt.Fprintln(s.Out, claudeSkillsDeliveryLine(info))
	}
	for _, c := range info.ClaudeSkillsClosed {
		fmt.Fprintf(s.Out, "!%s  (config — removed from the declared set)\n", c)
	}
	return nil
}

// expandClaudeSkillPath expands a leading ~ for the disk-side checks; the
// stored declaration keeps the user's spelling.
func expandClaudeSkillPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + strings.TrimPrefix(p, "~")
	}
	return p, nil
}
