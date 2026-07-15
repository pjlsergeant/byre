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
	"regexp"
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

// mcpBearerNameRe validates a --bearer env-var name at the CLI edge (a bad
// name would otherwise become a never-expanding literal template).
var mcpBearerNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// MCPAdd implements `byre mcp add <name> (<url> | -- <command...>)`:
// add-or-update the declaration in the target layer (the agent CLIs' own
// add verbs update in place; users expect the same), re-opening a matching
// `!name` closure if one was present. headers are "Name: value" pairs;
// bearer is sugar for the dominant one (Authorization = "Bearer ${NAME}").
func MCPAdd(s Streams, projectDir string, global bool, name string, rest, env, egress, headers []string, bearer string) error {
	m := config.MCP{Name: name, Env: env, Egress: egress}
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return fmt.Errorf("mcp add: --header wants \"Name: value\", got %q", h)
		}
		if m.Headers == nil {
			m.Headers = map[string]string{}
		}
		m.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if bearer != "" {
		if !mcpBearerNameRe.MatchString(bearer) {
			return fmt.Errorf("mcp add: --bearer wants an env var NAME (the token rides the box env), got %q", bearer)
		}
		if m.Headers == nil {
			m.Headers = map[string]string{}
		}
		m.Headers["Authorization"] = "Bearer ${" + bearer + "}"
	}
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
	if refs := m.HeaderEnvRefs(); len(refs) > 0 {
		fmt.Fprintf(s.Err, "byre: header ${...} refs expand at launch from the box env — provide %s via env_from_host or [env]\n", strings.Join(refs, ", "))
	}
	// The declaration bakes into the image (docker history shows it), like
	// [env] values — the disclosure keeps argv/query secrets a known edge,
	// never a surprise. Tokens ride env NAMES + env_from_host instead.
	fmt.Fprintf(s.Err, "byre: the declaration bakes into the image — keep secrets out of the %s; tokens ride --env names + env_from_host\n",
		map[bool]string{true: "url", false: "command"}[m.Remote()])
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
//   - the still-effective check UNRESOLVABLE (a broken skill, a bad
//     template) → write the closure regardless and say why: the closure
//     guarantees the verb's promise (gone from the effective set) whatever
//     byre couldn't check, at the cost of a possibly-inert marker the user
//     can see and delete. Never a refusal, never a silent resurrection
//     (maintainer ruling 2026-07-15, revising the round-4 refusal).
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

	// Would the name still be effective without a closure? (project only.)
	// An UNRESOLVABLE check (broken skill, bad template) neither refuses nor
	// silently proceeds: the closure is written regardless — it guarantees
	// the verb's promise against whatever couldn't be checked, and an inert
	// marker is visible and cheap to delete. (Codex round 4 found the
	// silent-proceed path; the refusal that replaced it was revised to this
	// by maintainer ruling 2026-07-15 — the user asked for a removal, not a
	// lecture.)
	stillEffective := false
	var checkErr error
	if !global {
		stillEffective, checkErr = mcpStillEffective(cur, name)
	}

	wroteClosure := false
	if (stillEffective || checkErr != nil) && !hadClosure {
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
	case hadEntry && wroteClosure && checkErr == nil:
		fmt.Fprintf(s.Err, "byre: removed mcp %s from the %s AND closed the name (\"!%s\") — a lower layer or skill still declares it\n", name, label, name)
	case hadEntry && wroteClosure:
		fmt.Fprintf(s.Err, "byre: removed mcp %s from the %s AND closed the name (\"!%s\")\n", name, label, name)
	case hadEntry:
		fmt.Fprintf(s.Err, "byre: removed mcp %s from the %s (%s)\n", name, label, path)
	case checkErr != nil:
		fmt.Fprintf(s.Err, "byre: closed mcp %s in the %s (\"!%s\")\n", name, label, name)
	default:
		fmt.Fprintf(s.Err, "byre: closed mcp %s in the %s (\"!%s\") — it was declared by a lower layer or skill\n", name, label, name)
	}
	if checkErr != nil {
		fmt.Fprintf(s.Err, "byre: couldn't verify lower layers/skills (%v) — the closure guarantees the removal either way; it's inert if nothing else declares %s (delete it in `byre config` if so)\n", checkErr, name)
	}
	fmt.Fprintln(s.Err, "byre: applies on the next develop.")
	return nil
}

// mcpStillEffective reports whether name survives in the effective MCP set
// with `cur` as the project layer (post tentative edit). An unresolvable
// check returns its error; the caller writes the guaranteeing closure and
// disclosure, never a refusal or a silent false.
func mcpStillEffective(cur config.Config, name string) (bool, error) {
	home, err := project.Home()
	if err != nil {
		return false, err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return false, err
	}
	if cat == nil {
		return false, fmt.Errorf("catalog unavailable")
	}
	effective, err := config.ResolveProposed(cur)
	if err != nil {
		return false, err
	}
	res, err := skills.Resolve(effective, cat)
	if err != nil {
		return false, err
	}
	set, err := skills.MCPSet(effective, res)
	if err != nil {
		return false, err
	}
	for _, d := range set {
		if d.MCP.Name == name {
			return true, nil
		}
	}
	return false, nil
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
