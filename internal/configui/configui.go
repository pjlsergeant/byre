// Package configui is byre's interactive (Bubble Tea) editor for a project's
// host-side store config and the global default.config. The Elm-architecture
// model (form.go) is driven headlessly in tests; the data layer here
// (parse/format/save) is unit-tested too.
package configui

import (
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/pjlsergeant/byre/internal/config"
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
	return config.AtomicWrite(path, b.String())
}

// handComments reports whether raw config content has hand-written full-line
// # comments — ones a re-marshaling Save would destroy. byre's own boilerplate
// headers (the managed-by header Save writes, onboarding's markers) don't
// count: they're regenerated or expendable, and warning on them would make
// every byre-created file cry wolf. Inline comments (after a value) are not
// detected — TOML strings can contain '#', and a false positive costs more
// than the rare miss.
func handComments(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		if byreBoilerplate(t) {
			continue
		}
		return true
	}
	return false
}

func byreBoilerplate(comment string) bool {
	for _, p := range []string{
		"# Managed by `byre config`",
		"# raw blocks (run_args",
		"# Created by byre",
		"# byre default.config",
	} {
		if strings.HasPrefix(comment, p) {
			return true
		}
	}
	return false
}
