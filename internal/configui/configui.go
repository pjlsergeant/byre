// Package configui is byre's interactive (Bubble Tea) editor for a project's
// host-side store config and the global default.config. The Elm-architecture
// model (form.go) is driven headlessly in tests; the data layer here
// (parse/format/save) is unit-tested too.
package configui

import (
	"fmt"
	"os"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/tomldoc"
)

// managedHeader fronts a config file byre CREATES. One line of ownership,
// nothing more: the file is shared custody (ADR 0044), and saves preserve
// whatever the user writes in it, this header included.
const managedHeader = "# Managed by `byre config`.\n\n"

// Save validates cfg as a single layer and reconciles it onto the file's
// current content with targeted, style-preserving edits (tomldoc): fields
// the caller didn't change produce no edit at all, so hand-written comments
// and formatting survive; a changed field rewrites only its own construct.
// A missing file is created in byre's house layout. Validation is
// ValidateLayer, NOT the resolved Validate: this file is one cascade layer,
// so `!name` removal entries are legal here and cross-layer collisions
// aren't its concern.
// follow states the target's trust class: false for the project-store
// config (the one file --self-edit mounts into a box), true for host-owned
// homes (default.config, named layers), where a dotfiles symlink is the
// user's own arrangement.
func Save(path string, follow bool, cfg config.Config) error {
	if err := cfg.ValidateLayer(); err != nil {
		return err
	}
	raw, err := hostopen.ReadFileBounded(path, follow, config.MaxConfigBytes)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		raw = []byte(managedHeader)
	}
	cur, err := config.Parse(raw)
	if err != nil {
		// Reconciling against a file byre can't read would guess at what to
		// preserve. The parse error names the problem; fixing the file is
		// the editor's flow (it refuses to open broken files the same way).
		return fmt.Errorf("%s: %w", path, err)
	}
	doc, err := tomldoc.Load(raw)
	if err != nil {
		return err
	}
	if err := reconcile(doc, cur, cfg); err != nil {
		return err
	}
	return config.AtomicWrite(path, string(doc.Bytes()))
}
