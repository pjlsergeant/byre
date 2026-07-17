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
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

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

	path, label, prepare, err := mcpLayerPath(projectDir, global)
	if err != nil {
		return err
	}
	cur, err := config.ParseFile(path)
	if err != nil {
		return err
	}

	reopened := false
	kept := cur.ClaudeSkills[:0:0]
	replaced := false
	for _, e := range cur.ClaudeSkills {
		if e.Name == "!"+name {
			reopened = true // a closure on this name is superseded by the add
			continue
		}
		if e.Name == name {
			kept = append(kept, cs)
			replaced = true
			continue
		}
		kept = append(kept, e)
	}
	if !replaced {
		kept = append(kept, cs)
	}
	cur.ClaudeSkills = kept
	if prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
	}
	if err := configui.Save(path, cur); err != nil {
		return err
	}

	verb := "added"
	if replaced {
		verb = "updated"
	}
	fmt.Fprintf(s.Err, "byre: %s claude skill %s in the %s (%s)\n", verb, name, label, path)
	if reopened {
		fmt.Fprintf(s.Err, "byre: the layer's \"!%s\" closure was removed — the add re-opens it\n", name)
	}
	fmt.Fprintf(s.Err, "byre: the directory bakes into the image at the next develop — edits to %s apply on rebuild\n", dir)
	fmt.Fprintln(s.Err, "byre: `byre status` shows the effective set and delivery.")
	return nil
}

// ClaudeSkillRemove implements `byre claude-skill remove <name>` — the same
// closure-smart contract as `byre mcp remove` (see MCPRemove for the ruling
// trail): delete the layer's own declaration; write the `!name` closure when
// the name is still effective from below OR the check is unresolvable (the
// closure guarantees the verb's promise; an inert marker is visible and
// cheap to delete); error only when there is nothing to remove anywhere.
func ClaudeSkillRemove(s Streams, projectDir string, global bool, name string) error {
	name = strings.TrimPrefix(name, "!") // tolerate a pasted closure spelling
	if !config.ValidClaudeSkillName(name) {
		return fmt.Errorf("claude-skill remove: %q is not a valid claude skill name", name)
	}

	path, label, prepare, err := mcpLayerPath(projectDir, global)
	if err != nil {
		return err
	}
	cur, err := config.ParseFile(path)
	if err != nil {
		return err
	}

	hadEntry, hadClosure := false, false
	kept := cur.ClaudeSkills[:0:0]
	for _, e := range cur.ClaudeSkills {
		switch e.Name {
		case name:
			hadEntry = true
		case "!" + name:
			hadClosure = true
			kept = append(kept, e) // an existing closure stays
		default:
			kept = append(kept, e)
		}
	}
	cur.ClaudeSkills = kept

	stillEffective := false
	var checkErr error
	if !global {
		stillEffective, checkErr = claudeSkillStillEffective(cur, name)
	}

	wroteClosure := false
	if (stillEffective || checkErr != nil) && !hadClosure {
		cur.ClaudeSkills = append(cur.ClaudeSkills, config.ClaudeSkill{Name: "!" + name})
		wroteClosure = true
	}
	if !hadEntry && !wroteClosure {
		if hadClosure {
			fmt.Fprintf(s.Err, "byre: claude skill %s is already closed in the %s — nothing to do\n", name, label)
			return nil
		}
		return fmt.Errorf("claude skill %s: not declared in the %s and not effective from below — nothing to remove", name, label)
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
	}
	if err := configui.Save(path, cur); err != nil {
		return err
	}

	switch {
	case hadEntry && wroteClosure && checkErr == nil:
		fmt.Fprintf(s.Err, "byre: removed claude skill %s from the %s AND closed the name (\"!%s\") — a lower layer or skill still declares it\n", name, label, name)
	case hadEntry && wroteClosure:
		fmt.Fprintf(s.Err, "byre: removed claude skill %s from the %s AND closed the name (\"!%s\")\n", name, label, name)
	case hadEntry:
		fmt.Fprintf(s.Err, "byre: removed claude skill %s from the %s (%s)\n", name, label, path)
	case checkErr != nil:
		fmt.Fprintf(s.Err, "byre: closed claude skill %s in the %s (\"!%s\")\n", name, label, name)
	default:
		fmt.Fprintf(s.Err, "byre: closed claude skill %s in the %s (\"!%s\") — it was declared by a lower layer or skill\n", name, label, name)
	}
	if checkErr != nil {
		fmt.Fprintf(s.Err, "byre: couldn't verify lower layers/skills (%v) — the closure guarantees the removal either way; it's inert if nothing else declares %s (delete it in `byre config` if so)\n", checkErr, name)
	}
	fmt.Fprintln(s.Err, "byre: applies on the next develop.")
	return nil
}

// claudeSkillStillEffective reports whether name survives in the effective
// Claude Skill set with `cur` as the project layer (post tentative edit).
func claudeSkillStillEffective(cur config.Config, name string) (bool, error) {
	home, err := project.Home()
	if err != nil {
		return false, err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return false, err
	}
	if cat == nil {
		return false, fmt.Errorf("catalog unavailable")
	}
	effective, err := config.ResolveProposed(cur)
	if err != nil {
		return false, err
	}
	res, err := skills.Resolve(effective, cat)
	if err != nil {
		return false, err
	}
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
