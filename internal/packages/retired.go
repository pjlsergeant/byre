package packages

// RetiredNames is the permanent in-binary table of bare names that a past
// byre release bundled and a later release does not. They stay
// protected exactly like bundled bare names -- no local or installed
// package may claim them; legacy dirs bearing them are LEGACY rows.
//
// Map values are one-line tombstones for remedy text. The pinned install
// hints are a migration aid and may be trimmed to bare "retired; see CHANGES"
// text in a later release; the name protection itself is permanent.
var RetiredNames = map[string]string{
	"codereview": "moved out of byre (2026-07-13) -- install it: byre skill install https://raw.githubusercontent.com/pjlsergeant/pjlsergeant-byre-skills/v1.0.2/skills/codereview/skill.toml --digest sha256:3d0bf433dfab52ad947e125987d50b44aa47000414bbe14023bbcf80277ade86, then reference pjlsergeant/codereview",
	"devlog":     "moved out of byre (2026-07-13) -- install it: byre skill install https://raw.githubusercontent.com/pjlsergeant/pjlsergeant-byre-skills/v1.0.0/skills/devlog/skill.toml --digest sha256:9ecb65b18386ceea0dc54b7bb040b42e29a9872ab8fed4f9b1f86d5562926c12, then reference pjlsergeant/devlog",
	// devloop was devlog's pre-rename name (2026-07-12); a description-only
	// compat stub carried it until the sunset (see CHANGES). Same remedy.
	"devloop": "renamed to devlog, then moved out of byre -- install it: byre skill install https://raw.githubusercontent.com/pjlsergeant/pjlsergeant-byre-skills/v1.0.0/skills/devlog/skill.toml --digest sha256:9ecb65b18386ceea0dc54b7bb040b42e29a9872ab8fed4f9b1f86d5562926c12, then reference pjlsergeant/devlog",
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
