// Package configui is byre's interactive (Bubble Tea / huh) editor for a
// project's host-side store config and the global default.config. The data layer
// here (parse/format/save) is unit-tested; the huh form wiring (form.go) is
// host-verified, since a TUI can't be driven headlessly.
package configui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"byre/internal/config"
)

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
