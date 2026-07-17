package packages

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Store stamp file: when its content matches the running byre version the
// bundled mirror is considered current. Regenerating on every version change
// is the mirror contract.
const stampName = "bundled/.byre-version"

// EnsureStore prepares ~/.byre for use under the package model:
//
//  1. Ensure skills/ and templates/ dirs exist.
//  2. Land the byre-owned AGENTS.md guide at the store root (rewritten
//     whenever it differs from the binary's copy).
//  3. On byre-version stamp mismatch: rewrite ~/.byre/bundled/ mirror from
//     embed.FS and update the stamp.
//  4. Surface LEGACY dirs (names matching bundled/retired) via the returned
//     notice; optional archive is a separate call (ArchiveLegacy).
//
// Unlike the deleted Materialize path, this NEVER copies bundled packages
// into skills/ or templates/. The loader reads bundled bytes from embed only.
//
// bundled is the embed.FS (skills/ + templates/ tops). byreVer is the stamp
// and the version written into generated [package] headers in the mirror.
// out, when non-nil, receives human notices (mirror regen, legacy found).
func EnsureStore(home string, bundled fs.FS, byreVer string, out io.Writer) error {
	for _, sub := range []string{"skills", "templates", "bundled"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			return err
		}
	}
	if err := ensureAgentsMD(home, out); err != nil {
		return err
	}
	stampPath := filepath.Join(home, stampName)
	cur, _ := os.ReadFile(stampPath)
	// A nil bundled FS (tests, partial fixtures) has no mirror to write --
	// same tolerance LoadCatalog extends.
	needMirror := bundled != nil && strings.TrimSpace(string(cur)) != byreVer
	if needMirror {
		if err := writeMirror(home, bundled, byreVer); err != nil {
			return fmt.Errorf("bundled mirror: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(stampPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(stampPath, []byte(byreVer+"\n"), 0o644); err != nil {
			return err
		}
		if out != nil {
			fmt.Fprintf(out, "byre: refreshed %s mirror for %s\n", DisplayPath(filepath.Join(home, "bundled")), byreVer)
		}
	}

	// Adoption-record sweep: pre-preset adoption records migrate to `applied`
	// markers (same concept -- the sha of what the project took on -- so
	// adopted projects land in the right drift state instead of losing
	// their history); sticky-decline records are deleted (with no
	// unsolicited prompt there is nothing to decline).
	sweepAdoptionRecords(home)

	// LEGACY notice: dirs under skills/templates whose bare names are
	// protected (bundled or retired). Never load them; offer archive once
	// per EnsureStore when any exist (caller may ignore the notice).
	legacy := findLegacyDirs(home, bundled)
	if len(legacy) > 0 && out != nil {
		fmt.Fprintf(out, "byre: found %d legacy materialized package dir(s) (never loaded):\n", len(legacy))
		for _, p := range legacy {
			fmt.Fprintf(out, "  %s\n", p)
		}
		fmt.Fprintln(out, "byre: to keep edits, fork first; to dismiss, run: byre skill archive-legacy")
		fmt.Fprintln(out, "      (or move them by hand to skills.legacy/ / templates.legacy/)")
	}
	return nil
}

// sweepAdoptionRecords is the adoption-record half of the migration sweep: per project
// store, `adopted` (the sha of the last adopted repo config) becomes an
// `applied` marker -- hash line, then a source line marking the migration --
// and `declined` records are removed. Idempotent, and it NEVER deletes the
// only copy of the history: `adopted` is removed only once an `applied`
// marker provably exists (already there, or the migrated write -- staged
// then hard-linked into place, so a crash cannot leave a truncated marker
// and a concurrent writer cannot be clobbered -- succeeded).
func sweepAdoptionRecords(home string) {
	entries, err := os.ReadDir(filepath.Join(home, "projects"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(home, "projects", e.Name())
		adopted := filepath.Join(dir, "adopted")
		applied := filepath.Join(dir, "applied")
		if b, err := os.ReadFile(adopted); err == nil {
			switch _, statErr := os.Stat(applied); {
			case statErr == nil:
				// A marker already exists; the old record is redundant.
				_ = os.Remove(adopted)
			case os.IsNotExist(statErr):
				h := strings.TrimSpace(string(b))
				// Create-if-absent, atomically: EnsureStore runs outside the
				// per-project setup lock, so a concurrent `preset apply` may
				// write a CURRENT marker between the stat and here -- a
				// replacing rename would clobber it with stale history.
				if createExclusive(applied, h+"\n(migrated from a pre-preset adoption record)\n") == nil {
					_ = os.Remove(adopted)
				}
				// Write failed (including lost the race): keep `adopted`;
				// the next sweep re-evaluates against the live marker.
			default:
				// Stat failed for an unknown reason: touch nothing.
			}
		}
		_ = os.Remove(filepath.Join(dir, "declined"))
	}
}

// createExclusive lands content at path atomically AND only if path does not
// exist: full bytes staged to a temp file, then hard-linked into place --
// link(2) fails on an existing destination, unlike rename(2), which replaces.
func createExclusive(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), path)
}

// findLegacyDirs returns store-relative paths of flat skill/template dirs
// whose names match a currently-bundled or retired bare name.
func findLegacyDirs(home string, bundled fs.FS) []string {
	protected := map[string]bool{}
	for bare := range RetiredNames {
		protected[bare] = true
	}
	for _, sub := range []string{"skills", "templates"} {
		if bundled == nil {
			break
		}
		entries, err := fs.ReadDir(bundled, sub)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				protected[e.Name()] = true
			}
		}
	}
	var out []string
	for _, sub := range []string{"skills", "templates"} {
		entries, err := os.ReadDir(filepath.Join(home, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if protected[e.Name()] {
				// Confirm it looks like a package (has primary file).
				prim := "skill.toml"
				if sub == "templates" {
					prim = "template.config"
				}
				if _, err := os.Stat(filepath.Join(home, sub, e.Name(), prim)); err == nil {
					out = append(out, filepath.Join(sub, e.Name()))
				}
			}
		}
	}
	return out
}

// ArchiveLegacy moves LEGACY dirs to skills.legacy/ and templates.legacy/
// (one-confirm archive). Returns the paths moved.
func ArchiveLegacy(home string, bundled fs.FS) ([]string, error) {
	legacy := findLegacyDirs(home, bundled)
	var moved []string
	for _, rel := range legacy {
		src := filepath.Join(home, rel)
		// rel is skills/<name> or templates/<name>
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) != 2 {
			parts = strings.SplitN(rel, "/", 2)
		}
		if len(parts) != 2 {
			continue
		}
		bakRoot := parts[0] + ".legacy"
		dstDir := filepath.Join(home, bakRoot)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return moved, err
		}
		dst := filepath.Join(dstDir, parts[1])
		// If destination exists, unique-ify.
		if _, err := os.Stat(dst); err == nil {
			tmp, terr := os.MkdirTemp(dstDir, parts[1]+".")
			if terr != nil {
				return moved, fmt.Errorf("archive %s: %w", rel, terr)
			}
			os.Remove(tmp) // MkdirTemp created a dir; we want the name for Rename
			dst = tmp
		}
		if err := os.Rename(src, dst); err != nil {
			return moved, fmt.Errorf("archive %s: %w", rel, err)
		}
		moved = append(moved, rel+" -> "+filepath.Join(bakRoot, filepath.Base(dst)))
	}
	return moved, nil
}

// writeMirror regenerates ~/.byre/bundled from embed.FS with a README and
// generated [package] headers on primary files.
func writeMirror(home string, bundled fs.FS, byreVer string) error {
	root := filepath.Join(home, "bundled")
	// Replace the whole tree so deleted bundled packages disappear.
	tmp, err := os.MkdirTemp(home, ".bundled-new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	readme := `# Bundled packages (display copy)

These are display copies of the packages shipped inside your byre binary.
The loader never reads this directory -- edits are ignored and overwritten
on the next byre version change.

To modify a bundled package, fork it:

    byre skill fork <name> <your-id>
    byre template fork <name> <your-id>
`
	if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte(readme), 0o644); err != nil {
		return err
	}

	err = fs.WalkDir(bundled, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == "." {
			return nil
		}
		out := filepath.Join(tmp, p)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := fs.ReadFile(bundled, p)
		if err != nil {
			return err
		}
		// Inject generated [package] into primary files when absent / always
		// refresh the frozen core fields for the mirror's human readers.
		base := filepath.Base(p)
		if base == "skill.toml" || base == "template.config" {
			b = mirrorPrimary(p, b, byreVer)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		return err
	}

	// Atomic-ish swap: rename old aside, new in, drop old.
	old := root + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(root); err == nil {
		if err := os.Rename(root, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, root); err != nil {
		_ = os.Rename(old, root) // best-effort restore
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// mirrorPrimary rewrites a primary file for the mirror: strip any existing
// [package] table and prepend a generated header.
func mirrorPrimary(embedPath string, raw []byte, byreVer string) []byte {
	// embedPath like skills/claude/skill.toml or templates/go/template.config
	parts := strings.Split(filepath.ToSlash(embedPath), "/")
	if len(parts) < 3 {
		return raw
	}
	kind := KindSkill
	if parts[0] == "templates" {
		kind = KindTemplate
	}
	bare := parts[1]
	id := BundledID(bare)
	desc := peekDescription(raw)
	body := StripPackageTable(raw)
	// Also strip a top-level description that moved into [package], to avoid
	// duplicate keys when the body still carries one -- leave body as-is;
	// stage-2 skill parse still accepts top-level description. Mirror is
	// display-only; both is fine.
	hdr := GenerateBundledHeader(id, string(kind), byreVer, desc)
	return append([]byte(hdr), body...)
}
