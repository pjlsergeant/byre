package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// Config implements `byre config` — the interactive editor for this project's
// host-side store config (~/.byre/projects/<id>/byre.config), and, with global,
// the global ~/.byre/default.config. Both are byre-owned/host-side, so editing
// them never touches the project tree.
func Config(s Streams, projectDir string, global bool) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	templatesDir := filepath.Join(home, "templates")
	skillsDir := filepath.Join(home, "skills")
	_ = builtins.MaterializeTemplates(templatesDir)
	_ = builtins.MaterializeSkills(skillsDir)
	templates := config.ListTemplates(templatesDir)
	agents := skills.ListAgentSkills(skillsDir)
	skillOpts := skills.ListSkills(skillsDir)
	skillDescs := skills.DescribeSkills(skillsDir)
	// Provenance inputs (ADR 0018): the resolved lower cascade per template,
	// so the project editor can mark inherited entries instead of showing the
	// layer's raw delta, plus each skill's runtime contribution for the
	// read-only (skill:name) rows. Degrade on error (a broken template or
	// skill just loses its marks); the --global editor gets no Lower -- it IS
	// the base layer.
	inh := configui.Inherited{Skills: map[string]configui.SkillRuntime{}}
	if !global {
		inh.HasLower = true
		if def, derr := config.ParseFile(filepath.Join(home, "default.config")); derr == nil {
			inh.Default = def
		}
		inh.Templates = map[string]config.Config{}
		for _, t := range templates {
			if tc, terr := config.ParseFile(config.TemplatePath(templatesDir, t)); terr == nil {
				inh.Templates[t] = tc
			}
		}
	}
	for _, n := range skillOpts {
		if sk, serr := skills.Load(skillsDir, n); serr == nil {
			inh.Skills[n] = configui.SkillRuntime{
				Mounts:  sk.File.Runtime.Mounts,
				Env:     sk.File.Runtime.Env,
				Egress:  sk.File.Runtime.Egress,
				Posture: sk.File.Runtime.NetworkPosture,
			}
		}
	}

	var path, title string
	var vols configui.VolumeAdmin // nil for --global (no project volumes)
	if global {
		path = filepath.Join(home, "default.config")
		title = "byre global config  (~/.byre/default.config)"
	} else {
		paths, perr := project.Resolve(projectDir)
		if perr != nil {
			return perr
		}
		if berr := paths.Bootstrap(); berr != nil {
			return berr
		}
		path = filepath.Join(paths.Dir, config.ProjectConfigName)
		title = "byre project config  (" + paths.ID + ")"
		vols = newVolumeAdmin(paths, projectDir) // nil if the engine/config won't resolve
	}

	cur, err := config.ParseFile(path)
	if err != nil {
		return err
	}
	// The editor saves in place (explicit ctrl+s), so Run reports whether the file
	// was written rather than handing back a config for us to save.
	// worktree_base is a host workflow preference edited in the GLOBAL config; the
	// project editor omits it (showing it there would imply a per-project unset
	// that the cascade can't honor once a global default exists).
	saved, err := configui.Run(title, path, cur, templates, agents, skillOpts, skillDescs, inh, vols, global)
	if err != nil {
		return err
	}
	if !saved {
		fmt.Fprintln(s.Err, "byre: config unchanged.")
		return nil
	}
	fmt.Fprintf(s.Err, "byre: wrote %s\n", path)
	return nil
}

// volumeAdmin is the engine-backed configui.VolumeAdmin for a project: it lists
// the resolved volume set (config + skills) with on-disk presence, and clears a
// volume. It mirrors `byre reset`, scoped to one volume.
type volumeAdmin struct {
	r          engineRunner
	paths      project.Paths
	projectDir string
}

// newVolumeAdmin builds the volume admin for a project, or returns nil (so the
// editor omits the Volumes section) when the config or engine won't resolve. The
// section is shown even with zero volumes — the screen re-resolves on each open,
// so volumes added later (e.g. via $EDITOR) appear without restarting.
func newVolumeAdmin(paths project.Paths, projectDir string) configui.VolumeAdmin {
	rv, err := resolve(paths, projectDir)
	if err != nil {
		return nil
	}
	eng, err := runner.Detect(rv.cfg.Engine, nil)
	if err != nil {
		return nil // no engine → can't list/clear; hide the section
	}
	return &volumeAdmin{r: runner.New(eng), paths: paths, projectDir: projectDir}
}

func dedupeVolumes(vs []config.Volume) []config.Volume {
	seen := map[string]bool{}
	out := make([]config.Volume, 0, len(vs))
	for _, v := range vs {
		if v.Name == "" || seen[v.Name] {
			continue
		}
		seen[v.Name] = true
		out = append(out, v)
	}
	return out
}

// SharedNote warns, before a clear, that a worktree's volumes are shared across
// the whole project (all its worktrees) — mirroring the loud banner `reset`/`forget` print, so
// clearing a volume from the config UI is as legible about its blast radius.
func (a *volumeAdmin) SharedNote() string {
	if a.paths.IsWorktree {
		return fmt.Sprintf("Shared with ALL worktrees of %s.", a.paths.Canonical)
	}
	return ""
}

// List re-resolves the config from disk so the volume set reflects the current
// state (e.g. after a $EDITOR edit to [[volumes]] or the agent), not a snapshot.
func (a *volumeAdmin) List() ([]configui.VolumeStatus, error) {
	rv, err := resolve(a.paths, a.projectDir)
	if err != nil {
		return nil, err
	}
	defs := dedupeVolumes(rv.volumes)
	out := make([]configui.VolumeStatus, 0, len(defs))
	declared := map[string]bool{}
	for _, v := range defs {
		exists, err := a.r.VolumeExists(scopedVolumeName(a.paths.ID, os.Getuid(), v))
		if err != nil {
			return nil, err
		}
		if v.MachineScoped() {
			declared[v.Name] = true
		}
		out = append(out, configui.VolumeStatus{Name: v.Name, Role: v.Role, Target: v.Target, Exists: exists, Machine: v.MachineScoped()})
	}
	// Orphaned machine-scoped volumes: present on the engine but no longer
	// declared by any enabled skill/config (e.g. shared-auth disabled after a
	// login). Listed so the deliberate-delete route reset/forget advertises
	// keeps working for them (review finding on ADR 0017's logout story).
	prefix := fmt.Sprintf("byre-machine-u%d-", os.Getuid())
	engineVols, err := a.r.VolumesByPrefix(prefix)
	if err != nil {
		return nil, err
	}
	for _, ev := range engineVols {
		name := strings.TrimPrefix(ev, prefix)
		if !declared[name] {
			out = append(out, configui.VolumeStatus{Name: name, Exists: true, Machine: true, Orphan: true})
		}
	}
	return out, nil
}

// Clear removes a volume under the project setup lock, re-checking for a live
// session inside it — the same guard `reset`/`forget` use, so a concurrent
// `byre develop` can't seed a volume we're deleting (or vice versa). The row's
// scope decides the Docker name: an orphaned machine volume may share its
// logical name with a declared project one, so name alone is ambiguous.
func (a *volumeAdmin) Clear(v configui.VolumeStatus) error {
	// io.Discard: this runs inside the TUI; a waiting note would corrupt the screen.
	return withSetupLock(io.Discard, a.paths.LockFile, func() error {
		if live, err := liveSession(a.r, a.paths.ID); err != nil {
			return fmt.Errorf("checking for a running session: %w", err)
		} else if len(live) > 0 {
			return fmt.Errorf("a session is running (%s) — exit it before clearing volumes", shortID(live[0]))
		}
		if v.Machine {
			// A machine-scoped volume is mounted by EVERY project's boxes, so
			// the this-project guard above isn't enough: refuse while ANY byre
			// session runs (bare label key = presence filter). Clearing it is
			// the machine-wide logout story (ADR 0017).
			if live, lerr := a.r.RunningContainersByLabel(labelKey); lerr != nil {
				return fmt.Errorf("checking for running byre sessions: %w", lerr)
			} else if len(live) > 0 {
				return fmt.Errorf("this volume is shared by ALL your projects and a byre session is running (%s) — exit every session before clearing it", shortID(live[0]))
			}
			return a.r.VolumeRemove(machineVolumeName(os.Getuid(), v.Name))
		}
		return a.r.VolumeRemove(volumeName(a.paths.ID, v.Name))
	})
}
