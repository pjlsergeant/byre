// Package builtins ships byre's built-in skills embedded in the binary and
// materializes them into ~/.byre/skills/ so they are loadable (and inspectable
// and editable) like any hand-dropped skill.
package builtins

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

//go:embed skills templates
var fsys embed.FS

// MaterializeSkills writes the built-in skills into destDir (~/.byre/skills).
func MaterializeSkills(destDir string) error { return materialize("skills", destDir) }

// Change records one skill or template changed by an update: its name and
// where its prior copy is kept (Backup is the backup slot, or a leftover stash
// path if the backup couldn't be published; "" only when newly installed).
type Change struct {
	Name   string
	Backup string
}

// UpdateSkills re-materializes the built-in skills into destDir, OVERWRITING
// existing copies with the shipped version (unlike Materialize, which never
// clobbers). A replaced copy that differs is preserved in an append-only backup
// under the sibling skills.bak/ dir. Returns the changes (installed or updated),
// sorted by name. Hand-dropped (non-built-in) skills are not touched.
func UpdateSkills(destDir string) ([]Change, error) { return update("skills", destDir) }

// UpdateTemplates is UpdateSkills for the built-in templates (backups land in
// templates.bak/) — so shipped template changes have the same pickup path as
// shipped skill changes.
func UpdateTemplates(destDir string) ([]Change, error) { return update("templates", destDir) }

// update re-materializes the embedded subtree sub into destDir, overwriting
// stale copies and preserving differing ones in an append-only <destDir>.bak/.
func update(sub, destDir string) ([]Change, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	names, err := fs.ReadDir(fsys, sub)
	if err != nil {
		return nil, err
	}
	var updated []Change
	for _, e := range names {
		if !e.IsDir() {
			continue
		}
		target := filepath.Join(destDir, e.Name())
		// Render the embedded version into a temp dir alongside the target.
		tmp, err := os.MkdirTemp(destDir, "."+e.Name()+"-new-")
		if err != nil {
			return nil, err
		}
		if err := copyTree(filepath.Join(sub, e.Name()), tmp); err != nil {
			os.RemoveAll(tmp)
			return nil, err
		}
		// Identical to what's on disk → nothing to do.
		if same, serr := sameTree(tmp, target); serr != nil {
			os.RemoveAll(tmp)
			return nil, serr
		} else if same {
			os.RemoveAll(tmp)
			continue
		}
		// Swap the new copy in. If a copy exists, stash it aside first (a dot-named
		// sibling, so listings ignore it — see ListAgentSkills), then commit the
		// new one, restoring the stash if the commit fails so a skill can never go
		// missing. backupStash then preserves the stash under an append-only,
		// unique name in skills.bak/ — it NEVER deletes or overwrites an existing
		// backup, so no edit (this one or an earlier one) can ever be lost.
		if _, err := os.Stat(target); err == nil {
			stash := tmp + "-old"
			if err := os.Rename(target, stash); err != nil {
				os.RemoveAll(tmp)
				return nil, err
			}
			if err := os.Rename(tmp, target); err != nil {
				os.RemoveAll(tmp)
				if rerr := os.Rename(stash, target); rerr != nil {
					return nil, fmt.Errorf("update of %q failed (%w); restore failed (%v) — the prior copy is at %s", e.Name(), err, rerr, stash)
				}
				return nil, fmt.Errorf("update of %q failed: %w", e.Name(), err)
			}
			updated = append(updated, Change{Name: e.Name(), Backup: backupStash(destDir, e.Name(), stash)})
		} else {
			if err := os.Rename(tmp, target); err != nil {
				os.RemoveAll(tmp)
				return nil, err
			}
			updated = append(updated, Change{Name: e.Name()})
		}
	}
	sort.Slice(updated, func(i, j int) bool { return updated[i].Name < updated[j].Name })
	return updated, nil
}

// backupStash preserves a replaced skill copy (already stashed aside) under an
// append-only, UNIQUE name in skills.bak/ (e.g. skills.bak/codex.A1b2). It never
// removes or overwrites an existing backup, so it cannot lose data — every update
// of a differing copy keeps its own backup. Best-effort: on any failure the stash
// is left in place (dot-named, ignored by listings) and stays recoverable.
// Returns the path to the prior copy: the published backup slot on success, or —
// if it couldn't be published — the leftover stash, so the caller can always
// disclose where the prior copy is. (Never returns "": there IS a prior copy.)
func backupStash(destDir, name, stash string) string {
	bakRoot := destDir + ".bak"
	if err := os.MkdirAll(bakRoot, 0o755); err != nil {
		return stash
	}
	// Reserve a unique slot name, then replace the reserved empty dir with the
	// stash. The name is unique, so this never clobbers an earlier backup.
	slot, err := os.MkdirTemp(bakRoot, name+".")
	if err != nil {
		return stash
	}
	if err := os.RemoveAll(slot); err != nil {
		return stash
	}
	if err := os.Rename(stash, slot); err != nil {
		return stash
	}
	return slot
}

// sameTree reports whether dirs a and b contain the same set of files with
// identical contents. A missing b is reported as not-same (not an error).
func sameTree(a, b string) (bool, error) {
	fa, err := fileSet(a)
	if err != nil {
		return false, err
	}
	fb, err := fileSet(b)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(fa) != len(fb) {
		return false, nil
	}
	for rel, ba := range fa {
		if bb, ok := fb[rel]; !ok || !bytes.Equal(ba, bb) {
			return false, nil
		}
	}
	return true, nil
}

// fileSet maps each regular file under dir (by relative path) to its contents.
func fileSet(dir string) (map[string][]byte, error) {
	set := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		set[rel] = b
		return nil
	})
	return set, err
}

// MaterializeTemplates writes the built-in templates into destDir
// (~/.byre/templates).
func MaterializeTemplates(destDir string) error { return materialize("templates", destDir) }

// materialize writes each built-in entry under embedded subdir `sub` into
// destDir/<name>/ if that directory does not already exist. Existing entries are
// never overwritten, so a user's edits (or a newer hand-dropped version) win.
//
// Each entry is copied into a temp dir and atomically renamed into place, so an
// interrupted or concurrent run never leaves a partially-copied entry that a
// later run would mistake for complete.
func materialize(sub, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	names, err := fs.ReadDir(fsys, sub)
	if err != nil {
		return err
	}
	for _, e := range names {
		if !e.IsDir() {
			continue
		}
		target := filepath.Join(destDir, e.Name())
		if _, err := os.Stat(target); err == nil {
			continue // already present — don't clobber
		} else if !os.IsNotExist(err) {
			return err
		}

		tmp, err := os.MkdirTemp(destDir, "."+e.Name()+"-tmp-")
		if err != nil {
			return err
		}
		if err := copyTree(filepath.Join(sub, e.Name()), tmp); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		if err := os.Rename(tmp, target); err != nil {
			os.RemoveAll(tmp)
			// Lost a race (another run created it) — that's fine.
			if _, statErr := os.Stat(target); statErr == nil {
				continue
			}
			return err
		}
	}
	return nil
}

// copyTree copies an embedded directory subtree to a destination directory.
func copyTree(embeddedDir, dest string) error {
	return fs.WalkDir(fsys, embeddedDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(embeddedDir, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}
