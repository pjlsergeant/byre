package configui

import (
	"os"
	"testing"
)

// Package-local fixture helpers (see commands/musthelpers_test.go for the
// rationale; per-package copies are the idiom, a shared test package is not).

func mustWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
