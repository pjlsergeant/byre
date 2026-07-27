package commands

import "github.com/akedrou/textdiff"

// unifiedDiff is THE human diff for byre-owned config files (the preset-apply
// review and the self-edit exit report), as lines ready for the caller's own
// prefixing. Unified-with-context (the gopls differ, vendored upstream copy):
// TOML array-of-tables lines don't identify their block on their own — a bare
// `- mode = "rw"` can't say WHICH [[mounts]] it belongs to — so the context
// lines are load-bearing for a consent prompt, not cosmetic. (Supersedes the
// hand-rolled changed-lines-only LCS, whose "TOML lines mostly carry their
// own context" ruling was falsified by exactly that case, 2026-07-10.)
// Empty when before and after are byte-identical; a final-newline-only edit
// shows as an explicit "\ No newline at end of file" hunk.
//
// On the dependency: akedrou/textdiff is a single-author pre-1.0 module, and
// it stays. One call, a pure function over two strings -- no network, no
// filesystem, no process -- so the blast radius of it going wrong is a
// wrongly-rendered diff, not a compromise; and the content it renders is
// byre's own config files, shown to a human who is about to answer a
// consent prompt about them. If it were ever abandoned, the vendored gopls
// differ it came from is the replacement.
func unifiedDiff(fromLabel, toLabel, before, after string) []string {
	return splitLines(textdiff.Unified(fromLabel, toLabel, before, after))
}
