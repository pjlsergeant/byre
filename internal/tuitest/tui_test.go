package tuitest

// First-wave TUI tests (the pty tier of the TUI-harness design): the shipped
// binary under a real tmux pane. Engine-free by construction — config edits
// --global, and the beat tests run with a PATH that resolves neither
// clipboard readers nor container engines (headless-ness is ENFORCED, never
// assumed: an inherited DISPLAY plus an installed xclip would silently flip
// the beat from degraded to live).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storeEnv is a fresh BYRE_HOME for one test.
func storeEnv(t *testing.T) (string, map[string]string) {
	t.Helper()
	store := t.TempDir()
	return store, map[string]string{"BYRE_HOME": store}
}

// downTo walks the global editor's fixed field order from the top (focus
// starts on the first GRANTS field) to the Base image row: Mounts, Ports,
// Egress, Env, Template, Agent, Base.
const downsToBase = 6

func TestIntegrationTUIConfigSaveThenQuit(t *testing.T) {
	Require(t)
	store, env := storeEnv(t)
	s := Start(t, Opts{Env: env}, Binary(t), "config", "--global")

	s.WaitFor("GRANTS")
	keys := make([]string, downsToBase)
	for i := range keys {
		keys[i] = "Down"
	}
	s.Keys(keys...)
	s.Type("debian:13")
	e := s.Keys("C-s")
	s.WaitForAfter(e, "Saved ✓")

	s.Keys("Escape") // clean after save: one esc quits
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	if final := s.CaptureNow(); !strings.Contains(final, "byre: wrote") {
		t.Fatalf("no write confirmation on exit:\n%s", final)
	}
	b, err := os.ReadFile(filepath.Join(store, "default.config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `base = "debian:13"`) {
		t.Fatalf("default.config = %q", b)
	}
}

func TestIntegrationTUIConfigCancelDiscards(t *testing.T) {
	Require(t)
	store, env := storeEnv(t)
	s := Start(t, Opts{Env: env}, Binary(t), "config", "--global")

	s.WaitFor("GRANTS")
	keys := make([]string, downsToBase)
	for i := range keys {
		keys[i] = "Down"
	}
	s.Keys(keys...)
	s.Type("junk")
	e := s.Keys("Escape") // dirty: first esc only arms the confirm
	s.WaitForAfter(e, "press esc/^q/^c again to discard")
	s.Keys("Escape")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	if final := s.CaptureNow(); !strings.Contains(final, "byre: config unchanged.") {
		t.Fatalf("no unchanged notice:\n%s", final)
	}
	if _, err := os.Stat(filepath.Join(store, "default.config")); !os.IsNotExist(err) {
		t.Fatalf("discarded edit still wrote default.config (stat err %v)", err)
	}
}

// beatEnv is the enforced-headless environment: a PATH resolving neither
// clipboard readers nor container engines, display variables unset.
func beatEnv(t *testing.T) Opts {
	t.Helper()
	_, env := storeEnv(t)
	env["PATH"] = t.TempDir() // empty: no xclip, no wl-paste, no docker
	return Opts{Env: env, Unset: []string{"DISPLAY", "WAYLAND_DISPLAY"}}
}

func TestIntegrationTUIBeatCancelDegraded(t *testing.T) {
	Require(t)
	s := Start(t, beatEnv(t), Binary(t), "deliver")

	s.WaitFor("no clipboard access here")
	s.Keys("C-c")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("cancel should exit 0, got %d\n%s", st, s.CaptureNow())
	}
	final := s.CaptureNow()
	if n := strings.Count(final, "cancelled — nothing delivered"); n != 1 {
		t.Fatalf("cancel notice appeared %d times, want exactly 1:\n%s", n, final)
	}
}

func TestIntegrationTUIBeatPasteDeliversText(t *testing.T) {
	Require(t)
	s := Start(t, beatEnv(t), Binary(t), "deliver")

	s.WaitFor("no clipboard access here")
	s.Paste("hello from a real bracketed paste")
	s.Keys("C-d")
	// The paste was accepted as the delivery's text source; with no engine
	// on PATH the delivery itself must then fail LOUDLY (never a silent
	// zero-box claim — the field-found Finder-launch bug).
	s.WaitFor("no container engine")
	if st := s.WaitForExit(); st == 0 {
		t.Fatalf("engine-less delivery should exit nonzero\n%s", s.CaptureNow())
	}
	final := s.CaptureNow()
	if strings.Contains(final, "cancelled") {
		t.Fatalf("paste path took the cancel branch:\n%s", final)
	}
}
