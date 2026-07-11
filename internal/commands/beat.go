package commands

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

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
	beatGesture                     // ctrl-v: read the pasteboard
	beatPaste                       // bracketed paste: streamed text in hand — the CALLER
	// decides whether it mirrors the pasteboard (real Cmd-V → full pasteboard
	// read) or is drag-typed content that was never ON the pasteboard (a file
	// dragged onto the window pastes its PATH — deliver that file, not the
	// stale clipboard; field-found 2026-07-10).
	beatText // degraded capture: content in hand
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
			seq, consumed := readBracketMarker(r)
			if seq == pasteStart {
				if canRead {
					// Capture the streamed text and hand it up: it's evidence
					// (real clipboard paste vs drag-typed path), not noise.
					var text bytes.Buffer
					captureUntilPasteEnd(r, &text)
					return beatPaste, text.Bytes(), nil
				}
				capturing = true
				captureUntilPasteEnd(r, &captured)
				// Paste captured; Ctrl-D still confirms (multi-paste allowed).
				continue
			}
			// Some other escape sequence. While waiting for a gesture it's
			// terminal chrome (an arrow key) — ignore it. In a degraded
			// capture it's CONTENT and must arrive intact.
			if !canRead && capturing {
				captured.WriteByte(0x1b)
				captured.Write(consumed)
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
// paste markers. On a non-marker it returns the bytes it consumed, so capture
// paths can preserve them verbatim instead of corrupting ESC-bearing content.
func readBracketMarker(r *bufio.Reader) (bracketSeq, []byte) {
	var consumed []byte
	next := func() (byte, bool) {
		b, err := r.ReadByte()
		if err != nil {
			return 0, false
		}
		consumed = append(consumed, b)
		return b, true
	}
	for _, want := range []byte("[20") {
		b, ok := next()
		if !ok || b != want {
			return notBracket, consumed
		}
	}
	kind, ok := next()
	if !ok || (kind != '0' && kind != '1') {
		return notBracket, consumed
	}
	if b, ok := next(); !ok || b != '~' {
		return notBracket, consumed
	}
	if kind == '0' {
		return pasteStart, consumed
	}
	return pasteEnd, consumed
}

// captureUntilPasteEnd copies paste content into out until the end marker,
// preserving any non-marker escape sequences byte-for-byte — pasted content
// legitimately contains ESC (ANSI-colored logs, terminal transcripts).
func captureUntilPasteEnd(r *bufio.Reader, out *bytes.Buffer) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		if b == 0x1b {
			seq, consumed := readBracketMarker(r)
			if seq == pasteEnd {
				return
			}
			out.WriteByte(0x1b)
			out.Write(consumed)
			continue
		}
		out.WriteByte(b)
	}
}

// beatPrompt tailors the beat's prompt to what the pasteboard HOLDS — types
// only, never content (Claude Code's own move: it can't see a failed Cmd-V
// either, so it samples the pasteboard and hints proactively). The beat
// re-samples every ~1.2s and redraws in place, so copying something new
// updates the line live. Ctrl-V goes LOUD (bold, cmd-v disclaimed) exactly
// when it matters: Cmd-V with an image-only clipboard sends the terminal
// NOTHING (no text representation, no event to catch — field-verified
// 2026-07-10). An empty/unreadable board still tells the whole story.
func beatPrompt(types []string) string {
	const bold, plain = "\x1b[1m", "\x1b[22m"
	switch {
	case hasType(types, typeFileRefs):
		return "byre: 📎 copied files on the clipboard — ctrl-v (or cmd-v) delivers them · ctrl-c cancels"
	case pickImageType(types) != "":
		return "byre: 🖼  image on the clipboard — press " + bold + "ctrl-v" + plain + " to deliver it (cmd-v won't work for images) · ctrl-c cancels"
	case hasType(types, "text/plain"):
		return "byre: 📝 text on the clipboard — ctrl-v / cmd-v delivers it, or paste/drag a file here · ctrl-c cancels"
	default:
		return "byre: 📋 clipboard looks empty — copy something (this line updates), or paste/drag a file here · ctrl-c cancels"
	}
}

// runPasteBeat wraps beatLoop in a real terminal: raw mode (so Ctrl-V and the
// paste arrive as bytes, not line-buffered input) and bracketed-paste mode on
// the terminal for the duration.
func runPasteBeat(s Streams, reader *clipBackend) (beatAction, []byte, error) {
	f, ok := s.In.(*os.File)
	if !ok {
		return beatCancelled, nil, fmt.Errorf("the paste beat needs a terminal on stdin")
	}
	canRead := reader != nil
	var stopSampler chan struct{}
	var samplerDone chan struct{}
	if canRead {
		// The LIVE prompt: redraw in place as the clipboard changes. Types
		// only, ~1.2s cadence (one cheap listTypes subprocess per tick,
		// only while the beat waits), redraw only on change (no flicker).
		draw := func(line string) { fmt.Fprint(s.Err, "\r\x1b[2K"+line) }
		var last string
		sample := func() {
			types, err := reader.listTypes()
			if err != nil {
				types = nil
			}
			if line := beatPrompt(types); line != last {
				last = line
				draw(line)
			}
		}
		sample()
		stopSampler = make(chan struct{})
		samplerDone = make(chan struct{})
		go func() {
			defer close(samplerDone)
			tick := time.NewTicker(1200 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-stopSampler:
					return
				case <-tick.C:
					sample()
				}
			}
		}()
	} else {
		fmt.Fprintln(s.Err, "byre: no clipboard access here — paste text to deliver it (text only; ctrl-d to finish, ctrl-c to cancel)")
	}
	state, err := xterm.MakeRaw(f.Fd())
	if err != nil {
		if stopSampler != nil {
			close(stopSampler)
			<-samplerDone
		}
		return beatCancelled, nil, fmt.Errorf("raw terminal mode: %w", err)
	}
	// Mode sequences are NOT chrome: they must reach the terminal DEVICE that
	// drives input (the same one MakeRaw touched), not stderr — with stderr
	// redirected, an armed-on-stderr sequence never reaches the terminal and
	// Cmd-V silently stops registering. Writing to the stdin TTY fd works
	// (it's the terminal device); human prompts stay on s.Err.
	fmt.Fprint(f, "\x1b[?2004h") // bracketed paste on
	defer func() {
		if stopSampler != nil {
			close(stopSampler)
			<-samplerDone
			fmt.Fprint(s.Err, "\r\x1b[2K") // erase the live prompt line
		}
		fmt.Fprint(f, "\x1b[?2004l")
		_ = xterm.Restore(f.Fd(), state)
		if stopSampler == nil {
			fmt.Fprintln(s.Err) // raw mode ate the echo; end the prompt line
		}
	}()
	return beatLoop(f, canRead)
}
