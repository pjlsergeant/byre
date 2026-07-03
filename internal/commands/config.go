package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"byre/internal/builtins"
	"byre/internal/config"
	"byre/internal/configui"
	"byre/internal/onboard"
	"byre/internal/project"
	"byre/internal/runner"
	"byre/internal/skills"
)

// Config implements `byre config` — the interactive editor for this project's
// host-side store config (~/.byre/projects/<id>/byre.config), and, with global,
// the global ~/.byre/default.config. Both are byre-owned/host-side, so editing
// them never touches the project tree.
func Config(projectDir string, global bool) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	templatesDir := filepath.Join(home, "templates")
	skillsDir := filepath.Join(home, "skills")
	_ = builtins.MaterializeTemplates(templatesDir)
	_ = builtins.MaterializeSkills(skillsDir)
	templates := onboard.ListTemplates(templatesDir)
	agents := skills.ListAgentSkills(skillsDir)
	skillOpts := skills.ListSkills(skillsDir)

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
	saved, err := configui.Run(title, path, cur, templates, agents, skillOpts, vols)
	if err != nil {
		return err
	}
	if !saved {
		fmt.Fprintln(os.Stderr, "byre: config unchanged.")
		return nil
	}
	fmt.Fprintf(os.Stderr, "byre: wrote %s\n", path)
	return nil
}

// volumeAdmin is the engine-backed configui.VolumeAdmin for a project: it lists
// the resolved volume set (config + skills) with on-disk presence, and clears a
// volume. It mirrors `byre reset`, scoped to one volume.
type volumeAdmin struct {
	r          *runner.Runner
	paths      project.Paths
	projectDir string
}

// newVolumeAdmin builds the volume admin for a project, or returns nil (so the
// editor omits the Volumes section) when the config or engine won't resolve. The
// section is shown even with zero volumes — the screen re-resolves on each open,
// so volumes added later (e.g. via $EDITOR) appear without restarting.
func newVolumeAdmin(paths project.Paths, projectDir string) configui.VolumeAdmin {
	cfg, _, err := resolve(paths, projectDir)
	if err != nil {
		return nil
	}
	eng, err := runner.Detect(cfg.Engine, nil)
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
// the whole repo family — mirroring the loud banner `reset`/`forget` print, so
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
	cfg, res, err := resolve(a.paths, a.projectDir)
	if err != nil {
		return nil, err
	}
	defs := dedupeVolumes(allVolumes(cfg, res.Volumes))
	out := make([]configui.VolumeStatus, 0, len(defs))
	for _, v := range defs {
		exists, err := a.r.VolumeExists(VolumeName(a.paths.ID, v.Name))
		if err != nil {
			return nil, err
		}
		out = append(out, configui.VolumeStatus{Name: v.Name, Role: v.Role, Target: v.Target, Exists: exists})
	}
	return out, nil
}

// Clear removes a volume under the project setup lock, re-checking for a live
// session inside it — the same guard `reset`/`forget` use, so a concurrent
// `byre develop` can't seed a volume we're deleting (or vice versa).
func (a *volumeAdmin) Clear(name string) error {
	return withSetupLock(a.paths.LockFile, func() error {
		if live, err := liveSession(a.r, a.paths.ID); err != nil {
			return fmt.Errorf("checking for a running session: %w", err)
		} else if len(live) > 0 {
			return fmt.Errorf("a session is running (%s) — exit it before clearing volumes", shortID(live[0]))
		}
		return a.r.VolumeRemove(VolumeName(a.paths.ID, name))
	})
}
