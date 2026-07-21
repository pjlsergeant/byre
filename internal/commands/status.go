package commands

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// statusInfo is the resolved, display-ready view of a project for `byre status`.
type statusInfo struct {
	Agent           string
	Template        string
	Chain           []string // named-layer extends chain, root-first ("" = none)
	Engine          string
	ID              string
	Canonical       string // the dir bound at /workspace (the worktree, for a worktree)
	WorktreeOf      string // main-worktree path when this is a linked worktree, else ""
	Skills          []string
	Binds           []config.Mount
	Ports           []config.Port
	Volumes         []config.Volume
	Grants          []skills.Grant    // per-skill runtime grants (attribution)
	EnvFromHost     map[string]string // resolved host-value passthrough (KEY -> source, ADR 0026)
	RunArgs         []string
	BuildRaw        []string             // dockerfile_pre + dockerfile_post (raw, not introspected)
	NetPosture      string               // a skill's declared network posture ("" = default open)
	NetPostureSkill string               // the skill declaring it
	Egress          []skills.EgressAllow // resolved allowlist (host:port + skill), shown when a posture is declared
	EgressClosed    []string             // the config's `!host[:port]` closures that survived the cascade
	// MCPs is the effective declared MCP set — wiring, not grants (its
	// carried egress rides the Egress rows attributed mcp:<name>); MCPClosed
	// is the config's `!name` MCP closures; AgentMCP is the selected agent's
	// adapter vouch ("inject" = the agent command consumes the baked file);
	// EnvProvided marks env keys this box actually supplies, for the
	// consumes-X (provided / NOT provided) annotations.
	MCPs        []skills.MCPDecl
	MCPClosed   []string
	AgentMCP    string
	EnvProvided map[string]bool
	// ClaudeSkills is the effective declared Claude Skill set — wiring, not
	// grants, zero exposure contribution (claudeskills.go); the closed/vouch
	// fields mirror the MCP trio.
	ClaudeSkills       []skills.ClaudeSkillDecl
	ClaudeSkillsClosed []string
	AgentClaudeSkills  string
	// Containments are skill-declared containment holes (warranty disclaimer).
	// Multi-declarer: all shown; other status rows stay unqualified.
	Containments      []skills.ContainmentDecl
	ProjectRunArgs    bool     // the PROJECT's own raw run_args present (degrades the posture claim)
	GuardMountShadow  bool     // a project mount/volume covers a security path (degrades the posture claim)
	Container         string   // this dir's running container id, or "" if none
	ContainerQueryErr string   // engine found but the container query failed — state is UNKNOWN, not absent
	SiblingQueryErr   string   // sibling-session query failed while the own-session query worked
	Orphaned          bool     // Container is running but its byre client is gone (terminal died; box survives)
	SiblingSessions   []string // OTHER live sessions in this project, "workdir-id (short-id)" (worktrees sharing these volumes)
	Rootless          bool     // true if the engine is rootless Podman
	KeepID            bool     // rootless Podman with keep-id mapping support (the supported rootless path)
	EngineErr         string   // why the engine/container state is unknown, if applicable
	SkillErr          string   // why skills couldn't be resolved, if applicable
	SelfEdit          string   // host store path when --self-edit is active, else ""
	Proposal          string   // note about a committed <project>/byre.config, if any
	Cat               *packages.Catalog
}

// Status implements `byre status`. selfEdit mirrors `develop --self-edit` so the
// grant it would add (rw ~/.byre) is announced here too.
func Status(s Streams, projectDir string, selfEdit bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Same loud id-collision check as the other read-only surfaces: on a
	// collision this view would describe ANOTHER project's config as this
	// one's grants — refuse instead of rendering a plausible lie.
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	// Ensure store (bundled mirror) before loading config (templates feed the
	// cascade). The error degrades the skills view below rather than failing
	// status.
	storeErr := builtins.EnsureStoreOut(paths.Home, s.Err)
	cat, _ := builtins.LoadCatalogRaw(paths.Home)

	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}

	// The extends chain is consumed by resolution, so read the pointer back
	// off the raw project layer for attribution. Load succeeded above, so
	// the chain walks; a race that breaks it mid-status just drops the row.
	var chain []string
	if raw, rerr := config.ParseFile(filepath.Join(paths.Dir, config.ProjectConfigName)); rerr == nil && raw.Extends != "" {
		if c, cerr := config.LoadExtendsChain(paths.Home, cat, raw.Extends); cerr == nil {
			chain = config.ChainNames(c)
		}
	}

	info := statusInfo{
		Agent:              cfg.Agent,
		Template:           cfg.Template,
		Chain:              chain,
		Engine:             cfg.Engine,
		ID:                 paths.ID,
		Canonical:          paths.WorkDir, // what actually mounts at /workspace
		Skills:             cfg.Skills,
		Binds:              cfg.Mounts,
		Ports:              cfg.Ports,
		Volumes:            cfg.Volumes,
		RunArgs:            cfg.RunArgs,
		EgressClosed:       cfg.EgressClosed,
		MCPClosed:          cfg.MCPClosed,
		ClaudeSkillsClosed: cfg.ClaudeSkillsClosed,
		BuildRaw:           append(append([]string{}, cfg.DockerfilePre...), cfg.DockerfilePost...),
		ProjectRunArgs:     len(cfg.RunArgs) > 0,
		EnvFromHost:        cfg.EnvFromHost,
		Cat:                cat,
	}
	// Config-declared MCPs stay visible even when skills fail to resolve (the
	// same config-only degradation as every other row); the resolved set below
	// replaces this with the skill union. The error is structurally nil: an
	// empty Resolved contributes no claims, and config-internal duplicate MCP
	// names — the one conflict cfg alone could carry — were refused upstream
	// by config.Load's per-layer validation (the merge replaces by name).
	info.MCPs, _ = skills.MCPSet(cfg, skills.Resolved{})
	// Same config-only degradation for the declared Claude Skill set.
	info.ClaudeSkills, _ = skills.ClaudeSkillSet(cfg, skills.Resolved{})
	info.EnvProvided = map[string]bool{}
	for k := range cfg.Env {
		info.EnvProvided[k] = true
	}
	for k, src := range cfg.EnvFromHost {
		if src != "" {
			info.EnvProvided[k] = true
		}
	}
	if paths.IsWorktree {
		info.WorktreeOf = paths.Canonical
	}
	if selfEdit {
		info.SelfEdit = paths.Dir
	}
	// Preset drift states: passive visibility of a repo-shipped preset, states
	// 1 (not applied) and 3 (diverged); the steady state stays silent.
	info.Proposal = presetNote(projectDir, paths)
	// Enrich with resolved skills so implicit/built-in contributions (the agent
	// skill, its .claude state volume, skill mounts) are shown, not just the
	// config-level view. Best-effort: a resolution error is surfaced, not fatal.
	if merr := storeErr; merr != nil {
		info.SkillErr = merr.Error()
	} else if cat == nil {
		info.SkillErr = "catalog unavailable"
	} else if res, rerr := skills.Resolve(cfg, cat); rerr != nil {
		info.SkillErr = rerr.Error()
	} else {
		// Validate the combined config+skills set the SAME way develop/dockerfile
		// do (resolve()), BEFORE committing it to info. A skill can contribute a
		// mount/volume that collides with a config one, or a duplicate volume name;
		// develop rejects that, so status shouldn't present it as active. On
		// failure, surface it and keep the config-only view. Best-effort, not fatal.
		rv := combine(cfg, res)
		if verr := rv.validate(); verr != nil {
			info.SkillErr = verr.Error()
		} else {
			info.Skills = res.Names()
			info.Binds = rv.mounts
			info.Volumes = rv.volumes
			info.Grants = res.Grants()
			info.RunArgs = append(append([]string{}, res.RunArgs()...), cfg.RunArgs...)
			info.NetPosture, info.NetPostureSkill = res.NetworkPosture()
			// A project mount/volume over a guarded path shadows byre's own
			// launcher/gate/netns file at runtime — the network claim can't stand.
			info.GuardMountShadow = len(guardMountVolumeHits(cfg, res)) > 0
			info.Egress = res.EgressAllows()
			// The `egress` config key is the user's extension path (ADR 0019),
			// so status must show those holes too — attributed to config, not a
			// skill — or it under-reports what the box can reach.
			info.Egress = append(info.Egress, configEgress(cfg.Egress)...)
			// The declared MCP set's CARRIED egress — implied by the wiring,
			// attributed mcp:<name>, closable like anything else.
			info.Egress = append(info.Egress, skills.MCPEgress(rv.mcps)...)
			info.Containments = res.Containments()
			info.MCPs = rv.mcps
			info.ClaudeSkills = rv.claudeSkills
			if res.Agent != nil {
				info.AgentMCP = res.Agent.MCP
				info.AgentClaudeSkills = res.Agent.ClaudeSkills
			}
			for k := range res.Env() {
				info.EnvProvided[k] = true
			}
		}
	}
	if eng, derr := runner.Detect(cfg.Engine, nil); derr != nil {
		info.Engine = orDefault(cfg.Engine, "auto")
		info.EngineErr = derr.Error()
	} else {
		info.Engine = string(eng)
		r := runner.New(eng)
		if rootless, rerr := r.IsRootlessPodman(); rerr == nil && rootless {
			info.Rootless = true
			if ok, kerr := r.SupportsKeepIDMapping(); kerr == nil && ok {
				info.KeepID = true
			}
		}
		// This dir's own session: the worktree label, so it reflects THIS worktree,
		// not a sibling (both carry the project label). A failed query is NOT
		// "not running" — a found binary whose daemon is down/unreachable must
		// render as unknown, not as a confident negative (the lifecycle
		// commands refuse in this state; status must not contradict them).
		mine, merr := r.RunningContainersByLabel(workdirLabel(paths))
		if merr != nil {
			info.ContainerQueryErr = firstLine(merr.Error())
		}
		if len(mine) > 0 {
			info.Container = mine[0]
			// A box outliving its byre (terminal killed, ssh dropped) keeps
			// running by design — but status must SAY so, or "running" reads
			// as a reachable session and the reset/forget refusal as a
			// contradiction. Best-effort: label errors leave plain "running".
			if labels, lerr := r.ContainerLabels(mine[0]); lerr == nil {
				info.Orphaned = clientGone(labels)
			}
		}
		// Other live sessions in the same project (worktrees sharing these
		// volumes). Surfaced so status doesn't imply "nothing running" while
		// reset/forget correctly refuse on the project label. Empty for a plain
		// project (no worktree siblings). Derived only when the own-session
		// query succeeded: siblingNames subtracts `mine` from the family, so
		// a failed own query with a succeeding family one (a transient
		// engine flap between them) would list THIS box as its own sibling.
		// The unknown own-state note covers that case instead.
		if merr == nil {
			if fam, cerr := r.RunningContainersByLabel(projectLabel(paths)); cerr == nil {
				info.SiblingSessions = siblingNames(r, mine, fam)
			} else {
				info.SiblingQueryErr = firstLine(cerr.Error())
			}
		}
	}

	renderStatus(s.Out, info)
	return nil
}

// pkgLine formats "id  provenance" for status. Falls back to the bare
// name when the catalog has no entry.
func pkgLine(cat *packages.Catalog, name string) string {
	if name == "" {
		return "(none)"
	}
	if cat == nil {
		return name
	}
	ent, ok := cat.Lookup(name)
	if !ok {
		return name
	}
	id := ent.ID
	if ent.Alias != "" && name == ent.Alias {
		// Config wrote the friendly alias; status shows canonical + label.
		id = ent.ID
	}
	return fmt.Sprintf("%-24s %s", id, ent.ProvenanceLabel())
}

// hostEnvRow renders the live env_from_host entries deterministically
// (sorted; disabled "" entries omitted), or "" when there are none.
func hostEnvRow(m map[string]string) string {
	var keys []string
	for k, src := range m {
		if src != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + " <- " + m[k]
	}
	return strings.Join(parts, ", ") + "  (host values passed in; env_from_host)"
}

// renderStatus writes the flat, scannable "what can this thing touch?" block.
// Raw run_args are shown verbatim and flagged as not introspected by byre.
// siblingNames renders the OTHER live sessions of the project (fam minus
// mine) as "workdir-id (short-id)" — the bare container id said "something
// else is running" without saying WHICH worktree (QA pass-2 finding). The
// label lookup is best-effort: a box missing it still shows its id.
func siblingNames(r interface {
	ContainerLabels(id string) (map[string]string, error)
}, mine, fam []string) []string {
	mineSet := map[string]bool{}
	for _, id := range mine {
		mineSet[id] = true
	}
	var names []string
	for _, id := range fam {
		if mineSet[id] {
			continue
		}
		name := shortID(id)
		if labels, err := r.ContainerLabels(id); err == nil && labels[workdirKey] != "" {
			name = labels[workdirKey] + " (" + shortID(id) + ")"
		}
		names = append(names, name)
	}
	return names
}

func renderStatus(w io.Writer, s statusInfo) {
	row := func(label, val string) {
		head := ""
		if label != "" {
			head = label + ":"
		}
		fmt.Fprintf(w, "%-13s %s\n", head, val)
	}

	if s.ID != "" {
		row("Project id", s.ID)
	}
	row("Agent", orDefault(s.Agent, "(none)"))
	if s.Template != "" {
		row("Template", pkgLine(s.Cat, s.Template))
	} else {
		row("Template", "(none)")
	}
	if len(s.Chain) > 0 {
		// Root-first, the project config last: the merge order.
		row("Extends", strings.Join(s.Chain, " -> ")+" -> project")
	}
	if s.Proposal != "" {
		row("Preset", s.Proposal)
	}
	if s.EngineErr != "" {
		row("Engine", s.Engine+"  (not found: "+s.EngineErr+")")
	} else if s.KeepID {
		row("Engine", s.Engine+fmt.Sprintf("  (rootless — keep-id: the box's dev user is uid %d, mapped to you)", genericUID))
	} else if s.Rootless {
		row("Engine", s.Engine+"  (rootless — this Podman lacks the keep-id mapping byre needs (4.3+); file ownership UNSUPPORTED)")
	} else {
		row("Engine", s.Engine)
	}
	row("Project", s.Canonical+" -> /workspace  (rw)")
	if s.WorktreeOf != "" {
		row("Worktree of", s.WorktreeOf+"  (config, volumes, image inherited)")
	}
	row("Network", networkLine(s))

	// Containment: warranty disclaimer for skill-declared holes (e.g.
	// docker-host). Other rows stay unqualified -- they describe what byre
	// built and still hold for the box; this row disclaims the hole once.
	// Multi-declarer: each skill gets its own attributed row.
	for i, c := range s.Containments {
		label := "Containment"
		if i > 0 {
			label = ""
		}
		row(label, fmt.Sprintf("🛑 HOLE -- %s  (skill: %s)", c.Text, c.Skill))
	}

	// When an allowlist-enforcing posture is in effect, list the allowlist so
	// "what can this box reach?" is legible — each host:port attributed to the
	// skill that asked for it (deduped on host:port; first declarer wins the
	// credit), and an entry a config closure subtracts shown as closed-by, not
	// vanished (the whole point of `!host` reaching past the cascade). With NO
	// such posture — the open default AND open-denylist, where the network is
	// open — skill-declared egress is suppressed (every agent skill declares
	// endpoints; on an open network the list is meaningless noise) — but the
	// user's own `egress` config entries still print, marked unenforced:
	// config must not carry invisible teeth that a later skill toggle arms
	// (ADR 0019).
	enforcesAllowlist := config.PostureEnforcesAllowlist(s.NetPosture)
	egress := s.Egress
	unenforced := ""
	if !enforcesAllowlist {
		egress = nil
		for _, a := range s.Egress {
			if a.Skill == skills.EgressFromConfig {
				egress = append(egress, a)
			}
		}
		unenforced = " — unenforced, network open"
	}
	if len(egress) > 0 {
		seen := map[string]bool{}
		first := true
		for _, a := range egress {
			hp := fmt.Sprintf("%s:%d", a.Host, a.Port)
			if seen[hp] {
				continue
			}
			seen[hp] = true
			label := "Egress"
			if !first {
				label = ""
			}
			first = false
			if c, closed := closedBy(s.EgressClosed, a.Host, a.Port); closed && enforcesAllowlist {
				row(label, fmt.Sprintf("%s  (%s — closed by config '!%s')", hp, a.Skill, c))
				continue
			}
			row(label, fmt.Sprintf("%s  (%s%s)", hp, a.Skill, unenforced))
		}
	}
	// The closures themselves, one row each — config that must never be
	// invisible, whatever the posture. Under open-denylist they are THE
	// enforced list; under deny-by-default they subtract from the allowlist
	// above; with no posture they are declared and inert like every other
	// egress entry.
	first := true
	for _, c := range s.EgressClosed {
		label := "Closed"
		if !first {
			label = ""
		}
		first = false
		row(label, closureLine(c, s))
	}

	if len(s.Ports) == 0 {
		row("Ports", "none")
	} else {
		for i, p := range s.Ports {
			label := "Ports"
			if i > 0 {
				label = ""
			}
			row(label, portStatusLine(p))
		}
	}

	if len(s.Binds) == 0 {
		row("Host mounts", "none")
	} else {
		for i, m := range s.Binds {
			label := "Host mounts"
			if i > 0 {
				label = ""
			}
			// A disabled mount is shown, marked -- staying visible while off is
			// the whole point of the switch (vs deleting the entry).
			mode := orDefault(m.Mode, "ro")
			if m.Disabled {
				mode += ", disabled"
			}
			row(label, fmt.Sprintf("%s -> %s  (%s)", m.Host, m.Target, mode))
		}
	}

	if s.SelfEdit != "" {
		row("Self-edit", fmt.Sprintf("%s -> %s  (rw)  [GRANT via --self-edit]", s.SelfEdit, selfEditTarget))
	}

	if s.SkillErr != "" {
		row("Skills", strings.Join(s.Skills, ", ")+"  (unresolved: "+s.SkillErr+")")
	} else if len(s.Skills) == 0 {
		row("Skills", "none")
	} else {
		// One row per skill with provenance label.
		for i, name := range s.Skills {
			label := "Skills"
			if i > 0 {
				label = ""
			}
			row(label, pkgLine(s.Cat, name))
		}
	}

	state, cache, machine := splitVolumes(s.Volumes)
	row("State vols", orDefault(strings.Join(state, ", "), "none"))
	row("Cache vols", orDefault(strings.Join(cache, ", "), "none"))
	// Machine-scoped volumes cross project boundaries by design (ADR 0017);
	// the row exists so that sharing is never invisible. Omitted entirely when
	// none are declared -- most boxes have no shared volumes to report.
	if len(machine) > 0 {
		row("Shared vols", strings.Join(machine, ", ")+"  (machine-wide, all your projects)")
	}

	// MCP servers: wiring, not grants (GLOSSARY) — configuration rows beside
	// the volumes, contributing zero to exposure; the egress each declaration
	// CARRIES already renders in the Egress rows above, attributed mcp:<name>.
	// These rows are config-application reporting: what's wired, from where,
	// what env it consumes (and whether this box provides it), and why it
	// won't work when byre can tell (endpoint closed, outbound unknown).
	for i, d := range s.MCPs {
		label := "MCP servers"
		if i > 0 {
			label = ""
		}
		row(label, mcpStatusLine(d, s))
	}
	if len(s.MCPs) > 0 {
		row("", mcpDeliveryLine(s))
	}
	// The `!name` MCP closures, one row each — configuration that must never
	// be invisible (same stance as egress Closed rows). Unlike an egress
	// closure these need no posture qualifier: the declared set is byre's own
	// construction, so the removal is always in effect.
	for i, c := range s.MCPClosed {
		label := "MCP closed"
		if i > 0 {
			label = ""
		}
		row(label, "!"+c+"  (config — removed from the declared set)")
	}

	// Claude Skills: the same wiring-not-a-grant posture as MCP — attributed
	// configuration rows, zero exposure contribution. A skill is instructions
	// plus support files; anything its scripts need at runtime is the
	// contributing byre skill's ordinary attributed business.
	for i, d := range s.ClaudeSkills {
		label := "Claude Skills"
		if i > 0 {
			label = ""
		}
		row(label, claudeSkillStatusLine(d))
	}
	if len(s.ClaudeSkills) > 0 {
		row("", claudeSkillsDeliveryLine(s))
	}
	for i, c := range s.ClaudeSkillsClosed {
		label := "CS closed"
		if i > 0 {
			label = ""
		}
		row(label, "!"+c+"  (config — removed from the declared set)")
	}

	// Host-value passthrough (env_from_host, ADR 0026): the one deliberate
	// host->box data channel, attributed source by source — the shipped git
	// identity included (byre's own defaults get no invisibility pass).
	// Disable with `KEY = ""` under env_from_host in any config layer.
	if hostEnv := hostEnvRow(s.EnvFromHost); hostEnv != "" {
		row("Host env", hostEnv)
	}

	// Skill-granted runtime holes, attributed to the skill that opened them.
	for i, g := range s.Grants {
		label := "Skill grants"
		if i > 0 {
			label = ""
		}
		var parts []string
		for _, m := range g.Mounts {
			parts = append(parts, fmt.Sprintf("mounts %s -> %s (%s)", m.Host, m.Target, orDefault(m.Mode, "ro")))
		}
		for _, c := range g.Caps {
			parts = append(parts, "+cap "+c)
		}
		if len(g.RunArgs) > 0 {
			parts = append(parts, "run args "+strings.Join(g.RunArgs, " "))
		}
		if g.NetnsInit != "" {
			parts = append(parts, "netns init "+g.NetnsInit+" (run as root + NET_ADMIN, outside the box)")
		}
		for _, p := range g.SockGroups {
			// Gid is resolved engine-side at launch; status names the path so
			// the collateral group grant is visible even before probe.
			parts = append(parts, "sock group access via "+p+" (gid resolved at launch; wider than the named path)")
		}
		row(label, g.Skill+": "+strings.Join(parts, "; "))
	}

	if len(s.RunArgs) > 0 {
		row("Raw run args", strings.Join(s.RunArgs, " ")+"   (passed through; not introspected)")
	}
	for i, l := range s.BuildRaw {
		label := "Raw build"
		if i > 0 {
			label = ""
		}
		row(label, l)
	}
	if len(s.BuildRaw) > 0 {
		row("", "(raw build lines above are passed through; not introspected)")
	}

	if s.EngineErr != "" {
		row("Container", "unknown (no engine)")
	} else if s.ContainerQueryErr != "" {
		row("Container", "unknown — the engine didn't answer: "+s.ContainerQueryErr)
	} else if s.Container != "" && s.Orphaned {
		row("Container", "running ("+shortID(s.Container)+") — orphaned: the byre that started it is gone; the box runs on. Reach it with 'byre shell', or stop it: "+s.Engine+" stop "+shortID(s.Container))
	} else if s.Container != "" {
		row("Container", "running ("+shortID(s.Container)+")")
	} else {
		row("Container", "not running")
	}
	if len(s.SiblingSessions) > 0 {
		row("Worktrees", fmt.Sprintf("%d other session(s) live: %s  (share these volumes)",
			len(s.SiblingSessions), strings.Join(s.SiblingSessions, ", ")))
	} else if s.SiblingQueryErr != "" {
		row("Worktrees", "sibling sessions unknown — the engine didn't answer: "+s.SiblingQueryErr)
	}
}

// mcpStatusLine renders one declared MCP server: what it is, who declared
// it, what env it consumes (with a provided / NOT provided verdict per
// name), and — when byre can tell — why it won't work: a remote endpoint a
// config closure closes (only claimed where closures are actually enforced:
// an allowlist posture subtracts it, open-denylist drops it; on an open
// network the closure is inert and so is the claim), or a local server
// whose outbound is unknown under an allowlist posture.
func mcpStatusLine(d skills.MCPDecl, s statusInfo) string {
	m := d.MCP
	var b strings.Builder
	if m.Remote() {
		fmt.Fprintf(&b, "%s — remote: %s", m.Name, m.URL)
	} else {
		fmt.Fprintf(&b, "%s — local: %s", m.Name, strings.Join(m.Command, " "))
	}
	src := "config"
	if d.Skill != skills.MCPFromConfig {
		src = "skill " + d.Skill
	}
	notes := []string{src}
	// Declared extra egress renders ON the row, whatever the posture: the
	// extras join the allowlist the moment a restrictive posture arms, and
	// the Egress section suppresses non-config attribution on an open
	// network — without this they'd be the ADR 0019 invisible-teeth pattern
	// (grok review, 2026-07-15).
	if len(m.Egress) > 0 {
		notes = append(notes, "+egress "+strings.Join(m.Egress, ", "))
	}
	// Header NAMES only (a value may carry a user's own literal secret);
	// their ${NAME} template refs join the consumed-env verdicts below.
	if names := m.HeaderNames(); len(names) > 0 {
		notes = append(notes, "headers "+strings.Join(names, ", "))
	}
	if consumed := m.ConsumedEnv(); len(consumed) > 0 {
		marks := make([]string, len(consumed))
		for i, k := range consumed {
			if s.EnvProvided[k] {
				marks[i] = k + " (provided)"
			} else {
				marks[i] = k + " (NOT provided by this box)"
			}
		}
		notes = append(notes, "consumes "+strings.Join(marks, ", "))
	}
	closuresEnforced := config.PostureEnforcesAllowlist(s.NetPosture) || s.NetPosture == config.PostureOpenDenylist
	if host, port, ok := m.Endpoint(); ok && closuresEnforced {
		if c, closed := closedBy(s.EgressClosed, host, port); closed {
			notes = append(notes, fmt.Sprintf("endpoint closed by config '!%s' — not operational", c))
		}
	}
	if !m.Remote() && len(m.Egress) == 0 && config.PostureEnforcesAllowlist(s.NetPosture) {
		notes = append(notes, fmt.Sprintf("outbound unknown — under %s, declare egress on this mcp if the server needs the network", s.NetPosture))
	}
	return b.String() + "  (" + strings.Join(notes, "; ") + ")"
}

// mcpDeliveryLine says how (whether) the declared set reaches the agent
// session. Injection is static truth — deterministic from the image — so it
// speaks plainly; an adapter-less agent gets the honest degradation: the
// set is baked at a stable path, the wiring into that agent is the user's.
func mcpDeliveryLine(s statusInfo) string {
	names := make([]string, len(s.MCPs))
	for i, d := range s.MCPs {
		names[i] = d.MCP.Name
	}
	list := strings.Join(names, ", ")
	switch {
	case s.SkillErr != "":
		return "-> delivery unknown (skills unresolved); declared set bakes to " + gen.MCPConfigPath
	case s.Agent == "":
		return "-> no agent selected; declared set bakes to " + gen.MCPConfigPath + " for anything that wants it"
	case s.AgentMCP == "inject":
		return fmt.Sprintf("-> the agent session receives: %s  (injected via %s)", list, gen.MCPConfigPath)
	default:
		return fmt.Sprintf("-> NOT delivered: agent skill %s has no MCP adapter — the set bakes to %s; wire it into the agent yourself", s.Agent, gen.MCPConfigPath)
	}
}

// claudeSkillStatusLine renders one declared Claude Skill: its name, source
// spelling, and who declared it. Content verdicts (SKILL.md present, bounds)
// are the bake's job — a status row reports the declaration, and a broken
// source fails the next develop with the same attribution.
func claudeSkillStatusLine(d skills.ClaudeSkillDecl) string {
	src := "config"
	from := d.CS.Path
	if d.Skill != skills.ClaudeSkillsFromConfig {
		src = "skill " + d.Skill
		from = d.CS.From
	}
	return fmt.Sprintf("%s — %s  (%s)", d.CS.Name, from, src)
}

// claudeSkillsDeliveryLine is the one delivery verdict row under the declared
// set, keyed off the selected agent's claude_skills vouch (the mcpDeliveryLine
// shape). The shadowing boundary rides the delivered line: byre never touches
// the agent's own ~/.claude/skills, so a same-name skill the in-box agent
// authored there wins over the delivered one.
func claudeSkillsDeliveryLine(s statusInfo) string {
	names := make([]string, len(s.ClaudeSkills))
	for i, d := range s.ClaudeSkills {
		names[i] = "/" + d.CS.Name
	}
	list := strings.Join(names, ", ")
	switch {
	case s.SkillErr != "":
		return "-> delivery unknown (skills unresolved); declared set bakes to " + gen.ClaudeSkillsPath
	case s.Agent == "":
		return "-> no agent selected; declared set bakes to " + gen.ClaudeSkillsPath + " for anything that wants it"
	case s.AgentClaudeSkills == "inject":
		return fmt.Sprintf("-> the agent session receives: %s  (via %s; a same-name skill in the agent's own state shadows byre's)", list, gen.ClaudeSkillsPath)
	default:
		return fmt.Sprintf("-> NOT delivered: agent skill %s has no claude-skills adapter — the set bakes to %s; wire it into the agent yourself", s.Agent, gen.ClaudeSkillsPath)
	}
}

// closedBy returns the config closure that subtracts the given host:port
// from the derived allowlist, if any.
func closedBy(closures []string, host string, port int) (string, bool) {
	entry := fmt.Sprintf("%s:%d", host, port)
	for _, c := range closures {
		if config.EgressClosureMatches(c, entry) {
			return c, true
		}
	}
	return "", false
}

// closureLine renders one `!host[:port]` closure row per the active posture's
// honesty rules: what a closure MEANS depends on what enforces it.
func closureLine(c string, s statusInfo) string {
	disp := c
	if config.ClosurePortless(c) {
		disp += " (every port)"
	}
	switch {
	case s.SkillErr != "":
		return disp + "  (config — posture unknown, skills unresolved)"
	case s.NetPosture == config.PostureOpenDenylist:
		return disp + "  (config — blocked; skill: " + s.NetPostureSkill + ")"
	case s.NetPosture == "":
		return disp + "  (config — unenforced, network open)"
	default:
		return disp + "  (config — removed from the allowlist)"
	}
}

// configEgress parses the resolved config's egress entries, attributed to
// config, so status shows the user's extension holes alongside the skills'.
// The resolved config is already validated, so parse failures can't happen;
// skipping is belt-and-braces, mirroring EgressAllows.
func configEgress(entries []string) []skills.EgressAllow {
	var out []skills.EgressAllow
	for _, e := range entries {
		host, port, err := config.ParseEgress(e)
		if err != nil {
			continue
		}
		out = append(out, skills.EgressAllow{Skill: skills.EgressFromConfig, Host: host, Port: port})
	}
	return out
}

// guardedPaths returns the image paths byre re-asserts at the Dockerfile tail
// for this project — the launcher always, plus a network-posture skill's launch
// gate and netns enforcement script(s). A project `files` entry (or raw build
// line) targeting one of these is overridden by byre's own copy at build time
// (gen's security guard); the caller warns so the override isn't silent.
func guardedPaths(res skills.Resolved) []string {
	paths := []string{gen.LauncherPath}
	if len(res.NetnsInits()) > 0 {
		paths = append(paths, gen.LaunchGatePath)
		for _, h := range res.NetnsInits() {
			// Clean the hook path: skills.Resolve requires it absolute but not
			// clean, so a target on its CANONICAL form (e.g. /usr/local/../usr/
			// local/bin/fw -> /usr/local/bin/fw) must still match here and in
			// warnGuardCollisions.
			paths = append(paths, path.Clean(h.Path))
		}
	}
	return paths
}

// warnGuardCollisions warns when a project `files` destination targets a
// byre-managed security path. byre's own copy wins at the build tail, so the
// file would not take effect there — the warning turns that silent override
// into a legible fact (footgun doctrine: tell, don't refuse). Skill-contributed
// files are byre's trusted construction and are not flagged; only the project's
// own `files` (cfg.Files) are.
func warnGuardCollisions(w io.Writer, cfg config.Config, res skills.Resolved) {
	guarded := map[string]bool{}
	for _, p := range guardedPaths(res) {
		guarded[p] = true
	}
	hitSet := map[string]bool{}
	mark := func(p string) {
		if c := path.Clean(p); guarded[c] {
			hitSet[c] = true
		}
	}
	for src, dest := range cfg.Files {
		// Compare against the destination Docker actually writes, not the raw
		// string. Two forms reach a guarded path without matching it literally:
		//   - file-form: dest names the file (possibly with "." / ".." segments
		//     path.Clean normalizes away);
		//   - directory-form: dest is a directory — trailing slash OR an existing
		//     image dir like /usr/local/bin — and Docker appends the source
		//     basename. dir(dest)+base(src) covers both without needing to know
		//     image state; it only lands on a guarded path when the basenames
		//     line up, i.e. an actual clobber, so it doesn't over-warn.
		// A directory *source* copies its contents in (not modeled here — it needs
		// staging introspection); the tail guard still wins in that case, so this
		// note is best-effort legibility, not the security boundary. Image paths
		// are POSIX, so path (not filepath).
		mark(dest)
		mark(path.Join(dest, path.Base(src)))
	}
	var hits []string
	for h := range hitSet {
		hits = append(hits, h)
	}
	sort.Strings(hits)
	for _, dest := range hits {
		fmt.Fprintf(w, "byre: note — a `files` entry targets %s, a byre-managed security path; byre re-asserts its own copy at the build tail, so your file does not take effect there.\n", dest)
	}
}

// covers reports whether a mount/volume target covers guarded: it equals the
// path or is an ancestor directory of it, so a mount/volume there shadows it.
func covers(target, guarded string) bool {
	target, guarded = path.Clean(target), path.Clean(guarded)
	return target == guarded || target == "/" || strings.HasPrefix(guarded, target+"/")
}

// guardMountVolumeHits returns the byre-managed security paths (launcher, launch
// gate, netns script) that a PROJECT-config mount or volume target covers. A
// typed mount/volume over such a path shadows byre's own file at RUNTIME --
// unlike `files`, which byre re-asserts at the build tail, byre cannot re-assert
// over a runtime mount. E.g. a `[[volumes]] target = "/etc/byre"` seeds the
// fresh volume with the launch gate, which the agent then owns and can delete; a
// `docker restart` recreates the netns without the firewall and the empty gate
// makes the launcher skip its wait (fail open). Only the project's own
// mounts/volumes are checked; skill contributions are byre's trusted
// construction (as with `files`).
func guardMountVolumeHits(cfg config.Config, res skills.Resolved) []string {
	guarded := guardedPaths(res)
	seen := map[string]bool{}
	var hits []string
	check := func(target string) {
		for _, g := range guarded {
			if covers(target, g) && !seen[g] {
				seen[g] = true
				hits = append(hits, g)
			}
		}
	}
	for _, m := range cfg.Mounts {
		if !m.Disabled {
			check(m.Target)
		}
	}
	for _, v := range cfg.Volumes {
		check(v.Target)
	}
	sort.Strings(hits)
	return hits
}

// warnGuardMountCollisions warns at develop when a project mount/volume covers a
// security path. This is a real containment hole (byre can't re-assert over a
// runtime mount), made legible per the footgun doctrine -- tell, don't refuse;
// status degrades the network claim to match.
func warnGuardMountCollisions(w io.Writer, cfg config.Config, res skills.Resolved) {
	for _, g := range guardMountVolumeHits(cfg, res) {
		fmt.Fprintf(w, "🛑 byre: a mount or volume covers %s, a byre-managed security path — it shadows byre's own file at runtime, so byre's containment (firewall / launch gate) is NOT guaranteed for this session.\n", g)
	}
}

// networkLine renders the Network row. Default: "open". With a skill-declared
// posture, the claim follows the footgun doctrine's honesty rules — status
// only asserts unqualified what byre set up itself, and never blocks anything:
//   - skill contributions never degrade the claim (enabling a skill IS
//     trusting it; its grants are attributed separately);
//   - the project's own raw escape hatches (run_args, dockerfile_pre/post)
//     degrade it — byre can't audit arbitrary argv or Dockerfile text;
//   - unresolved skills mean the posture is simply unknown.
func networkLine(s statusInfo) string {
	if s.SkillErr != "" {
		return "unknown  (skills unresolved)"
	}
	if s.NetPosture == "" {
		return "open"
	}
	claim := s.NetPosture
	if claim == config.PostureOpenDenylist {
		// The claim carries the count (grilled 2026-07-14): the closures are
		// the whole enforcement under this posture, so the top line says how
		// many. "Best-effort" honesty (IP-snapshot blocking, aimed at
		// well-behaved clients) lives in the skill's docs and Closed rows,
		// not in a hedge here — byre either applied the drops or the box
		// never launched (fail closed).
		n := len(s.EgressClosed)
		claim = fmt.Sprintf("%s (open network, %d %s blocked)", claim, n, plural(n, "host", "hosts"))
	}
	var raw []string
	if s.ProjectRunArgs {
		raw = append(raw, "raw run_args")
	}
	if len(s.BuildRaw) > 0 {
		raw = append(raw, "raw build lines")
	}
	if s.GuardMountShadow {
		raw = append(raw, "a mount/volume over a security path")
	}
	if len(raw) > 0 {
		return claim + "  (declared; " + strings.Join(raw, " + ") + " present — not guaranteed)"
	}
	return claim + "  (skill: " + s.NetPostureSkill + ")"
}

// portStatusLine renders a published port as "iface:host -> container", via
// the SAME normalization the runtime applies (config.PortEffective) — so
// status can't lie about the defaulted interface or host port.
func portStatusLine(p config.Port) string {
	iface, host := config.PortEffective(p)
	return fmt.Sprintf("%s:%d -> %d  (host -> container)", iface, host, p.Container)
}

func splitVolumes(vols []config.Volume) (state, cache, machine []string) {
	for _, v := range vols {
		switch {
		case v.MachineScoped():
			machine = append(machine, v.Name)
		case v.Role == "state":
			state = append(state, v.Name)
		default:
			cache = append(cache, v.Name)
		}
	}
	return state, cache, machine
}
