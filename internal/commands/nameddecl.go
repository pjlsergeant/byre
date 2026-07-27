package commands

// The named-declaration genus's layer-edit lifecycle, shared by the `byre
// mcp`, `byre claude-skill`, and `byre context` verbs: each edits ONE cascade layer (the
// project store config, or with global the machine default.config) through
// the same parse/validate/atomic-write path as the interactive editor, with
// the same closure-smart remove contract. Each verb keeps its own CLI-edge
// parsing, per-declaration validation, and post-write guidance; the
// lifecycle — layer resolution, reopen/replace, the still-effective check,
// closure writing, and the shared messages — lives here, once.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// declVerbs adapts one named-declaration vocabulary to the shared lifecycle.
type declVerbs[T any] struct {
	kind   string // message noun: "mcp", "claude skill"
	name   func(T) string
	marker func(name string) T
	// list addresses the vocabulary's declaration slice inside a parsed layer.
	list func(*config.Config) *[]T
	// effectiveHas reports whether name survives in the vocabulary's
	// effective set for a resolved config + skill resolution.
	effectiveHas func(effective config.Config, res skills.Resolved, name string) (bool, error)
}

// declLayerPath resolves which cascade layer file a verb edits, a short human
// name for it, and the deferred target setup, run only when a write lands:
// for a project layer that's the enrolling Bootstrap (the id-collision check
// still fails loudly up front, and a no-op remove on a never-seen project
// leaves no ~/.byre/projects/<id>); for the global layer it's a plain
// MkdirAll of home.
func declLayerPath(projectDir string, global bool) (path, label string, follow bool, prepare func() error, err error) {
	home, err := project.Home()
	if err != nil {
		return "", "", false, nil, err
	}
	if global {
		// Not a store — no enrollment semantics — but AtomicWrite no longer
		// creates directories, so a fresh machine's first `--global` verb
		// must be able to create ~/.byre itself when its write lands.
		// follow=true: default.config is host-owned (never inside any box
		// mount) and a dotfiles symlink there is the user's own arrangement.
		return filepath.Join(home, "default.config"), "global config", true,
			func() error { return os.MkdirAll(home, 0o755) }, nil
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return "", "", false, nil, err
	}
	if err := paths.ValidateExisting(); err != nil {
		return "", "", false, nil, err
	}
	// follow=false: the store project dir is what --self-edit mounts.
	return filepath.Join(paths.Dir, config.ProjectConfigName), "project config", false, paths.Bootstrap, nil
}

// saveDeclLayer validates and writes an edited layer. Validate runs before
// the enrolling prepare (mirrors the config editor's ordering): a save the
// validator would refuse must not enroll. Save re-runs the same check on the
// way to disk; the duplication buys the ordering.
func saveDeclLayer(path string, follow bool, cur config.Config, prepare func() error) error {
	if err := cur.ValidateLayer(); err != nil {
		return err
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
	}
	// force: this is a single-shot read-modify-write from the CLI verb, not
	// an interactive session with a long window -- there is no open-time
	// baseline to compare against, and the behavior is unchanged from before
	// the editor gained drift detection.
	return configui.Save(path, follow, cur, nil, nil, true)
}

// addNamedDecl add-or-updates a declaration in the target layer (the agent
// CLIs' own add verbs update in place; users expect the same), re-opening a
// matching `!name` closure, and prints the shared outcome messages. The
// caller prints any vocabulary-specific guidance after.
func addNamedDecl[T any](s Streams, projectDir string, global bool, v declVerbs[T], name string, decl T) error {
	path, label, follow, prepare, err := declLayerPath(projectDir, global)
	if err != nil {
		return err
	}
	cur, err := config.ParseFile(path, follow)
	if err != nil {
		return err
	}

	list := v.list(&cur)
	reopened := false
	kept := (*list)[:0:0]
	replaced := false
	for _, e := range *list {
		switch v.name(e) {
		case "!" + name:
			reopened = true // a closure on this name is superseded by the add
			continue
		case name:
			kept = append(kept, decl)
			replaced = true
			continue
		}
		kept = append(kept, e)
	}
	if !replaced {
		kept = append(kept, decl)
	}
	*list = kept
	if err := saveDeclLayer(path, follow, cur, prepare); err != nil {
		return err
	}

	verb := "added"
	if replaced {
		verb = "updated"
	}
	fmt.Fprintf(s.Err, "byre: %s %s %s in the %s (%s)\n", verb, v.kind, name, label, path)
	if reopened {
		fmt.Fprintf(s.Err, "byre: the layer's \"!%s\" closure was removed — the add re-opens it\n", name)
	}
	return nil
}

// removeNamedDecl is the closure-smart remove shared by the genus's verbs:
//   - declared in the target layer → delete the block; if the name is STILL
//     effective afterwards (a lower layer or an enabled skill declares it),
//     also write the `!name` closure, or the delete would just resurrect
//     the inherited one.
//   - not in the layer but effective → write the closure.
//   - nowhere → error.
//   - the still-effective check UNRESOLVABLE (a broken skill, a bad
//     template) → write the closure regardless and say why: the closure
//     guarantees the verb's promise (gone from the effective set) whatever
//     byre couldn't check, at the cost of a possibly-inert marker the user
//     can see and delete. Never a refusal, never a silent resurrection.
//
// The still-effective check only runs for the project layer (the cascade
// below it is knowable); a global remove that deletes an entry can't see
// every project's skills, so it reports what it did and leaves closures to
// an explicit remove in the affected project (or a hand edit).
func removeNamedDecl[T any](s Streams, projectDir string, global bool, v declVerbs[T], name string) error {
	path, label, follow, prepare, err := declLayerPath(projectDir, global)
	if err != nil {
		return err
	}
	cur, err := config.ParseFile(path, follow)
	if err != nil {
		return err
	}

	list := v.list(&cur)
	hadEntry, hadClosure := false, false
	kept := (*list)[:0:0]
	for _, e := range *list {
		switch v.name(e) {
		case name:
			hadEntry = true
		case "!" + name:
			hadClosure = true
			kept = append(kept, e) // an existing closure stays
		default:
			kept = append(kept, e)
		}
	}
	*list = kept

	stillEffective := false
	var checkErr error
	if !global {
		stillEffective, checkErr = declStillEffective(cur, v, name)
	}

	wroteClosure := false
	if (stillEffective || checkErr != nil) && !hadClosure {
		*list = append(*list, v.marker("!"+name))
		wroteClosure = true
	}
	if !hadEntry && !wroteClosure {
		if hadClosure {
			fmt.Fprintf(s.Err, "byre: %s %s is already closed in the %s — nothing to do\n", v.kind, name, label)
			return nil
		}
		return fmt.Errorf("%s %s: not declared in the %s and not effective from below — nothing to remove", v.kind, name, label)
	}
	if err := saveDeclLayer(path, follow, cur, prepare); err != nil {
		return err
	}

	switch {
	case hadEntry && wroteClosure && checkErr == nil:
		fmt.Fprintf(s.Err, "byre: removed %s %s from the %s AND closed the name (\"!%s\") — a lower layer or skill still declares it\n", v.kind, name, label, name)
	case hadEntry && wroteClosure:
		fmt.Fprintf(s.Err, "byre: removed %s %s from the %s AND closed the name (\"!%s\")\n", v.kind, name, label, name)
	case hadEntry:
		fmt.Fprintf(s.Err, "byre: removed %s %s from the %s (%s)\n", v.kind, name, label, path)
	case checkErr != nil:
		fmt.Fprintf(s.Err, "byre: closed %s %s in the %s (\"!%s\")\n", v.kind, name, label, name)
	default:
		fmt.Fprintf(s.Err, "byre: closed %s %s in the %s (\"!%s\") — it was declared by a lower layer or skill\n", v.kind, name, label, name)
	}
	if checkErr != nil {
		fmt.Fprintf(s.Err, "byre: couldn't verify lower layers/skills (%v) — the closure guarantees the removal either way; it's inert if nothing else declares %s (delete it in `byre config` if so)\n", checkErr, name)
	}
	fmt.Fprintln(s.Err, "byre: applies on the next develop.")
	return nil
}

// declStillEffective reports whether name survives in the vocabulary's
// effective set with `cur` as the project layer (post tentative edit). An
// unresolvable check returns its error; the caller writes the guaranteeing
// closure and disclosure, never a refusal or a silent false.
//
// The catalog -> ResolveProposed -> skills.Resolve orchestration is inlined
// here AND at review/preset sites with deliberately different error
// handling: this site must guarantee a closure, review degrades to
// best-effort. Consolidate only when the policies converge -- extraction
// waits for a second consumer with the SAME policy, not a lookalike.
func declStillEffective[T any](cur config.Config, v declVerbs[T], name string) (bool, error) {
	home, err := project.Home()
	if err != nil {
		return false, err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return false, err
	}
	effective, err := config.ResolveProposed(cur)
	if err != nil {
		return false, err
	}
	res, err := skills.Resolve(effective, cat)
	if err != nil {
		return false, err
	}
	return v.effectiveHas(effective, res, name)
}

// listDeclInfo is the shared prelude of the `mcp list` / `claude-skill list`
// renderers: resolve the project (collision-checked like status -- never
// render another project's declared set as this one's), load the config, and
// resolve skills, every failure past config-load DEGRADING into
// info.SkillErr rather than erroring -- the same posture status takes.
// fromCfg seeds the config-only view (used verbatim when skills can't
// resolve); fromResolved overlays the full effective view.
func listDeclInfo(s Streams, projectDir string, fromCfg func(*statusInfo, config.Config), fromResolved func(*statusInfo, resolved, skills.Resolved)) (statusInfo, error) {
	info := statusInfo{}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return info, err
	}
	if err := paths.ValidateExisting(); err != nil {
		return info, err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return info, err
	}
	info.Agent = cfg.Agent
	fromCfg(&info, cfg)
	if serr := builtins.EnsureStoreOut(paths.Home, s.Err); serr != nil {
		info.SkillErr = serr.Error()
	} else if cat, _ := builtins.LoadCatalogRaw(paths.Home); cat == nil {
		info.SkillErr = "catalog unavailable"
	} else if res, rerr := skills.Resolve(cfg, cat); rerr != nil {
		info.SkillErr = rerr.Error()
	} else {
		rv := combine(cfg, res)
		if verr := rv.validate(); verr != nil {
			info.SkillErr = verr.Error()
		} else {
			fromResolved(&info, rv, res)
		}
	}
	return info, nil
}
