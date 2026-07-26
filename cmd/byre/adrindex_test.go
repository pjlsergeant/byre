package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The doctrine index (docs/adr/README.md) is the artifact reviewers check
// diffs against, and its [arm]/[no arm] markers are how a reader tells an
// enforced rule from a convention. Both halves rot silently without these
// two arms: TestDoctrineIndexCoversCorpus fails when an ADR or principle
// lacks an index line (or the index names a ghost), and
// TestDoctrineIndexArmsResolve fails when a named arm no longer matches a
// test function in the repo.

var indexEntryRe = regexp.MustCompile(`(?m)^- (\d{4}|P\d+): .+ \[(?:arm: ([A-Za-z0-9_]+(?:, [A-Za-z0-9_]+)*)|no arm)\]$`)

func doctrineIndexEntries(t *testing.T) map[string][]string {
	t.Helper()
	index := readFileT(t, "../../docs/adr/README.md")
	entries := map[string][]string{}
	for _, m := range indexEntryRe.FindAllStringSubmatch(index, -1) {
		if _, dup := entries[m[1]]; dup {
			t.Errorf("index entry %s appears twice", m[1])
		}
		var arms []string
		if m[2] != "" {
			arms = strings.Split(m[2], ", ")
		}
		entries[m[1]] = arms
	}
	if len(entries) == 0 {
		t.Fatal("no index entries parsed from docs/adr/README.md -- format drifted, fix the index or this regexp")
	}
	return entries
}

func TestDoctrineIndexCoversCorpus(t *testing.T) {
	entries := doctrineIndexEntries(t)

	adrs, err := filepath.Glob("../../docs/adr/[0-9][0-9][0-9][0-9]-*.md")
	if err != nil || len(adrs) == 0 {
		t.Fatalf("globbing ADRs: %v (%d files)", err, len(adrs))
	}
	corpus := map[string]bool{}
	for _, f := range adrs {
		id := filepath.Base(f)[:4]
		corpus[id] = true
		if _, ok := entries[id]; !ok {
			t.Errorf("ADR %s has no line in docs/adr/README.md", filepath.Base(f))
		}
	}

	principles := readFileT(t, "../../docs/PRINCIPLES.md")
	for _, m := range regexp.MustCompile(`(?m)^## (\d+)\. `).FindAllStringSubmatch(principles, -1) {
		id := "P" + m[1]
		corpus[id] = true
		if _, ok := entries[id]; !ok {
			t.Errorf("principle %s has no line in docs/adr/README.md", id)
		}
	}

	for id := range entries {
		if !corpus[id] {
			t.Errorf("index entry %s matches no ADR file and no principle heading", id)
		}
	}
}

func TestDoctrineIndexArmsResolve(t *testing.T) {
	entries := doctrineIndexEntries(t)

	testFuncs := map[string]bool{}
	funcRe := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "site" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range funcRe.FindAllStringSubmatch(string(b), -1) {
			testFuncs[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repo for test functions: %v", err)
	}

	for id, arms := range entries {
		for _, arm := range arms {
			if !testFuncs[arm] {
				t.Errorf("index entry %s names arm %s, which matches no test function in the repo", id, arm)
			}
		}
	}
}
