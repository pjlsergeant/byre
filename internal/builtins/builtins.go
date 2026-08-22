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
	"sync"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/version"
)

//go:embed skills templates
var fsys embed.FS

func init() {
	// builtins is the composition point for catalog construction: it owns
	// the embedded content, the version, and (via imports config cannot
	// have) the full stage-2 parser set. This is the ONE loader seam config
	// consumes — everything else about a catalog is an explicit argument.
	config.CatalogLoader = LoadCatalogRaw
}

// stage2Hooks is the full eager stage-2 parser set for catalog ingest.
func stage2Hooks() packages.Stage2Hooks {
	return packages.Stage2Hooks{
		Skill:    skills.ValidatePrimaryBytes,
		Template: config.ValidateTemplateBytes,
	}
}

// EnsureStoreOut prepares the store at home: bundled mirror + legacy notices.
// notices, when non-nil, receives human-facing lines (mirror regen, LEGACY) --
// they print once per process, first non-nil writer wins. Strict paths
// (develop, resolve) pass nil and surface errors; soft paths (status) may
// log notices to stderr. The mirror stamp uses version.String()
// (human-facing); catalog compat uses version.Semver() via LoadCatalog.
func EnsureStoreOut(home string, notices io.Writer) error {
	return packages.EnsureStore(home, fsys, version.String(), ensureNotices(notices))
}

// LoadCatalogRaw builds a catalog without EnsureStore (tests that manage the
// store themselves). Display version is version.String(); compat is Semver.
func LoadCatalogRaw(home string) (*packages.Catalog, error) {
	return packages.LoadCatalog(home, fsys, version.String(), version.Semver(), stage2Hooks())
}

// noticeOnce ensures store-ensure human notices print at most once per process.
var (
	noticeMu   sync.Mutex
	noticeDone bool
)

// ensureNotices yields a writer that prints mirror/LEGACY notices on the first
// noticed call in this process; later calls with a writer are silent so
// develop+onboard do not double.
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
