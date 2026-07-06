package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiffLines(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		want          []string
	}{
		{"identical", "a\nb\n", "a\nb\n", nil},
		{"add", "a\n", "a\nb\n", []string{"+ b"}},
		{"remove", "a\nb\n", "a\n", []string{"- b"}},
		{"modify", "a\nb\nc\n", "a\nB\nc\n", []string{"- b", "+ B"}},
		{"create from nothing", "", "a\nb\n", []string{"+ a", "+ b"}},
		{"delete everything", "a\n", "", []string{"- a"}},
		{"insert between anchors", "a\nz\n", "a\nm\nz\n", []string{"+ m"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diffLines(c.before, c.after); !reflect.DeepEqual(got, c.want) {
				t.Errorf("diffLines(%q, %q) = %v, want %v", c.before, c.after, got, c.want)
			}
		})
	}
}

func TestReportSelfEditDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "byre.config")

	// Unchanged (including both-missing): silence.
	var out bytes.Buffer
	reportSelfEditDiff(&out, path, nil)
	if out.Len() != 0 {
		t.Errorf("expected silence for a missing, unsnapshotted config: %q", out.String())
	}
	if err := os.WriteFile(path, []byte("base = \"node:22\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	reportSelfEditDiff(&out, path, []byte("base = \"node:22\"\n"))
	if out.Len() != 0 {
		t.Errorf("expected silence for an unchanged config: %q", out.String())
	}

	// Changed: header + markers.
	out.Reset()
	reportSelfEditDiff(&out, path, []byte("base = \"debian:bookworm\"\n"))
	got := out.String()
	if !strings.Contains(got, "changed byre.config") ||
		!strings.Contains(got, `- base = "debian:bookworm"`) ||
		!strings.Contains(got, `+ base = "node:22"`) {
		t.Errorf("diff output wrong: %q", got)
	}

	// Trailing-newline-only change: header plus an explicit note, not a bare
	// header with no diff lines.
	if err := os.WriteFile(path, []byte("base = \"node:22\""), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	reportSelfEditDiff(&out, path, []byte("base = \"node:22\"\n"))
	if !strings.Contains(out.String(), "trailing-newline-only") {
		t.Errorf("expected the trailing-newline note: %q", out.String())
	}

	// Created EMPTY during the session (snapshot nil, file exists with no
	// content): existence is the change, and it must be reported.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	reportSelfEditDiff(&out, path, nil)
	if !strings.Contains(out.String(), "was created") {
		t.Errorf("expected the created-empty report: %q", out.String())
	}

	// Deleted during the session: named as deleted, lines shown as removed.
	if err := os.WriteFile(path, []byte("base = \"node:22\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	reportSelfEditDiff(&out, path, []byte("base = \"node:22\"\n"))
	if !strings.Contains(out.String(), "was deleted") || !strings.Contains(out.String(), `- base = "node:22"`) {
		t.Errorf("expected the deletion report with removed lines: %q", out.String())
	}

	// Deleted EMPTY config: still a reported change (existence flipped).
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	reportSelfEditDiff(&out, path, []byte{})
	if !strings.Contains(out.String(), "was deleted") {
		t.Errorf("expected the deleted-empty report: %q", out.String())
	}
}
