package tuitest

// The config-UI screen walker: proof of life for every sub-screen the
// project editor can reach without an engine. This is deliberately NOT
// behavior coverage (that stays demand-pull, per ADR 0038's discipline) —
// it pins one thing: each screen opens, paints on a real pty, and esc comes
// back out. The render-crash/blank-screen class (the inline renderer's
// height quirk is per-View) is exactly what a walker sees and a model test
// can't. One stable anchor per screen, nothing about contents.
//
// Not walked, and why: the per-row action menu needs an existing list row
// (a fresh config has none — adding one is behavior, not proof of life);
// the volumes admin needs an engine (a VM-tier sibling can add it the day
// it earns one).

import (
	"strings"
	"testing"
)

func TestIntegrationTUIConfigScreenWalk(t *testing.T) {
	Require(t)
	_, env := storeEnv(t)
	s := Start(t, Opts{Env: env, Dir: t.TempDir()}, Binary(t), "config")

	// The form. Focus starts on the first GRANTS field (Extra mounts).
	s.WaitFor("GRANTS")
	s.WaitFor("EXTENDS") // the always-shown section, before any scrolling

	// modeList: Enter on Extra mounts. The anchor is the list footer — the
	// form shows the same field LABEL, so labels can't prove the screen.
	e := s.Keys("Enter")
	s.WaitForAfter(e, "a add")

	// modeItem: the add editor over the mounts list.
	e = s.Keys("a")
	s.WaitForAfter(e, "Add Extra mount")

	// Back out one screen at a time — two Escapes in one send-keys arrive
	// as \x1b\x1b, which bubbletea reads as a single alt-modified key
	// (found live: the pair vanished and the walker stayed in the editor).
	// Separate sends, each proving its screen.
	e = s.Keys("Escape")
	s.WaitForAfter(e, "a add")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// modeSkills: Mounts → Skills is nine rows down (Ports, Egress, Env,
	// Base, Template, Agent, Engine, Packages, Skills).
	keys := make([]string, 9)
	for i := range keys {
		keys[i] = "Down"
	}
	e = s.Keys(append(keys, "Enter")...)
	s.WaitForAfter(e, "space toggle")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// modeText: Skills → Run args is four more (MCP servers, Claude
	// Skills, Extends, Run args).
	e = s.Keys("Down", "Down", "Down", "Down", "Enter")
	s.WaitForAfter(e, "accept + save")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// Nothing was edited: one quit key suffices and nothing was written.
	s.Keys("C-q")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	if final := s.CaptureNow(); !strings.Contains(final, "byre: config unchanged.") {
		t.Fatalf("walker left a mark:\n%s", final)
	}
}
