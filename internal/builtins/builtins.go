// Package builtins ships byre's built-in skills and templates embedded in
// the binary. Authoritative bytes live in embed.FS; the loader reads them
// from here (never from ~/.byre/bundled/, which is a display-only mirror).
//
// Store preparation (mirror + legacy migration) lives in packages.EnsureStore;
// EnsureStore here is a thin wrapper so call sites keep a single import.
package builtins

import (
	"embed"
	"io"
	"io/fs"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/version"
)

//go:embed skills templates
var fsys embed.FS

func init() {
	// Wire the catalog hooks config needs without config importing this
	// package (would cycle: config tests import builtins which imports config).
	// Compat paths use Semver so "(devel)" builds still parse requires_byre.
	config.BundledFS = FS
	config.ByreVersion = version.Semver
}

// FS returns the embedded bundled packages filesystem. Top-level entries are
// "skills" and "templates". Callers must not assume anything under
// ~/.byre/skills is a bundled package -- those are local only.
func FS() fs.FS { return fsys }

// EnsureStore prepares the store at home: bundled mirror + legacy notices.
// notices, when non-nil, receives human-facing lines (mirror regen, LEGACY).
// Strict paths (develop, resolve) should pass nil and surface errors; soft
// paths (status) may log notices to stderr.
func EnsureStore(home string) error {
	return EnsureStoreOut(home, nil)
}

// EnsureStoreOut is EnsureStore with an optional notice writer.
// The mirror stamp uses version.String() (human-facing); catalog compat
// uses version.Semver() separately via LoadCatalog.
func EnsureStoreOut(home string, notices io.Writer) error {
	return packages.EnsureStore(home, fsys, version.String(), notices)
}

// LoadCatalog builds the multi-provider catalog for home.
func LoadCatalog(home string) (*packages.Catalog, error) {
	if err := EnsureStore(home); err != nil {
		return nil, err
	}
	return LoadCatalogRaw(home)
}

// LoadCatalogRaw builds a catalog without EnsureStore (tests that manage the
// store themselves). Compat checks use version.Semver so a "(devel)" binary
// still accepts requires_byre constraints.
func LoadCatalogRaw(home string) (*packages.Catalog, error) {
	return packages.LoadCatalog(home, fsys, version.Semver())
}

// ArchiveLegacy moves LEGACY materialized dirs aside (D10).
func ArchiveLegacy(home string) ([]string, error) {
	return packages.ArchiveLegacy(home, fsys)
}
