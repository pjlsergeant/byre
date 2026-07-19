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
	"net"
	"net/url"
	"regexp"
	"sort"
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
	// Headers are HTTP headers a REMOTE server needs (static-token auth:
	// `Authorization = "Bearer ${TOKEN}"`, an X-Api-Key). Values are
	// TEMPLATES: a `${NAME}` reference is expanded at LAUNCH by the
	// delivering adapter (claude expands natively inside --mcp-config;
	// the codex wrapper maps pure-bearer to bearer_token_env_var, pure
	// `${VAR}` to env_http_headers, and expands anything else itself), so
	// the baked file carries only the template text — token values stay
	// launch-time env lookups, never config or image content. A literal
	// fragment is allowed and bakes like an [env] literal (documented,
	// never refused — the userinfo/argv stance). `${NAME}` refs get the
	// same provided/NOT-provided status verdicts as the Env list.
	Headers map[string]string `toml:"headers,omitempty"`
}

// Remote reports whether the declaration is a remote (url) server.
func (m MCP) Remote() bool { return m.URL != "" }

// Endpoint is the remote server's implied egress endpoint (host, port),
// derived from URL: an explicit URL port wins, else 443 for https and 80
// for http. The host is returned in egress-grammar form — an IPv6 literal
// comes back BRACKETED and canonicalized, so "%s:%d" compositions
// downstream stay parseable. ok is false for a local declaration or an
// unparseable URL (validation reports those; derivation stays total).
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
	return egressHostForm(u.Hostname()), port, true
}

// egressHostForm renders a URL hostname in the egress grammar: IPv6
// literals bracketed and canonicalized (RFC 5952), everything else as-is.
func egressHostForm(hostname string) string {
	if ip := net.ParseIP(hostname); ip != nil && ip.To4() == nil {
		return "[" + ip.String() + "]"
	}
	return hostname
}

// mcpNameRe is the MCP name grammar. Deliberately tighter than most: the
// name becomes a JSON key in the baked mcp.json and an attribution label
// (mcp:<name>) on status rows. No underscores, so a declared name can never
// carry the `byre__` prefix — reserved for free against a state-writing
// future byre walked back (ADR 0033, "The registrar that wasn't").
var mcpNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidMCPName reports whether s satisfies the MCP name grammar — for
// callers (the mcp verbs) that validate a bare name with no declaration
// around it. Single owner: the grammar lives in mcpNameRe alone.
func ValidMCPName(s string) bool { return mcpNameRe.MatchString(s) }

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
		// The url host becomes an implied egress entry, so it must be
		// expressible in the egress grammar (hostname, IPv4, or bracketed
		// IPv6 — egressHostForm brackets a v6 literal the way Endpoint
		// derives it). One owner: whatever ParseEgress accepts here is
		// exactly what the derived entry will re-parse as downstream.
		if _, _, err := ParseEgress(egressHostForm(u.Hostname())); err != nil {
			return fmt.Errorf("mcp %s: url host %q: not expressible in byre's egress grammar: %v", m.Name, u.Hostname(), err)
		}
		// Userinfo (user:pass@), query strings, and command argv are all
		// ALLOWED to carry whatever the user puts there — including secrets,
		// which then bake into the image like [env] literals. A refusal here
		// shipped briefly (codex review round 1) and was walked back by
		// maintainer ruling 2026-07-15: the threat model is the agent, never
		// the user (footgun doctrine), and a basic-auth URL is a real shape
		// (a self-hosted MCP behind a reverse proxy) with no alternative
		// spelling — refusing it breaks a working setup to police the user's
		// own config. The docs and `byre mcp add` disclose the bake instead.
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
	if len(m.Headers) > 0 && m.URL == "" {
		return fmt.Errorf("mcp %s: headers are for remote (url) servers — a local stdio server has no HTTP request to carry them", m.Name)
	}
	lowerSeen := map[string]string{}
	for _, k := range sortedHeaderKeys(m.Headers) {
		if !headerNameRe.MatchString(k) {
			return fmt.Errorf("mcp %s: header name %q: not a valid HTTP header name", m.Name, k)
		}
		// HTTP field names are case-insensitive: two case-variant keys are
		// one header wearing two spellings — which one wins would be
		// map-order luck. Refuse the ambiguity.
		if prev, dup := lowerSeen[strings.ToLower(k)]; dup {
			return fmt.Errorf("mcp %s: headers %q and %q are the same HTTP header (field names are case-insensitive) — keep one", m.Name, prev, k)
		}
		lowerSeen[strings.ToLower(k)] = k
		if err := mcpPrintable(m.Headers[k]); err != nil {
			return fmt.Errorf("mcp %s: header %s value: %w", m.Name, k, err)
		}
	}
	return nil
}

// headerNameRe is RFC 9110's field-name token grammar (tchar), length-capped:
// the name lands in JSON keys, codex -c TOML keys (both quoted), and status
// rows (no control chars in tchar). Real-world names like X_API_KEY and
// 2FA-Token are valid tokens (codex review round 10).
var headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,128}$")

// headerEnvRefRe finds ${NAME} template references in header values.
var headerEnvRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// HeaderEnvRefs lists the env var NAMES the declaration's header templates
// reference, sorted and deduped — they join the Env list on every surface
// that renders provided/NOT-provided verdicts.
func (m MCP) HeaderEnvRefs() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range sortedHeaderKeys(m.Headers) {
		for _, match := range headerEnvRefRe.FindAllStringSubmatch(m.Headers[k], -1) {
			if !seen[match[1]] {
				seen[match[1]] = true
				out = append(out, match[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

// ConsumedEnv is the declaration's full consumed-env name set: the explicit
// Env list plus header template references, deduped (a repeated Env entry
// included — validation permits it, one verdict is enough), Env order first
// — the one list status/list/review verdicts render.
func (m MCP) ConsumedEnv() []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, k := range m.Env {
		add(k)
	}
	for _, k := range m.HeaderEnvRefs() {
		add(k)
	}
	return out
}

// HeaderNames lists the declared header names, sorted — display surfaces
// print names, not values (a value may carry a user's literal secret).
func (m MCP) HeaderNames() []string { return sortedHeaderKeys(m.Headers) }

func sortedHeaderKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// mcpDeclOps plugs the [[mcp]] vocabulary into the shared named-declaration
// machinery (nameddecl.go): validation split, split/merge, closure taxonomy.
var mcpDeclOps = namedDeclOps[MCP]{
	label:      "mcp",
	markerNoun: "a real server",
	nameNoun:   "server name",
	nameRe:     mcpNameRe,
	name:       func(m MCP) string { return m.Name },
	markerExtras: func(m MCP) bool {
		return len(m.Command) > 0 || m.URL != "" || len(m.Env) > 0 || len(m.Egress) > 0 || len(m.Headers) > 0
	},
	validate: ValidateMCP,
}

// validateMCPsLayer / validateMCPsResolved check the [[mcp]] list per the
// shared lifecycle split (see nameddecl.go).
func (c Config) validateMCPsLayer() error {
	return validateNamedDeclsLayer(mcpDeclOps, c.MCPs, c.MCPClosed)
}

func (c Config) validateMCPsResolved() error {
	return validateNamedDeclsResolved(mcpDeclOps, c.MCPs, c.MCPClosed)
}

// mergeMCPs folds one cascade step of the [[mcp]] list into (open, closed)
// per the shared genus taxonomy (see mergeNamedDecls): closures survive in
// MCPClosed so they can subtract after the skill union (skills.MCPSet).
func mergeMCPs(base, over Config) (open []MCP, closed []string) {
	return mergeNamedDecls(base.MCPs, base.MCPClosed, over.MCPs, over.MCPClosed, mcpDeclOps.name)
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
			if len(m.Headers) > 0 {
				// Templates VERBATIM — ${NAME} refs expand at launch (claude
				// natively; the codex wrapper maps/expands), so the baked
				// file stays free of byre-placed secrets.
				entry["headers"] = m.Headers
			}
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
