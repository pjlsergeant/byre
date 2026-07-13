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
	"sync"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/version"
)

//go:embed skills templates
var fsys embed.FS

func init() {
	// Wire the catalog hooks config needs without config importing this
	// package (would cycle: config tests import builtins which imports config).
	// Display version for humans; compat for requires_byre only.
	config.BundledFS = FS
	config.ByreVersion = version.String
	config.ByreCompat = version.Semver
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
// uses version.Semver() separately via LoadCatalog. Notices print once
// per process (first non-nil writer wins).
func EnsureStoreOut(home string, notices io.Writer) error {
	return packages.EnsureStore(home, fsys, version.String(), ensureNotices(notices))
}

// LoadCatalog builds the multi-provider catalog for home.
func LoadCatalog(home string) (*packages.Catalog, error) {
	if err := EnsureStore(home); err != nil {
		return nil, err
	}
	return LoadCatalogRaw(home)
}

// LoadCatalogRaw builds a catalog without EnsureStore (tests that manage the
// store themselves). Display version is version.String(); compat is Semver.
// Eager stage-2 hooks are package-level on packages (wired by skills/config init).
func LoadCatalogRaw(home string) (*packages.Catalog, error) {
	return packages.LoadCatalog(home, fsys, version.String(), version.Semver())
}

// noticeOnce ensures store-ensure human notices print at most once per process.
var (
	noticeMu   sync.Mutex
	noticeDone bool
)

// EnsureStoreOut prints mirror/LEGACY notices on the first noticed call in this
// process; later calls with a writer are silent so develop+onboard do not double.
func ensureNotices(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	noticeMu.Lock()
	defer noticeMu.Unlock()
	if noticeDone {
		return nil
	}
	noticeDone = true
	return w
}

// ArchiveLegacy moves LEGACY materialized dirs aside (D10).
func ArchiveLegacy(home string) ([]string, error) {
	return packages.ArchiveLegacy(home, fsys)
}
