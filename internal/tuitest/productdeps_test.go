package tuitest

// These imports are never referenced: they put the product in this test
// binary's build, which the subprocess build of ./cmd/byre (Binary) does
// not. `go list -test -deps ./internal/tuitest` shows no product package
// without them, so a product package that stops COMPILING leaves this tier
// building and green.
//
// They are NOT the tier's cache key: `go test` keys a cached result on the
// test binary's content, and the linker drops blank-imported code nothing
// references, so an edit to a configui screen leaves this binary byte-
// identical. recordProductSources (tuitest.go) is what makes a product edit
// -- or a product file appearing or disappearing -- re-run the tier.
//
// internal/commands transitively reaches every byre package cmd/byre embeds;
// the rest of cmd/byre's own import block is named alongside it so the
// correspondence is auditable line by line instead of resting on a
// transitive fact a future import can quietly break.

import (
	_ "github.com/pjlsergeant/byre/internal/build"
	_ "github.com/pjlsergeant/byre/internal/commands"
	_ "github.com/pjlsergeant/byre/internal/deliver"
	_ "github.com/pjlsergeant/byre/internal/hostopen"
	_ "github.com/pjlsergeant/byre/internal/packages"
	_ "github.com/pjlsergeant/byre/internal/version"
)
