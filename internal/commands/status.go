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

// StatusInfo is the resolved, display-ready view of a project for `byre status`.
type StatusInfo struct {
	Agent      string
	Engine     string
	ID         string
	Canonical  string // the dir bound at /workspace (the worktree, for a worktree)
	WorktreeOf string // family (main worktree) path when this is a linked worktree, else ""
	Skills     []string
	Binds      []config.Mount
	Ports      []config.Port
	Volumes    []config.Volume
	Grants     []skills.Grant // per-skill runtime grants (attribution)
	RunArgs    []string
	BuildRaw   []string // dockerfile_pre + dockerfile_post (raw, not introspected)
	Container  string   // running container id, or "" if none
	Rootless   bool     // true if the engine is rootless Podman (unsupported ownership)
	EngineErr  string   // why the engine/container state is unknown, if applicable
	SkillErr   string   // why skills couldn't be resolved, if applicable
	SelfEdit   string   // host store path when --self-edit is active, else ""
	Proposal   string   // note about a committed <project>/byre.config, if any
}

// Status implements `byre status`. selfEdit mirrors `develop --self-edit` so the
// grant it would add (rw ~/.byre) is announced here too.
func Status(stdout io.Writer, projectDir string, selfEdit bool) error {
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

	info := StatusInfo{
		Agent:     cfg.Agent,
		Engine:    cfg.Engine,
		ID:        paths.ID,
		Canonical: paths.WorkDir, // what actually mounts at /workspace
		Skills:    cfg.Skills,
		Binds:     cfg.Mounts,
		Ports:     cfg.Ports,
		Volumes:   cfg.Volumes,
		RunArgs:   cfg.RunArgs,
		BuildRaw:  append(append([]string{}, cfg.DockerfilePre...), cfg.DockerfilePost...),
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
		binds := append(append([]config.Mount{}, cfg.Mounts...), res.Mounts...)
		vols := append(append([]config.Volume{}, cfg.Volumes...), res.Volumes...)
		if verr := (config.Config{Mounts: binds, Volumes: vols}).Validate(); verr != nil {
			info.SkillErr = verr.Error()
		} else {
			info.Skills = skillNames(res)
			info.Binds = binds
			info.Volumes = vols
			info.Grants = res.Grants
			info.RunArgs = append(append([]string{}, res.RunArgs...), cfg.RunArgs...)
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
		// Query the worktree label so status reflects THIS worktree's session, not
		// a sibling's (both carry the family label).
		if ids, cerr := r.RunningContainersByLabel(workdirLabel(paths)); cerr == nil && len(ids) > 0 {
			info.Container = ids[0]
		}
	}

	RenderStatus(stdout, info)
	return nil
}

// RenderStatus writes the flat, scannable "what can this thing touch?" block.
// Raw run_args are shown verbatim and flagged as not introspected by byre.
func RenderStatus(w io.Writer, s StatusInfo) {
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
	row("Network", "open")

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
}

func skillNames(res skills.Resolved) []string {
	names := make([]string, 0, len(res.SkillBlocks))
	for _, b := range res.SkillBlocks {
		names = append(names, b.Name)
	}
	return names
}

// portStatusLine renders a published port as "iface:host -> container", matching
// the runtime defaults (empty interface = 127.0.0.1, host 0 = the container port).
func portStatusLine(p config.Port) string {
	iface := orDefault(p.Interface, "127.0.0.1")
	host := p.Host
	if host == 0 {
		host = p.Container
	}
	return fmt.Sprintf("%s:%d -> %d  (host -> container)", iface, host, p.Container)
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
