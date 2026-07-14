package commands

// `byre mcp` — sugar over the [[mcp]] config vocabulary (ADR 0033). add and
// remove edit ONE cascade layer (the project store config, or with global
// the machine default.config) through the same parse/validate/atomic-write
// path as the interactive editor; list renders the EFFECTIVE set through
// status's own renderers so the two surfaces cannot drift. All host-side:
// nothing here touches the project tree.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// mcpLayerPath resolves which cascade layer file the verb edits and a short
// human name for it.
func mcpLayerPath(projectDir string, global bool) (path, label string, err error) {
	home, err := project.Home()
	if err != nil {
		return "", "", err
	}
	if global {
		return filepath.Join(home, "default.config"), "global config", nil
	}
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return "", "", err
	}
	if err := paths.Bootstrap(); err != nil {
		return "", "", err
	}
	return filepath.Join(paths.Dir, config.ProjectConfigName), "project config", nil
}

// MCPAdd implements `byre mcp add <name> (<url> | -- <command...>)`:
// add-or-update the declaration in the target layer (the agent CLIs' own
// add verbs update in place; users expect the same), re-opening a matching
// `!name` closure if one was present.
func MCPAdd(s Streams, projectDir string, global bool, name string, rest, env, egress []string) error {
	m := config.MCP{Name: name, Env: env, Egress: egress}
	switch {
	case len(rest) == 0:
		// The cobra layer enforces arity; kept for direct callers.
		return fmt.Errorf("mcp add: need a url or a command (put a local command after --)")
	case len(rest) == 1 && (strings.HasPrefix(rest[0], "https://") || strings.HasPrefix(rest[0], "http://")):
		m.URL = rest[0]
	default:
		m.Command = rest
	}
	if err := config.ValidateMCP(m); err != nil {
		return err
	}

	path, label, err := mcpLayerPath(projectDir, global)
	if err != nil {
		return err
	}
	cur, err := config.ParseFile(path)
	if err != nil {
		return err
	}

	reopened := false
	kept := cur.MCPs[:0:0]
	replaced := false
	for _, e := range cur.MCPs {
		if e.Name == "!"+name {
			reopened = true // a closure on this name is superseded by the add
			continue
		}
		if e.Name == name {
			kept = append(kept, m)
			replaced = true
			continue
		}
		kept = append(kept, e)
	}
	if !replaced {
		kept = append(kept, m)
	}
	cur.MCPs = kept
	if err := configui.Save(path, cur); err != nil {
		return err
	}

	verb := "added"
	if replaced {
		verb = "updated"
	}
	fmt.Fprintf(s.Err, "byre: %s mcp %s in the %s (%s)\n", verb, name, label, path)
	if reopened {
		fmt.Fprintf(s.Err, "byre: the layer's \"!%s\" closure was removed — the add re-opens it\n", name)
	}
	if host, port, ok := m.Endpoint(); ok {
		fmt.Fprintf(s.Err, "byre: remote url implies egress to %s:%d (attributed mcp:%s; closable with \"!%s:%d\" in egress)\n", host, port, name, host, port)
	}
	fmt.Fprintln(s.Err, "byre: `byre status` shows the effective set and delivery; applies on the next develop.")
	return nil
}

// MCPRemove implements `byre mcp remove <name>` — closure-smart:
//   - declared in the target layer → delete the block; if the name is STILL
//     effective afterwards (a lower layer or an enabled skill declares it),
//     also write the `!name` closure, or the delete would just resurrect
//     the inherited one.
//   - not in the layer but effective → write the closure.
//   - nowhere → error.
//
// The still-effective check only runs for the project layer (the cascade
// below it is knowable); a global remove that deletes an entry can't see
// every project's skills, so it reports what it did and leaves closures to
// an explicit `byre mcp remove` in the affected project (or a hand edit).
func MCPRemove(s Streams, projectDir string, global bool, name string) error {
	name = strings.TrimPrefix(name, "!") // tolerate a pasted closure spelling
	if !config.ValidMCPName(name) {
		return fmt.Errorf("mcp remove: %q is not a valid server name", name)
	}

	path, label, err := mcpLayerPath(projectDir, global)
	if err != nil {
		return err
	}
	cur, err := config.ParseFile(path)
	if err != nil {
		return err
	}

	hadEntry, hadClosure := false, false
	kept := cur.MCPs[:0:0]
	for _, e := range cur.MCPs {
		switch e.Name {
		case name:
			hadEntry = true
		case "!" + name:
			hadClosure = true
			kept = append(kept, e) // an existing closure stays
		default:
			kept = append(kept, e)
		}
	}
	cur.MCPs = kept

	// Would the name still be effective without a closure? (project only)
	stillEffective := false
	if !global {
		if home, herr := project.Home(); herr == nil {
			if cat, cerr := builtins.LoadCatalogRaw(home); cerr == nil && cat != nil {
				if effective, rerr := config.ResolveProposed(cur); rerr == nil {
					if res, serr := skills.Resolve(effective, cat); serr == nil {
						if set, merr := skills.MCPSet(effective, res); merr == nil {
							for _, d := range set {
								if d.MCP.Name == name {
									stillEffective = true
								}
							}
						}
					}
				}
			}
		}
	}

	wroteClosure := false
	if stillEffective && !hadClosure {
		cur.MCPs = append(cur.MCPs, config.MCP{Name: "!" + name})
		wroteClosure = true
	}
	if !hadEntry && !wroteClosure {
		if hadClosure {
			fmt.Fprintf(s.Err, "byre: mcp %s is already closed in the %s — nothing to do\n", name, label)
			return nil
		}
		return fmt.Errorf("mcp %s: not declared in the %s and not effective from below — nothing to remove", name, label)
	}
	if err := configui.Save(path, cur); err != nil {
		return err
	}

	switch {
	case hadEntry && wroteClosure:
		fmt.Fprintf(s.Err, "byre: removed mcp %s from the %s AND closed the name (\"!%s\") — a lower layer or skill still declares it\n", name, label, name)
	case hadEntry:
		fmt.Fprintf(s.Err, "byre: removed mcp %s from the %s (%s)\n", name, label, path)
	default:
		fmt.Fprintf(s.Err, "byre: closed mcp %s in the %s (\"!%s\") — it was declared by a lower layer or skill\n", name, label, name)
	}
	fmt.Fprintln(s.Err, "byre: applies on the next develop.")
	return nil
}

// MCPList implements `byre mcp list`: the effective declared set, rendered
// by the SAME functions status uses (mcpStatusLine/mcpDeliveryLine), so
// this view can never tell a different story.
func MCPList(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}
	info := statusInfo{
		Agent:        cfg.Agent,
		EgressClosed: cfg.EgressClosed,
		MCPClosed:    cfg.MCPClosed,
		EnvProvided:  map[string]bool{},
	}
	info.MCPs, _ = skills.MCPSet(cfg, skills.Resolved{})
	for k := range cfg.Env {
		info.EnvProvided[k] = true
	}
	for k, src := range cfg.EnvFromHost {
		if src != "" {
			info.EnvProvided[k] = true
		}
	}
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
			info.MCPs = rv.mcps
			info.NetPosture, info.NetPostureSkill = res.NetworkPosture()
			if res.Agent != nil {
				info.AgentMCP = res.Agent.MCP
			}
			for k := range res.Env() {
				info.EnvProvided[k] = true
			}
		}
	}

	if len(info.MCPs) == 0 && len(info.MCPClosed) == 0 {
		fmt.Fprintln(s.Out, "no MCP servers declared  (add one: byre mcp add <name> <url|-- command...>)")
		return nil
	}
	for _, d := range info.MCPs {
		fmt.Fprintln(s.Out, mcpStatusLine(d, info))
	}
	if len(info.MCPs) > 0 {
		fmt.Fprintln(s.Out, mcpDeliveryLine(info))
	}
	for _, c := range info.MCPClosed {
		fmt.Fprintf(s.Out, "!%s  (config — removed from the declared set)\n", c)
	}
	return nil
}
