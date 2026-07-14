package commands

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	xterm "github.com/charmbracelet/x/term"
)

// The paste beat: `byre deliver` with no arguments on a TTY does NOT ship the
// clipboard immediately — it waits for a paste gesture, giving the beat where
// "hold on, what's on my clipboard?" happens (the primary wrong-thing
// protection; ADR 0021). The gesture is the only trigger:
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

// beatLoop is the DEGRADED beat (no pasteboard read path — SSH'd into a
// headless box): raw keystrokes, capturing pasted/typed text until Ctrl-D,
// preserving content byte-for-byte (ESC-bearing pastes included — the reason
// this path stays hand-rolled while the live beat rides Bubble Tea). Pure
// over an io.Reader so tests drive it byte-by-byte.
func beatLoop(in io.Reader) (beatAction, []byte, error) {
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
		case b == 0x04: // ctrl-d ends the capture
			return beatText, captured.Bytes(), nil
		case b == 0x16: // ctrl-v: nothing to read here; keep waiting for text
			capturing = true
		case b == 0x1b: // possible bracketed-paste marker ESC [ 2 0 0 ~ / 2 0 1 ~
			seq, consumed := readBracketMarker(r)
			if seq == pasteStart {
				capturing = true
				captureUntilPasteEnd(r, &captured)
				// Paste captured; Ctrl-D still confirms (multi-paste allowed).
				continue
			}
			// Some other escape sequence: it's CONTENT and must arrive intact.
			if capturing {
				captured.WriteByte(0x1b)
				captured.Write(consumed)
			}
		default:
			capturing = true
			captured.WriteByte(b)
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
	var status, action string
	switch {
	case hasType(types, typeFileRefs):
		status = "📎 copied files on the clipboard"
		action = "ctrl-v (or cmd-v) delivers them"
	case pickImageType(types) != "":
		status = "🖼  image on the clipboard"
		action = "press " + rainbow("ctrl-v") + " to deliver it\n  (cmd-v won't work for images)"
	case hasType(types, "text/plain"):
		status = "📝 text on the clipboard"
		action = "ctrl-v / cmd-v delivers it —\n  or paste or drag a file here"
	default:
		status = "📋 clipboard looks empty"
		action = "copy something — this screen follows your clipboard —\n  or paste or drag a file here"
	}
	return "\n  " + pickTitleStyle.Render("byre deliver") + "\n\n  " + status + "\n\n  " + action + "\n\n  " + pickDimStyle.Render("ctrl-c cancels") + "\n"
}

// The LIVE beat is a Bubble Tea program — the house TUI owns raw mode,
// restore-on-exit, and in-place repaints that survive line wrapping (the
// hand-rolled \r-erase redraw stacked lines the moment the prompt wrapped,
// field-found 2026-07-10). Bracketed paste arrives as a first-class event;
// sampling is sequential by construction (the next tick is scheduled only
// when the previous read returns, so a hung pasteboard owner stalls the
// prompt, never accumulates subprocesses or blocks quitting).

type clipSampleMsg struct{ types []string }
type clipTickMsg struct{}

type beatModel struct {
	reader *clipBackend
	types  []string
	width  int
	action beatAction
	text   []byte
}

func (m beatModel) Init() tea.Cmd { return m.sample }

func (m beatModel) sample() tea.Msg {
	types, err := m.reader.listTypes()
	if err != nil {
		types = nil // degrade the prompt, never wedge it
	}
	return clipSampleMsg{types: types}
}

func (m beatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clipSampleMsg:
		m.types = msg.types
		return m, tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg { return clipTickMsg{} })
	case clipTickMsg:
		return m, m.sample
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		if msg.Paste {
			// A bracketed paste: the streamed text is EVIDENCE (real Cmd-V
			// vs a drag-typed path) — the caller classifies it.
			m.action = beatPaste
			m.text = []byte(string(msg.Runes))
			return m, tea.Quit
		}
		switch msg.Type {
		case tea.KeyCtrlV:
			m.action = beatGesture
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.action = beatCancelled
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m beatModel) View() string {
	block := beatPrompt(m.types)
	if m.width <= 0 {
		return block
	}
	// Clamp per line: bubbletea's renderer miscounts wrapped lines, and the
	// beat should read airily, not reflow.
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width-1, "…")
	}
	return strings.Join(lines, "\n")
}

// rainbow paints a word one 256-color hue per character, bold — reserved for
// THE key: the image prompt's ctrl-v, where macOS muscle memory (cmd-v) fails
// silently. Rainbow everywhere would be decoration; rainbow here is signal.
// 256-color, not truecolor: Terminal.app never learned truecolor.
func rainbow(word string) string {
	hues := []int{196, 208, 226, 46, 33, 129} // red orange yellow green blue purple
	var b strings.Builder
	for i, r := range word {
		fmt.Fprintf(&b, "\x1b[1;38;5;%dm%c", hues[i%len(hues)], r)
	}
	b.WriteString("\x1b[22;39m") // unbold, default foreground
	return b.String()
}

// runPasteBeat runs the beat. With a pasteboard reader it's the live Bubble
// Tea prompt (rendered to STDERR — stdout stays the contract); without one
// it's the raw-mode degraded capture.
func runPasteBeat(s Streams, reader *clipBackend) (beatAction, []byte, error) {
	f, ok := s.In.(*os.File)
	if !ok {
		return beatCancelled, nil, fmt.Errorf("the paste beat needs a terminal on stdin")
	}
	if reader != nil {
		// Bubble Tea arms bracketed paste through its OUTPUT writer — with
		// stderr redirected those sequences never reach the terminal and
		// Cmd-V arrives unbracketed (the same disarm bug fixed once in the
		// hand-rolled version). When stderr isn't a TTY, render on the
		// terminal device itself (the stdin fd — field-verified writable
		// when it's a tty).
		out := io.Writer(s.Err)
		if stdErrFile, ok := s.Err.(*os.File); !ok || !isTTY(stdErrFile) {
			out = f
		}
		m := beatModel{reader: reader, action: beatCancelled}
		res, err := tea.NewProgram(m, tea.WithOutput(out), tea.WithInput(f)).Run()
		if err != nil {
			return beatCancelled, nil, fmt.Errorf("paste beat: %w", err)
		}
		final := res.(beatModel)
		return final.action, final.text, nil
	}

	fmt.Fprintln(s.Err, "byre: no clipboard access here — paste text to deliver it (text only; ctrl-d to finish, ctrl-c to cancel)")
	state, err := xterm.MakeRaw(f.Fd())
	if err != nil {
		return beatCancelled, nil, fmt.Errorf("raw terminal mode: %w", err)
	}
	// The restore is registered BEFORE any tty write: if the arm write below
	// blocks (flow-controlled output), the deferred restore must already be
	// scheduled or the terminal stays raw with no way back.
	defer func() {
		// Restore FIRST: it's an ioctl and cannot block on output; a blocked
		// write after it hangs a COOKED terminal where ctrl-c works.
		_ = xterm.Restore(f.Fd(), state)
		fmt.Fprint(f, "\x1b[?2004l")
		fmt.Fprintln(s.Err) // raw mode ate the echo; end the prompt line
	}()
	// Mode sequences are NOT chrome: they must reach the terminal DEVICE that
	// drives input (the same one MakeRaw touched), not stderr — with stderr
	// redirected, an armed-on-stderr sequence never reaches the terminal.
	fmt.Fprint(f, "\x1b[?2004h") // bracketed paste on
	return beatLoop(f)
}
