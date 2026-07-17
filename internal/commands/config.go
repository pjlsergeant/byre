package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// chainContains reports whether needle appears in from's extends chain
// (from itself included), walked over the raw layers map with a seen-guard
// (the on-disk state may carry a cycle mid-edit). The EXTENDS picker uses it
// to exclude parents that would loop back through the layer being edited.
func chainContains(layers map[string]config.Config, from, needle string) bool {
	seen := map[string]bool{}
	for name := from; name != "" && !seen[name]; {
		if name == needle {
			return true
		}
		seen[name] = true
		name = layers[name].Extends
	}
	return false
}

// skillOpts is ListSkills minus unofferable stubs (see the call site).
func skillOpts(cat *packages.Catalog) []string {
	var out []string
	for _, n := range skills.ListSkills(cat) {
		if sk, err := skills.Load(cat, n); err == nil && skills.IsStub(sk.File) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// Config implements `byre config` — the interactive editor for this project's
// host-side store config (~/.byre/projects/<id>/byre.config); with global,
// the global ~/.byre/default.config; with layer, a named layer's
// ~/.byre/layers/<name>/layer.config. All byre-owned/host-side, so editing
// them never touches the project tree.
func Config(s Streams, projectDir string, global bool, layer string) error {
	if global && layer != "" {
		return fmt.Errorf("--global and --layer are different files; pick one")
	}
	home, err := project.Home()
	if err != nil {
		return err
	}
	// Best-effort: the editor should still open on a store that won't
	// prepare; develop's strict path reports the failure.
	_ = builtins.EnsureStoreOut(home, s.Err)
	cat, _ := builtins.LoadCatalogRaw(home)
	templates := config.ListTemplatesCatalog(cat)
	agents := skills.ListAgentSkills(cat)
	// Stubs (description-only compatibility shells: devloop,
	// grok-shared-auth) are not OFFERED: a picker has nothing to enable.
	// A config that already references one still shows it -- skillEntries
	// unions the config-side names back in, so it stays un-referenceable.
	skillOpts := skillOpts(cat)
	skillDescs := skills.DescribeSkills(cat)
	target := configui.TargetProject
	if global {
		target = configui.TargetGlobal
	}
	if layer != "" {
		if err := config.ValidateLayerName(layer); err != nil {
			return err
		}
		if _, err := os.Stat(config.LayerPath(home, layer)); err != nil {
			return fmt.Errorf("layer %q not found — create it first: byre layer new %s", layer, layer)
		}
		target = configui.TargetLayer
	}
	// Provenance inputs (ADR 0018): the resolved lower cascade per template,
	// so the project editor can mark inherited entries instead of showing the
	// layer's raw delta, plus each skill's runtime contribution for the
	// read-only (skill:name) rows. Degrade on error (a broken template or
	// skill just loses its marks); the --global editor gets no Lower -- it IS
	// the base layer. A layer editor's lower is default ⊕ its ancestors —
	// deliberately NOT any template (layers can't select shapes) and NOT the
	// projects extending it (descendants are out of view by design).
	inh := configui.Inherited{Skills: map[string]configui.SkillRuntime{}, Catalog: cat}
	if target != configui.TargetGlobal {
		inh.HasLower = true
		if def, derr := config.ParseFile(filepath.Join(home, "default.config")); derr == nil {
			inh.Default = def
		}
		inh.Templates = map[string]config.Config{}
		if cat != nil {
			for _, t := range templates {
				if ent, ok := cat.Lookup(t); ok && ent.Kind == packages.KindTemplate {
					// Load template body as a Config for inheritance marks.
					if raw, rerr := ent.ReadPrimary(); rerr == nil {
						body := packages.StripPackageTable(raw)
						if tc, terr := config.Parse(body); terr == nil {
							inh.Templates[t] = tc
						}
					}
				}
			}
		}
		// Named layers feed the EXTENDS picker and the live chain walk. The
		// picker offers loadable layers only — minus, in a layer editor, the
		// layer itself and anything whose chain runs through it (choosing
		// either would create the cycle the resolver hard-errors on).
		inh.Layers, _ = config.LoadableLayers(home, cat)
		for name := range inh.Layers {
			if layer != "" && (name == layer || chainContains(inh.Layers, name, layer)) {
				continue
			}
			inh.LayerNames = append(inh.LayerNames, name)
		}
		sort.Strings(inh.LayerNames)
	}
	for _, n := range skillOpts {
		if sk, serr := skills.Load(cat, n); serr == nil {
			// Key by display name (what the picker lists) and canonical ID.
			rt := configui.SkillRuntime{
				Mounts:       sk.File.Runtime.Mounts,
				Env:          sk.File.Runtime.Env,
				EnvDocs:      sk.File.Runtime.EnvDocs,
				Egress:       sk.File.Runtime.Egress,
				Offered:      sk.File.Runtime.EgressOffered,
				MCPs:         sk.File.MCPs,
				ClaudeSkills: sk.File.ClaudeSkills,
				Posture:      sk.File.Runtime.NetworkPosture,
				Containment:  sk.File.Runtime.Containment,
				CompanionFor: sk.File.CompanionAgent(),
				Provenance:   "",
			}
			if cat != nil {
				if ent, ok := cat.Lookup(n); ok {
					rt.Provenance = string(ent.Provenance)
					rt.ProvLabel = ent.ProvenanceLabel()
					if ent.Provenance == "invalid" || ent.Provenance == "conflict" || ent.Provenance == "legacy" {
						rt.DisabledReason = ent.Reason
					}
				}
			}
			inh.Skills[n] = rt
			inh.Skills[sk.Name] = rt
		}
	}

	var path, title string
	var vols configui.VolumeAdmin // nil for --global and --layer (no project volumes)
	var prepare func() error      // deferred store setup, run by the UI before its first write
	switch target {
	case configui.TargetGlobal:
		path = filepath.Join(home, "default.config")
		title = "byre global config  (~/.byre/default.config)"
	case configui.TargetLayer:
		path = config.LayerPath(home, layer)
		title = "byre layer config  (" + layer + ")"
	default:
		paths, perr := project.Resolve(projectDir)
		if perr != nil {
			return perr
		}
		// Fail the id-collision check loudly before the editor opens, but defer
		// the enrolling Bootstrap to write time: opening the editor on a project
		// byre has never seen and quitting without saving must leave no
		// ~/.byre/projects/<id> behind. The hook runs on EVERY landing write
		// (Bootstrap is idempotent), not just the first: Save's AtomicWrite
		// would happily MkdirAll a store a concurrent `byre forget` deleted
		// mid-session, and a store re-created WITHOUT its path record is a
		// half-enrollment the id-collision check can't see. Bootstrap riding
		// every write keeps dir and record inseparable.
		if verr := paths.ValidateExisting(); verr != nil {
			return verr
		}
		prepare = paths.Bootstrap
		path = filepath.Join(paths.Dir, config.ProjectConfigName)
		title = "byre project config  (" + paths.ID + ")"
		vols = newVolumeAdmin(paths, projectDir, prepare) // nil if the engine/config won't resolve
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
	saved, err := configui.Run(title, path, cur, templates, agents, skillOpts, skillDescs, inh, vols, target, prepare)
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
// volume. It mirrors `byre reset`, scoped to one volume — including reset's
// every-installed-engine stance: this screen is the advertised deliberate-
// delete route for machine volumes, and "logged out everywhere" would be a
// lie if a same-named volume survived on the engine the config doesn't name
// (the lifecycle-batch bug class; audit finding).
type volumeAdmin struct {
	rs         []engineRunner
	paths      project.Paths
	projectDir string
	prepare    func() error // re-ensures the store (dir + path record) before Clear locks
}

// newVolumeAdmin builds the volume admin for a project, or returns nil (so the
// editor omits the Volumes section) when the config or engines won't resolve.
// The section is shown even with zero volumes — the screen re-resolves on each
// open, so volumes added later (e.g. via $EDITOR) appear without restarting.
func newVolumeAdmin(paths project.Paths, projectDir string, prepare func() error) configui.VolumeAdmin {
	if _, err := resolve(paths, projectDir, nil); err != nil {
		return nil
	}
	rs, err := lifecycleEngines()
	if err != nil {
		return nil // no engine → can't list/clear; hide the section
	}
	return &volumeAdmin{rs: rs, paths: paths, projectDir: projectDir, prepare: prepare}
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
	rv, err := resolve(a.paths, a.projectDir, nil)
	if err != nil {
		return nil, err
	}
	defs := dedupeVolumes(rv.volumes)
	var out []configui.VolumeStatus
	// One pass per installed engine: a volume (declared or orphaned) can
	// exist on both docker and podman, and each copy is its own row — the
	// delete route must show and clear every copy, or "removed" is false.
	for _, r := range a.rs {
		eng := string(r.Engine())
		declared := map[string]bool{}
		for _, v := range defs {
			exists, err := r.VolumeExists(scopedVolumeName(a.paths.ID, os.Getuid(), v))
			if err != nil {
				return nil, err
			}
			if v.MachineScoped() {
				declared[v.Name] = true
			}
			out = append(out, configui.VolumeStatus{Name: v.Name, Role: v.Role, Target: v.Target, Exists: exists, Machine: v.MachineScoped(), Engine: eng})
		}
		// Orphaned machine-scoped volumes: present on the engine but no longer
		// declared by any enabled skill/config (e.g. shared-auth disabled after a
		// login). Listed so the deliberate-delete route reset/forget advertises
		// keeps working for them (review finding on ADR 0017's logout story).
		prefix := fmt.Sprintf("byre-machine-u%d-", os.Getuid())
		engineVols, err := r.VolumesByPrefix(prefix)
		if err != nil {
			return nil, err
		}
		for _, ev := range engineVols {
			name := strings.TrimPrefix(ev, prefix)
			if !declared[name] {
				out = append(out, configui.VolumeStatus{Name: name, Exists: true, Machine: true, Orphan: true, Engine: eng})
			}
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
	r := a.runnerFor(v.Engine)
	// The lock file lives in the project store, which an unrecorded project
	// doesn't have yet (e.g. clearing an orphaned machine volume from a
	// never-developed project) — enroll before locking; a clear is a
	// mutation, so that's fair even if the clear is then refused (the lock
	// can't exist without the store).
	if a.prepare != nil {
		if err := a.prepare(); err != nil {
			return err
		}
	}
	// io.Discard: this runs inside the TUI; a waiting note would corrupt the screen.
	return withSetupLock(io.Discard, a.paths.LockFile, func() error {
		if live, err := liveSession(r, a.paths.ID); err != nil {
			return fmt.Errorf("checking for a running session: %w", err)
		} else if len(live) > 0 {
			return fmt.Errorf("a session is running (%s) — exit it before clearing volumes", shortID(live[0]))
		}
		if v.Machine {
			// A machine-scoped volume is mounted by EVERY project's boxes, so
			// the this-project guard above isn't enough: refuse while ANY byre
			// session runs (bare label key = presence filter). Clearing it is
			// the machine-wide logout story (ADR 0017).
			if live, lerr := r.RunningContainersByLabel(labelKey); lerr != nil {
				return fmt.Errorf("checking for running byre sessions: %w", lerr)
			} else if len(live) > 0 {
				return fmt.Errorf("this volume is shared by ALL your projects and a byre session is running (%s) — exit every session before clearing it", shortID(live[0]))
			}
			return r.VolumeRemove(machineVolumeName(os.Getuid(), v.Name))
		}
		return r.VolumeRemove(volumeName(a.paths.ID, v.Name))
	})
}

// runnerFor maps a row's engine label back to its runner; rows always carry
// the engine they were listed from.
func (a *volumeAdmin) runnerFor(engine string) engineRunner {
	for _, r := range a.rs {
		if string(r.Engine()) == engine {
			return r
		}
	}
	return a.rs[0]
}
