package builtins

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shared-auth library is ONE authored file, dual-shipped: five skills
// stage it to /usr/local/lib/byre-shared-auth-lib.sh so each works alone,
// and ADR 0056 lets identical staged bytes compose while refusing anything
// else. The canonical copy is internal/builtins/byre-shared-auth-lib.sh,
// outside the embedded skills tree; every skill dir holds a copy of it.
//
// Copies, not a symlink into the canonical file: `go:embed` silently SKIPS a
// symlink, so a linked copy would never ship at all and every hook that
// sources it would refuse to do its job in a real box. This test is what
// keeps the copies honest -- the five shipped bytes are compared to the
// canonical file, and the loser of a drift would otherwise be decided by
// COPY order at build time.
func TestSharedAuthLibCopiesAreIdentical(t *testing.T) {
	canonical, err := os.ReadFile("byre-shared-auth-lib.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, cat := testCat(t)
	for _, skill := range []string{"codex-shared-auth", "gemini-shared-auth", "opencode-shared-auth", "codex", "opencode"} {
		shipped := filepath.Join(skillDir(t, cat, skill), "byre-shared-auth-lib.sh")
		got, err := os.ReadFile(shipped)
		if err != nil {
			t.Errorf("%s ships no shared-auth library: %v", skill, err)
			continue
		}
		if !bytes.Equal(got, canonical) {
			t.Errorf("%s's copy of the shared-auth library differs from the canonical internal/builtins/byre-shared-auth-lib.sh — edit the canonical file and copy it to internal/builtins/skills/%s/byre-shared-auth-lib.sh", skill, skill)
		}
	}
}

// `chmod <mode> --` works on GNU and FAILS on BSD: BSD chmod takes the mode as
// its first operand and its getopt stops there, so a `--` written after the
// mode is left as a second operand, read as a filename, and the command exits
// non-zero. `chmod -- <mode> FILE` is the ordering both accept. No Linux run
// can catch the wrong one (GNU chmod permutes and honours a trailing `--`),
// and these hooks run under BSD tools on the macOS CI leg -- where the wrong
// ordering fails every promotion and every codex publish, silently in the
// hooks that discard chmod's stderr. Hence a source pin over the shipped
// bytes.
func TestShippedHooksPutChmodModeAfterTheDashes(t *testing.T) {
	wrong := regexp.MustCompile(`chmod [0-7]+ --`)
	files, err := filepath.Glob(filepath.Join("skills", "*", "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "byre-shared-auth-lib.sh")
	// Floor, so a glob that stops matching fails instead of passing vacuously.
	if len(files) < 10 {
		t.Fatalf("expected the builtin skills' shell files, found only %d: %v", len(files), files)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		for n, line := range strings.Split(string(b), "\n") {
			if wrong.MatchString(line) {
				t.Errorf("%s:%d: the mode must come AFTER the `--` (BSD chmod's getopt stops at the mode operand, so the `--` becomes a filename): %s",
					f, n+1, strings.TrimSpace(line))
			}
		}
	}
}

// driveSharedAuthLib sources the canonical library and calls one of its
// functions with the given arguments, returning its exit status. The library is
// the unit here: the three shared-auth hooks all refuse a directory that is
// ALREADY at the shared path before they reach the promote, so only a race puts
// one there -- and a race is not what this needs to pin. The contract is.
func driveSharedAuthLib(t *testing.T, args ...string) int {
	t.Helper()
	driver := filepath.Join(physTempDir(t), "drive.sh")
	if err := os.WriteFile(driver, []byte("#!/bin/bash\nset -u\n. \"$1\"\nshift\n\"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	lib, err := filepath.Abs("byre-shared-auth-lib.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{driver, lib}, args...)...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("driving the library failed: %v (%s)", err, out)
	}
	if len(out) != 0 {
		t.Errorf("the library must print nothing; the caller owns the vocabulary: %q", out)
	}
	return ee.ExitCode()
}

// A claim the library refuses must leave NOTHING behind inside a directory at
// the shared path. `ln` and `mv` cannot take GNU's -T (BSD ln and mv on the
// macOS CI leg have none), so they put their source INSIDE a directory
// destination: by the time the inode checks reject the claim, this box's login
// already exists as <dir>/<temp name>, mode 600, on the machine-scoped identity
// volume every sibling box reads -- under a refusal that says the login stayed
// local. The refusal was always right; the leftover was the bug.
func TestSharedAuthLibRefusedPromoteLeavesNothingInADirectory(t *testing.T) {
	local := filepath.Join(physTempDir(t), "auth.json")
	if err := os.WriteFile(local, []byte(`{"login":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(physTempDir(t), "auth.json")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	// 2 is "something else is there": the caller re-vets the shared path and
	// refuses, which is what the opencode and gemini hooks do with a directory.
	if rc := driveSharedAuthLib(t, "byre_sa_promote", local, shared); rc != 2 {
		t.Fatalf("byre_sa_promote onto a directory = %d, want 2 (lost/refused)", rc)
	}
	if ents, err := os.ReadDir(shared); err != nil || len(ents) != 0 {
		t.Fatalf("the refused claim left this box's login inside the directory at the shared path: %v (%v)", ents, err)
	}
	isRegularWith(t, local, `{"login":true}`)
}
