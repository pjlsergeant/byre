package commands

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"byre/internal/builtins"
	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/runner"
	"byre/internal/skills"
)

// statusInfo is the resolved, display-ready view of a project for `byre status`.
type statusInfo struct {
	Agent           string
	Engine          string
	ID              string
	Canonical       string // the dir bound at /workspace (the worktree, for a worktree)
	WorktreeOf      string // family (main worktree) path when this is a linked worktree, else ""
	Skills          []string
	Binds           []config.Mount
	Ports           []config.Port
	Volumes         []config.Volume
	Grants          []skills.Grant // per-skill runtime grants (attribution)
	RunArgs         []string
	BuildRaw        []string             // dockerfile_pre + dockerfile_post (raw, not introspected)
	NetPosture      string               // a skill's declared network posture ("" = default open)
	NetPostureSkill string               // the skill declaring it
	Egress          []skills.EgressAllow // resolved allowlist (host:port + skill), shown when a posture is declared
	ProjectRunArgs  bool                 // the PROJECT's own raw run_args present (degrades the posture claim)
	CustomDF        bool                 // full-Dockerfile opt-out (skill build contributions never land)
	Container       string               // this dir's running container id, or "" if none
	SiblingSessions []string             // short ids of OTHER live sessions in this repo family (worktrees sharing these volumes)
	Rootless        bool                 // true if the engine is rootless Podman (unsupported ownership)
	EngineErr       string               // why the engine/container state is unknown, if applicable
	SkillErr        string               // why skills couldn't be resolved, if applicable
	SelfEdit        string               // host store path when --self-edit is active, else ""
	Proposal        string               // note about a committed <project>/byre.config, if any
}

// Status implements `byre status`. selfEdit mirrors `develop --self-edit` so the
// grant it would add (rw ~/.byre) is announced here too.
func Status(s Streams, projectDir string, selfEdit bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Materialize built-ins before loading config (templates feed the cascade).
	_ = builtins.MaterializeTemplates(filepath.Join(paths.Home, "templates"))
	skillsDir := filepath.Join(paths.Home, "skills")
	materializeErr := builtins.MaterializeSkills(skillsDir)

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
		CustomDF:       cfg.Dockerfile != "",
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
			// The firewall also honors FIREWALL_ALLOW from config env (the user
			// extension path), so status must show those holes too — attributed
			// to config, not a skill — or it under-reports what the box can reach.
			info.Egress = append(info.Egress, configEgress(cfg.Env["FIREWALL_ALLOW"])...)
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
		// not a sibling (both carry the family label).
		mine, _ := r.RunningContainersByLabel(workdirLabel(paths))
		if len(mine) > 0 {
			info.Container = mine[0]
		}
		// Other live sessions in the same repo family (worktrees sharing these
		// volumes). Surfaced so status doesn't imply "nothing running" while
		// reset/forget correctly refuse on the family label. Empty for a plain
		// project (its family set is just itself).
		if fam, cerr := r.RunningContainersByLabel(familyLabel(paths)); cerr == nil {
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

	// When a firewall posture is in effect, list its allowlist so "what can
	// this box reach?" is legible — each host:port attributed to the skill that
	// asked for it (deduped on host:port; first declarer wins the credit).
	if s.NetPosture != "" && len(s.Egress) > 0 {
		seen := map[string]bool{}
		first := true
		for _, a := range s.Egress {
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
			row(label, fmt.Sprintf("%s  (%s)", hp, a.Skill))
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
			row(label, fmt.Sprintf("%s -> %s  (%s)", m.Host, m.Target, orDefault(m.Mode, "ro")))
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

	state, cache := splitVolumes(s.Volumes)
	row("State vols", orDefault(strings.Join(state, ", "), "none"))
	row("Cache vols", orDefault(strings.Join(cache, ", "), "none"))

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

// configEgress parses the project's FIREWALL_ALLOW env value into egress
// entries attributed to config, so status shows the user's extension holes
// alongside the skills'. Malformed entries are dropped (as firewall.sh does).
func configEgress(raw string) []skills.EgressAllow {
	entries := skills.SplitEgress(raw)
	for i := range entries {
		entries[i].Skill = "config: FIREWALL_ALLOW"
	}
	return entries
}

// networkLine renders the Network row. Default: "open". With a skill-declared
// posture, the claim follows the footgun doctrine's honesty rules — status
// only asserts unqualified what byre set up itself, and never blocks anything:
//   - skill contributions never degrade the claim (enabling a skill IS
//     trusting it; its grants are attributed separately);
//   - the project's own raw escape hatches (run_args, dockerfile_pre/post)
//     degrade it — byre can't audit arbitrary argv or Dockerfile text;
//   - the full-Dockerfile opt-out means the skill's build bits never landed
//     in the image at all, so the wall byre would vouch for was never built;
//   - unresolved skills mean the posture is simply unknown.
func networkLine(s statusInfo) string {
	if s.SkillErr != "" {
		return "unknown  (skills unresolved)"
	}
	if s.NetPosture == "" {
		return "open"
	}
	if s.CustomDF {
		return s.NetPosture + "  (declared; custom Dockerfile — byre didn't build the wall)"
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
// the SAME normalization the runtime applies — so status can't lie about the
// defaulted interface or host port.
func portStatusLine(p config.Port) string {
	n := normalizePort(p)
	return fmt.Sprintf("%s:%d -> %d  (host -> container)", n.Interface, n.Host, n.Container)
}

func splitVolumes(vols []config.Volume) (state, cache []string) {
	for _, v := range vols {
		if v.Role == "state" {
			state = append(state, v.Name)
		} else {
			cache = append(cache, v.Name)
		}
	}
	return state, cache
}
