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
// diffs against, and its [arm]/[arm(gated)]/[no arm] markers are how a
// reader tells an enforced rule from a convention -- and an arm that fires
// in `go test ./...` from one only CI's gated job ever runs. All of it rots
// silently without these two arms: TestDoctrineIndexCoversCorpus fails when
// an ADR or principle lacks an index line (or the index names a ghost), and
// TestDoctrineIndexArmsResolve fails when a named arm no longer matches a
// test function in the repo, or when its (gated) marker no longer matches
// where that test actually lives.

var indexEntryRe = regexp.MustCompile(`(?m)^- (\d{4}|P\d+): .+ \[(?:arm(\(gated\))?: ([A-Za-z0-9_]+(?:, [A-Za-z0-9_]+)*)|no arm)\]$`)

// indexEntry is one index line's enforcement claim.
type indexEntry struct {
	arms  []string
	gated bool // marked [arm(gated): ...]: runs only behind BYRE_DOCKER_TESTS/BYRE_TUI_TESTS
}

func doctrineIndexEntries(t *testing.T) map[string]indexEntry {
	t.Helper()
	index := readFileT(t, "../../docs/adr/README.md")
	entries := map[string]indexEntry{}
	for _, m := range indexEntryRe.FindAllStringSubmatch(index, -1) {
		if _, dup := entries[m[1]]; dup {
			t.Errorf("index entry %s appears twice", m[1])
		}
		var arms []string
		if m[3] != "" {
			arms = strings.Split(m[3], ", ")
		}
		entries[m[1]] = indexEntry{arms: arms, gated: m[2] != ""}
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

// gatedTestFile reports whether this file is one where every test rides a
// tier gate. It judges the file's NAME, which is a convention the repo
// keeps, and not its contents: a file's text says nothing reliable here --
// tuitest holds ordinary unit tests beside its pty ones, and a comment
// mentioning BYRE_DOCKER_TESTS gates nothing at all. So: gated tests live
// in a file whose name says "integration", or under internal/tuitest.
func gatedTestFile(path string) bool {
	p := filepath.ToSlash(path)
	return strings.Contains(filepath.Base(p), "integration") ||
		strings.Contains(p, "internal/tuitest/")
}

func TestDoctrineIndexArmsResolve(t *testing.T) {
	entries := doctrineIndexEntries(t)

	// arm name -> the files defining it (a name repeated across packages
	// gets every definition checked; the marker has to fit them all).
	testFuncs := map[string][]string{}
	gatedFile := map[string]bool{}
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
		gatedFile[path] = gatedTestFile(path)
		for _, m := range funcRe.FindAllStringSubmatch(string(b), -1) {
			testFuncs[m[1]] = append(testFuncs[m[1]], path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repo for test functions: %v", err)
	}

	for id, e := range entries {
		for _, arm := range e.arms {
			files := testFuncs[arm]
			if len(files) == 0 {
				t.Errorf("index entry %s names arm %s, which matches no test function in the repo", id, arm)
				continue
			}
			// Both directions: an unmarked arm behind a gate overstates what
			// `go test ./...` proves, and a (gated) marker on a test that runs
			// everywhere understates it -- either way the marker misleads the
			// reviewer who is deciding how much this entry is worth reading.
			for _, f := range files {
				switch {
				case e.gated && !gatedFile[f]:
					t.Errorf("index entry %s marks arm %s [arm(gated)], but %s is not a gated-tier file -- gated tests live in an *integration*_test.go or under internal/tuitest; move the test or drop the (gated)", id, arm, f)
				case !e.gated && gatedFile[f]:
					t.Errorf("index entry %s marks arm %s [arm], but %s is a gated-tier file (an *integration*_test.go, or internal/tuitest) -- mark it [arm(gated)]", id, arm, f)
				}
			}
		}
	}
}
