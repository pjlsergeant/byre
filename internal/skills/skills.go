// Package skills loads skill packages from the multi-provider catalog and
// resolves their contributions to the layers byre controls: build (per-skill
// Dockerfile block), runtime (mounts/env/caps/run_args), state (named volumes),
// agent context, and — for agent skills — the launch command.
//
// "The agent is a skill": the `agent` config scalar names which enabled skill
// provides the default launch command. Names are resolved through the catalog
// (aliases expand to canonical IDs) before load.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// postureRe bounds a declared network_posture to a short display label —
// status prints it verbatim, so it must not carry spaces, parens, or control
// characters that could forge the surrounding status annotations.
var postureRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// oneLinerMaxLen bounds a skill's declared one-liners (containment,
// env_docs guidance). Status, launch, adoption, and the config UI print them
// as data on their own rows; a long blob would crowd the surfaces without
// adding honesty.
const oneLinerMaxLen = 300

// parseEgress delegates to the shared `host[:port]` grammar in config — the
// egress config key (ADR 0019) and skill egress are validated by one parser.
func parseEgress(entry string) (host string, port int, err error) {
	return config.ParseEgress(entry)
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
	// MCP is how THIS agent's session receives byre's declared MCP servers
	// (the [[mcp]] set): "inject" means the agent command itself consumes the
	// baked /etc/byre/mcp.json (e.g. claude's --mcp-config flag) — the skill
	// author VOUCHES the command does so. Absent means the agent has no MCP
	// adapter: declared servers still bake into the file, and status reports
	// them declared-but-not-delivered with the file path. Closed set —
	// injection is byre's only adapter mechanism (ADR 0033); an unknown
	// value is rejected, not treated as a vouch.
	MCP string `toml:"mcp"`
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
	// Description is a one-line, human-facing summary shown wherever skills
	// are enumerated side by side (e.g. the config UI's skills screen), so
	// near-namesakes like claude vs claude-shared-auth are distinguishable at
	// the point of choice. Optional for hand-dropped skills; every builtin
	// carries one.
	Description string `toml:"description"`
	// SharedAuthFor declares this skill as the shared-auth companion (ADR
	// 0017) for the named agent skill, making it OFFERABLE: when that agent
	// is selected, the onboarding picker asks whether to opt that box into
	// the agent's shared credentials (ADR 0025). Declaring the key is the
	// author VOUCHING the companion is ready to enable — a broken or
	// gate-pending companion (grok-shared-auth, gemini's OAuth path) omits
	// it and stays a hand-enabled expert option.
	SharedAuthFor string `toml:"shared_auth_for"`
	Build         struct {
		Apt        []string          `toml:"apt"`
		NpmGlobal  []string          `toml:"npm_global"`
		Dockerfile []string          `toml:"dockerfile"` // raw build lines
		Files      map[string]string `toml:"files"`      // skill-relative src -> absolute image dest
	} `toml:"build"`
	Runtime struct {
		Env map[string]string `toml:"env"`
		// EnvDocs documents env vars this skill CONSUMES but does not set:
		// var name -> a one-line guidance string (where the value comes from,
		// what it unlocks). Purely declarative — no validation of the box, no
		// warning when unset; the config UI env screen renders each undeclared
		// var as a dim suggestion row attributed to the skill. Guidance is
		// held to the same single-line/no-control-char/bounded shape as
		// containment so it stays legible DATA.
		EnvDocs map[string]string `toml:"env_docs"`
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
		// Egress is the set of hosts this skill NEEDS to reach to function, as
		// `host` or `host:port` (port defaults to 443). A network-posture
		// skill (firewall) unions every enabled skill's Egress into its
		// allowlist — an agent skill carries its OWN API endpoints, and
		// enabling the skill is the intent to open them (ADR 0020: functional
		// requirement, not convenience). Declarative: with no firewall enabled
		// it does nothing.
		Egress []string `toml:"egress"`
		// EgressOffered is a declared-but-CLOSED door (ADR 0020): same
		// grammar, never enforced. The config UI offers each entry as a
		// switch; opening writes it into the user's own config `egress`.
		// Convenience endpoints (registries, git hosting) belong here, not in
		// Egress — deny-by-default means the user opens their own doors.
		EgressOffered []string `toml:"egress_offered"`
		// SockGroups lists absolute in-box paths whose owning group the runner
		// must make reachable to the unprivileged dev user via numeric
		// --group-add at create time (docker-host's socket grant). Each path
		// must also be an active bind target on the same skill — the group
		// grant is wider than the named inode (every inode carrying that gid),
		// so it is itself an attributed grant (Grant.SockGroups).
		SockGroups []string `toml:"sock_groups"`
		// Containment is a skill-owned one-liner declaring a containment hole
		// (e.g. host Docker socket access). Purely declarative: byre prints it
		// attributed on status/launch/adoption/config UI and never inspects or
		// enforces it. Unlike network_posture (single declarer), several skills
		// may declare containment — all are rendered. Validated for single-line
		// / no control chars / bounded length so it stays legible DATA.
		Containment string `toml:"containment"`
	} `toml:"runtime"`
	Agent *AgentContrib `toml:"agent"`
	// MCPs are MCP servers this skill declares ([[mcp]] blocks, same grammar
	// as the config key). They union into the effective set AFTER the config
	// cascade merges (MCPSet); a config `!name` closure can subtract one.
	// Wiring, not grants: the carried egress/env render attributed mcp:<name>.
	MCPs    []config.MCP    `toml:"mcp"`
	Volumes []config.Volume `toml:"volumes"`
	Context struct {
		Text string `toml:"text"` // inline snippet
		File string `toml:"file"` // path (relative to the skill dir) to a snippet
	} `toml:"context"`
}

// IsStub reports whether a skill contributes NOTHING to a box -- no build
// content, no runtime grants, no volumes, no agent, no context, no
// companionship claim: a description-only compatibility shell (devloop,
// grok-shared-auth). Stubs exist so configs naming them keep resolving; a
// picker has nothing to offer for one -- it is only shown when a config
// already references it (so it can be un-referenced).
func IsStub(f File) bool {
	rt := f.Runtime
	return f.Agent == nil &&
		f.SharedAuthFor == "" &&
		len(f.Build.Apt) == 0 && len(f.Build.NpmGlobal) == 0 &&
		len(f.Build.Dockerfile) == 0 && len(f.Build.Files) == 0 &&
		len(rt.Env) == 0 && len(rt.EnvDocs) == 0 && len(rt.RunArgs) == 0 && len(rt.Caps) == 0 &&
		len(rt.Mounts) == 0 && rt.NetworkPosture == "" && rt.NetnsInit == "" &&
		len(rt.Egress) == 0 && len(rt.EgressOffered) == 0 &&
		len(rt.SockGroups) == 0 && rt.Containment == "" &&
		len(f.MCPs) == 0 &&
		len(f.Volumes) == 0 &&
		f.Context.Text == "" && f.Context.File == ""
}

// Skill is a loaded skill with its context text resolved. Files is filled by
// Resolve (Load alone doesn't validate build files). Name is the canonical
// package ID (aliases are expanded at load).
type Skill struct {
	Name    string
	File    File
	Context string      // resolved context snippet
	Files   []SkillFile // resolved [build].files, sorted by source
	dir     string      // host directory for payload resolution (set by loadEntry)
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
	// SockGroups are absolute in-box paths whose owning gid the runner will
	// --group-add (see Runtime.SockGroups). Wider than the named path alone.
	SockGroups []string
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
// netns hooks, sock_groups) for attribution in status and the adoption review.
func (r Resolved) Grants() []Grant {
	var out []Grant
	for _, sk := range r.Skills {
		rt := sk.File.Runtime
		if len(rt.Mounts) > 0 || len(rt.Caps) > 0 || len(rt.RunArgs) > 0 || rt.NetnsInit != "" || len(rt.SockGroups) > 0 {
			out = append(out, Grant{
				Skill:      sk.Name,
				Mounts:     rt.Mounts,
				Caps:       rt.Caps,
				RunArgs:    rt.RunArgs,
				NetnsInit:  rt.NetnsInit,
				SockGroups: append([]string{}, rt.SockGroups...),
			})
		}
	}
	return out
}

// SockGroup is one skill-declared sock_groups path (see Runtime.SockGroups),
// attributed for probe failures and grant rendering.
type SockGroup struct {
	Skill string
	Path  string // absolute in-box path (must match a bind target)
}

// SockGroups lists every enabled skill's sock_groups entries, in enable order.
func (r Resolved) SockGroups() []SockGroup {
	var out []SockGroup
	for _, sk := range r.Skills {
		for _, p := range sk.File.Runtime.SockGroups {
			out = append(out, SockGroup{Skill: sk.Name, Path: p})
		}
	}
	return out
}

// ContainmentDecl is one skill's declared containment hole one-liner, for
// rendering on status/launch/adoption/config UI (see Runtime.Containment).
type ContainmentDecl struct {
	Skill string
	Text  string
}

// Containments lists every enabled skill's containment declaration, in enable
// order. Multi-declarer: all are returned; never last-wins.
func (r Resolved) Containments() []ContainmentDecl {
	var out []ContainmentDecl
	for _, sk := range r.Skills {
		if t := sk.File.Runtime.Containment; t != "" {
			out = append(out, ContainmentDecl{Skill: sk.Name, Text: t})
		}
	}
	return out
}

// EnvDoc is one skill's declared consumed-env guidance line (see
// Runtime.EnvDocs): the skill reads Name at runtime; Text says how to supply
// it. Attributed to the skill for suggestion rendering.
type EnvDoc struct {
	Skill string
	Name  string
	Text  string
}

// EnvDocs lists every enabled skill's env_docs declarations, sorted by var
// name then skill, for stable rendering. Several skills documenting the same
// var is fine — docs don't conflict; all rows are returned.
func (r Resolved) EnvDocs() []EnvDoc {
	var out []EnvDoc
	for _, sk := range r.Skills {
		for _, k := range sortedKeys(sk.File.Runtime.EnvDocs) {
			out = append(out, EnvDoc{Skill: sk.Name, Name: k, Text: sk.File.Runtime.EnvDocs[k]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Skill < out[j].Skill
	})
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

// EgressFromConfig is the Skill attribution for egress entries contributed by
// the project's own `egress` config key rather than a skill. Status both
// produces it (configEgress) and filters on it (config entries still print,
// marked unenforced, when no posture is active — ADR 0019).
const EgressFromConfig = "config"

// The open-denylist posture vocabulary lives in config (PostureOpenDenylist,
// PostureEnforcesAllowlist) — the lowest legibility surface (config.Exposure)
// needs it, and this package already builds on config.

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

// ListSkills returns display names of all loadable skills in the catalog,
// sorted. Bundled skills appear under their bare alias; local/installed
// under their canonical ID. This is the set selectable via the `skills` list —
// including agent skills, which can legitimately be enabled as a plain skill.
func ListSkills(cat *packages.Catalog) []string {
	return list(cat, func(Skill) bool { return true })
}

// DescribeSkills returns each cleanly-loading skill's one-line description,
// keyed by display name. Skills without a description are absent from the map.
func DescribeSkills(cat *packages.Catalog) map[string]string {
	out := map[string]string{}
	for _, name := range ListSkills(cat) {
		if sk, err := Load(cat, name); err == nil && sk.File.Description != "" {
			out[name] = sk.File.Description
		}
	}
	return out
}

// SharedAuthClaimants returns every loadable skill that declares itself a
// shared-auth companion for agent (exact canonical-ID match after alias
// expansion). Bundled claimants list first; order among peers is by
// display name. Empty when none claim.
func SharedAuthClaimants(cat *packages.Catalog, agent string) []Skill {
	if agent == "" || cat == nil {
		return nil
	}
	// Pairing is by exact canonical ID.
	agentCanon := cat.ExpandAlias(agent)
	if agentCanon == "none" || agentCanon == "" {
		return nil
	}
	var bundled, other []Skill
	for _, ent := range cat.ListLoadable(packages.KindSkill) {
		sk, err := loadEntry(ent)
		if err != nil {
			continue
		}
		claim := cat.ExpandAlias(sk.File.SharedAuthFor)
		if claim == "" || claim != agentCanon {
			continue
		}
		if ent.Provenance == packages.ProvBundled {
			bundled = append(bundled, sk)
		} else {
			other = append(other, sk)
		}
	}
	sort.Slice(bundled, func(i, j int) bool { return bundled[i].Name < bundled[j].Name })
	sort.Slice(other, func(i, j int) bool { return other[i].Name < other[j].Name })
	return append(bundled, other...)
}

// SharedAuthCompanion returns the single ready shared-auth companion for
// agent, or "" when none or several claim (legacy single-claim helper). Prefer
// SharedAuthClaimants + picker for multi-claim.
func SharedAuthCompanion(cat *packages.Catalog, agent string) string {
	cs := SharedAuthClaimants(cat, agent)
	if len(cs) != 1 {
		return ""
	}
	// Prefer display/alias form for writing into config.
	if ent, ok := cat.Lookup(cs[0].Name); ok && ent.Alias != "" {
		return ent.Alias
	}
	return cs[0].Name
}

// ListAgentSkills returns display names of skills that provide an [agent]
// command (i.e. can be selected as `agent`), sorted.
func ListAgentSkills(cat *packages.Catalog) []string {
	return list(cat, func(sk Skill) bool {
		return sk.File.Agent != nil && sk.File.Agent.Command != ""
	})
}

// list returns sorted display names of loadable skills that satisfy keep.
func list(cat *packages.Catalog, keep func(Skill) bool) []string {
	if cat == nil {
		return nil
	}
	var out []string
	for _, ent := range cat.ListLoadable(packages.KindSkill) {
		sk, err := loadEntry(ent)
		if err != nil || !keep(sk) {
			continue
		}
		out = append(out, ent.DisplayName())
	}
	sort.Strings(out)
	return out
}

// Load reads and resolves a single skill by name (alias or canonical ID)
// through the catalog.
func Load(cat *packages.Catalog, name string) (Skill, error) {
	if cat == nil {
		return Skill{}, fmt.Errorf("skill %q: no catalog", name)
	}
	ent, err := cat.ResolveName(name)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", name, err)
	}
	if ent.Kind != packages.KindSkill {
		return Skill{}, fmt.Errorf("package %q is a %s, not a skill", ent.ID, ent.Kind)
	}
	return loadEntry(ent)
}

func init() {
	// Eager stage-2 for local skill catalog rows (round 3): primary only.
	packages.Stage2Skill = ValidatePrimaryBytes
}

// ValidatePrimaryBytes is the stage-2 skill.toml check used by catalog ingest
// and validate: strip [package], strict-decode schema (unknown keys fail).
// Does not resolve context files or build payloads (no extra I/O).
func ValidatePrimaryBytes(raw []byte) error {
	_, err := ParsePrimaryBytes(raw)
	return err
}

// ParsePrimaryBytes strict-parses skill.toml bytes into the File schema
// (stage 2, primary only -- no payload/context I/O). Used by install's grant
// summary, which must render what a manifest DECLARES before any snapshot
// exists to load.
func ParsePrimaryBytes(raw []byte) (File, error) {
	body := packages.StripPackageTable(raw)
	var f File
	md, err := toml.Decode(string(body), &f)
	if err != nil {
		return File{}, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return File{}, fmt.Errorf("unknown key(s) in skill.toml: %v", und)
	}
	return f, nil
}

// loadEntry strict-parses a skill entry's primary file (stage 2 after the
// catalog's stage-1 [package] check).
func loadEntry(ent *packages.Entry) (Skill, error) {
	raw, err := ent.ReadPrimary()
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, err)
	}
	// Stage 2: strip [package] so the strict skill schema does not see it.
	body := packages.StripPackageTable(raw)

	var f File
	md, err := toml.Decode(string(body), &f)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, err)
	}
	// byre owns the skill.toml schema — a typo'd key is an error, not a silent
	// no-op that produces a broken skill.
	if und := md.Undecoded(); len(und) > 0 {
		return Skill{}, fmt.Errorf("skill %q: unknown key(s) in skill.toml: %v", ent.ID, und)
	}
	// Prefer [package].description when the body has none.
	if f.Description == "" && ent.Description != "" {
		f.Description = ent.Description
	}

	// A skill's mounts and volumes join the same docker run command as the
	// config's own, so hold them to the same shape rules — config.Validate is
	// the one owner of mount/volume shape (role, seed combinations, target
	// grammar, host-path form) plus intra-skill name/target collisions.
	// Checked at load so `byre skill validate` green means the skill's grants
	// can actually run, instead of the shape error surfacing at the next
	// develop. Cross-skill/config collisions remain the resolved set's check
	// (commands.resolve).
	if err := (config.Config{Mounts: f.Runtime.Mounts, Volumes: f.Volumes}).Validate(); err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, err)
	}

	dir, err := ent.HostDir()
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, err)
	}
	ctx := f.Context.Text
	if f.Context.File != "" {
		path, perr := skillRelPath(dir, f.Context.File)
		if perr != nil {
			return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, perr)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return Skill{}, fmt.Errorf("skill %q context: %w", ent.ID, rerr)
		}
		ctx = string(b)
	}
	// Skill.Name is the canonical ID (comparisons, grants, status).
	return Skill{Name: ent.ID, File: f, Context: ctx, dir: dir}, nil
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
// the cfg.Agent skill enabled implicitly). Names are expanded through the
// catalog (aliases -> canonical IDs). The selected agent's skill must exist
// and provide an [agent] command. Cross-skill env-key conflicts are an error:
// two skills setting the SAME key to DIFFERENT values would otherwise resolve
// by enable order — silent and surprising.
func Resolve(cfg config.Config, cat *packages.Catalog) (Resolved, error) {
	if cat == nil {
		return Resolved{}, fmt.Errorf("skills: no catalog")
	}
	// Expand aliases so enable-order comparisons and agent matching use
	// canonical IDs. Config resolution should already have done this;
	// re-expanding is idempotent and keeps Resolve self-contained for tests.
	cfg.Agent = cat.ExpandAlias(cfg.Agent)
	for i, s := range cfg.Skills {
		cfg.Skills[i] = cat.ExpandAlias(s)
	}
	names := enabledSkillNames(cfg)

	var res Resolved
	envSetBy := map[string]string{} // env key -> skill that set it
	postureBy := ""                 // skill that declared network_posture
	netnsBy := ""                   // skill that declared netns_init
	agentFound := cfg.Agent == "" || cfg.Agent == "none"

	for _, name := range names {
		if name == "" || name == "none" {
			continue
		}
		// ID grammar is the load-bearing name check; rejects path escapes.
		if err := packages.ValidateID(strings.TrimPrefix(name, "!"), true); err != nil {
			return Resolved{}, fmt.Errorf("invalid skill name %q: %w", name, err)
		}
		sk, err := Load(cat, name)
		if err != nil {
			// Missing-reference errors always print the remedy: the
			// exact install command when a [sources] hint names one. Never
			// fetched -- acquisition on a third party's initiative is banned.
			if hint, ok := cfg.Sources[name]; ok {
				return Resolved{}, fmt.Errorf("%w\n  install it: %s", err, hint.InstallHint("skill"))
			}
			return Resolved{}, err
		}
		// Use canonical name everywhere downstream.
		name = sk.Name
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
		dir := sk.dir
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
		// And exactly ONE hook per box (mirroring the posture rule above): the
		// launch gate is opened by the hook's own script when it finishes (see
		// the firewall skill), so with two hooks the first would release the
		// agent before the second ran — its setup silently unapplied. If
		// multi-hook composition is ever wanted, gate signaling must first
		// move into byre's orchestrator (opened only after EVERY hook
		// succeeds); until then, refuse the ambiguity.
		if p := f.Runtime.NetnsInit; p != "" {
			if !filepath.IsAbs(p) {
				return Resolved{}, fmt.Errorf("skill %q: netns_init %q must be an absolute image path", name, p)
			}
			if netnsBy != "" {
				return Resolved{}, fmt.Errorf("skills %q and %q both declare a netns_init; disable one", netnsBy, name)
			}
			netnsBy = name
		}
		// egress entries feed a firewall allowlist and are passed to the netns
		// helper as data; validate host[:port] shape up front so a typo fails
		// loudly rather than silently dropping a host from the allowlist.
		// Offered entries (ADR 0020) are held to the same grammar: they become
		// real egress the moment a user opens one.
		for _, e := range append(append([]string{}, f.Runtime.Egress...), f.Runtime.EgressOffered...) {
			if _, _, eerr := parseEgress(e); eerr != nil {
				return Resolved{}, fmt.Errorf("skill %q: %w", name, eerr)
			}
		}

		// MCP declarations: same shape bar as the config key (one validator,
		// config.ValidateMCP). Markers are config vocabulary — a skill
		// DECLARES servers, it doesn't subtract them — and the name grammar
		// rejects '!' anyway. Intra-skill duplicates refuse here; duplicates
		// across sources (config+skill, skill+skill) are MCPSet's hard reject.
		mcpNames := map[string]bool{}
		for _, m := range f.MCPs {
			if err := config.ValidateMCP(m); err != nil {
				return Resolved{}, fmt.Errorf("skill %q: %w", name, err)
			}
			if mcpNames[m.Name] {
				return Resolved{}, fmt.Errorf("skill %q: mcp %s declared twice", name, m.Name)
			}
			mcpNames[m.Name] = true
		}

		// sock_groups: absolute paths that must also be active bind targets on
		// this skill (the runner probes the bind and --group-adds the gid). A
		// path with no matching mount would be a silent no-op — refuse.
		targets := map[string]bool{}
		for _, m := range f.Runtime.Mounts {
			if !m.Disabled && m.Target != "" {
				targets[m.Target] = true
			}
		}
		for _, p := range f.Runtime.SockGroups {
			if !filepath.IsAbs(p) {
				return Resolved{}, fmt.Errorf("skill %q: sock_groups path %q must be absolute", name, p)
			}
			if !targets[p] {
				return Resolved{}, fmt.Errorf("skill %q: sock_groups path %q must match an active mount target on the same skill", name, p)
			}
		}

		// containment is printed on four surfaces; hold it to single-line /
		// no-control-char / bounded length so a skill can't forge adjacent
		// status rows. Multi-declarer is allowed (unlike network_posture).
		if c := f.Runtime.Containment; c != "" {
			if err := validateOneLiner(c); err != nil {
				return Resolved{}, fmt.Errorf("skill %q: containment: %w", name, err)
			}
		}

		// env_docs guidance is printed on the config UI env screen; keys are
		// held to the env-key grammar and guidance to the containment shape
		// (single line, no control chars, bounded). Empty guidance is refused:
		// a suggestion row with nothing to say is a typo, not documentation.
		if err := config.ValidateContent("", nil, nil, f.Runtime.EnvDocs); err != nil {
			return Resolved{}, fmt.Errorf("skill %q: env_docs: %w", name, err)
		}
		for _, k := range sortedKeys(f.Runtime.EnvDocs) {
			g := f.Runtime.EnvDocs[k]
			if g == "" {
				return Resolved{}, fmt.Errorf("skill %q: env_docs %s: guidance must not be empty", name, k)
			}
			if err := validateOneLiner(g); err != nil {
				return Resolved{}, fmt.Errorf("skill %q: env_docs %s: %w", name, k, err)
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

		// [agent].mcp is a closed set — a typo'd adapter value would silently
		// degrade every box's MCP delivery to "no adapter". Checked for every
		// agent-capable skill, not just the selected one, so `skill validate`
		// paths that load through Resolve fail loudly.
		if f.Agent != nil {
			switch f.Agent.MCP {
			case "", "inject":
			default:
				return Resolved{}, fmt.Errorf("skill %q: [agent] mcp %q invalid (want \"inject\", or omit it: no adapter)", name, f.Agent.MCP)
			}
		}

		if name == cfg.Agent {
			if f.Agent == nil || f.Agent.Command == "" {
				return Resolved{}, fmt.Errorf("agent %q: skill has no [agent] command", name)
			}
			// If the agent declares a state volume, the skill must actually
			// contribute it — otherwise credentials won't persist.
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
// with this home (see internal/gen's core block and launcher — they spell it
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

// validateOneLiner holds a skill's declared one-liner (containment, env_docs
// guidance) to the shape status/launch/adoption/config UI can print as DATA:
// one line, no control characters, bounded length. Empty is handled by the
// caller (no declaration / refused, per field).
func validateOneLiner(s string) error {
	if s != strings.TrimSpace(s) {
		return fmt.Errorf("must not have leading/trailing whitespace")
	}
	if len(s) > oneLinerMaxLen {
		return fmt.Errorf("must be at most %d characters", oneLinerMaxLen)
	}
	for _, r := range s {
		if r == '\n' || r == '\r' {
			return fmt.Errorf("must be a single line (no newlines)")
		}
		// Any control char (ASCII C0/DEL and Unicode C1 like U+0085 NEL,
		// U+009B CSI) can forge adjacent status rows or terminal escapes when
		// rendered on the four surfaces; unicode.IsControl covers them all.
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	return nil
}

// validateSkillName requires a skill name to match the package ID grammar,
// so it can't escape the store or the build-context staging dir.
func validateSkillName(name string) error {
	if err := packages.ValidateID(name, true); err != nil {
		return fmt.Errorf("invalid skill name %q: %w", name, err)
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
