package commands

// `byre mcp` — sugar over the [[mcp]] config vocabulary (ADR 0033). add and
// remove edit ONE cascade layer (the project store config, or with global
// the machine default.config) through the same parse/validate/atomic-write
// path as the interactive editor; list renders the EFFECTIVE set through
// status's own renderers so the two surfaces cannot drift. All host-side:
// nothing here touches the project tree.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// mcpVerbs plugs the [[mcp]] vocabulary into the shared layer-edit lifecycle
// (nameddecl.go).
var mcpVerbs = declVerbs[config.MCP]{
	kind:   "mcp",
	name:   func(m config.MCP) string { return m.Name },
	marker: func(name string) config.MCP { return config.MCP{Name: name} },
	list:   func(c *config.Config) *[]config.MCP { return &c.MCPs },
	effectiveHas: func(effective config.Config, res skills.Resolved, name string) (bool, error) {
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
	},
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
	if err := addNamedDecl(s, projectDir, global, mcpVerbs, name, m); err != nil {
		return err
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

// MCPRemove implements `byre mcp remove <name>` — the shared closure-smart
// contract (see removeNamedDecl for the full taxonomy and ruling trail).
func MCPRemove(s Streams, projectDir string, global bool, name string) error {
	name = strings.TrimPrefix(name, "!") // tolerate a pasted closure spelling
	if !config.ValidMCPName(name) {
		return fmt.Errorf("mcp remove: %q is not a valid server name", name)
	}
	return removeNamedDecl(s, projectDir, global, mcpVerbs, name)
}

// MCPList implements `byre mcp list`: the effective declared set, rendered
// by the SAME functions status uses (mcpStatusLine/mcpDeliveryLine), so
// this view can never tell a different story.
func MCPList(s Streams, projectDir string) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Read-only, but collision-checked like status: never render another
	// project's declared set as this one's.
	if err := paths.ValidateExisting(); err != nil {
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
	// Error structurally nil: empty Resolved + config.Load already refused
	// config-internal duplicate names (see the same call in status.go).
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
