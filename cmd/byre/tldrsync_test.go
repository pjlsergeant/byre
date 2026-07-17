package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestHowDoITldrsMatchSite is P6's enforcement arm for the "How do I...?"
// index: the README keeps a SUBSET of the cookbook's entries (the
// show-off slots), each question plus its tldr VERBATIM. Character-for-
// character identity is the rot control -- a paraphrase drifts silently,
// an exact copy is diffable. The cookbook may carry entries the README
// doesn't; every README entry must exist in the cookbook with an
// identical tldr.
func TestHowDoITldrsMatchSite(t *testing.T) {
	readme := readFileT(t, "../../README.md")
	files, err := filepath.Glob("../../site/content/docs/how-do-i/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("globbing cookbook pages: %v (%d files)", err, len(files))
	}
	sort.Strings(files)
	var cookbook strings.Builder
	for _, f := range files {
		cookbook.WriteString(readFileT(t, f))
		cookbook.WriteString("\n")
	}

	got := readmeIndexPairs(readme)
	want := cookbookPairs(cookbook.String())

	if len(got) == 0 || len(want) == 0 {
		t.Fatalf("extracted %d README pairs and %d cookbook pairs -- extraction broke, fix the test", len(got), len(want))
	}
	if len(got) > len(want) {
		t.Errorf("README index has %d entries but the cookbook only %d -- an index entry without its recipe", len(got), len(want))
	}
	tldrs := map[string]string{}
	for _, p := range want {
		if _, dup := tldrs[p[0]]; dup {
			t.Errorf("cookbook question %q appears twice", p[0])
		}
		tldrs[p[0]] = p[1]
	}
	for _, p := range got {
		cb, ok := tldrs[p[0]]
		if !ok {
			t.Errorf("README index entry %q has no cookbook recipe (question must match the cookbook heading exactly)", p[0])
			continue
		}
		if p[1] != cb {
			t.Errorf("entry %q: tldr not verbatim\nREADME:   %q\ncookbook: %q", p[0], p[1], cb)
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
		// Only the paragraph-final newline comes off: interior trailing
		// spaces (markdown hard breaks) must count as drift.
		pairs = append(pairs, [2]string{m[1], strings.TrimSuffix(m[2], "\n")})
	}
	return pairs
}

// cookbookPairs parses the cookbook's group pages (concatenated): each ##
// heading ending in "?" is a question, and its first paragraph must be
// the tldr.
func cookbookPairs(cookbook string) [][2]string {
	var pairs [][2]string
	blocks := strings.Split(cookbook, "\n## ")[1:]
	for _, blk := range blocks {
		heading, rest, ok := strings.Cut(blk, "\n\n")
		if !ok || !strings.HasSuffix(heading, "?") {
			continue
		}
		para := rest
		if end := strings.Index(para, "\n\n"); end >= 0 {
			para = para[:end]
		}
		// Compare raw -- trailing spaces (markdown hard breaks) and any
		// HTML-escaping a tldr would need are drift, not noise.
		pairs = append(pairs, [2]string{heading, para})
	}
	return pairs
}
