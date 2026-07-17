package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestHowDoITldrsMatchSite is P6's enforcement arm for the "How do I...?"
// index: the README keeps each question plus its tldr VERBATIM, the site
// cookbook keeps the same pair above the full recipe. Character-for-
// character identity is the rot control -- a paraphrase drifts silently,
// an exact copy is diffable. This test extracts the ordered
// (question, tldr) pairs from both surfaces and compares the lot.
func TestHowDoITldrsMatchSite(t *testing.T) {
	readme := readFileT(t, "../../README.md")
	cookbook := readFileT(t, "../../site/content/docs/how-do-i.md")

	got := readmeIndexPairs(readme)
	want := cookbookPairs(cookbook)

	if len(got) == 0 || len(want) == 0 {
		t.Fatalf("extracted %d README pairs and %d cookbook pairs -- extraction broke, fix the test", len(got), len(want))
	}
	if len(got) != len(want) {
		t.Errorf("README index has %d entries, cookbook has %d -- every recipe earns an index slot and vice versa", len(got), len(want))
	}
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i][0] != want[i][0] {
			t.Errorf("entry %d: question differs\nREADME:   %q\ncookbook: %q", i, got[i][0], want[i][0])
			continue
		}
		if got[i][1] != want[i][1] {
			t.Errorf("entry %q: tldr not verbatim\nREADME:   %q\ncookbook: %q", got[i][0], got[i][1], want[i][1])
		}
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// readmeIndexPairs parses the README's "How do I...?" index: bold question
// lines, each followed by a tldr paragraph that ends at the ([recipe]...)
// link line.
func readmeIndexPairs(readme string) [][2]string {
	_, section, ok := strings.Cut(readme, "\n## How do I...?\n")
	if !ok {
		return nil
	}
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	var pairs [][2]string
	q := regexp.MustCompile(`(?m)^\*\*(.+\?)\*\*\n((?:.+\n)+?)\(\[recipe\]`)
	for _, m := range q.FindAllStringSubmatch(section, -1) {
		pairs = append(pairs, [2]string{m[1], strings.TrimSpace(m[2])})
	}
	return pairs
}

// cookbookPairs parses the site cookbook: each ## heading is a question,
// and its first paragraph must be the tldr.
func cookbookPairs(cookbook string) [][2]string {
	var pairs [][2]string
	blocks := strings.Split(cookbook, "\n## ")[1:]
	for _, blk := range blocks {
		heading, rest, ok := strings.Cut(blk, "\n\n")
		if !ok {
			continue
		}
		para := rest
		if end := strings.Index(para, "\n\n"); end >= 0 {
			para = para[:end]
		}
		// The site renders <agent> as &lt;agent&gt; where needed; tldrs
		// must not need that, so compare raw.
		pairs = append(pairs, [2]string{strings.TrimSpace(heading), strings.TrimSpace(para)})
	}
	return pairs
}
