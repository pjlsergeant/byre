package tuitest

// The config-UI screen walker: proof of life for every sub-screen the
// project editor can reach without an engine. This is deliberately NOT
// behavior coverage (that stays demand-pull, per ADR 0038's discipline) —
// it pins one thing: each screen opens, paints on a real pty, and esc comes
// back out. The render-crash/blank-screen class (the inline renderer's
// height quirk is per-View) is exactly what a walker sees and a model test
// can't. One stable anchor per screen, nothing about contents.
//
// Not walked, and why: the per-row action menu on a field with no rows (a
// fresh config has none — adding one is behavior, not proof of life); the
// volume DATA screen, which needs an engine (its declaration list is walked;
// only the engine-backed list-and-clear is excluded, and a VM-tier sibling
// can add it the day it earns one).
//
// The Down-counts below are positions in the project editor's focus order,
// which is assembled in internal/configui/form.go (newModel's sections). That
// order is pinned by TestProjectEditorFocusOrder in package configui, which
// names this file: a field inserted mid-form desyncs every count after it,
// and finding that out here costs a whole runner cycle.

import (
	"strings"
	"testing"
)

func TestIntegrationTUIConfigScreenWalk(t *testing.T) {
	Require(t)
	_, env := storeEnv(t)
	s := Start(t, Opts{Env: env, Dir: t.TempDir()}, Binary(t), "config")

	// The form. Focus starts on the first GRANTS field (Extra mounts).
	// Only the sections above the fold are asserted here: the form is taller
	// than a 30-row pty, so clipHeight windows it and the lower sections paint
	// when the cursor reaches them (EXTENDS is asserted at its own row below).
	// "the always-shown section" stopped being true the day the form grew.
	s.WaitFor("GRANTS")
	s.WaitFor("BUILD")

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

	// The env_from_host scheme picker, reached from the Env screen (three
	// rows down from Mounts). Walked because it is the one item editor whose
	// picker renders FIRST and rewrites a label as it moves -- the paint a
	// model test cannot prove. byre ships six passthrough keys, so a fresh
	// config always has a row to open.
	e = s.Keys("Down", "Down", "Down", "Enter")
	s.WaitForAfter(e, "a add")
	e = s.Keys("Enter")
	s.WaitForAfter(e, "Override here")
	e = s.Keys("Enter")
	s.WaitForAfter(e, "Source")
	// Focus lands on Key (the common path types straight into it), so reach
	// the picker before driving it -- ←/→ in an input moves the cursor.
	e = s.Keys("Up", "Right")
	s.WaitForAfter(e, "host variable")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "a add")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// The seed_prefs tri-state: Env vars → Seed prefs is five rows down (Base,
	// Template, Agent, Engine, Seed prefs). Walked because it is the only FORM
	// row driven by ←/→ that the walk passes, and its three states are what a
	// checkbox could not say. Nothing about the row's text — the dirty banner
	// is the proof it took the keypress — and Left puts it back, so the
	// walker still exits with nothing written.
	e = s.Keys("Down", "Down", "Down", "Down", "Down", "Right")
	s.WaitForAfter(e, "ctrl+s to save")
	e = s.Keys("Left")
	s.WaitForAfter(e, "No unsaved changes")

	// modeSkills: Seed prefs → Skills is two rows down (Packages, Skills).
	e = s.Keys("Down", "Down", "Enter")
	s.WaitForAfter(e, "space toggle")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// Skill files: one past Skills, read-only -- its own screen because "what
	// did my skills bake in" is a different question from "what am I staging
	// for the build".
	e = s.Keys("Down", "Enter")
	s.WaitForAfter(e, "read-only")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// Package sources: one past Skill files, the other read-only screen. Its
	// explainer names the flow that writes [sources], which is also what
	// distinguishes it from Skill files (both say "read-only"). A fresh config
	// has no hints, so the screen renders empty — which is the point: it opens
	// and paints without content to lean on.
	e = s.Keys("Down", "Enter")
	s.WaitForAfter(e, "preset apply")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// EXTENDS paints once the cursor scrolls to it: four rows past Package
	// sources (MCP servers, Claude Skills, Instructions, Extends). clipHeight
	// keeps the cursor row on screen, so reaching the row is what proves the
	// section renders — the old assertion assumed it was never clipped.
	s.Keys("Down", "Down", "Down", "Down")
	s.WaitFor("EXTENDS")

	// modeText: Extends → Run args is one more.
	e = s.Keys("Down", "Enter")
	s.WaitForAfter(e, "accept + save")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// Build files: one past Run args, in ADVANCED with the raw Dockerfile
	// blocks it feeds. A list field with its own item editor, like mounts.
	e = s.Keys("Down", "Enter")
	s.WaitForAfter(e, "a add")
	e = s.Keys("a")
	s.WaitForAfter(e, "Add Build file")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "a add")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// The volume DECLARATION list: three past Build files (Dockerfile before,
	// Dockerfile after, Volumes), last in ADVANCED. Walkable with no engine --
	// what a config SAYS about its volumes is grammar, and only the data
	// screen beside it needs a daemon to answer.
	e = s.Keys("Down", "Down", "Down", "Enter")
	s.WaitForAfter(e, "a add")
	e = s.Keys("a")
	s.WaitForAfter(e, "Add Volume")
	e = s.Keys("Escape")
	s.WaitForAfter(e, "a add")
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
