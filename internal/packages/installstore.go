package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/pjlsergeant/byre/internal/lock"
)

// Installed-store layout:
//
//	~/.byre/packages/<sha256-digest>/   installed snapshots, immutable
//	~/.byre/packages/index.toml         id -> {digest, version, kind, uri, installed_at}
//	~/.byre/packages/.lock              store-global mutation lock
//	~/.byre/packages/.gitignore         self-ignoring

// IndexEntry is one installed package's index row. URI and InstalledAt are
// provenance for humans, never an instruction byre follows.
type IndexEntry struct {
	Digest      string `toml:"digest"`
	Version     string `toml:"version"`
	Kind        string `toml:"kind"`
	URI         string `toml:"uri"`
	InstalledAt string `toml:"installed_at"`
}

type indexFile struct {
	Packages map[string]IndexEntry `toml:"packages"`
}

func packagesDir(home string) string { return filepath.Join(home, "packages") }
func indexPath(home string) string   { return filepath.Join(packagesDir(home), "index.toml") }

// SnapshotDir is the immutable snapshot directory for a digest.
func SnapshotDir(home, digest string) string {
	return filepath.Join(packagesDir(home), digest)
}

// ReadIndex loads the installed-package index. Missing file = empty index.
// Every entry's digest must be exactly 64 lowercase hex characters — the
// digest becomes a path component under packages/ that removal REMOVES
// RECURSIVELY, so a corrupted or hand-edited index (digest = "../victim")
// must die here, before any entry is exposed, never at a deletion site.
func ReadIndex(home string) (map[string]IndexEntry, error) {
	var f indexFile
	b, err := os.ReadFile(indexPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]IndexEntry{}, nil
		}
		return nil, err
	}
	if _, err := toml.Decode(string(b), &f); err != nil {
		return nil, fmt.Errorf("%s: %w", indexPath(home), err)
	}
	for id, e := range f.Packages {
		if !digestDirRe.MatchString(e.Digest) {
			return nil, fmt.Errorf("%s: package %q has a malformed digest %q (want 64 lowercase hex chars) — the index is corrupt; restore it or remove the bad entry",
				indexPath(home), id, e.Digest)
		}
	}
	if f.Packages == nil {
		f.Packages = map[string]IndexEntry{}
	}
	return f.Packages, nil
}

// writeIndex atomically replaces the index (write temp, rename).
func writeIndex(home string, idx map[string]IndexEntry) error {
	var b strings.Builder
	b.WriteString("# Installed packages -- maintained by `byre skill/template install`.\n")
	ids := make([]string, 0, len(idx))
	for id := range idx {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := idx[id]
		fmt.Fprintf(&b, "\n[packages.%q]\n", id)
		fmt.Fprintf(&b, "digest = %q\n", e.Digest)
		fmt.Fprintf(&b, "version = %q\n", e.Version)
		fmt.Fprintf(&b, "kind = %q\n", e.Kind)
		fmt.Fprintf(&b, "uri = %q\n", e.URI)
		fmt.Fprintf(&b, "installed_at = %q\n", e.InstalledAt)
	}
	tmp, err := os.CreateTemp(packagesDir(home), ".index-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), indexPath(home))
}

// WithStoreLock ensures the packages dir, takes the store-global lock,
// sweeps orphans, and runs fn.
func WithStoreLock(home string, fn func() error) error {
	dir := packagesDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Self-ignoring store: installed snapshots are reproducible
	// artifacts, not source.
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) {
		_ = os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	l, err := lock.Acquire(filepath.Join(dir, ".lock"))
	if err != nil {
		return err
	}
	defer l.Release()
	sweepOrphans(home)
	return fn()
}

var digestDirRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sweepOrphans removes crash leftovers under the lock: staging temp
// dirs, and digest-named snapshot dirs the index does not reference. Runs
// best-effort; the store must never fail to open because a sweep could not.
func sweepOrphans(home string) {
	idx, err := ReadIndex(home)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, e := range idx {
		live[e.Digest] = true
	}
	entries, err := os.ReadDir(packagesDir(home))
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".stage-") || strings.HasPrefix(name, ".index-") {
			_ = os.RemoveAll(filepath.Join(packagesDir(home), name))
			continue
		}
		if e.IsDir() && digestDirRe.MatchString(name) && !live[name] {
			_ = os.RemoveAll(filepath.Join(packagesDir(home), name))
		}
	}
}

// Snapshot is a fully-fetched, verified package ready to land: the manifest
// bytes (which ARE the primary file) plus payload contents keyed by
// package-relative destination.
type Snapshot struct {
	ID       string
	Digest   string
	Primary  string // "skill.toml" or "template.config"
	Manifest []byte
	Files    map[string][]byte
	Exec     map[string]bool
	Entry    IndexEntry

	// ExpectPrior is the index digest the caller's consent decision was
	// based on ("" = the id was absent). The land step re-checks it UNDER
	// the lock: a concurrent install that changed the reviewed state must
	// not ride a consent given for a different state.
	ExpectPrior string

	// Repair forces a rewrite even when the snapshot directory already
	// exists: content-addressing normally makes a present dir a no-write,
	// but a caller repairing a broken snapshot (catalog INVALID row) knows
	// the on-disk copy cannot be trusted.
	Repair bool
}

// ErrStoreChanged reports that the index moved between the consent decision
// and the store lock; the caller re-runs so the review reflects reality.
var ErrStoreChanged = fmt.Errorf("the installed-package index changed while confirming; re-run to review the current state")

// LandSnapshot writes a snapshot and flips the index, ordered against
// process failure: snapshot directory completely first, index atomically
// second, superseded snapshot deleted last — a killed process leaves either
// the old state or the new, plus orphans sweepOrphans reclaims. No fsync
// barriers ride the renames, so power loss/kernel failure may still lose a
// publication that appeared to succeed; the recovery is a reinstall from the
// manifest URI (the source of truth is the archive, never this disk), which
// is why the stronger guarantee is deliberately not bought here. Call inside
// WithStoreLock.
func LandSnapshot(home string, s Snapshot) error {
	// The digest becomes a path component under packages/ that this
	// function both RemoveAll's (the Repair branch) and creates — fail
	// closed on shape before it touches anything. Live callers pass
	// PackageDigest output, so this only ever fires on a future caller's
	// bug, at the line where it would matter.
	if !digestDirRe.MatchString(s.Digest) {
		return fmt.Errorf("snapshot digest %q is malformed (want 64 lowercase hex chars)", s.Digest)
	}
	// Re-check the consent precondition under the lock (TOCTOU guard).
	idx0, err := ReadIndex(home)
	if err != nil {
		return err
	}
	if idx0[s.ID].Digest != s.ExpectPrior {
		return ErrStoreChanged
	}
	final := SnapshotDir(home, s.Digest)
	needWrite := false
	switch _, err := os.Stat(final); {
	case err == nil:
		// Same digest already on disk: content-addressed, nothing to write --
		// unless the caller is repairing a snapshot it knows is broken, in
		// which case the stale dir is removed and rewritten in full. (A crash
		// between the remove and the rename leaves the same broken state the
		// repair was fixing; the reinstall remedy still applies.)
		if s.Repair {
			if err := os.RemoveAll(final); err != nil {
				return err
			}
			needWrite = true
		}
	case !os.IsNotExist(err):
		// A Stat failure is NOT "already present": indexing a snapshot we
		// cannot prove exists breaks the snapshot-first, index-second
		// ordering guarantee.
		return err
	default:
		needWrite = true
	}
	if needWrite {
		stage, err := os.MkdirTemp(packagesDir(home), ".stage-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err := os.WriteFile(filepath.Join(stage, s.Primary), s.Manifest, 0o644); err != nil {
			return err
		}
		for dest, content := range s.Files {
			p := filepath.Join(stage, filepath.FromSlash(dest))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if s.Exec[dest] {
				mode = 0o755
			}
			if err := os.WriteFile(p, content, mode); err != nil {
				return err
			}
		}
		if err := os.Rename(stage, final); err != nil {
			return err
		}
	}

	old, had := idx0[s.ID]
	idx0[s.ID] = s.Entry
	if err := writeIndex(home, idx0); err != nil {
		return err
	}
	if had && old.Digest != s.Digest && !digestReferenced(idx0, old.Digest) {
		// Superseded snapshot deleted last; rollback is reinstalling the old
		// manifest URI -- the source is the archive, not our disk.
		removeSnapshot(home, old.Digest)
	}
	return nil
}

// removeSnapshot recursively deletes a snapshot dir by digest. The shape
// gate re-asserts what ReadIndex already enforced (defence in depth: this
// is a line where a bad value becomes an unconstrained rm -rf). The store's
// other recursive deletions carry their own gates: LandSnapshot validates
// s.Digest up front (covering its Repair remove), and sweepOrphans deletes
// only dirent names that match digestDirRe.
func removeSnapshot(home, digest string) {
	if !digestDirRe.MatchString(digest) {
		return
	}
	_ = os.RemoveAll(SnapshotDir(home, digest))
}

// RemoveInstalled drops an id from the index and deletes its snapshot when no
// other id references the digest. Call inside WithStoreLock.
func RemoveInstalled(home, id string) error {
	idx, err := ReadIndex(home)
	if err != nil {
		return err
	}
	old, ok := idx[id]
	if !ok {
		return fmt.Errorf("package %q is not installed", id)
	}
	delete(idx, id)
	if err := writeIndex(home, idx); err != nil {
		return err
	}
	if !digestReferenced(idx, old.Digest) {
		removeSnapshot(home, old.Digest)
	}
	return nil
}

func digestReferenced(idx map[string]IndexEntry, digest string) bool {
	for _, e := range idx {
		if e.Digest == digest {
			return true
		}
	}
	return false
}
