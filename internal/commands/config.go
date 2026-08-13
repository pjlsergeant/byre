package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
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
	// A full-screen editor with no terminal has nothing to draw on and no
	// way to take a keystroke; refusing here beats bubbletea's own failure,
	// and beats a headless run appearing to succeed. P6 makes the editor the
	// interface, so the refusal owes the script a route: the declaration
	// verbs, and the file itself (P1's defended right).
	if !s.TTY {
		return fmt.Errorf("byre config is an interactive editor -- run it on a TTY. From a script: `byre mcp`, `byre context` and `byre claude-skill` add and remove those declarations, and the config file is plain TOML you can edit directly (`byre status` names the one this project reads)")
	}
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
	// Stubs (description-only compatibility shells) are not OFFERED: a
	// picker has nothing to enable.
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
		if _, err := hostopen.PlainStat(config.LayerPath(home, layer), hostopen.StoreOwned); err != nil {
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
		if def, derr := config.ParseFile(filepath.Join(home, "default.config"), true); derr == nil {
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
				Mounts:        sk.File.Runtime.Mounts,
				Volumes:       sk.File.Volumes,
				Env:           sk.File.Runtime.Env,
				Files:         sk.File.Build.Files,
				EnvDocs:       sk.File.Runtime.EnvDocs,
				Egress:        sk.File.Runtime.Egress,
				Offered:       sk.File.Runtime.EgressOffered,
				MCPs:          sk.File.MCPs,
				ClaudeSkills:  sk.File.ClaudeSkills,
				Posture:       sk.File.Runtime.NetworkPosture,
				Containment:   sk.File.Runtime.Containment,
				CompanionFor:  sk.File.CompanionAgent(),
				SharedAuthFor: sk.File.SharedAuthFor,
				Provenance:    "",
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
	// The editor's ^e handoff execs a shell; a project edit hands it that
	// project's box-writable roots, and --global/--layer (which belong to no
	// project) hand it nothing to decline.
	var editorRoots hostexec.Roots
	var vols configui.VolumeAdmin // nil for --global and --layer (no project volumes)
	var prepare func() error      // deferred store setup, run by the UI before its first write
	var lockFile string           // the lock guarding THIS file ("" = no shared contender)
	// livePaths is the project whose session liveness qualifies the editor's
	// exposure headline and its save report. Zero for --global/--layer: those
	// files belong to no project, so there is no box for them to be about.
	var livePaths project.Paths
	var liveProject bool
	switch target {
	case configui.TargetGlobal:
		path = filepath.Join(home, "default.config")
		// The title names the REAL file, tilde-abbreviated only when it
		// truly lives under the user's home: a hardcoded "~/.byre" under a
		// BYRE_HOME override put the wrong path in the title, five lines
		// above a footer showing the right one (store notices already follow
		// this rule; the QA playbook pins it for them).
		title = "byre global config  (" + packages.DisplayPath(path) + ")"
		// Not a store — no enrollment semantics — but AtomicWrite no longer
		// creates directories, and quitting an unsaved editor should leave no
		// fresh ~/.byre behind either: create home only when a write lands.
		prepare = func() error { return hostopen.PlainMkdirAll(home, 0o755, hostopen.StoreOwned) }
	case configui.TargetLayer:
		path = config.LayerPath(home, layer)
		title = "byre layer config  (" + layer + ")"
		layerDir := filepath.Dir(path)
		prepare = func() error { return hostopen.PlainMkdirAll(layerDir, 0o755, hostopen.StoreOwned) }
		// The LAYER's own lock, not any project's: a layer file is reachable
		// from every project extending it, and `byre credentials set --layer`
		// compare-and-swaps it under this same lock. Without it an editor save
		// and a credential write cross — one silently reconciles the other
		// away, and a credential write that minted the file's identity would
		// leave the identity and its rows in different generations, which is
		// exactly what the compare-and-swap exists to prevent.
		// prepare (the layer dir's MkdirAll) runs before the first write, so
		// the lock always has a directory to live in.
		lockFile = config.LayerLockPath(home, layer)
	default:
		paths, perr := project.Resolve(projectDir)
		if perr != nil {
			return perr
		}
		// Canonical, not WorkDir: planFiles stages sources against Canonical,
		// and the editor's missing-source note must ask the same tree the
		// build will.
		inh.ProjectDir = paths.Canonical
		editorRoots = boxWritableRoots(paths)
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
		lockFile = paths.LockFile
		livePaths, liveProject = paths, true
		path = filepath.Join(paths.Dir, config.ProjectConfigName)
		title = "byre project config  (" + paths.ID + ")"
		vols = newVolumeAdmin(s.Err, paths, projectDir, prepare) // nil if the engine/config won't resolve
	}

	cur, err := config.ParseFile(path, target != configui.TargetProject)
	if err != nil {
		return err
	}
	// The editor saves in place (explicit ctrl+s), so Run reports whether the file
	// was written rather than handing back a config for us to save.
	// worktree_base is a host workflow preference edited in the GLOBAL config; the
	// project editor omits it (showing it there would imply a per-project unset
	// that the cascade can't honor once a global default exists).
	// Writes ride the lock that guards the FILE: the project store's setup
	// lock (worktree sessions share one store, so two editors saving at once
	// is an ordinary shape), or the layer's own lock (every project extending
	// the layer writes that one file, and `byre credentials --layer` takes the
	// same lock). --global has no shared contender and passes no guard -- the
	// drift check still applies, unserialized.
	var guard func(func() error) error
	if lockFile != "" {
		guard = func(write func() error) error { return withSetupLock(s.Err, lockFile, write) }
	}
	// The editor's exposure headline keeps NEXT-LAUNCH semantics throughout --
	// it describes the config being edited. This qualifier LABELS that, it does
	// not re-scope it: with a box already running, "next launch" is a real
	// later event rather than the thing about to happen. Probed once at open,
	// and staleness is harmless in the one direction that matters -- if the box
	// exits mid-session the "changes apply at next launch" half stays true.
	var liveNote string
	if liveProject && boxRunningForEdit(livePaths, cur) {
		liveNote = EditorLiveBoxNote
	}
	saved, err := configui.Run(title, path, cur, templates, agents, skillOpts, skillDescs, inh, vols, target, prepare, guard, editorRoots, liveNote)
	if err != nil {
		return err
	}
	if !saved {
		fmt.Fprintln(s.Err, "byre: config unchanged.")
		return nil
	}
	// Re-probed rather than reused: this line is fresh at the moment it is
	// actionable -- you just changed a grant and it has not taken effect yet --
	// and a session that started or ended during the edit would make the
	// open-time answer wrong here. An unreachable engine degrades to the plain
	// message, silently.
	if liveProject && boxRunningForEdit(livePaths, cur) {
		fmt.Fprintf(s.Err, "byre: wrote %s — %s\n", path, EditorSaveLiveClause)
		return nil
	}
	fmt.Fprintf(s.Err, "byre: wrote %s\n", path)
	return nil
}

// EditorLiveBoxNote qualifies the config editor's exposure headline while a
// box is running, and EditorSaveLiveClause qualifies the save report. Exported
// because two packages print them and the tests assert their presence rather
// than their wording -- so the sentences change in one place.
const (
	EditorLiveBoxNote    = "box running -- changes apply at next launch"
	EditorSaveLiveClause = "a box is running; changes apply at the next develop."
)

// boxRunningForEdit probes whether a session is live for this worktree.
//
// An UNSOLICITED probe, held to status's own discipline: no engine, a declined
// binary, a daemon that will not answer -- every failure degrades to false and
// says nothing, because the user asked to edit a config, not to hear about
// their engine. The claim it feeds is purely additive (a note appears), so the
// worst a degraded probe costs is a note that would have been useful.
func boxRunningForEdit(paths project.Paths, cfg config.Config) bool {
	eng, exe, err := runner.Detect(cfg.Engine, hostexec.Looker(boxWritableRoots(paths)))
	if err != nil {
		return false
	}
	ids, err := runner.New(eng, exe).RunningContainersByLabel(workdirLabel(paths))
	return err == nil && len(ids) > 0
}

// volumeAdmin is the engine-backed configui.VolumeAdmin for a project: it lists
// the resolved volume set (config + skills) with on-disk presence, and clears a
// volume. It mirrors `byre reset`, scoped to one volume — including reset's
// every-installed-engine stance: this screen is the advertised deliberate-
// delete route for machine volumes, and "logged out everywhere" would be a
// lie if a same-named volume survived on the engine the config doesn't name
// (the lifecycle-batch bug class).
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
//
// w takes the one case where a missing section needs a reason: an engine byre
// found and DECLINED to run. "No engine installed" explains itself and the
// row's absence is unsurprising; a section that vanishes because a binary is
// shadowed, or sits behind a relative PATH entry, would otherwise be a silent
// product hole (P0 -- the screen is the product).
func newVolumeAdmin(w io.Writer, paths project.Paths, projectDir string, prepare func() error) configui.VolumeAdmin {
	if _, err := resolve(paths, projectDir, nil); err != nil {
		return nil
	}
	rs, err := lifecycleEngines(boxWritableRoots(paths))
	if err != nil {
		var declined declinedEngine
		if errors.As(err, &declined) {
			fmt.Fprintf(w, "byre: %v The editor's Volumes section is hidden — byre can't list or clear volumes it can't reach.\n", declined)
		}
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
func (a *volumeAdmin) List() ([]configui.VolumeStatus, []string, error) {
	rv, err := resolve(a.paths, a.projectDir, nil)
	if err != nil {
		return nil, nil, err
	}
	defs := dedupeVolumes(rv.volumes)
	var out []configui.VolumeStatus
	var notes []string
	// One pass per installed engine: a volume (declared or orphaned) can
	// exist on both docker and podman, and each copy is its own row — the
	// delete route must show and clear every copy, or "removed" is false.
	//
	// Per-engine degrade (live field report 2026-07-17): an engine that
	// can't be queried — the canonical case is podman installed with its
	// machine stopped — must not kill the whole view. Its copies become a
	// loud note (the claim narrows honestly: not shown, not clearable this
	// session) while the other engine's rows list normally. Partial rows
	// from a mid-sweep failure are discarded with the note — half an
	// engine's truth is worse than a disclosed absence.
	for _, r := range a.rs {
		eng := string(r.Engine())
		var engRows []configui.VolumeStatus
		engFail := func(err error) {
			notes = append(notes, fmt.Sprintf("%s unreachable — its volume copies aren't shown and can't be cleared here (%s)", eng, firstLine(err.Error())))
		}
		declared := map[string]bool{}
		ok := true
		for _, v := range defs {
			exists, verr := r.VolumeExists(scopedVolumeName(a.paths.ID, os.Getuid(), v))
			if verr != nil {
				engFail(verr)
				ok = false
				break
			}
			if v.MachineScoped() {
				declared[v.Name] = true
			}
			engRows = append(engRows, configui.VolumeStatus{Name: v.Name, Role: v.Role, Target: v.Target, Exists: exists, Machine: v.MachineScoped(), Engine: eng})
		}
		if !ok {
			continue
		}
		// Orphaned machine-scoped volumes: present on the engine but no longer
		// declared by any enabled skill/config (e.g. shared-auth disabled after a
		// login). Listed so the deliberate-delete route reset/forget advertises
		// keeps working for them (ADR 0017's logout story).
		prefix := fmt.Sprintf("byre-machine-u%d-", os.Getuid())
		engineVols, verr := r.VolumesByPrefix(prefix)
		if verr != nil {
			engFail(verr)
			continue
		}
		for _, ev := range engineVols {
			name := strings.TrimPrefix(ev, prefix)
			if !declared[name] {
				engRows = append(engRows, configui.VolumeStatus{Name: name, Exists: true, Machine: true, Orphan: true, Engine: eng})
			}
		}
		out = append(out, engRows...)
	}
	return out, notes, nil
}

// firstLine trims a multi-line engine error to its first line — podman's
// cannot-connect message is four lines of remediation prose that would
// swallow the volumes screen.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
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
