package commands

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"

	xterm "github.com/charmbracelet/x/term"
)

// The paste beat: `byre deliver` with no arguments on a TTY does NOT ship the
// clipboard immediately — it waits for a paste gesture, giving the beat where
// "hold on, what's on my clipboard?" happens (the primary wrong-thing
// protection; decisions D17-D19). The gesture is the only trigger:
//
//   - Ctrl-V, the app-level key (Claude Code's own model: catch the gesture,
//     read the system pasteboard out-of-band — image bytes never traverse the
//     terminal, which is why this works for screenshots), or
//   - a bracketed paste (Cmd-V via the terminal): the streamed text is
//     DISCARDED and the pasteboard read instead, which yields the same text
//     or better (file refs, image data the terminal paste couldn't carry).
//
// Degraded (no pasteboard read path — SSH'd into a headless box): the
// bracketed paste's streamed text IS the content, text only, Ctrl-D ends it.
// No Enter trigger anywhere: Enter isn't semantically paste.

type beatAction int

const (
	beatCancelled beatAction = iota // ctrl-c / EOF: user chose not to
	beatGesture                     // paste gesture seen: read the pasteboard
	beatText                        // degraded capture: content in hand
)

// beatLoop consumes raw keystrokes and decides. canRead says a pasteboard
// read path exists; without one the loop captures pasted/typed text until
// Ctrl-D. Pure over an io.Reader so tests drive it byte-by-byte.
func beatLoop(in io.Reader, canRead bool) (beatAction, []byte, error) {
	r := bufio.NewReader(in)
	var captured bytes.Buffer
	capturing := false
	for {
		b, err := r.ReadByte()
		if err != nil {
			if capturing && captured.Len() > 0 {
				return beatText, captured.Bytes(), nil
			}
			return beatCancelled, nil, nil
		}
		switch {
		case b == 0x03: // ctrl-c
			return beatCancelled, nil, nil
		case b == 0x04: // ctrl-d: ends a degraded capture (ignored otherwise)
			if !canRead {
				return beatText, captured.Bytes(), nil
			}
		case b == 0x16: // ctrl-v: the app-level gesture
			if canRead {
				return beatGesture, nil, nil
			}
			capturing = true // degraded: nothing to read; keep waiting for text
		case b == 0x1b: // possible bracketed-paste marker ESC [ 2 0 0 ~ / 2 0 1 ~
			seq, ok := readBracketMarker(r)
			if !ok {
				continue // some other escape sequence; ignore
			}
			if seq == pasteStart {
				if canRead {
					// Discard the streamed text (the pasteboard has it, or better).
					discardUntilPasteEnd(r)
					return beatGesture, nil, nil
				}
				capturing = true
				captureUntilPasteEnd(r, &captured)
				// Paste captured; Ctrl-D still confirms (multi-paste allowed).
			}
		default:
			if !canRead {
				capturing = true
				captured.WriteByte(b)
			}
		}
	}
}

type bracketSeq int

const (
	notBracket bracketSeq = iota
	pasteStart            // ESC [ 2 0 0 ~
	pasteEnd              // ESC [ 2 0 1 ~
)

// readBracketMarker reads the bytes after an ESC and classifies bracketed-
// paste markers. Anything else is consumed as far as read and ignored.
func readBracketMarker(r *bufio.Reader) (bracketSeq, bool) {
	expect := func(want byte) bool {
		b, err := r.ReadByte()
		return err == nil && b == want
	}
	if !expect('[') || !expect('2') || !expect('0') {
		return notBracket, false
	}
	b, err := r.ReadByte()
	if err != nil {
		return notBracket, false
	}
	if !expect('~') {
		return notBracket, false
	}
	switch b {
	case '0':
		return pasteStart, true
	case '1':
		return pasteEnd, true
	}
	return notBracket, false
}

func discardUntilPasteEnd(r *bufio.Reader) {
	var sink bytes.Buffer
	captureUntilPasteEnd(r, &sink)
}

func captureUntilPasteEnd(r *bufio.Reader, out *bytes.Buffer) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		if b == 0x1b {
			if seq, ok := readBracketMarker(r); ok && seq == pasteEnd {
				return
			}
			continue
		}
		out.WriteByte(b)
	}
}

// runPasteBeat wraps beatLoop in a real terminal: raw mode (so Ctrl-V and the
// paste arrive as bytes, not line-buffered input) and bracketed-paste mode on
// the terminal for the duration.
func runPasteBeat(s Streams, canRead bool) (beatAction, []byte, error) {
	f, ok := s.In.(*os.File)
	if !ok {
		return beatCancelled, nil, fmt.Errorf("the paste beat needs a terminal on stdin")
	}
	if canRead {
		fmt.Fprintln(s.Err, "byre: paste to deliver the clipboard (ctrl-c to cancel)")
	} else {
		fmt.Fprintln(s.Err, "byre: no clipboard access here — paste text to deliver it (text only; ctrl-d to finish, ctrl-c to cancel)")
	}
	state, err := xterm.MakeRaw(f.Fd())
	if err != nil {
		return beatCancelled, nil, fmt.Errorf("raw terminal mode: %w", err)
	}
	fmt.Fprint(s.Err, "\x1b[?2004h") // bracketed paste on
	defer func() {
		fmt.Fprint(s.Err, "\x1b[?2004l")
		_ = xterm.Restore(f.Fd(), state)
		fmt.Fprintln(s.Err) // raw mode ate the echo; end the prompt line
	}()
	return beatLoop(f, canRead)
}
