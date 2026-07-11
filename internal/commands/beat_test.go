package commands

import (
	"strings"
	"testing"
)

func TestBeatCtrlVIsTheGesture(t *testing.T) {
	action, _, err := beatLoop(strings.NewReader("\x16"), true)
	if err != nil || action != beatGesture {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatBracketedPasteCapturesTextAsEvidence(t *testing.T) {
	// Cmd-V arrives as a bracketed paste; the streamed text is returned so
	// the caller can tell a real clipboard paste from a drag-typed path
	// (field-found 2026-07-10: drags paste text that was never on the
	// pasteboard — discarding it delivered a stale clipboard).
	in := "\x1b[200~some pasted text\x1b[201~"
	action, text, err := beatLoop(strings.NewReader(in), true)
	if err != nil || action != beatPaste {
		t.Fatalf("action = %v err = %v", action, err)
	}
	if string(text) != "some pasted text" {
		t.Fatalf("streamed paste text should be captured, got %q", text)
	}
}

func TestBeatCtrlCCancels(t *testing.T) {
	action, _, err := beatLoop(strings.NewReader("\x03"), true)
	if err != nil || action != beatCancelled {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatEnterIsNotAGesture(t *testing.T) {
	// Enter isn't semantically paste: a newline then ctrl-c must cancel, not fire.
	action, _, err := beatLoop(strings.NewReader("\r\n\x03"), true)
	if err != nil || action != beatCancelled {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatEOFCancels(t *testing.T) {
	action, _, err := beatLoop(strings.NewReader(""), true)
	if err != nil || action != beatCancelled {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatDegradedCapturesPastedText(t *testing.T) {
	// No pasteboard read path: the bracketed paste's text IS the content,
	// ended by ctrl-d.
	in := "\x1b[200~the actual content\x1b[201~\x04"
	action, text, err := beatLoop(strings.NewReader(in), false)
	if err != nil || action != beatText {
		t.Fatalf("action = %v err = %v", action, err)
	}
	if string(text) != "the actual content" {
		t.Fatalf("text = %q", text)
	}
}

func TestBeatDegradedCapturesTypedText(t *testing.T) {
	action, text, err := beatLoop(strings.NewReader("typed\x04"), false)
	if err != nil || action != beatText {
		t.Fatalf("action = %v err = %v", action, err)
	}
	if string(text) != "typed" {
		t.Fatalf("text = %q", text)
	}
}

func TestBeatDegradedEOFWithContentDelivers(t *testing.T) {
	// A ssh channel closing after the paste still yields the content.
	action, text, err := beatLoop(strings.NewReader("\x1b[200~x\x1b[201~"), false)
	if err != nil || action != beatText || string(text) != "x" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}

func TestBeatOtherEscapeSequencesIgnored(t *testing.T) {
	// An arrow key (ESC [ A) must not confuse the loop; ctrl-v after it fires.
	action, _, err := beatLoop(strings.NewReader("\x1b[A\x16"), true)
	if err != nil || action != beatGesture {
		t.Fatalf("action = %v err = %v", action, err)
	}
}

func TestBeatDegradedPreservesEscapesInContent(t *testing.T) {
	// Review finding: pasted content containing ESC (ANSI-colored logs) must
	// arrive byte-for-byte, not have escape-ish runs eaten.
	in := "\x1b[200~red:\x1b[31mtext\x1b[0m done\x1b[201~\x04"
	action, text, err := beatLoop(strings.NewReader(in), false)
	if err != nil || action != beatText {
		t.Fatalf("action = %v err = %v", action, err)
	}
	if string(text) != "red:\x1b[31mtext\x1b[0m done" {
		t.Fatalf("content corrupted: %q", text)
	}
}

func TestBeatDegradedPreservesEscOutsidePaste(t *testing.T) {
	action, text, err := beatLoop(strings.NewReader("a\x1b[31mb\x04"), false)
	if err != nil || action != beatText || string(text) != "a\x1b[31mb" {
		t.Fatalf("action=%v text=%q err=%v", action, text, err)
	}
}
