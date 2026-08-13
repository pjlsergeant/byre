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
// Egress, Env, Template, Agent, Base. Credentials stopped being a field of
// its own when credential values moved inline onto the Env screen's rows.
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
	// Cancel exits 1: nothing was
	// delivered, so scripts must see nonzero; the notice stays for humans.
	if st := s.WaitForExit(); st != 1 {
		t.Fatalf("cancel should exit 1 (nothing delivered), got %d\n%s", st, s.CaptureNow())
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

// Exit code 2 is byre's usage-error contract, and until here nothing
// observed it as a PROCESS status: the run()-level tests assert a
// usageError value, and the mapping from that value to os.Exit(2) lives in
// main(), past the seam they reach. So does the print beside it -- the one
// exit boundary that does not go through fatal().
//
// The operand is hostile for that second reason: an ESC and a newline in
// argv must reach the screen as DATA. The rejection quotes the operand with
// %q, so the whole thing lands on one line with the control bytes spelled
// out -- an ESC that survived would have moved the cursor instead of
// printing, and a newline that survived would have framed a line of its own
// under byre's message.
func TestIntegrationTUIUsageErrorExitsTwo(t *testing.T) {
	Require(t)
	_, env := storeEnv(t)
	const hostile = "boom\n\x1b[31mred"
	s := Start(t, Opts{Env: env, Dir: t.TempDir()}, Binary(t), "config", hostile)

	s.WaitFor(`["boom\n\x1b[31mred"]`)
	if st := s.WaitForExit(); st != 2 {
		t.Fatalf("a malformed invocation must exit 2 (the usage-error contract), got %d\n%s", st, s.CaptureNow())
	}
	// A floor, not the proof above it: capture-pane renders escapes rather
	// than reporting them, so this catches only what a captured screen could
	// still carry.
	if final := s.CaptureNow(); strings.ContainsRune(final, 0x1b) {
		t.Fatalf("raw escape on the screen:\n%q", final)
	}
}

// A passthrough added through the Source picker must appear ON THE SCREEN
// that added it, before any save. This is the pty tier because the model
// tier cannot fail it: the model DID hold the new key -- m.hostEnv had it,
// the save wrote it -- while the rows were built from the config as loaded,
// so the row never painted and neither count moved. Only a test that reads
// the pane can tell "the state is right" from "the user can see it".
//
// The screen's own duplicate check is what made the silence loud in the
// field: a second add of the same key answers "duplicate key" while naming
// something the list does not contain.
func TestIntegrationTUIAddedPassthroughAppearsOnScreen(t *testing.T) {
	Require(t)
	_, env := storeEnv(t)
	s := Start(t, Opts{Env: env, Dir: t.TempDir()}, Binary(t), "config")

	s.WaitFor("GRANTS")
	// Env vars is three below the first GRANTS field (Mounts, Ports, Egress).
	e := s.Keys("Down", "Down", "Down", "Enter")
	s.WaitForAfter(e, "a add")
	e = s.Keys("a")
	s.WaitForAfter(e, "Add Env var")

	// Focus lands on Key. Reach the Source picker with Up, then walk it to
	// env: (value -> git: -> env:); ←/→ inside an input would move the
	// cursor instead, which is why the walk starts by leaving the field.
	s.Type("QA_NEW")
	e = s.Keys("Up", "Right", "Right")
	s.WaitForAfter(e, "host variable")
	// Down twice (Source -> Key -> argument) and name a host variable. The
	// row beside "host variable" is a dim PLACEHOLDER, not a value: accepting
	// without typing here is refused, correctly, as an invalid env var name.
	s.Keys("Down", "Down")
	e = s.Type("HOME")
	s.WaitForAfter(e, "HOME") // typed text is async; accept only once painted
	e = s.Keys("Enter")

	// The assertion has to be a string only the LIST can show. "QA_NEW"
	// alone is on the editor too (its Key field), so waiting for it would
	// pass without the row ever painting -- which is the whole bug.
	s.WaitForAfter(e, "QA_NEW <- host env:HOME")

	s.Keys("Escape")
	form := s.WaitFor("$EDITOR")
	// The form's summary counts it too: byre ships six passthroughs, so a
	// seventh is the proof the tally reads the live edit and not the file.
	if !strings.Contains(form, "7 vars") {
		t.Fatalf("form summary did not count the added passthrough:\n%s", form)
	}
}
