package configui

import (
	"os"
	"testing"
)

// Fixture helper: setup failures abort the test at the fixture line instead
// of letting it run against a file it never wrote.

func mustWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
