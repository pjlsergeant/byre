// Package skills loads skill bundles from ~/.byre/skills/<name>/ and resolves
// their contributions to the layers byre controls: build (per-skill Dockerfile
// block), runtime (mounts/env/caps/run_args), state (named volumes), agent
// context, and — for agent skills — the launch command.
//
// "The agent is a skill": the `agent` config scalar names which enabled skill
// provides the default launch command.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"byre/internal/config"
	"byre/internal/gen"
)

// AgentContrib is the agent-skill launch contribution.
type AgentContrib struct {
	Command string `toml:"command"` // what the launcher execs (e.g. "claude --dangerously-skip-permissions")
	State   string `toml:"state"`   // name of its state volume (informational)
	// ContextTarget is the in-image path where THIS agent reads project memory
	// (e.g. claude -> /home/dev/.claude/CLAUDE.md). When set, the launcher places
	// the concatenated skill [context] there at runtime so it reaches the agent.
	// Must be absolute; it typically lives in the agent's state volume (which is
	// only mounted at runtime, hence launcher-time placement, not a build COPY).
	ContextTarget string `toml:"context_target"`
	// Prefs declares the curated, non-secret pref files (theme, keybindings) the
	// user may opt to seed from the host into a fresh state volume (config
	// seed_prefs = true). Optional; requires a state volume to land in.
	Prefs *PrefsSpec `toml:"prefs"`
}

// PrefsSpec is one agent's curated, non-secret host preferences, eligible for a
// one-time opt-in seed into a fresh state volume (config seed_prefs). The skill
// author VOUCHES that every listed path is pure prefs (no embedded secrets): a
// file that can hide a credential (e.g. an agent settings.json mixing theme with
// env/apiKeyHelper/MCP tokens) must NOT be listed — list only the structurally
// secret-incapable files (e.g. keybindings.json, a themes/ dir).
type PrefsSpec struct {
	From  string   `toml:"from"`  // host config dir (e.g. "~/.claude"); ~ expands at seed time
	Files []string `toml:"files"` // pref paths (files or dirs) relative to From
}

// File is the on-disk skill.toml schema.
type File struct {
	Build struct {
		Apt        []string          `toml:"apt"`
		NpmGlobal  []string          `toml:"npm_global"`
		Dockerfile []string          `toml:"dockerfile"` // raw build lines
		Files      map[string]string `toml:"files"`      // skill-relative src -> absolute image dest
	} `toml:"build"`
	Runtime struct {
		Env     map[string]string `toml:"env"`
		RunArgs []string          `toml:"run_args"`
		Caps    []string          `toml:"caps"`
		Mounts  []config.Mount    `toml:"mounts"`
	} `toml:"runtime"`
	Agent   *AgentContrib   `toml:"agent"`
	Volumes []config.Volume `toml:"volumes"`
	Context struct {
		Text string `toml:"text"` // inline snippet
		File string `toml:"file"` // path (relative to the skill dir) to a snippet
	} `toml:"context"`
}

// Skill is a loaded skill with its context text resolved.
type Skill struct {
	Name    string
	File    File
	Context string // resolved context snippet
}

// Grant records a single skill's runtime grants, for legible attribution in
// `byre status` (e.g. which skill mounts a host socket).
type Grant struct {
	Skill  string
	Mounts []config.Mount
	Caps   []string
}

// SkillFile is one resolved file a skill ships into the image: a source inside
// the skill's own dir (validated for containment) copied to an absolute image
// path. The build stage stages Src into the build context; gen emits the COPY.
type SkillFile struct {
	Skill string
	Src   string // absolute host path, resolved within the skill dir
	Rel   string // cleaned skill-relative source (preserves subdirs for staging)
	Dest  string // absolute image path
}

// Resolved is the aggregate of all enabled skills' contributions.
type Resolved struct {
	SkillBlocks  []gen.SkillBlock  // per-skill build blocks, in order
	Env          map[string]string // runtime env
	RunArgs      []string
	Caps         []string
	Mounts       []config.Mount
	Volumes      []config.Volume
	Grants       []Grant     // per-skill runtime grants (mounts/caps) for attribution
	SkillFiles   []SkillFile // files skills ship into the image (staged by build)
	AgentCommand string      // selected agent's launch command ("" if no agent)
	AgentState   string      // selected agent's state volume name ("" if none)
	Context      string      // concatenated agent-context snippets
	// AgentContextTarget is where the selected agent reads project memory; the
	// launcher places Context there at runtime. "" if no agent declares one.
	AgentContextTarget string
	// AgentPrefs is the selected agent's curated seedable prefs (nil if none). The
	// seed only runs when the user opts in (config seed_prefs) and the agent's
	// state volume is fresh.
	AgentPrefs *PrefsSpec
}

// ListSkills returns the names of all skills in skillsDir, sorted. Broken skills
// are skipped. This is the full set selectable via the `skills` list — including
// agent skills, which can legitimately be enabled as a plain skill (e.g. codex
// installed for byre-codereview while claude is the launched agent), separate
// from the `agent` choice. Broken skills are skipped rather than failing.
func ListSkills(skillsDir string) []string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := Load(skillsDir, e.Name()); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// ListAgentSkills returns the names of skills in skillsDir that provide an
// [agent] command (i.e. can be selected as `agent`), sorted. Broken skills are
// skipped rather than failing the whole listing.
func ListAgentSkills(skillsDir string) []string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var agents []string
	for _, e := range entries {
		// Skip non-dirs and dot-prefixed entries (temp/stash dirs from
		// `byre skill update`, backups, etc. are not skills).
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sk, err := Load(skillsDir, e.Name())
		if err != nil {
			continue
		}
		if sk.File.Agent != nil && sk.File.Agent.Command != "" {
			agents = append(agents, e.Name())
		}
	}
	sort.Strings(agents)
	return agents
}

// Load reads and resolves a single skill from skillsDir/<name>/skill.toml.
func Load(skillsDir, name string) (Skill, error) {
	dir := filepath.Join(skillsDir, name)
	var f File
	md, err := toml.DecodeFile(filepath.Join(dir, "skill.toml"), &f)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, fmt.Errorf("skill %q not found in %s", name, skillsDir)
		}
		return Skill{}, fmt.Errorf("skill %q: %w", name, err)
	}
	// byre owns the skill.toml schema — a typo'd key is an error, not a silent
	// no-op that produces a broken skill.
	if und := md.Undecoded(); len(und) > 0 {
		return Skill{}, fmt.Errorf("skill %q: unknown key(s) in skill.toml: %v", name, und)
	}

	ctx := f.Context.Text
	if f.Context.File != "" {
		path, perr := skillRelPath(dir, f.Context.File)
		if perr != nil {
			return Skill{}, fmt.Errorf("skill %q: %w", name, perr)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return Skill{}, fmt.Errorf("skill %q context: %w", name, rerr)
		}
		ctx = string(b)
	}
	return Skill{Name: name, File: f, Context: ctx}, nil
}

// skillRelPath resolves a skill-relative file path, rejecting absolute paths,
// lexical "../" escapes, and symlinks that point outside the skill directory.
func skillRelPath(dir, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path must be relative to the skill dir: %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the skill dir: %q", rel)
	}

	// Resolve symlinks on both sides and confirm the target is still contained,
	// so a symlink inside the bundle can't read an arbitrary host file.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(filepath.Join(realDir, clean))
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(realDir, realFull)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the skill dir via symlink: %q", rel)
	}
	return realFull, nil
}

// Resolve loads every enabled skill (the cfg.Skills list, plus the cfg.Agent
// skill enabled implicitly) and aggregates their contributions. The selected
// agent's skill must exist and provide an [agent] command.
func Resolve(cfg config.Config, skillsDir string) (Resolved, error) {
	names := enabledSkillNames(cfg)

	var res Resolved
	res.Env = map[string]string{}
	agentFound := cfg.Agent == ""

	for _, name := range names {
		// Validate before Load: a skill name is a single path element. An unsafe
		// name ("../x") would let Load read outside skillsDir and would escape the
		// build-context staging dir (skills/<name>/...).
		if err := validateSkillName(name); err != nil {
			return Resolved{}, err
		}
		sk, err := Load(skillsDir, name)
		if err != nil {
			return Resolved{}, err
		}
		f := sk.File

		res.SkillBlocks = append(res.SkillBlocks, gen.SkillBlock{
			Name:       name,
			Apt:        f.Build.Apt,
			NpmGlobal:  f.Build.NpmGlobal,
			Dockerfile: f.Build.Dockerfile,
		})

		// Files this skill ships into the image. Resolve sources within the skill
		// dir (reject escapes) and require absolute destinations. Sorted by source
		// for deterministic build-context staging and COPY emission.
		dir := filepath.Join(skillsDir, name)
		for _, src := range sortedKeys(f.Build.Files) {
			dest := f.Build.Files[src]
			if !filepath.IsAbs(dest) {
				return Resolved{}, fmt.Errorf("skill %q: file destination %q must be an absolute image path", name, dest)
			}
			real, perr := skillRelPath(dir, src)
			if perr != nil {
				return Resolved{}, fmt.Errorf("skill %q: build file: %w", name, perr)
			}
			res.SkillFiles = append(res.SkillFiles, SkillFile{
				Skill: name, Src: real, Rel: filepath.Clean(src), Dest: dest,
			})
		}
		for k, v := range f.Runtime.Env {
			res.Env[k] = v
		}
		res.RunArgs = append(res.RunArgs, f.Runtime.RunArgs...)
		res.Caps = append(res.Caps, f.Runtime.Caps...)
		res.Mounts = append(res.Mounts, f.Runtime.Mounts...)
		res.Volumes = append(res.Volumes, f.Volumes...)

		if len(f.Runtime.Mounts) > 0 || len(f.Runtime.Caps) > 0 {
			res.Grants = append(res.Grants, Grant{Skill: name, Mounts: f.Runtime.Mounts, Caps: f.Runtime.Caps})
		}
		if sk.Context != "" {
			if res.Context != "" {
				res.Context += "\n\n"
			}
			res.Context += sk.Context
		}

		if name == cfg.Agent {
			if f.Agent == nil || f.Agent.Command == "" {
				return Resolved{}, fmt.Errorf("agent %q: skill has no [agent] command", name)
			}
			// If the agent declares a state volume, the skill must actually
			// contribute it — otherwise credentials won't persist (M5).
			if f.Agent.State != "" && !hasStateVolume(f.Volumes, f.Agent.State) {
				return Resolved{}, fmt.Errorf("agent %q: [agent].state %q is not a state volume contributed by the skill", name, f.Agent.State)
			}
			if t := f.Agent.ContextTarget; t != "" {
				if err := validateContextTarget(t); err != nil {
					return Resolved{}, fmt.Errorf("agent %q: [agent].context_target %q: %w", name, t, err)
				}
			}
			if p := f.Agent.Prefs; p != nil {
				if err := validatePrefs(p, f.Agent.State); err != nil {
					return Resolved{}, fmt.Errorf("agent %q: [agent.prefs]: %w", name, err)
				}
				res.AgentPrefs = p
			}
			res.AgentCommand = f.Agent.Command
			res.AgentState = f.Agent.State
			res.AgentContextTarget = f.Agent.ContextTarget
			agentFound = true
		}
	}

	if !agentFound {
		return Resolved{}, fmt.Errorf("agent %q: not among enabled skills", cfg.Agent)
	}
	return res, nil
}

// devHome is the in-box agent home; context_target must stay within it so a
// skill can't use the launcher's root-time context placement to write arbitrary
// container paths (e.g. /workspace, /etc/passwd).
const devHome = "/home/dev"

// validateContextTarget requires an absolute path contained within devHome.
func validateContextTarget(t string) error {
	if !filepath.IsAbs(t) {
		return fmt.Errorf("must be an absolute path")
	}
	rel, err := filepath.Rel(devHome, filepath.Clean(t))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must be a file strictly within %s", devHome)
	}
	return nil
}

// validatePrefs checks an [agent.prefs] block: it must declare a host source
// dir and at least one file, the agent must have a state volume for the prefs to
// land in, and every listed path must be relative and stay within `from` (the
// paths are copied into the state volume at the same relative location, so an
// absolute or escaping path could write outside it). It does NOT and cannot
// verify the files are secret-free — that is the skill author's responsibility.
func validatePrefs(p *PrefsSpec, state string) error {
	if p.From == "" {
		return fmt.Errorf("from is required")
	}
	if len(p.Files) == 0 {
		return fmt.Errorf("files is required (at least one pref path)")
	}
	if state == "" {
		return fmt.Errorf("requires [agent].state (a state volume to seed into)")
	}
	for _, f := range p.Files {
		if !relSafe(f) {
			return fmt.Errorf("file %q must be relative and stay within from", f)
		}
	}
	return nil
}

// relSafe reports whether p names a relative path strictly BELOW its root: no
// absolute path, no ".." escape, and not the root itself ("." — which for prefs
// would copy the entire from-dir, smuggling in the curated-out secret-bearing
// files). It compares the cleaned form, so "./x", "a/.." etc. are normalized.
func relSafe(p string) bool {
	if p == "" || filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// validateSkillName requires a skill name to be a single, non-relative path
// element — so it can't escape skillsDir on Load or the build-context staging dir.
func validateSkillName(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') ||
		strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf("invalid skill name %q (must be a single path element)", name)
	}
	return nil
}

// sortedKeys returns a map's keys in sorted order, for deterministic iteration.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hasStateVolume(vols []config.Volume, name string) bool {
	for _, v := range vols {
		if v.Name == name && v.Role == "state" {
			return true
		}
	}
	return false
}

// enabledSkillNames is cfg.Skills with the agent skill appended if not already
// present (the agent is enabled implicitly by the `agent` scalar).
func enabledSkillNames(cfg config.Config) []string {
	names := append([]string{}, cfg.Skills...)
	if cfg.Agent == "" {
		return names
	}
	for _, n := range names {
		if n == cfg.Agent {
			return names
		}
	}
	return append(names, cfg.Agent)
}
