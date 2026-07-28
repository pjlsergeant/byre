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
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
)

// postureRe bounds a declared network_posture to a short display label —
// status prints it verbatim, so it must not carry spaces, parens, or control
// characters that could forge the surrounding status annotations.
var postureRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// oneLinerMaxLen bounds a skill's declared one-liners (containment,
// env_docs guidance). Status, launch, preset apply, and the config UI print
// them as data on their own rows; a long blob would crowd the surfaces without
// adding honesty.
const oneLinerMaxLen = 300

// MaxContextBytes bounds a skill's [context] file — it is baked into the box
// as agent memory, so it is prose-sized by nature; the cap exists so a huge
// or concurrently-growing file cannot balloon the host process (same read
// discipline as the manifest cap, sized for documents rather than configs).
const MaxContextBytes = 1 << 20 // 1 MiB

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
	// Context is how THIS agent's session receives the baked agent context
	// (chassis facts, skill snippets, [[context]] standing instructions —
	// /etc/byre/agent-context.md, plus the launcher's per-session additions
	// in $BYRE_SESSION_CONTEXT): "inject" means the agent command itself
	// consumes them (claude: --append-system-prompt-file + a second append
	// for the env var) — the skill author VOUCHES the command does so, the
	// MCP/claude_skills pattern (ADR 0046). Absent means no adapter: the
	// context still bakes, and status reports declared-but-not-delivered
	// with the path. byre NEVER writes an agent-owned file to deliver
	// prose: that file belongs to the user (or the agent), never to byre
	// (ADR 0046). The retired key it replaced is documented on ContextTarget
	// below -- the one place its name still has to appear.
	Context string `toml:"context"`
	// ContextTarget is the RETIRED delivery key (pre-ADR 0046 skills
	// declared where byre should write the agent's memory file). Parsed so
	// an installed skill from that era still loads; its value is ignored —
	// context is injected or not delivered, never written into agent state.
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
	// ClaudeSkills is the same vouch for byre's declared Claude Skill set:
	// "inject" means the agent command consumes the baked
	// /etc/byre/claude-skills tree (claude: --add-dir — the skills load bare,
	// as /name). The vouch is THAT the agent consumes the contract, not how;
	// the mechanism lives in the command string. Absent means no adapter:
	// the set still bakes, and status reports declared-but-not-delivered
	// with the path. Closed set, typos rejected.
	ClaudeSkills string `toml:"claude_skills"`
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
	// CompanionFor names the agent skill this skill is a companion to (ADR
	// 0034) — a pairing FACT with display teeth only: the config UI nests
	// the skill under its agent's row so the relationship is visible at the
	// point of enablement. It makes no readiness claim and triggers no
	// offer. Gate-pending companions (gemini-shared-auth's OAuth path,
	// opencode-shared-auth) declare this and NOT shared_auth_for.
	// Mutually exclusive with shared_auth_for, which implies the same
	// pairing — the pairing is declared exactly once, so declaring both is
	// a parse error (when a gate passes, swap this key for the vouch).
	CompanionFor string `toml:"companion_for"`
	// SharedAuthFor declares this skill as the shared-auth companion (ADR
	// 0017) for the named agent skill, making it OFFERABLE: when that agent
	// is selected, the onboarding picker asks whether to opt that box into
	// the agent's shared credentials (ADR 0025). Declaring the key is the
	// author VOUCHING the companion is ready to enable — a broken or
	// gate-pending companion (grok-shared-auth, gemini's OAuth path) omits
	// it and stays a hand-enabled expert option. Implies the companion_for
	// pairing (ADR 0034); mutually exclusive with it — declaring both is a
	// parse error.
	SharedAuthFor string `toml:"shared_auth_for"`
	Build         struct {
		Apt        []string          `toml:"apt"`
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
		// attributed on status/launch/preset-apply/config UI and never inspects or
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
	MCPs []config.MCP `toml:"mcp"`
	// ClaudeSkills are Claude Skills this skill ships ([[claude_skills]]
	// blocks with `from` — a directory relative to the skill dir). They union
	// into the effective set AFTER the config cascade merges
	// (ClaudeSkillSet); a config `!name` closure can subtract one. Wiring,
	// not grants (claudeskills.go).
	ClaudeSkills []config.ClaudeSkill `toml:"claude_skills"`
	Volumes      []config.Volume      `toml:"volumes"`
	Context      struct {
		Text string `toml:"text"` // inline snippet
		File string `toml:"file"` // path (relative to the skill dir) to a snippet
	} `toml:"context"`
}

// CompanionAgent resolves the skill's companion pairing (ADR 0034): the
// agent skill this skill augments, or "" for a non-companion. shared_auth_for
// implies the pairing, so a vouched companion needs no companion_for of its
// own. This is the FACT consumers (config-UI nesting, `skill show`) read;
// the onboarding offer reads SharedAuthFor directly — the vouch, never this.
func (f File) CompanionAgent() string {
	if f.CompanionFor != "" {
		return f.CompanionFor
	}
	return f.SharedAuthFor
}

// IsStub reports whether a skill contributes NOTHING to a box -- no build
// content, no runtime grants, no volumes, no agent, no context, no
// companionship claim: a description-only compatibility shell. Stubs exist
// so configs naming them keep resolving through a rename's support window
// (then the name moves to packages.RetiredNames); a picker has nothing to
// offer for one -- it is only shown when a config already references it (so
// it can be un-referenced). No bundled stub currently exists.
func IsStub(f File) bool {
	rt := f.Runtime
	return f.Agent == nil &&
		f.CompanionFor == "" && f.SharedAuthFor == "" &&
		len(f.Build.Apt) == 0 &&
		len(f.Build.Dockerfile) == 0 && len(f.Build.Files) == 0 &&
		len(rt.Env) == 0 && len(rt.EnvDocs) == 0 && len(rt.RunArgs) == 0 && len(rt.Caps) == 0 &&
		len(rt.Mounts) == 0 && rt.NetworkPosture == "" && rt.NetnsInit == "" &&
		len(rt.Egress) == 0 && len(rt.EgressOffered) == 0 &&
		len(rt.SockGroups) == 0 && rt.Containment == "" &&
		len(f.MCPs) == 0 &&
		len(f.ClaudeSkills) == 0 &&
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
	// Provenance is how the package entered the catalog (bundled / installed /
	// local). Build uses it to order skill blocks for layer-cache locality;
	// it never affects the agent-facing enable order.
	Provenance packages.Provenance
	// ClaudeSkills are the skill's [[claude_skills]] contributions with their
	// `from` dirs resolved against the skill dir (containment-checked), in
	// declaration order — filled by Resolve, consumed via ClaudeSkillSet.
	ClaudeSkills []ClaudeSkillDecl
	dir          string // host directory for payload resolution (set by loadEntry)
}

// Grant records a single skill's runtime grants, for legible attribution in
// `byre status` and the grant review (e.g. which skill mounts a host
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
	Dockerfile []string    // raw lines
	Files      []SkillFile // files this skill ships into the image
	// Provenance rides along so build can order blocks by volatility class
	// (ADR 0041) without reaching back into the catalog.
	Provenance packages.Provenance
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
			Dockerfile: sk.File.Build.Dockerfile,
			Files:      sk.Files,
			Provenance: sk.Provenance,
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

// ReservedEnvSet is one skill runtime-env key inside byre's reserved
// BYRE_ namespace: the variables that parameterize the chassis scripts.
// A skill setting one is ACCEPTED -- enabling a skill is trusting it,
// and refusal here would be theater while raw Dockerfile lines exist --
// but never silent: status renders each and degrades the claims it can
// skew (the same key is refused outright in config [env]).
type ReservedEnvSet struct {
	Skill string
	Key   string
}

// ReservedEnv lists the skills' BYRE_-namespace runtime env keys, in
// enable order then key order -- deterministic for rendering.
func (r Resolved) ReservedEnv() []ReservedEnvSet {
	var out []ReservedEnvSet
	for _, sk := range r.Skills {
		for _, k := range slices.Sorted(maps.Keys(sk.File.Runtime.Env)) {
			if strings.HasPrefix(k, "BYRE_") {
				out = append(out, ReservedEnvSet{Skill: sk.Name, Key: k})
			}
		}
	}
	return out
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
// netns hooks, sock_groups) for attribution in status and the grant review.
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
// rendering on status/launch/preset-apply/config UI (see Runtime.Containment).
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

// SameSkillRef reports whether two references name the same skill, tolerating
// the alias/canonical spelling difference: a pick stored as the canonical id
// and a row displaying the alias are one package, and a byte comparison would
// call them two. The ONE spelling-equality rule the shared-auth surfaces use --
// liveness and prefill both, because a predicate that accepts a spelling its
// caller's own matching rejects is a pick that reads live and selects nothing.
func SameSkillRef(cat *packages.Catalog, a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if cat == nil {
		return false
	}
	return cat.ExpandAlias(a) == cat.ExpandAlias(b)
}

// SharedAuthPickLive reports whether a STORED shared-auth pick still names a
// skill vouching itself as agent's companion. The one owner of that question:
// a stored pick is a name, and three surfaces have to agree on whether the
// name still means anything -- the interactive offer, the apply path that
// skips it (defaults.skip_questions), and the config editor's read-only row.
// Two implementations of it would drift into a grant one surface allows and
// another flags.
//
// Alias-tolerant, because a pick is stored in whatever form the picker offered
// (the alias where one exists, the canonical id otherwise) -- the same
// tolerance SharedAuthAlreadyOn extends, for the same reason.
func SharedAuthPickLive(cat *packages.Catalog, agent, pick string) bool {
	if agent == "" || pick == "" || cat == nil {
		return false
	}
	for _, c := range SharedAuthClaimants(cat, agent) {
		if SameSkillRef(cat, c.Name, pick) {
			return true
		}
	}
	return false
}

// SharedAuthCompanion returns the single ready shared-auth companion for
// agent, plus HOW MANY skills claim the pairing. A name comes back only for
// exactly one claimant -- byre never picks between rival claimants -- but the
// count is what tells "nobody offers this" apart from "several do": returning
// only the name collapsed both into "", and `--shared-auth` reported an
// ambiguity as "no ready companion skill", sending the user looking for a
// package they already had two of. Prefer SharedAuthClaimants + picker where
// an interactive choice is possible.
func SharedAuthCompanion(cat *packages.Catalog, agent string) (companion string, claimants int) {
	cs := SharedAuthClaimants(cat, agent)
	if len(cs) != 1 {
		return "", len(cs)
	}
	// Prefer display/alias form for writing into config.
	if ent, ok := cat.Lookup(cs[0].Name); ok && ent.Alias != "" {
		return ent.Alias, 1
	}
	return cs[0].Name, 1
}

// ListAgentSkills returns display names of skills that provide an [agent]
// command (i.e. can be selected as `agent`), sorted.
func ListAgentSkills(cat *packages.Catalog) []string {
	return list(cat, func(sk Skill) bool {
		return sk.File.Agent != nil && sk.File.Agent.Command != ""
	})
}

// list returns sorted display names of loadable skills that satisfy keep, and
// demotes the ones that do not load at all to catalog problem rows (see
// MarkLoadFailures -- the marking is the whole reason list and that pass are
// one loop).
func list(cat *packages.Catalog, keep func(Skill) bool) []string {
	if cat == nil {
		return nil
	}
	var out []string
	for _, ent := range cat.ListLoadable(packages.KindSkill) {
		sk, err := loadEntry(ent)
		if err != nil {
			// A skill whose primary parsed but whose full load fails is BROKEN,
			// not absent: dropping it silently left the user a healthy catalog
			// row, no picker entry, and nothing to read. The reason is the load
			// error minus the identity the row already displays.
			cat.MarkInvalid(ent, strings.TrimPrefix(err.Error(), fmt.Sprintf("skill %q: ", ent.ID)))
			continue
		}
		if !keep(sk) {
			continue
		}
		out = append(out, ent.DisplayName())
	}
	sort.Strings(out)
	return out
}

// MarkLoadFailures demotes every catalog skill that fails a full load to an
// INVALID problem row, for surfaces that read the catalog directly (`byre
// skill list`) rather than through the skills package. The pickers get the
// same marking as a side effect of listing.
func MarkLoadFailures(cat *packages.Catalog) {
	list(cat, func(Skill) bool { return false })
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
	f, err := decodeSkillFile(body)
	if err != nil {
		return File{}, err
	}
	if err := validatePairing(f); err != nil {
		return File{}, err
	}
	// The intra-skill value rules, at the bytes boundary: install refuses a
	// package whose values byre cannot run, and catalog ingest marks an
	// installed one INVALID (ADR 0029's amendment), instead of both waiting
	// for the develop that finally resolves it.
	if err := validateValues(f); err != nil {
		return File{}, err
	}
	return f, nil
}

// decodeSkillFile strict-decodes skill.toml body bytes: byre owns the
// skill.toml schema, so a typo'd key is an error, not a silent no-op that
// produces a broken skill.
func decodeSkillFile(body []byte) (File, error) {
	var f File
	d := toml.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			keys := make([]string, len(strict.Errors))
			for i, de := range strict.Errors {
				keys[i] = strings.Join(de.Key(), ".")
			}
			// A REMOVED key gets its remedy, not a bare unknown-key list: the
			// author wrote something byre used to support, and "unknown" reads
			// as a typo they cannot find.
			for _, k := range keys {
				if remedy := removedSkillKeys[k]; remedy != "" {
					return File{}, fmt.Errorf("%s", remedy)
				}
			}
			return File{}, fmt.Errorf("unknown key(s) in skill.toml: [%s]", strings.Join(keys, " "))
		}
		return File{}, err
	}
	return f, nil
}

// removedSkillKeys maps a skill.toml key byre REMOVED to its remedy. Same
// stance as config's refusedConfigKeys: loud, with the replacement spelled
// out, because a silently-ignored build key changes what the image contains
// without saying so.
var removedSkillKeys = map[string]string{
	"build.npm_global": "skill.toml: [build] npm_global is removed. It assumed node/npm in the image and named one ecosystem in byre's vocabulary; use a raw build line instead:\n  [build]\n  dockerfile = [\"RUN npm install -g <pkg>\"]",
}

// validatePairing refuses a manifest declaring both pairing keys: the
// pairing is stated exactly once — companion_for (the bare fact) or
// shared_auth_for (the vouch, which subsumes it) — so two spellings of one
// fact can never drift apart, and no value comparison (with its alias-vs-
// canonical-ID ambiguity — parse-time has no catalog to expand either) is
// ever needed (ADR 0034). Runs in both primary-file parse paths so install
// preflight and load agree on what is a valid skill.
func validatePairing(f File) error {
	if f.CompanionFor != "" && f.SharedAuthFor != "" {
		return fmt.Errorf("companion_for (%q) and shared_auth_for (%q) are both set; shared_auth_for already implies the pairing — drop companion_for", f.CompanionFor, f.SharedAuthFor)
	}
	return nil
}

// validateValues is the ONE intra-skill value check: every rule byre can judge
// from a single skill.toml alone, with no other skill and no resolved set in
// view. Three paths reach it, which is the whole point -- develop (Resolve, via
// loadEntry), `byre skill validate` (validateOne, via Load -> loadEntry), and
// the bytes-only pair install and catalog ingest share (ParsePrimaryBytes).
// While these rules lived in Resolve alone, `network_posture = "Deny-Default"`
// passed validate, pack, inspect, install and list, then failed at the first
// develop -- as late as byre can possibly say it.
//
// Set-dependent rules are deliberately NOT here, because they cannot be: one
// posture and one netns_init per box, an env key two skills set differently,
// MCP and Claude Skill names colliding across sources, the agent naming an
// enabled skill. Those are properties of a SET, so they stay in Resolve, and
// `byre skill validate` is a partial promise by construction (docs/SKILLS.md
// states it).
//
// Errors return unprefixed; each caller wraps with the identity it holds
// (`skill %q:` at load, the package id at install).
func validateValues(f File) error {
	// A skill's build content is interpolated into the same generated
	// Dockerfile/shell as the project config, so hold its typed fields to the
	// same allowlists — not as a trust boundary (a skill you enabled can run
	// anything via a raw [build].dockerfile line), but so a typed field stays
	// legible data: `apt` holds package names, and the escape hatch for
	// arbitrary commands is the explicit raw block. Env values are only ever
	// emitted %q-quoted, so only keys are checked (via ValidateContent).
	if err := config.ValidateContent("", f.Build.Apt, nil, f.Runtime.Env); err != nil {
		return err
	}

	// Files this skill ships into the image: absolute destinations, one source
	// per destination, and sources that stay inside the skill dir. The symlink
	// half of containment needs the directory on disk and stays in Resolve.
	destBy := map[string]string{} // image dest -> the source that claimed it
	for _, src := range slices.Sorted(maps.Keys(f.Build.Files)) {
		dest := f.Build.Files[src]
		if !filepath.IsAbs(dest) {
			return fmt.Errorf("file destination %q must be an absolute image path", dest)
		}
		// Two sources for one destination: only one file can be there, and
		// which one is map-iteration order at every consumer that keys by
		// dest (planGuard's byDest, so a guarded launch gate or firewall
		// script could be re-asserted from either). Silent shadowing of an
		// authoring mistake, refused where the author can see it.
		if prev, dup := destBy[dest]; dup {
			return fmt.Errorf("build files %q and %q both install to %q; one destination, one source", prev, src, dest)
		}
		destBy[dest] = src
		if err := relWithinSkill(src); err != nil {
			return fmt.Errorf("build file: %w", err)
		}
	}

	// network_posture is printed by status; hold it to a tight shape so a
	// skill can't smuggle formatting/control text into the output.
	if p := f.Runtime.NetworkPosture; p != "" && !postureRe.MatchString(p) {
		return fmt.Errorf("network_posture %q: must match %s", p, postureRe)
	}
	// netns_init runs as root in the box's netns; require an absolute image
	// path so it stays legible data (the script itself is skill-shipped).
	if p := f.Runtime.NetnsInit; p != "" && !filepath.IsAbs(p) {
		return fmt.Errorf("netns_init %q must be an absolute image path", p)
	}
	// egress entries feed a firewall allowlist and are passed to the netns
	// helper as data; validate host[:port] shape up front so a typo fails
	// loudly rather than silently dropping a host from the allowlist.
	// Offered entries (ADR 0020) are held to the same grammar: they become
	// real egress the moment a user opens one.
	for _, e := range append(append([]string{}, f.Runtime.Egress...), f.Runtime.EgressOffered...) {
		if _, _, err := parseEgress(e); err != nil {
			return err
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
			return err
		}
		if mcpNames[m.Name] {
			return fmt.Errorf("mcp %s declared twice", m.Name)
		}
		mcpNames[m.Name] = true
	}

	// Claude Skill contributions: shape-check the declaration (skill home
	// spells its source `from`, and ValidateClaudeSkill's RelSafe is the
	// lexical containment on it). Content validation (SKILL.md, frontmatter,
	// bounds) is the bake's job, one owner for both homes. Intra-skill
	// duplicates refuse here; duplicates across sources are ClaudeSkillSet's
	// hard reject.
	csNames := map[string]bool{}
	for _, cs := range f.ClaudeSkills {
		if err := config.ValidateClaudeSkill(cs, true); err != nil {
			return err
		}
		if csNames[cs.Name] {
			return fmt.Errorf("claude skill %s declared twice", cs.Name)
		}
		csNames[cs.Name] = true
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
			return fmt.Errorf("sock_groups path %q must be absolute", p)
		}
		if !targets[p] {
			return fmt.Errorf("sock_groups path %q must match an active mount target on the same skill", p)
		}
	}

	// containment is printed on four surfaces; hold it to single-line /
	// no-control-char / bounded length so a skill can't forge adjacent
	// status rows.
	if c := f.Runtime.Containment; c != "" {
		if err := validateOneLiner(c); err != nil {
			return fmt.Errorf("containment: %w", err)
		}
	}

	// env_docs guidance is printed on the config UI env screen; keys are
	// held to the env-key grammar and guidance to the containment shape
	// (single line, no control chars, bounded). Empty guidance is refused:
	// a suggestion row with nothing to say is a typo, not documentation.
	if err := config.ValidateContent("", nil, nil, f.Runtime.EnvDocs); err != nil {
		return fmt.Errorf("env_docs: %w", err)
	}
	for _, k := range slices.Sorted(maps.Keys(f.Runtime.EnvDocs)) {
		g := f.Runtime.EnvDocs[k]
		if g == "" {
			return fmt.Errorf("env_docs %s: guidance must not be empty", k)
		}
		if err := validateOneLiner(g); err != nil {
			return fmt.Errorf("env_docs %s: %w", k, err)
		}
	}

	// [agent] adapters are closed sets — a typo'd value would silently degrade
	// every box's MCP/context/skills delivery to "no adapter". Judged for any
	// skill carrying an [agent] table, not only the selected agent: a manifest
	// is right or wrong on its own terms, and the skill someone selects
	// tomorrow should not be the first to say so.
	if f.Agent != nil {
		switch f.Agent.MCP {
		case "", "inject":
		default:
			return fmt.Errorf("[agent] mcp %q invalid (want \"inject\", or omit it: no adapter)", f.Agent.MCP)
		}
		switch f.Agent.ClaudeSkills {
		case "", "inject":
		default:
			return fmt.Errorf("[agent] claude_skills %q invalid (want \"inject\", or omit it: no adapter)", f.Agent.ClaudeSkills)
		}
		switch f.Agent.Context {
		case "", "inject":
		default:
			return fmt.Errorf("[agent] context %q invalid (want \"inject\", or omit it: no adapter)", f.Agent.Context)
		}
		// A declared state volume the skill does not contribute means
		// credentials silently fail to persist. The rule reads the skill's OWN
		// [[volumes]], so it is intra-skill despite sitting under the agent
		// selection until now.
		if f.Agent.State != "" && !hasStateVolume(f.Volumes, f.Agent.State) {
			return fmt.Errorf("[agent].state %q is not a state volume contributed by the skill", f.Agent.State)
		}
		if p := f.Agent.Prefs; p != nil {
			if err := validatePrefs(p, f.Agent.State); err != nil {
				return fmt.Errorf("[agent.prefs]: %w", err)
			}
		}
	}
	return nil
}

// relWithinSkill is the LEXICAL half of skill-relative containment: an absolute
// path or a "../" escape is an authoring error judgable from the manifest
// alone, so validate and install refuse it with no skill directory in hand.
// skillRelPath adds the symlink half, which needs one.
func relWithinSkill(rel string) error {
	if filepath.IsAbs(rel) {
		return fmt.Errorf("path must be relative to the skill dir: %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes the skill dir: %q", rel)
	}
	return nil
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

	f, err := decodeSkillFile(body)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, err)
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
	// shared_auth_for implies the companion_for pairing; declaring both is
	// a redundancy that could drift, refused rather than resolved.
	if perr := validatePairing(f); perr != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, perr)
	}
	// The intra-skill value rules. Bundled skills skip catalog stage 2, and
	// nothing re-runs it on an entry already in the catalog, so load is where
	// `byre skill validate` and develop both meet these rules.
	if verr := validateValues(f); verr != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, verr)
	}

	dir, err := ent.HostDir()
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, err)
	}
	ctx := f.Context.Text
	if f.Context.File != "" {
		// The root descriptor is pinned FIRST, so everything after — the
		// skillRelPath refusals (absolute path, ../ escape, symlink out of
		// the dir; kept for their legible messages) and the open itself —
		// resolves against the directory that was actually opened, and the
		// descriptor is judged (regular file only) so a FIFO or device
		// fails the load instead of wedging develop.
		root, rerr := hostopen.PlainOpenRoot(dir, hostopen.StoreOwned)
		if rerr != nil {
			return Skill{}, fmt.Errorf("skill %q context: %w", ent.ID, rerr)
		}
		if _, perr := skillRelPath(dir, f.Context.File); perr != nil {
			root.Close()
			return Skill{}, fmt.Errorf("skill %q: %w", ent.ID, perr)
		}
		fh, _, rerr := hostopen.OpenRegularIn(root, filepath.Clean(f.Context.File))
		root.Close()
		if rerr != nil {
			return Skill{}, fmt.Errorf("skill %q context: %w", ent.ID, rerr)
		}
		b, rerr := io.ReadAll(io.LimitReader(fh, MaxContextBytes+1))
		fh.Close()
		if rerr != nil {
			return Skill{}, fmt.Errorf("skill %q context: %w", ent.ID, rerr)
		}
		if len(b) > MaxContextBytes {
			return Skill{}, fmt.Errorf("skill %q context: %s exceeds %d bytes (limit)", ent.ID, f.Context.File, MaxContextBytes)
		}
		ctx = string(b)
	}
	// Skill.Name is the canonical ID (comparisons, grants, status).
	return Skill{Name: ent.ID, File: f, Context: ctx, dir: dir, Provenance: ent.Provenance}, nil
}

// skillRelPath resolves a skill-relative file path, rejecting absolute paths,
// lexical "../" escapes, and symlinks that point outside the skill directory.
func skillRelPath(dir, rel string) (string, error) {
	if err := relWithinSkill(rel); err != nil {
		return "", err
	}
	clean := filepath.Clean(rel)

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
// skill enabled implicitly) and checks what only the SET can answer. Names are
// expanded through the catalog (aliases -> canonical IDs). The selected
// agent's skill must exist and provide an [agent] command. Cross-skill env-key
// conflicts are an error: two skills setting the SAME key to DIFFERENT values
// would otherwise resolve by enable order — silent and surprising.
//
// Each skill's own values were judged at load (validateValues), which is what
// lets validate and install refuse them too; the filesystem work that needs
// the skill dir (resolving [build].files and Claude Skill sources through
// symlinks) is here because it is resolution, not validation.
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

		// Every intra-skill value rule already fired at load (validateValues).
		// What is left here is what only a SET can answer.

		// Files this skill ships into the image: resolve each source within the
		// skill dir (the symlink half of the containment validateValues already
		// judged lexically). Sorted by source for deterministic build-context
		// staging and COPY emission.
		dir := sk.dir
		for _, src := range slices.Sorted(maps.Keys(f.Build.Files)) {
			real, perr := skillRelPath(dir, src)
			if perr != nil {
				return Resolved{}, fmt.Errorf("skill %q: build file: %w", name, perr)
			}
			sk.Files = append(sk.Files, SkillFile{Src: real, Rel: filepath.Clean(src), Dest: f.Build.Files[src]})
		}

		// One network, so one declared posture (unlike env, even equal
		// duplicates are refused: each claims to have established the stance).
		if f.Runtime.NetworkPosture != "" {
			if postureBy != "" {
				return Resolved{}, fmt.Errorf("skills %q and %q both declare a network_posture; disable one", postureBy, name)
			}
			postureBy = name
		}
		// Exactly ONE netns hook per box (mirroring the posture rule above):
		// the launch gate is opened by the hook's own script when it finishes
		// (see the firewall skill), so with two hooks the first would release
		// the agent before the second ran — its setup silently unapplied. If
		// multi-hook composition is ever wanted, gate signaling must first
		// move into byre's orchestrator (opened only after EVERY hook
		// succeeds); until then, refuse the ambiguity.
		if f.Runtime.NetnsInit != "" {
			if netnsBy != "" {
				return Resolved{}, fmt.Errorf("skills %q and %q both declare a netns_init; disable one", netnsBy, name)
			}
			netnsBy = name
		}

		// Claude Skill sources resolve against the skill dir, same split as
		// [build].files above.
		for _, cs := range f.ClaudeSkills {
			src, perr := skillRelPath(dir, cs.From)
			if perr != nil {
				return Resolved{}, fmt.Errorf("skill %q: claude skill %s: %w", name, cs.Name, perr)
			}
			sk.ClaudeSkills = append(sk.ClaudeSkills, ClaudeSkillDecl{Skill: name, CS: cs, SrcDir: src})
		}

		// Cross-skill env conflicts: a differing value for the same key would be
		// resolved by enable order — refuse instead. The same value twice is
		// harmless (order-independent) and allowed.
		for _, k := range slices.Sorted(maps.Keys(f.Runtime.Env)) {
			if other, ok := envSetBy[k]; ok && other != name {
				if prev := envValue(res.Skills, other, k); prev != f.Runtime.Env[k] {
					return Resolved{}, fmt.Errorf("skills %q and %q both set env %s to different values; disable one or align them", other, name, k)
				}
				continue
			}
			envSetBy[k] = name
		}

		res.Skills = append(res.Skills, sk)

		// Which skill is the agent is a property of the config, not of any
		// manifest: the [agent] block's own values were judged at load.
		if name == cfg.Agent {
			if f.Agent == nil || f.Agent.Command == "" {
				return Resolved{}, fmt.Errorf("agent %q: skill has no [agent] command", name)
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
// literally in shell/Dockerfile text, pinned by gen's golden test).
const DevHome = "/home/dev"

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
// guidance) to the shape status/launch/preset-apply/config UI can print as DATA:
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
