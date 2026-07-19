package skills

// The named-declaration genus's effective-set rules, shared by MCPSet and
// ClaudeSkillSet (config.nameddecl.go owns the cascade-side split/merge): a
// CLOSED name is subtracted from every source — it neither delivers nor
// collides; a duplicate ACTIVE name across sources (config+skill,
// skill+skill) is a hard reject naming both claimants, with the `!name`
// closure as the suggested remedy — the only per-declaration fix for a
// skill+skill collision short of disabling a whole skill.

import (
	"fmt"
	"slices"
)

// declClaims tracks one vocabulary's name claims while an effective set is
// built, enforcing the shared closed/duplicate rules above.
type declClaims struct {
	label      string // error noun: "mcp", "claude skill"
	listName   string // the config list the remedy names: "mcp", "claude_skills"
	fromConfig string // the vocabulary's own config-declared sentinel
	closed     []string
	claimed    map[string]string // name -> claiming source
}

func newDeclClaims(label, listName, fromConfig string, closed []string) *declClaims {
	return &declClaims{label: label, listName: listName, fromConfig: fromConfig, closed: closed, claimed: map[string]string{}}
}

// claim records src's declaration of name. active=false means the name is
// closed (subtracted from this source too, no collision possible); an error
// names both claimants and the closure remedy.
func (c *declClaims) claim(src, name string) (active bool, err error) {
	if slices.Contains(c.closed, name) {
		return false, nil
	}
	if prev, ok := c.claimed[name]; ok {
		return false, fmt.Errorf("%s %s: declared by both %s and %s — remove one, or close the name with \"!%s\" in the config %s list",
			c.label, name, c.sourceLabel(prev), c.sourceLabel(src), name, c.listName)
	}
	c.claimed[name] = src
	return true, nil
}

// sourceLabel renders a claim source for the duplicate error against the
// vocabulary's OWN config sentinel — never a shared constant, so the two
// vocabularies' sentinels can diverge without silently mislabeling.
func (c *declClaims) sourceLabel(src string) string {
	if src == c.fromConfig {
		return "the config"
	}
	return fmt.Sprintf("skill %q", src)
}
