package config

import (
	"os"
	"strings"
	"testing"
)

// This package's tests resolve against the local-only catalog BY NAME:
// production gets the real loader from builtins' init, and an uninstalled
// CatalogLoader is an error, not a fallback — so the choice the old silent
// fallback used to make is spelled here, once, where a reader can see it.
// Tests that need a fixture catalog swap CatalogLoader themselves and
// restore the previous value.
func TestMain(m *testing.M) {
	CatalogLoader = LoadLocalOnlyCatalog
	os.Exit(m.Run())
}

// An uninstalled CatalogLoader is a composition bug and must fail loudly
// naming the remedy — the silent bundled-less fallback it replaces
// resolved WRONG with no signal (ADR 0049 residue, closed 2026-08-23).
// Both resolution and the cascade walk refuse.
func TestUninstalledCatalogLoaderIsALoudError(t *testing.T) {
	prev := CatalogLoader
	CatalogLoader = nil
	t.Cleanup(func() { CatalogLoader = prev })

	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	if _, err := Load(proj); err == nil ||
		!strings.Contains(err.Error(), "CatalogLoader is not installed") ||
		!strings.Contains(err.Error(), "builtins") {
		t.Errorf("Load must refuse naming the seam and the remedy, got: %v", err)
	}
	if _, err := CascadeFiles(proj); err == nil ||
		!strings.Contains(err.Error(), "CatalogLoader is not installed") {
		t.Errorf("CascadeFiles must refuse the same way Load does, got: %v", err)
	}
}

// mergeT folds a raw over-layer onto a raw base layer — the two-layer
// cascade the merge-rule tests exercise; the returned view carries the
// fold's closures. mergeM continues a fold from an accumulated view, the
// way the real cascade threads its accumulator.
func mergeT(base, over Config) Merged {
	c, cl := mergeStep(base, Closures{}, over)
	return Merged{Config: c, Closures: cl}
}

func mergeM(base Merged, over Config) Merged {
	c, cl := mergeStep(base.Config, base.Closures, over)
	return Merged{Config: c, Closures: cl}
}
