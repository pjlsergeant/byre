package editorcmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/hostexec"
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
	cmd, err := Command(editor, target, hostexec.NewRoots())
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, rerr := os.ReadFile(log)
	if rerr != nil {
		t.Fatal(rerr)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(got) != 2 || got[0] != "--wait" || got[1] != target {
		t.Fatalf("args = %q, want [--wait %s]", got, target)
	}
}

// byre picks the SHELL here, so it answers to the resolution rule: a shell
// resolved out of a directory the box writes is declined, and the caller gets
// the refusal instead of a command. (The editor VALUE stays the user's own
// opaque fragment -- nothing here parses or judges it.)
func TestCommandDeclinesShellUnderBoxWritableRoot(t *testing.T) {
	sh, err := hostexec.Look("sh", hostexec.NewRoots())
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	// Name the resolved shell's own directory as box-writable: whatever PATH
	// answered with, it now falls inside a declining root.
	cmd, err := Command("vi", "/tmp/x", hostexec.NewRoots(filepath.Dir(sh)))
	if cmd != nil {
		t.Error("a declined shell must yield no command to run")
	}
	if !errors.As(err, new(*hostexec.ShadowError)) {
		t.Fatalf("err = %v, want *hostexec.ShadowError", err)
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
