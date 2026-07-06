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

// report snapshots dir, applies mutate, and returns the exit report's output.
func report(t *testing.T, dir string, mutate func()) string {
	t.Helper()
	before := snapshotStore(dir)
	mutate()
	var out bytes.Buffer
	reportSelfEditChanges(&out, dir, before)
	return out.String()
}

func TestReportSelfEditChanges(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "byre.config")
	ctx := filepath.Join(dir, "context")
	if err := os.MkdirAll(ctx, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(cfg, "base = \"node:22\"\n")
	write(filepath.Join(ctx, "Dockerfile.generated"), "FROM node:22\n")

	t.Run("untouched store is silent", func(t *testing.T) {
		if got := report(t, dir, func() {}); got != "" {
			t.Errorf("expected silence: %q", got)
		}
	})

	t.Run("config edit shows a content diff", func(t *testing.T) {
		got := report(t, dir, func() { write(cfg, "base = \"node:22\"\nrun_args = [\"--privileged\"]\n") })
		if !strings.Contains(got, "byre.config (applies on the next develop):") ||
			!strings.Contains(got, `+ run_args = ["--privileged"]`) {
			t.Errorf("config diff wrong: %q", got)
		}
		if strings.Contains(got, "changed:") {
			t.Errorf("config must not ALSO appear in the file listing: %q", got)
		}
	})

	t.Run("other store files are listed by status", func(t *testing.T) {
		got := report(t, dir, func() {
			write(filepath.Join(ctx, "Dockerfile.generated"), "FROM evil\n")
			write(filepath.Join(ctx, "planted.sh"), "#!/bin/sh\n")
			if err := os.Remove(filepath.Join(dir, "byre.config")); err != nil {
				t.Fatal(err)
			}
		})
		for _, want := range []string{
			"changed: context/Dockerfile.generated",
			"added:   context/planted.sh",
			"(deleted)", // byre.config, in its own section
			`- base = "node:22"`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in: %q", want, got)
			}
		}
		if strings.Contains(got, "deleted: byre.config") {
			t.Errorf("byre.config must not ALSO appear in the file listing: %q", got)
		}
	})

	// State from the previous subtest: no byre.config, planted.sh present.
	t.Run("created empty config is reported", func(t *testing.T) {
		got := report(t, dir, func() { write(cfg, "") })
		if !strings.Contains(got, "(created)") {
			t.Errorf("expected the created report: %q", got)
		}
	})

	t.Run("deleted empty config is reported", func(t *testing.T) {
		got := report(t, dir, func() {
			if err := os.Remove(cfg); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(got, "(deleted)") {
			t.Errorf("expected the deleted report: %q", got)
		}
	})

	t.Run("trailing-newline-only edit is named", func(t *testing.T) {
		write(cfg, "base = \"node:22\"\n")
		got := report(t, dir, func() { write(cfg, "base = \"node:22\"") })
		if !strings.Contains(got, "trailing-newline-only") {
			t.Errorf("expected the trailing-newline note: %q", got)
		}
	})

	t.Run("retargeted symlink is a change", func(t *testing.T) {
		a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		write(a, "a\n")
		write(b, "b\n")
		link := filepath.Join(dir, "link")
		if err := os.Symlink(a, link); err != nil {
			t.Fatal(err)
		}
		got := report(t, dir, func() {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(b, link); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(got, "changed: link") {
			t.Errorf("expected the retargeted symlink reported: %q", got)
		}
	})
}
