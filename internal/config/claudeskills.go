package config

// Claude Skill declarations ([[claude_skills]] blocks): byre's vocabulary for
// shipping Claude Skills — Anthropic's agent-skill format, a directory whose
// root holds a SKILL.md — into the box. A Claude Skill is WIRING, not a grant
// (the [[mcp]] genus): its instructions confer nothing bash doesn't already
// have inside the box, so declarations list as configuration, attributed, and
// contribute zero to the exposure line. Anything a skill's scripts need at
// runtime (env, egress) is the contributing byre skill's ordinary business,
// attributed there.
//
// Same two homes and merge taxonomy as [[mcp]]: byre.config layers replace by
// name; skill.toml contributions union AFTER the merge; a `!name` closure is
// kept through the merge (ClaudeSkillsClosed) and subtracts after the skill
// union, so "this skill, minus one of its Claude Skills" works. Duplicate
// ACTIVE names across sources hard-reject (skills.ClaudeSkillSet).
//
// The two homes spell their source differently — and that's the schema, not
// an accident: config declares `path` (a host directory, `~/…` or absolute:
// configs live in the user's store, and a default.config declaration must
// reach the same dir from every project — deliberately wider than the
// project-relative `files` key, safe because the payload is validated as a
// Claude Skill, bounded, and symlink-rejected at staging); a skill.toml
// declares `from` (a package-relative directory, containment-checked like
// [build].files).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ClaudeSkill is one declared Claude Skill. Exactly one of Path (config home)
// or From (skill.toml home) is set.
type ClaudeSkill struct {
	Name string `toml:"name"`
	// Path is the config home's source: a host directory (`~/…` or absolute)
	// whose root holds SKILL.md. Validated as a Claude Skill and staged into
	// the image at bake time; a missing or malformed dir fails the develop,
	// not the declaration.
	Path string `toml:"path,omitempty"`
	// From is the skill.toml home's source: a directory relative to the
	// skill's own dir, containment-checked at resolve like [build].files.
	From string `toml:"from,omitempty"`
}

// claudeSkillNameRe is the Claude Skill name grammar — the same shape as MCP
// names, for the same reasons: the name becomes a directory name in the baked
// tree, the agent's /name invocation, and an attribution label on status
// rows. It must also equal the SKILL.md frontmatter name (Anthropic's format
// requires the frontmatter name to match its directory), which the bake-time
// validation enforces.
var claudeSkillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidClaudeSkillName reports whether s satisfies the name grammar — for
// callers (the claude-skill verbs) validating a bare name with no declaration
// around it.
func ValidClaudeSkillName(s string) bool { return claudeSkillNameRe.MatchString(s) }

// ValidateClaudeSkill checks one declaration's own shape (not its disk
// content — that's the bake-time check, skills.ValidateClaudeSkillDir).
// fromSkill selects the home: a skill.toml contribution declares `from`, a
// config declaration declares `path`; each home's other key is refused so a
// misplaced declaration fails at the file that holds it.
func ValidateClaudeSkill(cs ClaudeSkill, fromSkill bool) error {
	if !claudeSkillNameRe.MatchString(cs.Name) {
		// Echo at most 64 runes of the rejected input: the message renders in
		// the config UI's error line, and an unbounded echo (a stray paste)
		// turns it into a wall.
		name := []rune(cs.Name)
		if len(name) > 64 {
			name = append(name[:64], '…')
		}
		return fmt.Errorf("claude skill name %q: must be lowercase [a-z0-9-], starting with a letter or digit (max 64 chars)", string(name))
	}
	if fromSkill {
		if cs.From == "" {
			return fmt.Errorf("claude skill %s: needs `from` (a directory relative to the skill dir)", cs.Name)
		}
		if cs.Path != "" {
			return fmt.Errorf("claude skill %s: `path` is config vocabulary — a skill.toml contribution uses `from`", cs.Name)
		}
		if !RelSafe(cs.From) {
			return fmt.Errorf("claude skill %s: from %q must be a relative path within the skill dir", cs.Name, cs.From)
		}
		return nil
	}
	if cs.Path == "" {
		return fmt.Errorf("claude skill %s: needs `path` (a host directory whose root holds SKILL.md)", cs.Name)
	}
	if cs.From != "" {
		return fmt.Errorf("claude skill %s: `from` is skill.toml vocabulary — a config declaration uses `path`", cs.Name)
	}
	// Same anchor rule as mount hosts (`~`-anchored or absolute) but not
	// validateHostPath itself: its comma rule is docker --mount grammar, and
	// this path is a staged COPY source — a comma is fine here.
	if cs.Path != "~" && !strings.HasPrefix(cs.Path, "~/") && !filepath.IsAbs(cs.Path) {
		return fmt.Errorf("claude skill %s: path %q must be absolute or ~/…", cs.Name, cs.Path)
	}
	return nil
}

// claudeSkillDeclOps plugs the [[claude_skills]] vocabulary into the shared
// named-declaration machinery (nameddecl.go).
var claudeSkillDeclOps = namedDeclOps[ClaudeSkill]{
	label:        "claude skill",
	markerNoun:   "a real declaration",
	nameNoun:     "claude skill name",
	nameRe:       claudeSkillNameRe,
	name:         func(cs ClaudeSkill) string { return cs.Name },
	markerExtras: func(cs ClaudeSkill) bool { return cs.Path != "" || cs.From != "" },
	validate:     func(cs ClaudeSkill) error { return ValidateClaudeSkill(cs, false) },
}

// validateClaudeSkillsLayer / validateClaudeSkillsResolved check the
// [[claude_skills]] list per the shared lifecycle split (see nameddecl.go).
func (c Config) validateClaudeSkillsLayer() error {
	return validateNamedDeclsLayer(claudeSkillDeclOps, c.ClaudeSkills, c.ClaudeSkillsClosed)
}

func (c Config) validateClaudeSkillsResolved() error {
	return validateNamedDeclsResolved(claudeSkillDeclOps, c.ClaudeSkills, c.ClaudeSkillsClosed)
}

// mergeClaudeSkills folds one cascade step of the [[claude_skills]] list into
// (open, closed) per the shared genus taxonomy (see mergeNamedDecls):
// closures survive in ClaudeSkillsClosed so they can subtract after the skill
// union (skills.ClaudeSkillSet).
func mergeClaudeSkills(base, over Config) (open []ClaudeSkill, closed []string) {
	return mergeNamedDecls(base.ClaudeSkills, base.ClaudeSkillsClosed, over.ClaudeSkills, over.ClaudeSkillsClosed, claudeSkillDeclOps.name)
}
