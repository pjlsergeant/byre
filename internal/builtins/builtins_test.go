package builtins

import (
	"testing"

	"github.com/pjlsergeant/byre/internal/packages"
)

// testCat builds a catalog over a fresh home with bundled embed.FS.
func testCat(t *testing.T) (home string, cat *packages.Catalog) {
	t.Helper()
	home = t.TempDir()
	cat, err := packages.LoadCatalog(home, FS(), "0.2.0", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return home, cat
}

// skillDir returns the host directory for a bundled/local skill (extracted embed).
func skillDir(t *testing.T, cat *packages.Catalog, name string) string {
	t.Helper()
	ent, err := cat.ResolveName(name)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := ent.HostDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
