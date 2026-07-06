package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// reportSelfEditDiff closes a --self-edit session by showing what the agent
// changed in byre.config against the pre-session snapshot -- the edit applies
// on the next develop, and this is the user's one pushed chance to see it
// before then. Silent when nothing changed.
func reportSelfEditDiff(w io.Writer, path string, before []byte) {
	after, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(w, "byre: cannot re-read %s to report self-edit changes: %v\n", path, err)
		return
	}
	// A missing file reads as nil, an existing empty file as a non-nil empty
	// slice — bytes.Equal can't tell them apart, so compare existence too
	// (creating or deleting an EMPTY config is still a change worth reporting).
	beforeMissing, afterMissing := before == nil, err != nil
	if beforeMissing == afterMissing && bytes.Equal(before, after) {
		return
	}
	fmt.Fprintln(w, "🛑 self-edit: the agent changed byre.config this session. The diff applies on the next develop:")
	if afterMissing {
		fmt.Fprintln(w, "   (byre.config was deleted)")
	} else if beforeMissing {
		fmt.Fprintln(w, "   (byre.config was created)")
	}
	lines := diffLines(string(before), string(after))
	// Content changed but no line differs: the only edit was the final newline,
	// which splitLines normalizes away. Say so rather than printing a bare header.
	if len(lines) == 0 && !beforeMissing && !afterMissing {
		fmt.Fprintln(w, "   (trailing-newline-only change)")
	}
	for _, l := range lines {
		fmt.Fprintln(w, "   "+l)
	}
}

// diffLines is a minimal line diff (LCS): changed lines only, "- " removed and
// "+ " added, in file order. Configs are tiny, so the O(n*m) table is fine and
// context lines aren't worth the hunking machinery -- TOML lines mostly carry
// their own context.
func diffLines(before, after string) []string {
	a, b := splitLines(before), splitLines(after)
	// lcs[i][j] = length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, "- "+a[i])
	}
	for ; j < len(b); j++ {
		out = append(out, "+ "+b[j])
	}
	return out
}

// splitLines splits file content into lines without a trailing phantom empty
// line ("" means no lines, so a created-from-nothing file diffs as all "+").
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
