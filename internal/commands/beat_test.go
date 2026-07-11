package commands

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- the LIVE beat (Bubble Tea model) ---

func beatKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func TestBeatModelCtrlVIsTheGesture(t *testing.T) {
	m, cmd := beatModel{action: beatCancelled}.Update(beatKey(tea.KeyCtrlV))
	if got := m.(beatModel); got.action != beatGesture || cmd == nil {
		t.Fatalf("action = %v cmd = %v", got.action, cmd)
	}
}

func TestBeatModelPasteCapturesTextAsEvidence(t *testing.T) {
	// Cmd-V arrives as a bracketed paste; the text is returned so the caller
	// can tell a real clipboard paste from a drag-typed path (field-found
	// 2026-07-10: drags paste text that was never on the pasteboard).
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/Users/p/authors.txt "), Paste: true}
	m, cmd := beatModel{action: beatCancelled}.Update(msg)
	got := m.(beatModel)
	if got.action != beatPaste || string(got.text) != "/Users/p/authors.txt " || cmd == nil {
		t.Fatalf("action=%v text=%q cmd=%v", got.action, got.text, cmd)
	}
}

func TestBeatModelCtrlCCancels(t *testing.T) {
	m, cmd := beatModel{action: beatCancelled}.Update(beatKey(tea.KeyCtrlC))
	if got := m.(beatModel); got.action != beatCancelled || cmd == nil {
		t.Fatalf("action = %v cmd = %v", got.action, cmd)
	}
}

func TestBeatModelEnterIsNotAGesture(t *testing.T) {
	m, cmd := beatModel{action: beatCancelled}.Update(beatKey(tea.KeyEnter))
	if got := m.(beatModel); got.action != beatCancelled || cmd != nil {
		t.Fatalf("enter must not fire or quit: action=%v cmd=%v", got.action, cmd)
	}
}

func TestBeatModelSampleUpdatesPromptAndReschedules(t *testing.T) {
	m, cmd := beatModel{}.Update(clipSampleMsg{types: []string{"image/png"}})
	got := m.(beatModel)
	if !strings.Contains(got.View(), "image on the clipboard") {
		t.Fatalf("view = %q", got.View())
	}
	if cmd == nil {
		t.Fatal("a sample must schedule the next tick")
	}
}

func TestBeatModelViewTruncatesToWidth(t *testing.T) {
	// The hand-rolled redraw stacked lines when the prompt wrapped; the tea
	// view truncates to the window instead.
	m, _ := beatModel{}.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m2, _ := m.Update(clipSampleMsg{types: []string{"image/png"}})
	view := m2.(beatModel).View()
	visible := 0
	inEsc := false
	for _, r := range view {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		default:
			visible++
		}
	}
	if visible > 30 {
		t.Fatalf("view not truncated: %d visible chars in %q", visible, view)
	}
}

// --- the DEGRADED beat (raw loop; content must survive byte-for-byte) ---

func TestBeatLoopCtrlCCancels(t *testing.T) {
	action, _, err := beatLoop(strings.NewReader("\x03"))
	if err != nil || action != beatCancelled {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatLoopEOFCancels(t *testing.T) {
	action, _, err := beatLoop(strings.NewReader(""))
	if err != nil || action != beatCancelled {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatLoopCapturesPastedText(t *testing.T) {
	in := "\x1b[200~the actual content\x1b[201~\x04"
	action, text, err := beatLoop(strings.NewReader(in))
	if err != nil || action != beatText || string(text) != "the actual content" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}

func TestBeatLoopCapturesTypedText(t *testing.T) {
	action, text, err := beatLoop(strings.NewReader("typed\x04"))
	if err != nil || action != beatText || string(text) != "typed" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}

func TestBeatLoopEOFWithContentDelivers(t *testing.T) {
	action, text, err := beatLoop(strings.NewReader("\x1b[200~x\x1b[201~"))
	if err != nil || action != beatText || string(text) != "x" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}

func TestBeatLoopPreservesEscapesInContent(t *testing.T) {
	// Pasted content containing ESC (ANSI-colored logs) must arrive
	// byte-for-byte — the reason this path stays hand-rolled.
	in := "\x1b[200~red:\x1b[31mtext\x1b[0m done\x1b[201~\x04"
	action, text, err := beatLoop(strings.NewReader(in))
	if err != nil || action != beatText || string(text) != "red:\x1b[31mtext\x1b[0m done" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}

func TestBeatLoopPreservesEscOutsidePaste(t *testing.T) {
	action, text, err := beatLoop(strings.NewReader("a\x1b[31mb\x04"))
	if err != nil || action != beatText || string(text) != "a\x1b[31mb" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}

// --- the prompt (shared) ---

func TestBeatPromptSamplesTheClipboard(t *testing.T) {
	cases := []struct {
		types []string
		want  string
	}{
		{[]string{typeFileRefs, "image/png"}, "📎 copied files"},
		{[]string{"image/png", "text/plain"}, "🖼  image on the clipboard"},
		{[]string{"image/tiff"}, "🖼  image on the clipboard"},
		{[]string{"text/plain"}, "📝 text on the clipboard"},
		{nil, "📋 clipboard looks empty"},
	}
	for _, c := range cases {
		if got := beatPrompt(c.types); !strings.Contains(got, c.want) {
			t.Errorf("beatPrompt(%v) = %q, want it to mention %q", c.types, got, c.want)
		}
	}
}

func TestBeatPromptImageWarnsAboutCmdVAndBoldsCtrlV(t *testing.T) {
	got := beatPrompt([]string{"image/png"})
	if !strings.Contains(got, "cmd-v won't work") {
		t.Fatalf("image prompt must warn about Cmd-V: %q", got)
	}
	if !strings.Contains(got, "\x1b[1mctrl-v\x1b[22m") {
		t.Fatalf("ctrl-v should be bold when it matters most: %q", got)
	}
}
