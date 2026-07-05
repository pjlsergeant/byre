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
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"byre/internal/config"
)

// postureRe bounds a declared network_posture to a short display label —
// status prints it verbatim, so it must not carry spaces, parens, or control
// characters that could forge the surrounding status annotations.
var postureRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// egressHostRe bounds an egress hostname: a plain DNS name (or IP), no shell
// metacharacters. The value is passed to the netns helper as an env var and
// used there in `getent`/`iptables` args, so keeping it to hostname characters
// keeps it injection-safe and legible.
var egressHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.:_-]*[A-Za-z0-9])?$`)

// parseEgress splits a `host` or `host:port` entry, defaulting the port to
// 443. It rejects a malformed host or an out-of-range port. A bracketed IPv6
// literal ("[::1]:443") is out of scope — egress entries are hostnames or
// IPv4; a bare IPv6 with colons is ambiguous with host:port, so reject it.
func parseEgress(entry string) (host string, port int, err error) {
	e := strings.TrimSpace(entry)
	if e == "" {
		return "", 0, fmt.Errorf("empty egress entry")
	}
	host, port = e, 443
	// Split a trailing :port only when the tail is all digits, so a hostname
	// stays intact and an accidental IPv6 (multiple colons) is caught below.
	if i := strings.LastIndexByte(e, ':'); i >= 0 {
		if p, perr := strconv.Atoi(e[i+1:]); perr == nil {
			host, port = e[:i], p
		}
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("egress %q: port out of range", entry)
	}
	if strings.ContainsRune(host, ':') || !egressHostRe.MatchString(host) {
		return "", 0, fmt.Errorf("egress %q: not a valid host[:port]", entry)
	}
	return host, port, nil
}

// AgentContrib is the agent-skill launch contribution.
type AgentContrib struct {
	Command string `toml:"command"` // what the launcher execs (e.g. "claude --dangerously-skip-permissions")
	// State names the skill's state volume. Load-bearing, not informational:
	// Resolve requires the skill to contribute it (credentials must persist),
	// and seed_prefs seeds into it.
	State string `toml:"state"`
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
		// NetworkPosture is the network stance this skill establishes (e.g.
		// "deny-by-default"). Purely declarative: byre prints it in status and
		// the launch line instead of the default "open", attributed to the
		// skill — core never inspects or enforces it. Status degrades the claim
		// when project-level raw escape hatches could undermine it (see
		// commands/status).
		NetworkPosture string `toml:"network_posture"`
		// NetnsInit names an entrypoint (absolute image path) that byre runs in
		// the box's network namespace as root with CAP_NET_ADMIN, from a
		// run-to-completion helper container, after the box starts. This is the
		// firewall skill's application vehicle: rules are programmed from
		// OUTSIDE the box, so nothing inside it needs (or gets) privileges.
		NetnsInit string `toml:"netns_init"`
		// Egress is the set of hosts this skill needs to reach, as `host` or
		// `host:port` (port defaults to 443). A network-posture skill
		// (firewall) unions every enabled skill's Egress into its allowlist —
		// so an agent skill carries its OWN API endpoints rather than the
		// firewall hardcoding them, and enabling only what you use opens only
		// what it needs. Declarative: with no firewall enabled it does nothing.
		Egress []string `toml:"egress"`
	} `toml:"runtime"`
	Agent   *AgentContrib   `toml:"agent"`
	Volumes []config.Volume `toml:"volumes"`
	Context struct {
		Text string `toml:"text"` // inline snippet
		File string `toml:"file"` // path (relative to the skill dir) to a snippet
	} `toml:"context"`
}

// Skill is a loaded skill with its context text resolved. Files is filled by
// Resolve (Load alone doesn't validate build files).
type Skill struct {
	Name    string
	File    File
	Context string      // resolved context snippet
	Files   []SkillFile // resolved [build].files, sorted by source
}

// Grant records a single skill's runtime grants, for legible attribution in
// `byre status` and the adoption review (e.g. which skill mounts a host
// socket, or passes raw docker run args).
type Grant struct {
	Skill     string
	Mounts    []config.Mount
	Caps      []string
	RunArgs   []string
	NetnsInit string // entrypoint run in the box's netns as root (see Runtime.NetnsInit)
}

// SkillFile is one resolved file a skill ships into the image: a source inside
// the skill's own dir (validated for containment) copied to an absolute image
// path. The build stage stages Src into the build context; gen emits the COPY.
type SkillFile struct {
	Src  string // absolute host path, resolved within the skill dir
	Rel  string // cleaned skill-relative source (preserves subdirs for staging)
	Dest string // absolute image path
}

// BuildBlock is one skill's build contribution, in enable order — the package's
// own view of it, so skills doesn't import the generator; build maps it onto
// gen.SkillBlock (and stages Files into the build context).
type BuildBlock struct {
	Name       string
	Apt        []string
	NpmGlobal  []string
	Dockerfile []string    // raw lines
	Files      []SkillFile // files this skill ships into the image
}

// Resolved is the set of enabled skills — loaded and validated, in enable
// order — plus the selected agent's contribution. Everything else (env,
// mounts, grants, build blocks, ...) is DERIVED by methods, so an aggregate
// can't drift from the per-skill data it projects.
type Resolved struct {
	Skills []Skill
	// Agent is the selected agent skill's [agent] block (nil when no agent is
	// configured). The skill it came from is also in Skills.
	Agent *AgentContrib
}

// Names lists the enabled skills, in enable order.
func (r Resolved) Names() []string {
	names := make([]string, 0, len(r.Skills))
	for _, sk := range r.Skills {
		names = append(names, sk.Name)
	}
	return names
}

// BuildBlocks is the per-skill build contributions, in enable order.
func (r Resolved) BuildBlocks() []BuildBlock {
	blocks := make([]BuildBlock, 0, len(r.Skills))
	for _, sk := range r.Skills {
		blocks = append(blocks, BuildBlock{
			Name:       sk.Name,
			Apt:        sk.File.Build.Apt,
			NpmGlobal:  sk.File.Build.NpmGlobal,
			Dockerfile: sk.File.Build.Dockerfile,
			Files:      sk.Files,
		})
	}
	return blocks
}

// Env merges the skills' runtime env. Resolve rejected cross-skill conflicts,
// so the merge is order-independent.
func (r Resolved) Env() map[string]string {
	env := map[string]string{}
	for _, sk := range r.Skills {
		for k, v := range sk.File.Runtime.Env {
			env[k] = v
		}
	}
	return env
}

// RunArgs concatenates the skills' raw run args, in enable order.
func (r Resolved) RunArgs() []string {
	var out []string
	for _, sk := range r.Skills {
		out = append(out, sk.File.Runtime.RunArgs...)
	}
	return out
}

// Caps concatenates the skills' added capabilities, in enable order.
func (r Resolved) Caps() []string {
	var out []string
	for _, sk := range r.Skills {
		out = append(out, sk.File.Runtime.Caps...)
	}
	return out
}

// Mounts concatenates the skills' host mounts, in enable order.
func (r Resolved) Mounts() []config.Mount {
	var out []config.Mount
	for _, sk := range r.Skills {
		out = append(out, sk.File.Runtime.Mounts...)
	}
	return out
}

// Volumes concatenates the skills' named volumes, in enable order.
func (r Resolved) Volumes() []config.Volume {
	var out []config.Volume
	for _, sk := range r.Skills {
		out = append(out, sk.File.Volumes...)
	}
	return out
}

// Grants projects each skill's runtime grants (mounts, caps, raw run args,
// netns hooks) for attribution in status and the adoption review.
func (r Resolved) Grants() []Grant {
	var out []Grant
	for _, sk := range r.Skills {
		rt := sk.File.Runtime
		if len(rt.Mounts) > 0 || len(rt.Caps) > 0 || len(rt.RunArgs) > 0 || rt.NetnsInit != "" {
			out = append(out, Grant{Skill: sk.Name, Mounts: rt.Mounts, Caps: rt.Caps, RunArgs: rt.RunArgs, NetnsInit: rt.NetnsInit})
		}
	}
	return out
}

// NetnsHook is one skill's declared netns-init entrypoint (see
// Runtime.NetnsInit), attributed to the skill for error messages and status.
type NetnsHook struct {
	Skill string
	Path  string
}

// NetnsInits lists the declared netns-init hooks, in enable order.
func (r Resolved) NetnsInits() []NetnsHook {
	var out []NetnsHook
	for _, sk := range r.Skills {
		if p := sk.File.Runtime.NetnsInit; p != "" {
			out = append(out, NetnsHook{Skill: sk.Name, Path: p})
		}
	}
	return out
}

// EgressAllow is one host:port an enabled skill needs to reach, attributed to
// the skill — for status legibility (which skill opened which hole).
type EgressAllow struct {
	Skill string
	Host  string
	Port  int
}

// EgressAllows lists every enabled skill's egress entries, parsed and
// attributed, in enable order. Resolve validated them, so parsing can't fail.
func (r Resolved) EgressAllows() []EgressAllow {
	var out []EgressAllow
	for _, sk := range r.Skills {
		for _, e := range sk.File.Runtime.Egress {
			host, port, err := parseEgress(e)
			if err != nil {
				continue // unreachable: Resolve validated every entry
			}
			out = append(out, EgressAllow{Skill: sk.Name, Host: host, Port: port})
		}
	}
	return out
}

// Egress is the deduped, normalized (host:port) union of every enabled skill's
// egress entries — what a network-posture skill's helper consumes to build its
// allowlist. Order is first-seen across skills, so it's deterministic.
func (r Resolved) Egress() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range r.EgressAllows() {
		hp := fmt.Sprintf("%s:%d", a.Host, a.Port)
		if !seen[hp] {
			seen[hp] = true
			out = append(out, hp)
		}
	}
	return out
}

// NetworkPosture is the declared network posture and the skill declaring it
// ("", "" when no enabled skill declares one — the caller renders the default
// "open"). Resolve rejected conflicting declarations, so the first is the only.
func (r Resolved) NetworkPosture() (posture, skill string) {
	for _, sk := range r.Skills {
		if p := sk.File.Runtime.NetworkPosture; p != "" {
			return p, sk.Name
		}
	}
	return "", ""
}

// Context concatenates the skills' context snippets, in enable order.
func (r Resolved) Context() string {
	var b strings.Builder
	for _, sk := range r.Skills {
		if sk.Context == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(sk.Context)
	}
	return b.String()
}

// AgentCommand is the selected agent's launch command ("" if no agent).
func (r Resolved) AgentCommand() string {
	if r.Agent == nil {
		return ""
	}
	return r.Agent.Command
}

// AgentState is the selected agent's state volume name ("" if none).
func (r Resolved) AgentState() string {
	if r.Agent == nil {
		return ""
	}
	return r.Agent.State
}

// AgentContextTarget is where the selected agent reads project memory; the
// launcher places Context there at runtime. "" if no agent declares one.
func (r Resolved) AgentContextTarget() string {
	if r.Agent == nil {
		return ""
	}
	return r.Agent.ContextTarget
}

// AgentPrefs is the selected agent's curated seedable prefs (nil if none). The
// seed only runs when the user opts in (config seed_prefs) and the agent's
// state volume is fresh.
func (r Resolved) AgentPrefs() *PrefsSpec {
	if r.Agent == nil {
		return nil
	}
	return r.Agent.Prefs
}

// ListSkills returns the names of all skills in skillsDir, sorted. This is the
// full set selectable via the `skills` list — including agent skills, which can
// legitimately be enabled as a plain skill (e.g. codex installed for
// byre-codereview while claude is the launched agent), separate from the
// `agent` choice.
func ListSkills(skillsDir string) []string {
	return list(skillsDir, func(Skill) bool { return true })
}

// ListAgentSkills returns the names of skills in skillsDir that provide an
// [agent] command (i.e. can be selected as `agent`), sorted.
func ListAgentSkills(skillsDir string) []string {
	return list(skillsDir, func(sk Skill) bool {
		return sk.File.Agent != nil && sk.File.Agent.Command != ""
	})
}

// list returns the sorted names of skills in skillsDir that load cleanly and
// satisfy keep. Broken skills are skipped rather than failing the listing;
// non-dirs and dot-prefixed entries (temp/stash dirs from `byre skill update`,
// backups) are not skills.
func list(skillsDir string, keep func(Skill) bool) []string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sk, err := Load(skillsDir, e.Name())
		if err != nil || !keep(sk) {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
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

// Resolve loads and validates every enabled skill (the cfg.Skills list, plus
// the cfg.Agent skill enabled implicitly). The selected agent's skill must
// exist and provide an [agent] command. Cross-skill env-key conflicts are an
// error: two skills setting the SAME key to DIFFERENT values would otherwise
// resolve by enable order — silent and surprising.
func Resolve(cfg config.Config, skillsDir string) (Resolved, error) {
	names := enabledSkillNames(cfg)

	var res Resolved
	envSetBy := map[string]string{} // env key -> skill that set it
	postureBy := ""                 // skill that declared network_posture
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

		// A skill's build content is interpolated into the same generated
		// Dockerfile/shell as the project config, so hold its typed fields to the
		// same allowlists — not as a trust boundary (a skill you enabled can run
		// anything via a raw [build].dockerfile line), but so a typed field stays
		// legible data: `apt` holds package names, and the escape hatch for
		// arbitrary commands is the explicit raw block. Env values are only ever
		// emitted %q-quoted, so only keys are checked (via ValidateContent).
		if err := config.ValidateContent("", f.Build.Apt, f.Build.NpmGlobal, f.Runtime.Env); err != nil {
			return Resolved{}, fmt.Errorf("skill %q: %w", name, err)
		}

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
			sk.Files = append(sk.Files, SkillFile{Src: real, Rel: filepath.Clean(src), Dest: dest})
		}

		// network_posture is printed by status; hold it to a tight shape so a
		// skill can't smuggle formatting/control text into the output, and
		// reject two skills both claiming the network stance — there is one
		// network, so one declared posture (unlike env, even equal duplicates
		// are refused: each claims to have established the stance).
		if p := f.Runtime.NetworkPosture; p != "" {
			if !postureRe.MatchString(p) {
				return Resolved{}, fmt.Errorf("skill %q: network_posture %q: must match %s", name, p, postureRe)
			}
			if postureBy != "" {
				return Resolved{}, fmt.Errorf("skills %q and %q both declare a network_posture; disable one", postureBy, name)
			}
			postureBy = name
		}
		// netns_init runs as root in the box's netns; require an absolute image
		// path so it stays legible data (the script itself is skill-shipped).
		if p := f.Runtime.NetnsInit; p != "" && !filepath.IsAbs(p) {
			return Resolved{}, fmt.Errorf("skill %q: netns_init %q must be an absolute image path", name, p)
		}
		// egress entries feed a firewall allowlist and are passed to the netns
		// helper as data; validate host[:port] shape up front so a typo fails
		// loudly rather than silently dropping a host from the allowlist.
		for _, e := range f.Runtime.Egress {
			if _, _, eerr := parseEgress(e); eerr != nil {
				return Resolved{}, fmt.Errorf("skill %q: %w", name, eerr)
			}
		}

		// Cross-skill env conflicts: a differing value for the same key would be
		// resolved by enable order — refuse instead. The same value twice is
		// harmless (order-independent) and allowed.
		for _, k := range sortedKeys(f.Runtime.Env) {
			if other, ok := envSetBy[k]; ok && other != name {
				if prev := envValue(res.Skills, other, k); prev != f.Runtime.Env[k] {
					return Resolved{}, fmt.Errorf("skills %q and %q both set env %s to different values; disable one or align them", other, name, k)
				}
				continue
			}
			envSetBy[k] = name
		}

		res.Skills = append(res.Skills, sk)

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
			}
			res.Agent = f.Agent
			agentFound = true
		}
	}

	if !agentFound {
		return Resolved{}, fmt.Errorf("agent %q: not among enabled skills", cfg.Agent)
	}
	return res, nil
}

// DevHome is the in-box agent home. The generated image bakes the dev user
// with this home (see internal/gen's infra layer and launcher — they spell it
// literally in shell/Dockerfile text, pinned by gen's golden test), and
// context_target must stay within it so a skill can't use the launcher's
// context placement to write arbitrary container paths (e.g. /workspace,
// /etc/passwd).
const DevHome = "/home/dev"

// validateContextTarget requires an absolute path contained within DevHome.
func validateContextTarget(t string) error {
	if !filepath.IsAbs(t) {
		return fmt.Errorf("must be an absolute path")
	}
	rel, err := filepath.Rel(DevHome, filepath.Clean(t))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must be a file strictly within %s", DevHome)
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
		// Strictly below `from`: "." would copy the entire from-dir, smuggling in
		// the curated-out secret-bearing files.
		if !config.RelSafe(f) {
			return fmt.Errorf("file %q must be relative and stay within from", f)
		}
	}
	return nil
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

// envValue looks up the env value skill `skill` set for key k (for conflict
// error messages).
func envValue(sks []Skill, skill, k string) string {
	for _, sk := range sks {
		if sk.Name == skill {
			return sk.File.Runtime.Env[k]
		}
	}
	return ""
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
