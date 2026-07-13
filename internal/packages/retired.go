package packages

// RetiredNames is the permanent in-binary table of bare names that a past
// byre release bundled and a later release does not (D15). They stay
// protected exactly like bundled bare names -- no local or installed
// package may claim them; legacy dirs bearing them are LEGACY rows (D10).
//
// Phase 1: the table is wired and empty of *active* retirees. codereview
// and devlog stay bundled until phase 3 (D12 move). The keys below are the
// planned retirees, kept here as documentation of the shape; Populate when
// the move lands. Until then Protected() only covers live bundled bare
// names.
//
// Map values are one-line tombstones for D9e remedy text.
var RetiredNames = map[string]string{
	// Phase 3 will activate these when the packages leave the binary:
	// "codereview": "retired from the binary; install pjlsergeant/codereview (see CHANGES)",
	// "devlog":     "retired from the binary; install pjlsergeant/devlog (see CHANGES)",
}

// RetiredTombstone returns the tombstone for a retired bare name, or "".
func RetiredTombstone(bare string) string {
	return RetiredNames[bare]
}

// IsRetired reports whether bare is in the retired table.
func IsRetired(bare string) bool {
	_, ok := RetiredNames[bare]
	return ok
}
