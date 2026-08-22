package packages

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostopen"
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
//
// Unlike the deleted Materialize path, this NEVER copies bundled packages
// into skills/ or templates/. The loader reads bundled bytes from embed only.
// A local dir wearing a protected (bundled or retired) name is the
// catalog's business: it ingests as an INVALID row with the rename remedy
// and is never loaded (the archive-legacy machinery retired 2026-08-23,
// ADR 0049 #4).
//
// bundled is the embed.FS (skills/ + templates/ tops). byreVer is the stamp
// and the version written into generated [package] headers in the mirror.
// out, when non-nil, receives human notices (mirror regen, legacy found).
func EnsureStore(home string, bundled fs.FS, byreVer string, out io.Writer) error {
	// NOT "bundled": every touch of the mirror path, its CREATION included,
	// happens under the store lock below. An unlocked MkdirAll here lands in
	// the window a locked writer's swap has the tree renamed aside -- so the
	// winner's rename-in meets an occupied path, its best-effort restore finds
	// the path taken too, and the only complete copy of the mirror is stranded
	// at bundled.old under a name nothing reads.
	for _, sub := range []string{"skills", "templates"} {
		if err := hostopen.PlainMkdirAll(filepath.Join(home, sub), 0o755, hostopen.StoreOwned); err != nil {
			return err
		}
	}
	if err := ensureAgentsMD(home, out); err != nil {
		return err
	}
	root := filepath.Join(home, "bundled")
	if bundled == nil {
		// No embedded FS (tests, partial fixtures): nothing to mirror, but the
		// dir is part of the store's shape either way (the tolerance
		// LoadCatalog extends). The CREATE still takes the lock. "This process
		// never swaps" is not the invariant that matters -- another process on
		// the same store may be mid-swap, and this one having nothing to write
		// says nothing about that one. A stat first keeps the ordinary case
		// lock-free, and like every other decision here it is made again under
		// the lock.
		// IsDir, not merely "the stat worked": a regular file sitting at the
		// mirror path is not a store byre can use, and treating a successful
		// stat as sufficient turned what MkdirAll used to refuse into a silent
		// success. Both checks, since the one under the lock is the one that
		// decides.
		if fi, err := hostopen.PlainStat(root, hostopen.StoreOwned); err != nil || !fi.IsDir() {
			if lerr := WithStoreLock(home, func() error {
				if fi, serr := hostopen.PlainStat(root, hostopen.StoreOwned); serr == nil && fi.IsDir() {
					return nil
				}
				return hostopen.PlainMkdirAll(root, 0o755, hostopen.StoreOwned)
			}); lerr != nil {
				return lerr
			}
		}
	} else if stale, _ := mirrorStale(home, root, byreVer); stale {
		// The regeneration happens under the store-global lock. writeMirror's
		// swap is three renames over one path, and EVERY byre command runs
		// EnsureStore -- so two starting at once (a develop and a status, two
		// worktree sessions) is ordinary, not a theoretical race, and their
		// swaps interleave into a ~/.byre/bundled that is missing or half a
		// tree.
		//
		// The check above is the steady-state fast path and NOTHING more: it is
		// right on every run but the one that upgrades, and everything it
		// decided is decided again under the lock. That re-decision is the
		// point -- the fast path is exactly what goes stale while queueing, so
		// the process that waited would otherwise regenerate a mirror the
		// winner just wrote, swapping the tree a third process may be reading.
		wrote := false
		if err := WithStoreLock(home, func() error {
			if stale, _ := mirrorStale(home, root, byreVer); !stale {
				return nil
			}
			if err := writeMirror(home, bundled, byreVer); err != nil {
				return fmt.Errorf("bundled mirror: %w", err)
			}
			stampPath := filepath.Join(home, stampName)
			if err := hostopen.PlainMkdirAll(filepath.Dir(stampPath), 0o755, hostopen.StoreOwned); err != nil {
				return err
			}
			if err := hostopen.PlainWriteFile(stampPath, []byte(byreVer+"\n"), 0o644, hostopen.StoreOwned); err != nil {
				return err
			}
			wrote = true
			return nil
		}); err != nil {
			return err
		}
		if wrote && out != nil {
			fmt.Fprintf(out, "byre: refreshed %s mirror for %s\n", DisplayPath(root), byreVer)
		}
	}

	return nil
}

// mirrorStale reports whether ~/.byre/bundled needs regenerating: the version
// stamp does not match, or the tree the stamp vouches for is not there. The
// second half is what lets the mirror's creation live under the lock -- a
// missing tree is a reason to take the lock, never a reason to mkdir past it.
// present is returned for the caller's own reporting.
func mirrorStale(home, root, byreVer string) (stale, present bool) {
	fi, err := hostopen.PlainStat(root, hostopen.StoreOwned)
	present = err == nil && fi.IsDir()
	cur, _ := hostopen.PlainReadFile(filepath.Join(home, stampName), hostopen.StoreOwned)
	return strings.TrimSpace(string(cur)) != byreVer || !present, present
}

// midSwap runs inside the swap's open window -- after the old mirror is
// renamed aside and before the new one is renamed in, the moment when
// ~/.byre/bundled does not exist. A no-op in production; the concurrency test
// holds the window open with it to prove nothing outside the lock recreates
// the path. A seam, because the window is where the bug lived and a test that
// cannot enter it can only assert the outcome it happens to get.
var midSwap = func() {}

// writeMirror regenerates ~/.byre/bundled from embed.FS with a README and
// generated [package] headers on primary files.
func writeMirror(home string, bundled fs.FS, byreVer string) error {
	root := filepath.Join(home, "bundled")
	// Replace the whole tree so deleted bundled packages disappear.
	tmp, err := hostopen.PlainMkdirTemp(home, ".bundled-new-", hostopen.StoreOwned)
	if err != nil {
		return err
	}
	defer hostopen.PlainRemoveAll(tmp, hostopen.ByreCreated)

	readme := `# Bundled packages (display copy)

These are display copies of the packages shipped inside your byre binary.
The loader never reads this directory -- edits are ignored and overwritten
on the next byre version change.

To modify a bundled package, fork it:

    byre skill fork <name> <your-id>
    byre template fork <name> <your-id>
`
	if err := hostopen.PlainWriteFile(filepath.Join(tmp, "README.md"), []byte(readme), 0o644, hostopen.ByreCreated); err != nil {
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
			return hostopen.PlainMkdirAll(out, 0o755, hostopen.ByreCreated)
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
		if err := hostopen.PlainMkdirAll(filepath.Dir(out), 0o755, hostopen.ByreCreated); err != nil {
			return err
		}
		return hostopen.PlainWriteFile(out, b, 0o644, hostopen.ByreCreated)
	})
	if err != nil {
		return err
	}

	// Atomic-ish swap: rename old aside, new in, drop old.
	old := root + ".old"
	_ = hostopen.PlainRemoveAll(old, hostopen.StoreOwned)
	if _, err := hostopen.PlainStat(root, hostopen.StoreOwned); err == nil {
		if err := hostopen.PlainRename(root, old, hostopen.StoreOwned); err != nil {
			return err
		}
	}
	midSwap()
	if err := hostopen.PlainRename(tmp, root, hostopen.StoreOwned); err != nil {
		_ = hostopen.PlainRename(old, root, hostopen.StoreOwned) // best-effort restore
		return err
	}
	_ = hostopen.PlainRemoveAll(old, hostopen.StoreOwned)
	return nil
}

// mirrorPrimary rewrites a primary file for the mirror: strip any existing
// package tree and prepend a generated header.
//
// No strict parse stands behind the strip here, unlike every other consumer:
// the mirror is display-only (ADR 0029), written from bytes byre SHIPS. A
// bundled manifest the strip cannot read comes through whole, so the header
// would be prepended to a file that still declares its own [package] -- and
// that is a defect in this repo's own skills, caught by the builtins golden
// tests long before a mirror is written.
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
