package packages

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// copyTree copies src to dst preserving the executable bit -- Pack records
// `executable = true` per payload, so a mode-losing copy would produce a
// manifest that differs from the committed one for the wrong reason.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRepoSkillsAreCommittedPacked pins the freshness of every skill.toml
// that lives in THIS repo (skills/*): re-packing the directory must reproduce
// the committed manifest byte for byte.
//
// Pack writes an EXHAUSTIVE [[package.files]] list, so adding, removing or
// editing any payload file invalidates the committed manifest. Nothing else
// in the unit suite notices -- the staleness surfaces only later, at
// `byre skill install`, on a hash mismatch. That has now bitten twice
// (f87d0d8, then again when skills/inttest/dind/ was added), caught both
// times by a human. This test is the machine catching it instead.
//
// Pack is documented as a fixed point, which is what makes byte equality the
// right assertion rather than a looser one.
func TestRepoSkillsAreCommittedPacked(t *testing.T) {
	root := filepath.Join("..", "..", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		// Not a checkout of this repo (a consumer vendoring the package, say):
		// there is nothing to pin, and failing would be noise.
		t.Skipf("no %s directory: %v", root, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		primary := filepath.Join(dir, "skill.toml")
		committed, err := os.ReadFile(primary)
		if err != nil {
			continue // not a skill package
		}
		t.Run(e.Name(), func(t *testing.T) {
			m, _, err := ParseManifestCore(committed)
			if err != nil {
				t.Fatalf("committed manifest does not parse: %v", err)
			}
			if m.ID == "" || IsBare(m.ID) {
				t.Fatalf("committed manifest declares no qualified id (got %q)", m.ID)
			}

			// Pack works on a LOCAL package, and the catalog id must match the
			// declared one -- so lay the copy out under a throwaway home at
			// skills/<owner>/<name>.
			home := t.TempDir()
			copyTree(t, dir, filepath.Join(home, "skills", filepath.FromSlash(m.ID)))

			// A deliberately-high byre version: this test pins PACKING, not
			// requires_byre satisfaction, and the real version is empty in a
			// dev build. A skill whose constraint outruns the release is a
			// separate concern with its own failure at install.
			cat, err := LoadCatalog(home, nil, "v999.0.0", "999.0.0", Stage2Hooks{})
			if err != nil {
				t.Fatal(err)
			}
			ent, err := cat.ResolveName(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			packed, _, err := Pack(ent)
			if err != nil {
				t.Fatalf("re-pack failed: %v", err)
			}

			if string(packed) != string(committed) {
				// Repo-relative, not test-relative: the remedy has to be
				// runnable as printed, from the repo root.
				rel := "skills/" + e.Name()
				t.Errorf("%s/skill.toml is stale -- re-packing does not reproduce it.\n"+
					"Run (from the repo root): scripts/repack-skill.sh %s\n"+
					"(a payload file was added, removed or edited without re-packing)",
					rel, rel)
			}
		})
	}
}
