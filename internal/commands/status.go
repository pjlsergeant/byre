package commands

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// statusInfo is the resolved, display-ready view of a project for `byre status`.
type statusInfo struct {
	Agent           string
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
	// Containments are skill-declared containment holes (warranty disclaimer).
	// Multi-declarer: all shown; other status rows stay unqualified.
	Containments    []skills.ContainmentDecl
	ProjectRunArgs  bool     // the PROJECT's own raw run_args present (degrades the posture claim)
	Container       string   // this dir's running container id, or "" if none
	SiblingSessions []string // short ids of OTHER live sessions in this project (worktrees sharing these volumes)
	Rootless        bool     // true if the engine is rootless Podman (unsupported ownership)
	EngineErr       string   // why the engine/container state is unknown, if applicable
	SkillErr        string   // why skills couldn't be resolved, if applicable
	SelfEdit        string   // host store path when --self-edit is active, else ""
	Proposal        string   // note about a committed <project>/byre.config, if any
}

// Status implements `byre status`. selfEdit mirrors `develop --self-edit` so the
// grant it would add (rw ~/.byre) is announced here too.
func Status(s Streams, projectDir string, selfEdit bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Materialize built-ins before loading config (templates feed the cascade).
	// The error degrades the skills view below rather than failing status.
	skillsDir := filepath.Join(paths.Home, "skills")
	materializeErr := builtins.EnsureStore(paths.Home)

	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}

	info := statusInfo{
		Agent:          cfg.Agent,
		Engine:         cfg.Engine,
		ID:             paths.ID,
		Canonical:      paths.WorkDir, // what actually mounts at /workspace
		Skills:         cfg.Skills,
		Binds:          cfg.Mounts,
		Ports:          cfg.Ports,
		Volumes:        cfg.Volumes,
		RunArgs:        cfg.RunArgs,
		BuildRaw:       append(append([]string{}, cfg.DockerfilePre...), cfg.DockerfilePost...),
		ProjectRunArgs: len(cfg.RunArgs) > 0,
		EnvFromHost:    cfg.EnvFromHost,
	}
	if paths.IsWorktree {
		info.WorktreeOf = paths.Canonical
	}
	if selfEdit {
		info.SelfEdit = paths.Dir
	}
	switch proposalState(projectDir, paths) {
	case "pending":
		info.Proposal = "repo ships a byre.config — PENDING review (run develop to adopt)"
	case "adopted":
		info.Proposal = "running an adopted repo byre.config"
	case "declined":
		info.Proposal = "repo ships a byre.config — DECLINED (editing it re-prompts)"
	}
	// Enrich with resolved skills so implicit/built-in contributions (the agent
	// skill, its .claude state volume, skill mounts) are shown, not just the
	// config-level view. Best-effort: a resolution error is surfaced, not fatal.
	if merr := materializeErr; merr != nil {
		info.SkillErr = merr.Error()
	} else if res, rerr := skills.Resolve(cfg, skillsDir); rerr != nil {
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
			info.Egress = res.EgressAllows()
			// The `egress` config key is the user's extension path (ADR 0019),
			// so status must show those holes too — attributed to config, not a
			// skill — or it under-reports what the box can reach.
			info.Egress = append(info.Egress, configEgress(cfg.Egress)...)
			info.Containments = res.Containments()
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
		}
		// This dir's own session: the worktree label, so it reflects THIS worktree,
		// not a sibling (both carry the project label).
		mine, _ := r.RunningContainersByLabel(workdirLabel(paths))
		if len(mine) > 0 {
			info.Container = mine[0]
		}
		// Other live sessions in the same project (worktrees sharing these
		// volumes). Surfaced so status doesn't imply "nothing running" while
		// reset/forget correctly refuse on the project label. Empty for a plain
		// project (no worktree siblings).
		if fam, cerr := r.RunningContainersByLabel(projectLabel(paths)); cerr == nil {
			mineSet := map[string]bool{}
			for _, id := range mine {
				mineSet[id] = true
			}
			for _, id := range fam {
				if !mineSet[id] {
					info.SiblingSessions = append(info.SiblingSessions, shortID(id))
				}
			}
		}
	}

	renderStatus(s.Out, info)
	return nil
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
	if s.Proposal != "" {
		row("Repo config", s.Proposal)
	}
	if s.EngineErr != "" {
		row("Engine", s.Engine+"  (not found: "+s.EngineErr+")")
	} else if s.Rootless {
		row("Engine", s.Engine+"  (rootless — file ownership UNSUPPORTED in v0; use rootful)")
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

	// When a firewall posture is in effect, list its allowlist so "what can
	// this box reach?" is legible — each host:port attributed to the skill that
	// asked for it (deduped on host:port; first declarer wins the credit).
	// With NO posture, skill-declared egress is suppressed (every agent skill
	// declares endpoints; on an open network the list is meaningless noise) —
	// but the user's own `egress` config entries still print, marked
	// unenforced: config must not carry invisible teeth that a later skill
	// toggle arms (ADR 0019).
	egress := s.Egress
	unenforced := ""
	if s.NetPosture == "" {
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
			row(label, fmt.Sprintf("%s  (%s%s)", hp, a.Skill, unenforced))
		}
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
	} else {
		row("Skills", orDefault(strings.Join(s.Skills, ", "), "none"))
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
	} else if s.Container != "" {
		row("Container", "running ("+shortID(s.Container)+")")
	} else {
		row("Container", "not running")
	}
	if len(s.SiblingSessions) > 0 {
		row("Worktrees", fmt.Sprintf("%d other session(s) live: %s  (share these volumes)",
			len(s.SiblingSessions), strings.Join(s.SiblingSessions, ", ")))
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
	var raw []string
	if s.ProjectRunArgs {
		raw = append(raw, "raw run_args")
	}
	if len(s.BuildRaw) > 0 {
		raw = append(raw, "raw build lines")
	}
	if len(raw) > 0 {
		return s.NetPosture + "  (declared; " + strings.Join(raw, " + ") + " present — not guaranteed)"
	}
	return s.NetPosture + "  (skill: " + s.NetPostureSkill + ")"
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
