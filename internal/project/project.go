// Package project derives byre's per-project identity and on-disk locations.
//
// project_id = sha256(canonical absolute path), truncated. It is a *naming*
// device only (Docker-safe), never a caching key — Docker owns caching.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hashLen is how many hex chars of the sha256 disambiguate a project. The hash
// is only a uniqueness suffix on a human-readable slug, so 6 (24 bits) is ample.
const hashLen = 6

// maxSlug caps the readable slug length so Docker object names stay reasonable.
const maxSlug = 40

// nonAlnum matches runs of characters not allowed in a Docker name component.
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Canonicalize resolves a project path to a stable absolute form: symlinks
// resolved, cleaned, no trailing slash. Two paths denoting the same directory
// canonicalize identically, so they yield the same id.
func Canonicalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Resolve symlinks when the path exists; fall back to the cleaned absolute
	// path otherwise, so an id can still be computed for a not-yet-created dir.
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	if abs != string(filepath.Separator) {
		abs = strings.TrimRight(abs, string(filepath.Separator))
	}
	return abs, nil
}

// ID returns the project identity: a readable slug of the last two path
// components plus a short hash of the canonical path for uniqueness, e.g.
// /Users/me/dev/byre -> "byre-dev-0877d7". The "byre-" prefix that callers add
// makes the final Docker name valid regardless of the slug content.
func ID(path string) (string, error) {
	canon, err := Canonicalize(path)
	if err != nil {
		return "", err
	}
	return idFromCanonical(canon), nil
}

// idFromCanonical builds the id from an already-canonicalized path, so the
// recorded path and the id derive from the same canonicalization.
func idFromCanonical(canon string) string {
	leaf := sanitize(filepath.Base(canon))
	parent := sanitize(filepath.Base(filepath.Dir(canon)))

	parts := make([]string, 0, 2)
	if leaf != "" {
		parts = append(parts, leaf)
	}
	if parent != "" {
		parts = append(parts, parent)
	}
	slug := strings.Join(parts, "-")
	if len(slug) > maxSlug {
		slug = strings.Trim(slug[:maxSlug], "-")
	}
	if slug == "" {
		slug = "project"
	}

	sum := sha256.Sum256([]byte(canon))
	return slug + "-" + hex.EncodeToString(sum[:])[:hashLen]
}

// sanitize lowercases a path component and reduces it to Docker-name-safe
// characters ([a-z0-9], runs of anything else collapse to a single dash).
func sanitize(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// Home returns the byre home directory (~/.byre), overridable via BYRE_HOME
// (used by tests and unusual setups).
func Home() (string, error) {
	if h := os.Getenv("BYRE_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".byre"), nil
}

// Paths holds the on-disk locations byre uses for one project.
//
// The build context is a subdirectory so the path record and lock file (which
// live directly under Dir) are not sent to the engine as build context.
type Paths struct {
	ID         string // project_id
	Canonical  string // canonical project directory
	Home       string // ~/.byre
	Dir        string // ~/.byre/projects/<id>
	ContextDir string // ~/.byre/projects/<id>/context  (docker build context)
	Dockerfile string // <ContextDir>/Dockerfile.generated
	PathRecord string // ~/.byre/projects/<id>/path
	LockFile   string // ~/.byre/projects/<id>/lock
}

// Resolve computes the id and on-disk paths for a project directory.
func Resolve(projectDir string) (Paths, error) {
	canon, err := Canonicalize(projectDir)
	if err != nil {
		return Paths{}, err
	}
	id := idFromCanonical(canon)
	home, err := Home()
	if err != nil {
		return Paths{}, err
	}
	dir := filepath.Join(home, "projects", id)
	ctx := filepath.Join(dir, "context")
	return Paths{
		ID:         id,
		Canonical:  canon,
		Home:       home,
		Dir:        dir,
		ContextDir: ctx,
		Dockerfile: filepath.Join(ctx, "Dockerfile.generated"),
		PathRecord: filepath.Join(dir, "path"),
		LockFile:   filepath.Join(dir, "lock"),
	}, nil
}

// Bootstrap ensures ~/.byre/projects/<id>/ exists and records the canonical
// path. If a different path is already recorded under the same id, that is a
// hash collision and Bootstrap returns an error rather than silently reusing
// another project's image and volumes.
func (p Paths) Bootstrap() error {
	if err := os.MkdirAll(p.ContextDir, 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(p.PathRecord)
	switch {
	case err == nil:
		// Trim only the record's trailing newline — Unix paths may legitimately
		// end in spaces, so TrimSpace would cause false collisions.
		if rec := strings.TrimSuffix(string(existing), "\n"); rec != p.Canonical {
			return fmt.Errorf("project id %s collision: recorded path %q != current %q", p.ID, rec, p.Canonical)
		}
		return nil
	case errors.Is(err, os.ErrNotExist):
		return os.WriteFile(p.PathRecord, []byte(p.Canonical+"\n"), 0o644)
	default:
		return err
	}
}
