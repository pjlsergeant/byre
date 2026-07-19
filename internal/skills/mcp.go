package skills

// The effective MCP set: config-declared [[mcp]] blocks (post-cascade) plus
// every enabled skill's contributions, with the config's `!name` closures
// subtracting LAST — after the skill union — which is what puts a
// skill-declared server in a closure's reach (ADR 0030 semantics, adopted
// wholesale). One owner: gen/build bake this set into /etc/byre/mcp.json,
// status renders it, and the implied-egress derivation reads it — all
// through MCPSet, so the surfaces can't drift from what the box gets.

import (
	"github.com/pjlsergeant/byre/internal/config"
)

// MCPFromConfig is the Skill attribution for MCP servers declared by the
// config's own [[mcp]] blocks rather than a skill (mirrors EgressFromConfig).
const MCPFromConfig = "config"

// MCPDecl is one effective MCP declaration, attributed to its source for
// status legibility (which skill wired which server).
type MCPDecl struct {
	Skill string // contributing skill's canonical ID, or MCPFromConfig
	MCP   config.MCP
}

// MCPSet forms the effective declared set per the shared genus rules
// (declClaims): duplicate ACTIVE names across sources hard-reject — replace-
// by-name is cascade vocabulary, and a skill silently shadowing another's
// server (or the config's) would be surprising; a CLOSED name neither
// delivers nor collides. A closure matching nothing is inert (config
// hygiene, not an error).
func MCPSet(cfg config.Config, r Resolved) ([]MCPDecl, error) {
	var out []MCPDecl
	claims := newDeclClaims("mcp", "mcp", cfg.MCPClosed)
	add := func(src string, m config.MCP) error {
		active, err := claims.claim(src, m.Name)
		if err != nil {
			return err
		}
		if active {
			out = append(out, MCPDecl{Skill: src, MCP: m})
		}
		return nil
	}
	for _, m := range cfg.MCPs {
		if err := add(MCPFromConfig, m); err != nil {
			return nil, err
		}
	}
	for _, sk := range r.Skills {
		for _, m := range sk.File.MCPs {
			if err := add(sk.Name, m); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// MCPList strips attribution for the consumers that want the bare
// declarations (the mcp.json render).
func MCPList(decls []MCPDecl) []config.MCP {
	out := make([]config.MCP, 0, len(decls))
	for _, d := range decls {
		out = append(out, d.MCP)
	}
	return out
}

// MCPEgress derives the egress a declared set CARRIES: each remote server's
// URL endpoint plus its declared extra egress hosts, attributed
// "mcp:<name>". These join the resolved allowlist like skill egress —
// implied by enabling the wiring, closable by a `!host[:port]` closure
// (which then renders on the MCP's own status row as endpoint-closed).
// Local (command) servers imply nothing: their outbound is unknown by
// construction, which is status's "unknown outbound" row, not an allowlist
// entry.
func MCPEgress(decls []MCPDecl) []EgressAllow {
	var out []EgressAllow
	for _, d := range decls {
		attr := "mcp:" + d.MCP.Name
		if host, port, ok := d.MCP.Endpoint(); ok {
			out = append(out, EgressAllow{Skill: attr, Host: host, Port: port})
		}
		for _, e := range d.MCP.Egress {
			host, port, err := config.ParseEgress(e)
			if err != nil {
				continue // unreachable: validated at declaration
			}
			out = append(out, EgressAllow{Skill: attr, Host: host, Port: port})
		}
	}
	return out
}
