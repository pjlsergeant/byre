package config

// Context declarations ([[context]] blocks): byre's vocabulary for standing
// agent instructions -- prose the OPERATOR wants in the agent's memory in
// every box the declaring layer reaches. The cascade is the scoping: a
// declaration in default.config reaches every box on the machine, a template
// or named layer reaches its stack, the project config reaches one project.
// This is the operator's voice, distinct from the two prose channels that
// already exist: a skill's own [context] table (the skill author's opinions,
// riding the skill toggle) and a repo's in-tree CLAUDE.md (the project's
// voice -- committed, collaborator-visible, and agent-writable). Config
// declarations live host-side in the store, out of the boxed agent's
// reach short of a `--self-edit` session's explicit grant over the
// project layer.
//
// A declaration is WIRING, not a grant: text confers nothing bash doesn't
// already have inside the box (the [[claude_skills]] stance). The `file`
// form is a declared build input read at bake time -- a missing or unreadable
// file fails the develop, not the declaration.
//
// One home only -- config layers. Unlike the [[mcp]] genus there is no
// skill.toml twin (a skill's prose is its [context] table), so nothing
// unions in after the cascade and a `!name` closure is fully spent removing
// the inherited declaration during the merge. It still rides the shared
// named-declaration machinery (nameddecl.go): layers replace by name, the
// closure grammar and its refusals are uniform with [[mcp]] and
// [[claude_skills]], and ContextsClosed carries survivors inertly.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ContextDecl is one declared standing-instruction snippet. Exactly one of
// Text (inline prose) or File (a host file read at bake time) is set -- the
// declaration self-discriminates, like [[mcp]]'s command/url.
type ContextDecl struct {
	Name string `toml:"name"`
	// Text is the snippet inline. TOML multi-line strings keep it readable;
	// the portable form for a distributable template.
	Text string `toml:"text,omitempty"`
	// File is a host path (`~/…` or absolute -- the [[claude_skills]] `path`
	// anchor rule: configs live in the user's store, and a default.config
	// declaration must reach the same file from every project). Read at bake
	// time, size-capped; the content lands in the baked agent context, so a
	// rebuild picks up edits.
	File string `toml:"file,omitempty"`
}

// contextNameRe is the context name grammar -- the MCP shape, for the same
// reasons: the name is a merge identity and an attribution label; keeping
// one grammar across the named-declaration genus keeps the closure spelling
// (`!name`) unambiguous everywhere.
var contextNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidContextName reports whether s satisfies the context name grammar --
// for callers (the context verbs) validating a bare name with no
// declaration around it.
func ValidContextName(s string) bool { return contextNameRe.MatchString(s) }

// ValidateContextDecl checks one declaration's own shape (not its file's
// content or existence -- that's the bake-time read in build.Assemble).
func ValidateContextDecl(cd ContextDecl) error {
	if !contextNameRe.MatchString(cd.Name) {
		// Echo at most 64 runes of the rejected input (the claude-skill
		// stance): the message renders in an error line, and an unbounded
		// echo turns it into a wall.
		name := []rune(cd.Name)
		if len(name) > 64 {
			name = append(name[:64], '…')
		}
		return fmt.Errorf("context name %q: must be lowercase [a-z0-9-], starting with a letter or digit (max 64 chars)", string(name))
	}
	switch {
	case cd.Text != "" && cd.File != "":
		return fmt.Errorf("context %s: has both text and file (a snippet is inline or a file, not both)", cd.Name)
	case cd.Text == "" && cd.File == "":
		return fmt.Errorf("context %s: needs text (an inline snippet) or file (a host file, ~/… or absolute)", cd.Name)
	}
	// The text renders verbatim in the config UI (the mcpPrintable stance):
	// an ESC sequence in an inherited layer's prose could forge the
	// surrounding terminal UI when its row is opened. Prose has no
	// legitimate control characters beyond newline and tab.
	for _, r := range cd.Text {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("context %s: text must not contain control characters (beyond newline and tab)", cd.Name)
		}
	}
	if cd.File != "" && cd.File != "~" && !strings.HasPrefix(cd.File, "~/") && !filepath.IsAbs(cd.File) {
		return fmt.Errorf("context %s: file %q must be absolute or ~/…", cd.Name, cd.File)
	}
	return nil
}

// contextDeclOps plugs the [[context]] vocabulary into the shared
// named-declaration machinery (nameddecl.go).
var contextDeclOps = namedDeclOps[ContextDecl]{
	label:        "context",
	markerNoun:   "a real declaration",
	nameNoun:     "context name",
	nameRe:       contextNameRe,
	name:         func(cd ContextDecl) string { return cd.Name },
	markerExtras: func(cd ContextDecl) bool { return cd.Text != "" || cd.File != "" },
	validate:     ValidateContextDecl,
}

// validateContextsLayer / validateContextsResolved check the [[context]] list
// per the shared lifecycle split (see nameddecl.go).
func (c Config) validateContextsLayer() error {
	return validateNamedDeclsLayer(contextDeclOps, c.Contexts, c.ContextsClosed)
}

func (c Config) validateContextsResolved() error {
	return validateNamedDeclsResolved(contextDeclOps, c.Contexts, c.ContextsClosed)
}

// mergeContexts folds one cascade step of the [[context]] list into
// (open, closed) per the shared genus taxonomy (see mergeNamedDecls). With no
// second home the closure's work is done by the merge itself; survivors in
// ContextsClosed are inert.
func mergeContexts(base, over Config) (open []ContextDecl, closed []string) {
	return mergeNamedDecls(base.Contexts, base.ContextsClosed, over.Contexts, over.ContextsClosed, contextDeclOps.name)
}
