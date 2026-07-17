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
	"slices"
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

// validateClaudeSkills checks the [[claude_skills]] list per the shared
// layer/resolved split, mirroring validateMCPs: layer mode permits
// `name = "!skill"` closure markers and rejects in-layer duplicates; resolved
// mode rejects markers (Merge extracts them into ClaudeSkillsClosed) and
// duplicates alike.
func (c Config) validateClaudeSkills(layer bool) error {
	seen := map[string]bool{}
	for _, cs := range c.ClaudeSkills {
		if isRemoval(cs.Name) {
			if !layer {
				return fmt.Errorf("claude skill %s: a closure marker is only meaningful in a cascade layer", cs.Name)
			}
			// A marker is name-only — other fields set suggest a real
			// declaration with a mistyped name; refuse rather than silently
			// discard it.
			if cs.Path != "" || cs.From != "" {
				return fmt.Errorf("claude skill %s: a closure marker takes only a name — other fields here suggest a real declaration with a mistyped name", cs.Name)
			}
			if !claudeSkillNameRe.MatchString(cs.Name[1:]) {
				return fmt.Errorf("claude skill closure %q: %q is not a valid claude skill name", cs.Name, cs.Name[1:])
			}
			continue
		}
		if err := ValidateClaudeSkill(cs, false); err != nil {
			return err
		}
		if seen[cs.Name] {
			return fmt.Errorf("claude skill %s appears twice in this file; merge would keep only the last one", cs.Name)
		}
		seen[cs.Name] = true
	}
	for _, cl := range c.ClaudeSkillsClosed {
		if !claudeSkillNameRe.MatchString(cl) {
			return fmt.Errorf("claude skill closure %q: not a valid claude skill name", cl)
		}
	}
	return nil
}

// mergeClaudeSkills folds one cascade step of the [[claude_skills]] list into
// (open, closed), mirroring mergeMCPs: a `!name` closure is NOT consumed when
// it removes a declaration — it survives the cascade (ClaudeSkillsClosed) so
// it can subtract the same name from the EFFECTIVE set after skill
// contributions union in (skills.ClaudeSkillSet). Precedence stays
// cascade-ordered: a later layer's plain declaration re-opens an earlier
// layer's closure; within one layer a closure beats a plain declaration.
func mergeClaudeSkills(base, over Config) (open []ClaudeSkill, closed []string) {
	open, closed = splitClaudeSkills(base.ClaudeSkills, base.ClaudeSkillsClosed)
	overOpen, overClosed := splitClaudeSkills(over.ClaudeSkills, over.ClaudeSkillsClosed)
	for _, cs := range overOpen {
		closed = filter(closed, func(c string) bool { return c != cs.Name })
		replaced := false
		for i := range open {
			if open[i].Name == cs.Name {
				open[i] = cs
				replaced = true
				break
			}
		}
		if !replaced {
			open = append(open, cs)
		}
	}
	for _, c := range overClosed {
		open = filter(open, func(cs ClaudeSkill) bool { return cs.Name != c })
		if !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	return open, closed
}

// splitClaudeSkills separates a [[claude_skills]] list into real declarations
// and the stripped names of its `!name` closure markers, folding an already-
// populated ClaudeSkillsClosed (a previously merged config re-entering Merge)
// into the latter.
func splitClaudeSkills(decls []ClaudeSkill, alreadyClosed []string) (open []ClaudeSkill, closed []string) {
	for _, cs := range decls {
		if isRemoval(cs.Name) {
			closed = append(closed, cs.Name[1:])
			continue
		}
		open = append(open, cs)
	}
	for _, c := range alreadyClosed {
		if !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	return open, closed
}
