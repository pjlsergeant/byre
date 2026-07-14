package config

// MCP declarations ([[mcp]] blocks): byre's vocabulary for wiring Model
// Context Protocol servers into the box. An MCP declaration is WIRING —
// configuration, like a package — not a grant: a stdio server is a process
// (nothing bash lacks), a remote one reaches nothing the firewall doesn't
// allow. What's real are the grants it CARRIES (its implied/declared egress,
// its consumed env), which render where grants always render, attributed
// mcp:<name>.
//
// Declarations live in two homes — byre.config layers and skill.toml
// contributions — under one merge taxonomy: within config layers a later
// layer replaces by name (normal cascade); skill contributions union AFTER
// the merge; a `!name` closure adopts the egress-closure semantic WHOLESALE
// (ADR 0030): kept through the merge (never consumed, see MCPClosed) and
// subtracted after the skill union, so "this skill, minus one of its
// servers" works. Duplicate ACTIVE declarations across sources are a hard
// reject (skills.MCPSet).

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// MCP is one declared MCP server. Exactly one of Command (a local stdio
// server, argv form) or URL (a remote streamable-HTTP server) is set — the
// declaration self-discriminates; there is no transport field.
type MCP struct {
	Name string `toml:"name"`
	// Command is the stdio server's argv ([0] = binary). Pure wiring: the
	// binary itself arrives via the existing build machinery (a skill's
	// build block, apt, npm_global) — byre installs nothing from an [[mcp]].
	Command []string `toml:"command,omitempty"`
	// URL is the remote server's endpoint. Its host is an IMPLIED egress
	// entry — attributed mcp:<name>, closable like any other (a `!host`
	// closure closing it renders on the MCP's own status row).
	URL string `toml:"url,omitempty"`
	// Env is the var NAMES this server consumes — never values. Values
	// arrive via env_from_host / [env] (attributed grants); a claude box's
	// stdio servers inherit the full box env, so a provided name reaches
	// the server with no further wiring. Names-only keeps tokens out of
	// byre files; status marks each name provided/not-provided.
	Env []string `toml:"env,omitempty"`
	// Egress is extra hosts this server needs beyond the URL's own (e.g.
	// an OAuth authorize host), same grammar as the egress config key.
	Egress []string `toml:"egress,omitempty"`
}

// Remote reports whether the declaration is a remote (url) server.
func (m MCP) Remote() bool { return m.URL != "" }

// Endpoint is the remote server's implied egress endpoint (host, port),
// derived from URL: an explicit URL port wins, else 443 for https and 80
// for http. ok is false for a local declaration or an unparseable URL
// (validation reports those; derivation stays total).
func (m MCP) Endpoint() (host string, port int, ok bool) {
	if m.URL == "" {
		return "", 0, false
	}
	u, err := url.Parse(m.URL)
	if err != nil || u.Hostname() == "" {
		return "", 0, false
	}
	port = 443
	if u.Scheme == "http" {
		port = 80
	}
	if p := u.Port(); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	return u.Hostname(), port, true
}

// mcpNameRe is the MCP name grammar. Deliberately tighter than most: the
// name becomes a JSON key in the baked mcp.json, an attribution label
// (mcp:<name>) on status rows, and — for state-writing adapters — part of a
// registered server id. No underscores, so a declared name can never carry
// the reserved `byre__` registrar prefix.
var mcpNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateMCP checks one declaration's own shape. Shared by the config
// validators and skills.Resolve — config-declared and skill-declared
// servers are held to the same bar.
func ValidateMCP(m MCP) error {
	if !mcpNameRe.MatchString(m.Name) {
		return fmt.Errorf("mcp name %q: must be lowercase [a-z0-9-], starting with a letter or digit (max 64 chars)", m.Name)
	}
	switch {
	case len(m.Command) > 0 && m.URL != "":
		return fmt.Errorf("mcp %s: has both command and url (a server is local or remote, not both)", m.Name)
	case len(m.Command) == 0 && m.URL == "":
		return fmt.Errorf("mcp %s: needs a command (local stdio server) or a url (remote server)", m.Name)
	}
	for _, arg := range m.Command {
		if err := mcpPrintable(arg); err != nil {
			return fmt.Errorf("mcp %s: command element %q: %w", m.Name, arg, err)
		}
	}
	if len(m.Command) > 0 && strings.TrimSpace(m.Command[0]) == "" {
		return fmt.Errorf("mcp %s: command[0] (the binary) must not be empty", m.Name)
	}
	if m.URL != "" {
		if err := mcpPrintable(m.URL); err != nil {
			return fmt.Errorf("mcp %s: url: %w", m.Name, err)
		}
		u, err := url.Parse(m.URL)
		if err != nil {
			return fmt.Errorf("mcp %s: url %q: %w", m.Name, m.URL, err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("mcp %s: url %q: scheme must be https or http", m.Name, m.URL)
		}
		if u.Hostname() == "" {
			return fmt.Errorf("mcp %s: url %q: missing a host", m.Name, m.URL)
		}
		// user:pass@ is credential syntax with no legitimate MCP-endpoint use,
		// and the URL bakes into a secret-free file in the image (ADR 0033) —
		// same stance as env_from_host refusing literals: a secret in wiring
		// costume is refused at the shape level, not policed. (A query string
		// stays allowed — legitimate endpoint shapes exist — and bakes into
		// the image like an [env] literal; the docs say so.)
		if u.User != nil {
			return fmt.Errorf("mcp %s: url must not carry credentials (user@host) — tokens ride env names + env_from_host, never the baked url", m.Name)
		}
	}
	for _, k := range m.Env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("mcp %s: env entry %q: not a valid environment variable name (env lists NAMES; values belong in [env] or env_from_host)", m.Name, k)
		}
	}
	for _, e := range m.Egress {
		if _, _, err := ParseEgress(e); err != nil {
			return fmt.Errorf("mcp %s: %w", m.Name, err)
		}
	}
	return nil
}

// mcpPrintable rejects control characters and (for a one-token field)
// whitespace-only content: command elements and URLs are printed verbatim on
// status/inspect rows, so a control char could forge adjacent output.
func mcpPrintable(s string) error {
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	return nil
}

// validateMCPs checks the [[mcp]] list per the shared layer/resolved split:
// layer mode permits `name = "!server"` closure markers (name-only, like
// mount markers) and rejects in-layer duplicate names (merge would silently
// replace); resolved mode rejects markers (Merge extracts them into
// MCPClosed) and duplicates alike.
func (c Config) validateMCPs(layer bool) error {
	seen := map[string]bool{}
	for _, m := range c.MCPs {
		if isRemoval(m.Name) {
			if !layer {
				return fmt.Errorf("mcp %s: a closure marker is only meaningful in a cascade layer", m.Name)
			}
			// A marker is name-only — other fields set suggest a real server
			// with a mistyped name; refuse rather than silently discard it.
			if len(m.Command) > 0 || m.URL != "" || len(m.Env) > 0 || len(m.Egress) > 0 {
				return fmt.Errorf("mcp %s: a closure marker takes only a name — other fields here suggest a real server with a mistyped name", m.Name)
			}
			if !mcpNameRe.MatchString(m.Name[1:]) {
				return fmt.Errorf("mcp closure %q: %q is not a valid server name", m.Name, m.Name[1:])
			}
			continue
		}
		if err := ValidateMCP(m); err != nil {
			return err
		}
		if seen[m.Name] {
			return fmt.Errorf("mcp %s appears twice in this file; merge would keep only the last one", m.Name)
		}
		seen[m.Name] = true
	}
	for _, cl := range c.MCPClosed {
		if !mcpNameRe.MatchString(cl) {
			return fmt.Errorf("mcp closure %q: not a valid server name", cl)
		}
	}
	return nil
}

// mergeMCPs folds one cascade step of the [[mcp]] list into (open, closed),
// mirroring mergeEgress: a `!name` closure is NOT consumed when it removes a
// declaration — it survives the cascade (in MCPClosed) so it can subtract
// the same name from the EFFECTIVE set after skill contributions union in
// (skills.MCPSet). Precedence stays cascade-ordered: a later layer's plain
// declaration re-opens an earlier layer's closure; within one layer a
// closure beats a plain declaration (adds fold first, closures after).
// Open declarations replace by name (structured cascade, like volumes),
// closures match by exact name.
func mergeMCPs(base, over Config) (open []MCP, closed []string) {
	open, closed = splitMCPs(base.MCPs, base.MCPClosed)
	overOpen, overClosed := splitMCPs(over.MCPs, over.MCPClosed)
	for _, m := range overOpen {
		closed = filter(closed, func(c string) bool { return c != m.Name })
		replaced := false
		for i := range open {
			if open[i].Name == m.Name {
				open[i] = m
				replaced = true
				break
			}
		}
		if !replaced {
			open = append(open, m)
		}
	}
	for _, c := range overClosed {
		open = filter(open, func(m MCP) bool { return m.Name != c })
		if !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	return open, closed
}

// splitMCPs separates an [[mcp]] list into real declarations and the
// stripped names of its `!name` closure markers, folding an already-
// populated MCPClosed (a previously merged config re-entering Merge) into
// the latter.
func splitMCPs(mcps []MCP, mcpClosed []string) (open []MCP, closed []string) {
	for _, m := range mcps {
		if isRemoval(m.Name) {
			closed = append(closed, m.Name[1:])
			continue
		}
		open = append(open, m)
	}
	for _, c := range mcpClosed {
		if !slices.Contains(closed, c) {
			closed = append(closed, c)
		}
	}
	return open, closed
}

// MCPConfigJSON renders the effective declared set as the canonical
// /etc/byre/mcp.json — the file every byre box bakes (empty set included:
// `{"mcpServers": {}}`), which the claude skill's agent command injects via
// --mcp-config and any other consumer may read. The path and this format are
// a quasi-public contract: pinned by gen's golden test; format changes are
// versioned decisions.
//
// Claude-style env VALUE stanzas are DELIBERATELY absent: claude's stdio
// servers inherit the full agent process env (spike-verified), so
// env_from_host/[env] values reach them with no stanza — while a rendered
// `${NAME}` for an UNSET var would pass the literal string through as a
// garbage credential. Declared env NAMES ride the `x_byre_env` extension
// key instead (claude ignores it — spike-verified; a scrubbed-env consumer
// like the codex adapter turns it into its by-name passthrough, e.g.
// codex's env_vars).
//
// Determinism: encoding/json sorts map keys, so identical declarations
// yield byte-identical output.
func MCPConfigJSON(mcps []MCP) []byte {
	servers := map[string]any{}
	for _, m := range mcps {
		entry := map[string]any{}
		if m.Remote() {
			entry["type"] = "http"
			entry["url"] = m.URL
		} else {
			args := m.Command[1:]
			if args == nil {
				args = []string{}
			}
			entry["type"] = "stdio"
			entry["command"] = m.Command[0]
			entry["args"] = args
		}
		if len(m.Env) > 0 {
			entry["x_byre_env"] = m.Env
		}
		servers[m.Name] = entry
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
	if err != nil {
		// Unreachable: the value is maps/strings/slices only.
		panic(fmt.Sprintf("mcp.json render: %v", err))
	}
	return append(b, '\n')
}
