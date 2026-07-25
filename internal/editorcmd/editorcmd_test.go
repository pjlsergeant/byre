package editorcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The launcher must honor shell semantics: a quoted executable path with
// spaces plus flags — the shape a whitespace split can never parse.
func TestCommandHonorsShellQuoting(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "my editor.sh")
	log := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "prose.md")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	editor := `"` + bin + `" --wait`
	if err := Command(editor, target).Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(got) != 2 || got[0] != "--wait" || got[1] != target {
		t.Fatalf("args = %q, want [--wait %s]", got, target)
	}
}

func TestResolveFallsBackToVi(t *testing.T) {
	t.Setenv("EDITOR", "   ")
	if got := Resolve(); got != "vi" {
		t.Fatalf("Resolve() = %q", got)
	}
	t.Setenv("EDITOR", "code -w")
	if got := Resolve(); got != "code -w" {
		t.Fatalf("Resolve() = %q", got)
	}
}
