// Package configui is byre's interactive (Bubble Tea) editor for a project's
// host-side store config and the global default.config. The Elm-architecture
// model (form.go) is driven headlessly in tests; the data layer here
// (parse/format/save) is unit-tested too.
package configui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"byre/internal/config"
)

// Save validates cfg as a single layer, marshals it to TOML (only set fields,
// via omitempty), and writes it to path atomically with a managed-by header. Raw
// fields (run_args, dockerfile_*) round-trip untouched. Validation is
// ValidateLayer, NOT the resolved Validate: this file is one cascade layer, so
// `!name` removal entries are legal here and cross-layer collisions aren't its
// concern — using Validate made any config with a removal entry unsaveable.
func Save(path string, cfg config.Config) error {
	if err := cfg.ValidateLayer(); err != nil {
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
