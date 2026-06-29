// Package configui is byre's interactive (Bubble Tea / huh) editor for a
// project's host-side store config and the global default.config. The data layer
// here (parse/format/save) is unit-tested; the huh form wiring (form.go) is
// host-verified, since a TUI can't be driven headlessly.
package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"byre/internal/config"
)

// formatList renders a string slice as one item per line (for a textarea).
func formatList(items []string) string { return strings.Join(items, "\n") }

// parseList parses one item per line, dropping blanks/whitespace.
func parseList(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// formatEnv renders an env map as sorted KEY=VALUE lines (stable output).
func formatEnv(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseEnv parses KEY=VALUE lines into a map (blank lines skipped). The key is
// trimmed; the value is kept EXACTLY as written (after the first '='), so an
// intentional value like " x " survives a round-trip.
func parseEnv(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			return nil, fmt.Errorf("env line %q must be KEY=VALUE", ln)
		}
		if k = strings.TrimSpace(k); k == "" {
			return nil, fmt.Errorf("env line %q has an empty key", ln)
		}
		m[k] = v
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}

// formatMounts renders mounts as `host -> target (mode)` lines.
func formatMounts(ms []config.Mount) string {
	var b strings.Builder
	for _, m := range ms {
		mode := m.Mode
		if mode == "" {
			mode = "ro"
		}
		fmt.Fprintf(&b, "%s -> %s (%s)\n", m.Host, m.Target, mode)
	}
	return strings.TrimRight(b.String(), "\n")
}

// parseMounts parses `host -> target [(mode)]` lines into mounts.
func parseMounts(s string) ([]config.Mount, error) {
	var out []config.Mount
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		host, rest, ok := strings.Cut(t, "->")
		if !ok {
			return nil, fmt.Errorf("mount line %q must be 'host -> target [(rw)]'", ln)
		}
		host = strings.TrimSpace(host)
		rest = strings.TrimSpace(rest)
		mode := ""
		if i := strings.LastIndex(rest, "("); i >= 0 && strings.HasSuffix(rest, ")") {
			mode = strings.TrimSpace(rest[i+1 : len(rest)-1])
			rest = strings.TrimSpace(rest[:i])
		}
		if host == "" || rest == "" {
			return nil, fmt.Errorf("mount line %q needs both a host and a target", ln)
		}
		if strings.Contains(rest, "->") {
			// More than one "->" — the path is ambiguous (and paths shouldn't
			// contain the delimiter). Reject rather than silently mis-split.
			return nil, fmt.Errorf("mount line %q: a path contains '->' (the host/target delimiter)", ln)
		}
		out = append(out, config.Mount{Host: host, Target: rest, Mode: mode})
	}
	return out, nil
}

// Save validates cfg, marshals it to TOML (only set fields, via omitempty), and
// writes it to path atomically with a managed-by header. Raw fields
// (run_args, dockerfile_*) round-trip untouched.
func Save(path string, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Managed by `byre config`. Structured fields are edited there;\n")
	b.WriteString("# raw blocks (run_args, dockerfile_pre/post) are edited here by hand.\n\n")
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return err
	}
	return atomicWrite(path, b.String())
}

func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".byre-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
