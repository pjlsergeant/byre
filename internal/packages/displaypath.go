package packages

import (
	"os"
	"path/filepath"
	"strings"
)

// DisplayPath renders a store path for human notices: the user's real home
// prefix contracts to "~" for readability, and any other root (BYRE_HOME
// overrides outside $HOME, tests) prints as-is. Notices must name the path
// byre actually used — a hardcoded "~/.byre" lies under BYRE_HOME (field-QA
// 2026-07-17, finding 1).
func DisplayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rel
	}
	return p
}
