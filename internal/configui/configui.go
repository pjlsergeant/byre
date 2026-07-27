// Package configui is byre's interactive (Bubble Tea) editor for a project's
// host-side store config and the global default.config. The Elm-architecture
// model (form.go) is driven headlessly in tests; the data layer here
// (parse/format/save) is unit-tested too.
package configui

import (
	"bytes"
	"errors"
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
// ErrDrift reports that the file changed on disk since the editor opened it.
// The editor's desired config is built on the config it READ at open, so a
// key another session added since is not in it -- writing would reconcile
// that key away. Concurrent worktree sessions share one project store, so
// this is reachable in ordinary use, not a theoretical race.
var ErrDrift = errors.New("the config file changed on disk since this editor opened it")

// Save writes cfg, preserving everything it does not structure. openRaw and
// openErr are the file as the editor first read it: unless force is set, a
// disk state that no longer matches them aborts with ErrDrift instead of
// overwriting. Callers hold whatever lock the target deserves around this
// (the project store's setup lock) -- Save itself knows nothing about
// project layout.
func Save(path string, follow bool, cfg config.Config, openRaw []byte, openErr error, force bool) error {
	if err := cfg.ValidateLayer(); err != nil {
		return err
	}
	raw, err := hostopen.ReadFileBounded(path, follow, config.MaxConfigBytes)
	if !force && !sameFileState(raw, err, openRaw, openErr) {
		return ErrDrift
	}
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

// sameFileState compares two (bytes, error) reads of the same path. Absent on
// both sides counts as unchanged; any read failure other than absence is
// treated as drift -- byre cannot establish the file is what it was, and
// "unchanged" is only ever claimed on positive evidence (reportSaved's rule).
func sameFileState(a []byte, aErr error, b []byte, bErr error) bool {
	switch {
	case aErr == nil && bErr == nil:
		return bytes.Equal(a, b)
	case os.IsNotExist(aErr) && os.IsNotExist(bErr):
		return true
	default:
		return false
	}
}
