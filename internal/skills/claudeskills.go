package skills

// The effective Claude Skill set: config-declared [[claude_skills]] blocks
// (post-cascade) plus every enabled skill's contributions, with the config's
// `!name` closures subtracting LAST — after the skill union (the MCPSet
// semantics, wholesale). One owner: the bake stages this set into
// /etc/byre/claude-skills, status renders it — both through ClaudeSkillSet,
// so the surfaces can't drift from what the box gets.
//
// This file also owns the bake-time "looks like a Claude Skill" check
// (ValidateClaudeSkillDir): the format has exactly two load-bearing fields —
// frontmatter `name` and `description` — and byre validates that much plus
// bounds, nothing deeper. Claude's fuller contract (other frontmatter keys,
// body semantics) is claude's and evolves with it; a malformed skill is
// non-fatal to a claude session (spike-verified), so this check is hygiene
// with good attribution, not session survival.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
)

// ClaudeSkillsFromConfig is the attribution for Claude Skills declared by the
// config's own [[claude_skills]] blocks rather than a skill (mirrors
// MCPFromConfig).
const ClaudeSkillsFromConfig = "config"

// ClaudeSkillDecl is one effective Claude Skill declaration, attributed to
// its source for status legibility.
type ClaudeSkillDecl struct {
	Skill string // contributing skill's canonical ID, or ClaudeSkillsFromConfig
	CS    config.ClaudeSkill
	// SrcDir is the resolved host source directory for SKILL contributions
	// (containment-checked by Resolve). Empty for config declarations, whose
	// `path` expands at bake time — a config row must not require disk access
	// to render on status.
	SrcDir string
}

// ClaudeSkillSet forms the effective declared set. Duplicate ACTIVE names
// across sources hard-reject with both claimants named; a CLOSED name is not
// active (it neither delivers nor collides), which makes `!name` the remedy
// the duplicate error suggests. A closure matching nothing is inert.
func ClaudeSkillSet(cfg config.Config, r Resolved) ([]ClaudeSkillDecl, error) {
	var out []ClaudeSkillDecl
	claimedBy := map[string]string{}
	add := func(src string, d ClaudeSkillDecl) error {
		if slices.Contains(cfg.ClaudeSkillsClosed, d.CS.Name) {
			return nil // closed: subtracted from every source, so never active
		}
		if prev, ok := claimedBy[d.CS.Name]; ok {
			return fmt.Errorf("claude skill %s: declared by both %s and %s — remove one, or close the name with \"!%s\" in the config claude_skills list",
				d.CS.Name, claudeSkillSourceLabel(prev), claudeSkillSourceLabel(src), d.CS.Name)
		}
		claimedBy[d.CS.Name] = src
		out = append(out, d)
		return nil
	}
	for _, cs := range cfg.ClaudeSkills {
		if err := add(ClaudeSkillsFromConfig, ClaudeSkillDecl{Skill: ClaudeSkillsFromConfig, CS: cs}); err != nil {
			return nil, err
		}
	}
	for _, sk := range r.Skills {
		for _, c := range sk.ClaudeSkills {
			if err := add(sk.Name, c); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func claudeSkillSourceLabel(src string) string {
	if src == ClaudeSkillsFromConfig {
		return "the config"
	}
	return fmt.Sprintf("skill %q", src)
}

// Bounds for one Claude Skill directory. The file cap matches the installed-
// package payload cap; the byte cap keeps a fat-fingered `path` from becoming
// a giant COPY layer ("not a skill: 40,000 files" beats a gigabyte image).
const (
	MaxClaudeSkillFiles = 64
	MaxClaudeSkillBytes = 8 << 20
)

// ValidateClaudeSkillDir is the bake-time "looks like a Claude Skill" check
// for one resolved source directory: SKILL.md at the root, YAML frontmatter
// with non-empty name and description, frontmatter name equal to the declared
// name (the bake stages the dir under the declared name, and claude requires
// the frontmatter name to match its directory — a mismatch would deliver a
// skill whose identity disagrees with what status reports), and file-count /
// total-size bounds. Symlinks are rejected here with attribution (staging
// would refuse them anyway, less legibly). Runs for both homes at bake, and
// for `byre claude-skill add` before it writes a declaration.
func ValidateClaudeSkillDir(dir, name string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("claude skill %s: %w", name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("claude skill %s: %s is not a directory", name, dir)
	}
	fmName, fmDesc, err := claudeSkillFrontmatter(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("claude skill %s: %s: %w", name, dir, err)
	}
	if fmName != name {
		return fmt.Errorf("claude skill %s: SKILL.md frontmatter name is %q — the declared name and the frontmatter name must match (claude derives the skill's identity from its directory, which byre names after the declaration); pick one spelling", name, fmName)
	}
	if strings.TrimSpace(fmDesc) == "" {
		return fmt.Errorf("claude skill %s: SKILL.md frontmatter needs a non-empty description (it is what tells the agent when to use the skill)", name)
	}

	files, bytes := 0, int64(0)
	err = filepath.Walk(dir, func(p string, fi os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s: not allowed in a claude skill dir (copy plain files)", p)
		}
		if fi.IsDir() {
			return nil
		}
		// Only regular files stage: a FIFO would block the staging copy's
		// os.Open indefinitely, and a device could dodge the size bound.
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s: not a regular file — a claude skill dir holds plain files only", p)
		}
		files++
		bytes += fi.Size()
		if files > MaxClaudeSkillFiles {
			return fmt.Errorf("more than %d files — not stageable as a claude skill", MaxClaudeSkillFiles)
		}
		if bytes > MaxClaudeSkillBytes {
			return fmt.Errorf("more than %d bytes — not stageable as a claude skill", MaxClaudeSkillBytes)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("claude skill %s: %s: %w", name, dir, err)
	}
	return nil
}

// ClaudeSkillDirName reads the SKILL.md frontmatter name from dir — the
// identity `byre claude-skill add` derives when no --name is passed (the
// declared name must equal it anyway; ValidateClaudeSkillDir enforces the
// pair).
func ClaudeSkillDirName(dir string) (string, error) {
	name, _, err := claudeSkillFrontmatter(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("claude skill dir %s: %w", dir, err)
	}
	return name, nil
}

// claudeSkillFrontmatter extracts the name and description from a SKILL.md's
// YAML frontmatter: a leading `---` line, a YAML block, a closing `---` line.
// Unknown frontmatter keys are ignored — the format is Anthropic's and grows
// fields; byre reads only the two it depends on.
func claudeSkillFrontmatter(path string) (name, desc string, err error) {
	// Lstat before reading: a FIFO named SKILL.md would block os.ReadFile
	// indefinitely, and a symlinked SKILL.md must be rejected before being
	// followed, not after (the walk's checks run later than this read).
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("no SKILL.md at the directory root — a claude skill is a directory whose root holds one")
		}
		return "", "", err
	}
	if !fi.Mode().IsRegular() {
		return "", "", fmt.Errorf("SKILL.md is not a regular file — a claude skill dir holds plain files only")
	}
	// The Lstat above owns the message; the actual read rides hostopen so a
	// swap in the window between the two (regular → FIFO/symlink) is refused
	// at the descriptor instead of followed or blocked on — and the read is
	// capped at the dir's own aggregate bound (this runs BEFORE the walk
	// enforces it, so an oversized or concurrently-growing SKILL.md must be
	// stopped here, not trusted to a later check).
	fh, _, err := hostopen.OpenRegular(path, false)
	if err != nil {
		return "", "", err
	}
	defer fh.Close()
	raw, err := io.ReadAll(io.LimitReader(fh, MaxClaudeSkillBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(raw) > MaxClaudeSkillBytes {
		return "", "", fmt.Errorf("SKILL.md exceeds %d bytes — not stageable as a claude skill", MaxClaudeSkillBytes)
	}
	body, ok := bytes.CutPrefix(raw, []byte("---\n"))
	if !ok {
		if body, ok = bytes.CutPrefix(raw, []byte("---\r\n")); !ok {
			return "", "", fmt.Errorf("SKILL.md must open with `---` YAML frontmatter")
		}
	}
	var block []byte
	closed := false
	for _, line := range bytes.Split(body, []byte("\n")) {
		if string(bytes.TrimRight(line, "\r")) == "---" {
			closed = true
			break
		}
		block = append(block, line...)
		block = append(block, '\n')
	}
	if !closed {
		return "", "", fmt.Errorf("SKILL.md frontmatter is not closed (no terminating `---` line)")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(block, &fm); err != nil {
		return "", "", fmt.Errorf("SKILL.md frontmatter is not valid YAML: %v", err)
	}
	if strings.TrimSpace(fm.Name) == "" {
		return "", "", fmt.Errorf("SKILL.md frontmatter needs a non-empty name")
	}
	return fm.Name, fm.Description, nil
}
