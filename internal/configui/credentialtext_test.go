package configui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/pjlsergeant/byre/internal/credentials"
)

func credKey(m model, key tea.KeyType) model {
	next, _ := m.Update(tea.KeyMsg{Type: key})
	return settleCredScreen(next.(model))
}

func credPaste(m model, value string) model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value), Paste: true})
	return settleCredScreen(next.(model))
}

// Model tests acknowledge the screen commands as Bubble Tea does; the tmux
// test separately exercises the actual command/renderer handshake.
func settleCredScreen(m model) model {
	for m.credTextTransition != credentialTextSteady {
		var msg tea.Msg
		switch m.credTextTransition {
		case credentialTextEntering:
			msg = credentialRevealMsg{}
		case credentialTextHiding:
			msg = credentialHiddenMsg{}
		case credentialTextExiting:
			msg = credentialClosedMsg{}
		}
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func openVisibleCredText(t *testing.T, m model) model {
	t.Helper()
	if m.width == 0 || m.height == 0 {
		m.width, m.height = 80, 24
	}
	m = credKey(m, tea.KeyCtrlE)
	if m.mode != modeCredText || m.credTextVisible || !strings.Contains(m.View(), "without masking") {
		t.Fatal("editor must warn before showing any draft")
	}
	m = credKey(m, tea.KeyCtrlE)
	if !m.credTextVisible || !strings.Contains(m.View(), "VISIBLE") {
		t.Fatal("explicit acknowledgement must open visibly disclosed editor")
	}
	return m
}

func TestCredentialSingleLineRejectsWholePasteAndNeverSavesOnEnter(t *testing.T) {
	msgs := []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyCtrlJ}}
	for _, value := range []string{"PRIVATE\nKEY\n", "PRIVATE\rKEY", "PRIVATE\r\nKEY", "PRIVATE\tKEY", "PRIVATE\x1bKEY", "PRIVATE\ufffdKEY"} {
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value), Paste: true})
	}
	for focus := 0; focus < 4; focus++ {
		for _, msg := range msgs {
			admin := newFakeCredAdmin()
			m := addCredential(credModel(t, admin, nil), credKindEnv, "KEY", "before")
			m.focusItem(focus)
			next, _ := m.Update(msg)
			m = next.(model)
			valueField := focus == 2
			note := "cannot accept"
			if !valueField {
				note = "Enter never saves"
				if msg.Paste {
					note = "Paste rejected"
				}
			}
			if m.mode != modeItem || len(admin.writes) != 0 || m.inputs[0].Value() != "KEY" || m.inputs[1].Value() != "before" || strings.Contains(m.View(), "PRIVATE") || !strings.Contains(m.View(), note) {
				t.Fatalf("focus %d input %q must be refused without echo, mutation or save", focus, msg.String())
			}
			if (m.credInputWarning != "") != valueField {
				t.Fatal("only Value refusals should latch")
			}
			if !valueField {
				continue
			}
			m = credPaste(m, "tail") // unbracketed paste's later chunk
			m = credKey(m, tea.KeyCtrlS)
			if m.mode != modeItem || len(admin.writes) != 0 || m.inputs[1].Value() != "before" {
				t.Fatal("rejected input must latch, preventing a truncated save")
			}
			m = openVisibleCredText(t, m)
			if m.credText.value != "" {
				t.Fatal("rejected paste must be re-entered, never transferred")
			}
			m = credKey(m, tea.KeyEsc)
			if m.credInputWarning == "" {
				t.Fatal("cancel must preserve the refusal")
			}
		}
	}
}

func TestMultilineCredentialDraftRoundTrip(t *testing.T) {
	for _, kind := range []int{credKindEnv, credKindFile} {
		t.Run(string(credentialKind(kind)), func(t *testing.T) {
			admin := newFakeCredAdmin()
			m := addCredential(credModel(t, admin, nil), kind, "KEY", "")
			m.width, m.height = 80, 24
			m = openVisibleCredText(t, m)
			m = credKey(m, tea.KeyCtrlT) // keep line endings as pasted
			value := "  -----BEGIN PRIVATE KEY-----\r\n\t秘密 key\n" + strings.Repeat("line\n", 150) + "-----END PRIVATE KEY-----\n\n"
			m = credPaste(m, value)
			if m.credText.value != value {
				t.Fatal("paste changed whitespace or truncated lines")
			}
			m = credKey(m, tea.KeyCtrlS)
			if len(admin.writes) != 0 || m.mode != modeItem || m.credDraft != value {
				t.Fatal("accepting editor draft must not persist")
			}
			if view := m.View(); strings.Contains(view, "PRIVATE KEY") || !strings.Contains(view, "Not saved yet") {
				t.Fatal("form must hide the draft and disclose unsaved state")
			}
			m.focusItem(2)
			m = credPaste(m, "accidental\nsecond paste")
			if m.credInputWarning != "" || m.credDraft != value || !strings.Contains(m.itemErr, "Paste rejected") {
				t.Fatal("refused paste must not invalidate an accepted draft")
			}
			m = openVisibleCredText(t, m)
			if m.credText.value != value {
				t.Fatal("reopening must preserve exact draft")
			}
			m = credPaste(m, "cancel this")
			m = credKey(m, tea.KeyEsc)
			if m.credDraft != value {
				t.Fatal("cancel changed accepted draft")
			}
			m = credKey(m, tea.KeyCtrlS)
			if m.mode != modeCredPass || len(admin.writes) != 0 {
				t.Fatal("first form save must ask for identity passphrase")
			}
			m = typeCredPass(t, m, "pw", "pw")
			if m.mode != modeList || string(admin.open(t, "KEY", "pw")) != value {
				t.Fatal("saved credential did not decrypt byte-exactly")
			}
			if m.credDraft != "" || m.credText.value != "" || m.inputs[1].Value() != "" {
				t.Fatal("successful save retained plaintext draft")
			}
			m.itemHostEnv = true
			m = m.startItem(0)
			m = openVisibleCredText(t, m)
			if m.credText.value != "" {
				t.Fatal("stored credential must never be loaded")
			}
			m = credPaste(m, "replacement\n")
			m = credKey(m, tea.KeyCtrlS)
			m = credKey(m, tea.KeyCtrlS)
			if m.mode != modeList || string(admin.open(t, "KEY", "pw")) != "replacement\n" || admin.mints != 1 {
				t.Fatal("existing identity replacement must preserve newline without a second mint")
			}
		})
	}
}

func TestCredentialDraftSurvivesRefusalAndPassphraseCancel(t *testing.T) {
	admin := newFakeCredAdmin()
	m := addCredential(credModel(t, admin, nil), credKindEnv, "KEY", "")
	m = credPaste(openVisibleCredText(t, m), "whole\nvalue\n")
	m = credKey(m, tea.KeyCtrlS)
	m = credKey(m, tea.KeyCtrlS)
	m = credKey(m, tea.KeyEsc)
	if m.credDraft != "whole\nvalue\n" || admin.sets != 0 {
		t.Fatal("passphrase cancel lost draft or wrote")
	}
	m = credKey(m, tea.KeyCtrlS)
	admin.err = errors.New("write refused")
	m = typeCredPass(t, m, "pw", "pw")
	if m.mode != modeItem || m.credDraft != "whole\nvalue\n" || !strings.Contains(m.itemErr, "refused") {
		t.Fatal("write refusal must preserve draft for retry")
	}
	m = credKey(m, tea.KeyEsc)
	if m.credDraft != "" || m.credMultiline {
		t.Fatal("form cancel retained draft")
	}
}

func TestCredentialSourceSwitchClearsMultilineDraft(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
	m = credPaste(openVisibleCredText(t, m), "private\nkey")
	m = credKey(m, tea.KeyCtrlS)
	m.focusItem(0)
	m = credKey(m, tea.KeyLeft)
	if m.credDraft != "" || m.credMultiline || strings.Contains(m.View(), "private") {
		t.Fatal("source switch retained secret")
	}
}

func TestCredentialTextEditingPreservesTextAndEscapesControls(t *testing.T) {
	e := credentialText{}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("α")}, {Type: tea.KeySpace, Runes: []rune{' '}},
		{Type: tea.KeyRunes, Runes: []rune("β")}, {Type: tea.KeyEnter},
		{Type: tea.KeySpace, Runes: []rune{' '}}, {Type: tea.KeyRunes, Runes: []rune("γ")},
		{Type: tea.KeyUp}, {Type: tea.KeyLeft}, {Type: tea.KeyDown}, {Type: tea.KeyBackspace},
		{Type: tea.KeyHome}, {Type: tea.KeyDelete}, {Type: tea.KeyEnd}, {Type: tea.KeyTab},
	} {
		e = e.update(key)
	}
	if e.value != "α β\n\t" || e.pos != 5 {
		t.Fatalf("edit = %q cursor %d", e.value, e.pos)
	}
	e.keepCR = true
	e = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\x1b[2J\r\t\x00"), Paste: true})
	rows, _ := e.rows(80)
	view := ansi.Strip(strings.Join(rows, "\n"))
	if strings.ContainsAny(view, "\x1b\r\t\x00") || !strings.Contains(view, "\\x1b") {
		t.Fatal("raw terminal controls escaped the display")
	}
	if !strings.Contains(e.value, "\x1b[2J\r\t\x00") {
		t.Fatal("display escaping mutated draft")
	}
}

func TestCredentialTextCapRejectsWholeInsertion(t *testing.T) {
	e := credentialText{value: strings.Repeat("x", credentials.MaxValue-1)}
	e = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é"), Paste: true})
	if len(e.value) != credentials.MaxValue-1 || !strings.Contains(e.err, "256 KiB") {
		t.Fatal("cap must reject whole insertion, not truncate")
	}
}

func TestCredentialWhitespaceLayout(t *testing.T) {
	for _, tt := range []struct {
		value string
		width int
		want  string
		lines string
	}{
		{"a\rb\r", 80, "a␍\nb␍\n∎", "3 lines"},
		{"a\r\nb\n", 80, "a␍↵\nb↵\n∎", "3 lines"},
		{"\r\n\r\n", 80, "␍↵\n␍↵\n∎", "3 lines"},
		{"a\tB\t", 80, "a──────⇥B──────⇥∎", "1 line"},
		{"界\tX", 80, "界─────⇥X∎", "1 line"},
		{"1234567\r\nx", 8, "1234567\n␍↵\nx∎", "2 lines"},
		{"123456789\tX", 10, "123456789\n───────⇥X∎", "1 line"},
	} {
		e := credentialText{value: tt.value, pos: len([]rune(tt.value))}
		rows, cursor := e.rows(tt.width)
		if got := ansi.Strip(strings.Join(rows, "\n")); got != tt.want || cursor != len(rows)-1 {
			t.Fatalf("%q: rows %q, cursor row %d; want %q", tt.value, got, cursor, tt.want)
		}
		if e.value != tt.value || credentialLines(e.value) != tt.lines {
			t.Fatalf("%q: value changed or line count incorrect", tt.value)
		}
		for _, row := range rows {
			if ansi.StringWidth(row) > tt.width {
				t.Fatalf("%q: rendered row exceeds width %d", tt.value, tt.width)
			}
		}
	}
}

func TestCredentialHelpersAreBlueButLiteralMarkersAreNot(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	e := credentialText{value: "\tA\r\n⇥", pos: 5}
	rows, _ := e.rows(80)
	for _, marker := range []string{"───────⇥", "␍", "↵"} {
		if !strings.Contains(rows[0], credentialMarkerStyle.Render(marker)) || !strings.Contains(rows[0], "94m") {
			t.Fatalf("%q must be blue", marker)
		}
	}
	if rows[1] != "⇥"+credentialMarkerStyle.Reverse(true).Render("∎") {
		t.Fatal("literal marker must stay uncoloured; end marker must keep its blue cursor")
	}
}

func TestCredentialNavigationRecognizesEveryLineEnding(t *testing.T) {
	for _, sep := range []string{"\r", "\n", "\r\n"} {
		value := strings.Join([]string{"abc", "def", "xy"}, sep)
		e := credentialText{value: value, pos: 1}
		e = e.update(tea.KeyMsg{Type: tea.KeyDown})
		if e.pos != 4+len(sep) {
			t.Fatalf("%q: down reached %d", sep, e.pos)
		}
		e = e.update(tea.KeyMsg{Type: tea.KeyHome})
		if e.pos != 3+len(sep) {
			t.Fatalf("%q: home reached %d", sep, e.pos)
		}
		e = e.update(tea.KeyMsg{Type: tea.KeyEnd})
		if e.pos != 6+len(sep) {
			t.Fatalf("%q: end reached %d", sep, e.pos)
		}
		e = e.update(tea.KeyMsg{Type: tea.KeyUp})
		if e.pos != 3 || e.value != value {
			t.Fatalf("%q: up reached %d or navigation changed bytes", sep, e.pos)
		}
	}
}

func TestCredentialRuneBatchesAreNotShortcuts(t *testing.T) {
	for _, word := range []string{"esc", "enter", "ctrl+s", "ctrl+e", "home", "end", "left", "up", "tab", "backspace"} {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(word)}
		m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
		m.focusItem(2)
		next, _ := m.Update(msg)
		m = next.(model)
		if m.mode != modeItem || m.inputs[1].Value() != word || m.credInputWarning != "" {
			t.Fatalf("single-line field treated %q as a shortcut", word)
		}
		m = openVisibleCredText(t, m)
		next, _ = m.Update(msg)
		m = next.(model)
		if m.mode != modeCredText || !m.credTextVisible || m.credText.value != word+word {
			t.Fatalf("multiline editor treated %q as a shortcut", word)
		}
	}
}

func TestCredentialScreenTransitionsNeverRevealOnPrimaryScreen(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "private")
	m.width, m.height = 80, 24
	m = credKey(m, tea.KeyCtrlE) // warning
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = next.(model)
	if cmd == nil || m.credTextVisible || strings.Contains(m.View(), "private") {
		t.Fatal("draft rendered before alternate-screen acknowledgement")
	}
	// A paste arriving while the screen command is in flight is retained.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\nfast paste"), Paste: true})
	m = next.(model)
	if strings.Contains(m.View(), "fast paste") {
		t.Fatal("queued paste rendered too early")
	}
	m = settleCredScreen(m)
	if m.credText.value != "private\nfast paste" {
		t.Fatal("screen transition dropped typing")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if m.credTextTransition != credentialTextHiding || strings.Contains(m.View(), "private") || cmd == nil {
		t.Fatal("editor must buffer hidden form before requesting screen exit")
	}
	next, cmd = m.Update(credentialHiddenMsg{})
	m = next.(model)
	if m.credTextTransition != credentialTextExiting || cmd == nil || strings.Contains(m.View(), "private") {
		t.Fatal("screen exit must retain hidden frame")
	}
	m = settleCredScreen(m)
	if m.mode != modeItem || m.credDraft != "private\nfast paste" {
		t.Fatal("screen exit lost accepted draft")
	}
}

func TestCredentialSaveKeepsItsDisclosureAndOtherEditsUnsaved(t *testing.T) {
	admin := newFakeCredAdmin()
	m := addCredential(credModel(t, admin, nil), credKindEnv, "KEY", "old")
	m = typeCredPass(t, m.commitItem(), "pw", "pw")
	m.env = append(m.env, kvItem{Key: "UNSAVED", Value: "literal"})
	m.itemHostEnv = true
	m = m.startItem(0)
	m.inputs[1].SetValue("new")
	m = credKey(m, tea.KeyCtrlS)
	if !strings.Contains(m.status, "credential set") || !strings.Contains(m.status, "next develop") || !m.dirty() {
		t.Fatal("credential save must preserve its result and not save unrelated edits")
	}
	// Keep that boundary uniform even when the replacement is empty: a save
	// from a credential form does not unexpectedly save unrelated changes.
	m.itemHostEnv = true
	m = m.startItem(0)
	m = credKey(m, tea.KeyCtrlS)
	if !m.dirty() || !strings.Contains(m.status, "unchanged") {
		t.Fatal("unchanged credential must not save unrelated edits either")
	}
}

func TestCredentialClosingDoesNotReplayEditorKeysOnTheForm(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlS, tea.KeyEsc} {
		admin := newFakeCredAdmin()
		m := addCredential(credModel(t, admin, nil), credKindEnv, "KEY", "private")
		m = openVisibleCredText(t, m)
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS}) // accept, begin hiding
		m = next.(model)
		next, _ = m.Update(tea.KeyMsg{Type: key}) // still the editor's keymap
		m = next.(model)
		next, _ = m.Update(credentialHiddenMsg{})
		m = next.(model)
		next, _ = m.Update(tea.KeyMsg{Type: key}) // terminal exit in flight
		m = settleCredScreen(next.(model))
		if m.mode != modeItem || m.credDraft != "private" || len(admin.writes) != 0 {
			t.Fatal("closing key persisted or discarded the accepted draft")
		}
		m = credKey(m, tea.KeyCtrlS) // a NEW form save remains available
		if m.mode != modeCredPass {
			t.Fatal("explicit form save did not request persistence")
		}
	}
}

func TestCredentialDisablesHostClipboardCommand(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "before")
	for _, in := range m.inputs {
		if in.KeyMap.Paste.Enabled() {
			t.Fatal("private clipboard command must be disabled on both inputs")
		}
	}
	for focus := 0; focus < 4; focus++ {
		m.focusItem(focus)
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
		m = next.(model)
		if cmd != nil || m.inputs[0].Value() != "KEY" || m.inputs[1].Value() != "before" || !strings.Contains(m.itemErr, "terminal's paste") {
			t.Fatalf("focus %d must refuse host clipboard lookup without changing either input", focus)
		}
	}
}

func TestCredentialVisibilityNoticeAndResize(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "private")
	m.width, m.height = 60, 16
	m = credKey(m, tea.KeyCtrlE)
	if len(strings.Split(m.viewCredText(), "\n")) > m.height-1 {
		t.Fatal("consent notice does not fit its minimum height")
	}
	for _, fragment := range []string{"without masking", "^e", "CRLF line endings become LF", "^t"} {
		if !strings.Contains(m.View(), fragment) {
			t.Fatalf("minimum consent screen hides %q: %s", fragment, m.View())
		}
	}
	m = credKey(m, tea.KeyCtrlE)
	m.width, m.height = 59, 10
	if strings.Contains(m.View(), "private") || !strings.Contains(m.View(), "enlarge") {
		t.Fatal("tiny editor must hide text and pause with a remedy")
	}
	m = credPaste(m, "not inserted while paused")
	if m.credText.value != "private" {
		t.Fatal("editor modified an invisible draft")
	}
	min := m.credentialTextMinHeight()
	if min != 16 || !strings.Contains(m.View(), "60×16") {
		t.Fatalf("live editor minimum is %d; the pause must name it:\n%s", min, m.View())
	}
	m.width, m.height = 60, min
	for _, fragment := range []string{"VISIBLE", "NOT loaded", "NOT save", credentialLFModeNote, "^t", "line endings", "private"} {
		if !strings.Contains(m.View(), fragment) {
			t.Fatalf("minimum live editor hides %q:\n%s", fragment, m.View())
		}
	}
	if len(strings.Split(m.View(), "\n")) > m.height-1 {
		t.Fatal("minimum live editor does not fit its minimum height")
	}
	// The controls pack into lines that fit: every entry whole, no line
	// wider than the pane, so clipLines never has to cut one.
	for _, l := range strings.Split(m.viewCredText(), "\n") {
		if ansi.StringWidth(l) > 60 || strings.HasSuffix(ansi.Strip(l), "…") {
			t.Fatalf("line does not fit 60 columns: %q", ansi.Strip(l))
		}
	}
	if l := lineWith(t, m.View(), "discard"); !strings.Contains(ansi.Strip(l), "discard changes") {
		t.Fatalf("controls entry cut: %q", ansi.Strip(l))
	}
	m.width = 100
	if l := lineWith(t, m.View(), "newline"); !strings.Contains(ansi.Strip(l), "line endings") {
		t.Fatalf("wide controls must stay on one line: %q", ansi.Strip(l))
	}
	m.width = 60
	m.height = min - 1
	if strings.Contains(m.View(), "private") || !strings.Contains(m.View(), "enlarge") {
		t.Fatal("editor below its minimum height must pause")
	}
	m.height = min
	// The end-line warning and an error together still fit, with two text
	// rows, and clipping drops none of them.
	m = credPaste(m, "\n-----END OPENSSH PRIVATE KEY-----")
	m = credKey(m, tea.KeyCtrlV)
	if len(strings.Split(m.viewCredText(), "\n")) > m.height-1 {
		t.Fatalf("warning and error do not fit the minimum height:\n%s", m.View())
	}
	// At the minimum the warnings' padding is dropped: the warning follows
	// the position line directly.
	if above, _ := warningNeighbours(t, m.View(), "No final line break", "end."); !strings.Contains(above, "bytes ·") {
		t.Fatalf("minimum editor must drop the warning padding, got %q above it:\n%s", above, ansi.Strip(m.View()))
	}
	// Join the wrapped lines so the warning's fragment is found whole.
	view := strings.Join(strings.Fields(ansi.Strip(m.View())), " ")
	for _, fragment := range []string{"VISIBLE", credentialLFModeNote, credentialEndNoBreak, "Press Enter at the end", "terminal's paste", "^s blocked", "line endings", "view 1–2/2"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("minimum editor with warning and error hides %q:\n%s", fragment, m.View())
		}
	}
	m = credKey(m, tea.KeyLeft)
	m = credKey(m, tea.KeyCtrlS)
	if !strings.Contains(m.View(), "Not saved yet") || !strings.Contains(m.View(), "hidden") {
		t.Fatal("narrow form must keep the unsaved/hidden draft summary visible")
	}
}

// At the minimum size a wrapped error takes rows from the text, never from
// the warning, the error itself, or the controls: the frame must not reach
// clipHeight's marker.
func TestCredentialMinimumEditorKeepsATallErrorWhole(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
	m = openVisibleCredText(t, m)
	m.width, m.height = 60, m.credentialTextMinHeight()
	m = credPaste(m, crPastedKey)
	m = credPaste(m, strings.Repeat("x", credentials.MaxValue))
	if m.credText.err == "" || len(strings.Split(ansi.Strip(m.errLine(m.credText.err)), "\n")) < 2 {
		t.Fatalf("the check needs an error that wraps at width 60, got %q", m.credText.err)
	}
	raw := m.View()
	view := strings.Join(strings.Fields(ansi.Strip(raw)), " ")
	for _, fragment := range []string{credentialEndNoBreak, "Press Enter at the end", "exceeds 256 KiB", "Nothing inserted", "^s blocked", "bytes · 4 lines · view", "-----END OPENSSH PRIVATE KEY-----", "discard changes", "line endings"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("minimum editor with a tall error hides %q:\n%s", fragment, ansi.Strip(raw))
		}
	}
	if strings.Contains(view, "more below") || strings.Contains(view, "more above") {
		t.Fatalf("minimum editor with a tall error was clipped:\n%s", ansi.Strip(raw))
	}
}

func TestCredentialRefusalIsOneRedError(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	for _, width := range []int{60, 80} {
		m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
		m.width, m.height = width, 24
		m.focusItem(2)
		m = credKey(credPaste(m, "secret\nvalue"), tea.KeyCtrlS)
		view := m.View()
		if !strings.Contains(view, m.errLine(credentialSingleLineWarning)) || !strings.Contains(view, "31m") {
			t.Fatalf("width %d: refusal must use the red error renderer", width)
		}
		plain := ansi.Strip(view)
		if strings.Count(plain, "✗") != 1 || strings.Contains(plain, credentialInputNote) || strings.Contains(plain, "secret\nvalue") {
			t.Fatal("refusal must not repeat guidance, stack errors, or echo the rejected value")
		}
		for _, fragment := range []string{"whole value", "64 KiB", "^e", "save credential only", "esc"} {
			if !strings.Contains(plain, fragment) {
				t.Fatalf("width %d: missing %q", width, fragment)
			}
		}
	}
}

// A key as a terminal pastes it: CR line breaks, and none after the last line.
const crPastedKey = "-----BEGIN OPENSSH PRIVATE KEY-----\rb3BlbnNzaC1rZXktdjE=\rAAAA\r-----END OPENSSH PRIVATE KEY-----"

const lfKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjE=\nAAAA\n-----END OPENSSH PRIVATE KEY-----"

func TestCredentialPasteNormalizesLineEndingsByDefault(t *testing.T) {
	for _, tt := range []struct{ pasted, want string }{
		{crPastedKey, lfKey},
		{strings.ReplaceAll(crPastedKey, "\r", "\r\n"), lfKey},
		{crPastedKey + "\r", lfKey + "\n"},
		{crPastedKey + "\r\n", lfKey + "\n"},
		{lfKey + "\n", lfKey + "\n"},
	} {
		e := credentialText{}.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.pasted), Paste: true})
		if e.value != tt.want || e.pos != len([]rune(tt.want)) {
			t.Fatalf("%q: value %q cursor %d", tt.pasted, e.value, e.pos)
		}
	}
	// Only line breaks change: tabs and other controls stay as pasted.
	e := credentialText{}.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\tb\x1b\r\r\nc"), Paste: true})
	if e.value != "a\tb\x1b\n\nc" {
		t.Fatalf("value %q", e.value)
	}
}

func TestCredentialEndLineWithoutFinalBreak(t *testing.T) {
	for _, tt := range []struct {
		draft string
		want  bool
	}{
		{lfKey, true},
		{"-----END OPENSSH PRIVATE KEY-----", true},
		{"-----END CERTIFICATE-----", true},
		{lfKey + "\n", false},
		{lfKey + "\r\n", false},
		{crPastedKey + "\r", false}, // as-pasted: the ␍ marker shows it
		{lfKey + " ", false},
		{lfKey + "\nmore", false},
		{"  -----END OPENSSH PRIVATE KEY-----", false},
		{"-----END-----", false},
		{"-----BEGIN OPENSSH PRIVATE KEY-----", false},
		{"", false},
		{"ghp_0123456789abcdef", false},
	} {
		if got := endsInBareEndLine(tt.draft); got != tt.want {
			t.Errorf("%q: got %v, want %v", tt.draft, got, tt.want)
		}
	}
}

func TestCredentialEditorWarnsUntilTheEndLineIsTerminated(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
	m = credPaste(openVisibleCredText(t, m), crPastedKey)
	if view := ansi.Strip(m.View()); !strings.Contains(view, credentialEndNoBreak) || !strings.Contains(view, "Press Enter") {
		t.Fatalf("pasted key must show the end-line warning and its remedy:\n%s", view)
	}
	m = credKey(m, tea.KeyEnter)
	if m.credText.value != lfKey+"\n" || strings.Contains(m.View(), credentialEndNoBreak) {
		t.Fatal("Enter at the end must terminate the key and clear the warning")
	}
	m = credKey(m, tea.KeyBackspace)
	m = credPaste(m, "\nnot a key")
	if strings.Contains(m.View(), credentialEndNoBreak) {
		t.Fatal("a draft not ending in an END line must not warn")
	}
}

func TestCredentialFormWarnsBeforeSavingAnUnterminatedEndLine(t *testing.T) {
	admin := newFakeCredAdmin()
	m := addCredential(credModel(t, admin, nil), credKindFile, "KEY", "")
	m = credPaste(openVisibleCredText(t, m), crPastedKey)
	m = credKey(m, tea.KeyCtrlS)
	view := ansi.Strip(m.View())
	if m.mode != modeItem || !strings.Contains(view, credentialEndNoBreak) || !strings.Contains(view, "^e") || strings.Contains(view, "PRIVATE KEY") {
		t.Fatalf("form must warn about the accepted draft without showing it:\n%s", view)
	}
	// A warning, not a gate: the form still saves the draft as accepted.
	m = credKey(m, tea.KeyCtrlS)
	m = typeCredPass(t, m, "pw", "pw")
	if m.mode != modeList || string(admin.open(t, "KEY", "pw")) != lfKey {
		t.Fatal("warning must not block or alter the save")
	}
	m.itemHostEnv = true
	m = m.startItem(0)
	m = credPaste(openVisibleCredText(t, m), lfKey+"\n")
	m = credKey(m, tea.KeyCtrlS)
	if m.mode != modeItem || !m.credentialItem() || !m.credMultiline || m.credDraft != lfKey+"\n" {
		t.Fatal("the negative check below needs the credential form holding the accepted draft")
	}
	if strings.Contains(m.View(), credentialEndNoBreak) {
		t.Fatal("a terminated key must not warn on the form")
	}
}

// The form's ^s saves without another screen in between, so the warning and
// its remedy must survive the height clip at the editor's minimum size, with
// a layer disclosure competing for the same rows.
func TestCredentialFormEndLineWarningSurvivesTheMinimumSize(t *testing.T) {
	// As pasted, the same key also keeps its CRs, so the CR note competes
	// for the rows too.
	for _, asPasted := range []bool{false, true} {
		admin := newFakeCredAdmin()
		admin.disclosure = "writes to layer acme (/x/layer.config), used by 3 projects — this changes the value for every project extending it"
		m := addCredential(credModel(t, admin, nil), credKindFile, "KEY", "")
		m = openVisibleCredText(t, m)
		m.width, m.height = 60, m.credentialTextMinHeight()
		if asPasted {
			m = credKey(m, tea.KeyCtrlT)
		}
		m = credPaste(m, crPastedKey)
		m = credKey(m, tea.KeyCtrlS)
		if m.mode != modeItem || m.height != 16 {
			t.Fatalf("want the form at 60x16, got mode %v at height %d", m.mode, m.height)
		}
		fragments := []string{credentialEndNoBreak, "Open ^e and press Enter at the end", "layer acme"}
		if asPasted {
			fragments = append(fragments, "3 CRs "+credentialCRsNeedLF+credentialCRFormRemedy)
		}
		// Join the wrapped lines so each fragment is found whole.
		view := strings.Join(strings.Fields(ansi.Strip(m.View())), " ")
		for _, fragment := range fragments {
			if !strings.Contains(view, fragment) {
				t.Fatalf("as pasted %v: the 60x16 form hides %q:\n%s", asPasted, fragment, ansi.Strip(m.View()))
			}
		}
	}
}

func TestCredentialAsPastedKeepsLineEndingsByteExact(t *testing.T) {
	pasted := "a\rb\r\nc\n"
	e := credentialText{}.update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !e.keepCR {
		t.Fatal("^t must switch to as-pasted")
	}
	e = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	if e.value != pasted {
		t.Fatalf("value %q", e.value)
	}
}

func TestCredentialTypedEnterInsertsLFInEitherMode(t *testing.T) {
	for _, keepCR := range []bool{false, true} {
		for _, key := range []tea.KeyType{tea.KeyEnter, tea.KeyCtrlJ} {
			e := credentialText{value: "ab", pos: 1, keepCR: keepCR}.update(tea.KeyMsg{Type: key})
			if e.value != "a\nb" || e.pos != 2 {
				t.Fatalf("keepCR=%v: value %q cursor %d", keepCR, e.value, e.pos)
			}
		}
	}
}

func TestCredentialSwitchingToLFConvertsDraftInPlace(t *testing.T) {
	value := "ab\r\ncd\rEF\r\n"
	for _, tt := range []struct {
		pos  int
		want int // the same logical character after conversion
	}{
		{0, 0},  // a
		{2, 2},  // the CR opening a CRLF: the LF that replaces it
		{3, 2},  // between CR and LF: the LF that replaces the pair
		{4, 3},  // c
		{6, 5},  // lone CR
		{7, 6},  // E
		{11, 9}, // end
	} {
		e := credentialText{value: value, pos: tt.pos, keepCR: true}
		e = e.update(tea.KeyMsg{Type: tea.KeyCtrlT})
		if e.keepCR || e.value != "ab\ncd\nEF\n" || e.pos != tt.want {
			t.Fatalf("pos %d: value %q cursor %d, want cursor %d", tt.pos, e.value, e.pos, tt.want)
		}
	}
	// Switching back changes nothing already in the draft.
	e := credentialText{value: "a\nb", pos: 1}.update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !e.keepCR || e.value != "a\nb" || e.pos != 1 {
		t.Fatalf("value %q cursor %d", e.value, e.pos)
	}
}

func TestCredentialCapAppliesToConvertedInsertion(t *testing.T) {
	// CRLF counts as one byte once converted, so this fits exactly.
	e := credentialText{value: strings.Repeat("x", credentials.MaxValue-2)}
	e = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\r\n\r\n"), Paste: true})
	if len(e.value) != credentials.MaxValue || e.err != "" {
		t.Fatalf("len %d err %q", len(e.value), e.err)
	}
}

func TestCredentialEditorShowsLineEndingMode(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	m = openVisibleCredText(t, m)
	if view := ansi.Strip(m.View()); !strings.Contains(view, credentialLFModeNote) || strings.Contains(view, credentialAsPastedModeNote) || !strings.Contains(view, "^t") {
		t.Fatalf("editor must name LF mode and its toggle:\n%s", view)
	}
	m = credKey(m, tea.KeyCtrlT)
	if view := ansi.Strip(m.View()); !strings.Contains(view, credentialAsPastedModeNote) || strings.Contains(view, credentialLFModeNote) {
		t.Fatalf("editor must name as-pasted mode:\n%s", view)
	}
}

var dimRun = regexp.MustCompile("\x1b\\[2m.*?\x1b\\[0m")

// The mode line names its own key in both modes, styled as a controls-line
// key rather than folded into the dim note.
func TestCredentialModeLineNamesItsToggle(t *testing.T) {
	useANSI256(t)
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	m = openVisibleCredText(t, m)
	for _, mode := range []string{"LF", "as pasted"} {
		l := lineWith(t, m.View(), "Line endings:")
		if !strings.Contains(ansi.Strip(dimRun.ReplaceAllString(l, "")), "^t") {
			t.Fatalf("%s: the mode line must show ^t outside its dim note: %q", mode, l)
		}
		m = credKey(m, tea.KeyCtrlT)
	}
}

func TestCredentialCRPasteReachesTheSavePathAsLF(t *testing.T) {
	admin := newFakeCredAdmin()
	m := addCredential(credModel(t, admin, nil), credKindFile, "KEY", "")
	m = credPaste(openVisibleCredText(t, m), crPastedKey)
	m = credKey(m, tea.KeyEnter) // the final line break a paste does not carry
	m = credKey(m, tea.KeyCtrlS)
	want := lfKey + "\n"
	if m.credDraft != want {
		t.Fatalf("draft %q", m.credDraft)
	}
	m = credKey(m, tea.KeyCtrlS)
	m = typeCredPass(t, m, "pw", "pw")
	if m.mode != modeList || string(admin.open(t, "KEY", "pw")) != want {
		t.Fatal("saved credential is not the LF draft")
	}
}

func TestCredentialReopenedCRDraftOpensAsPasted(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	m = openVisibleCredText(t, m)
	m = credKey(m, tea.KeyCtrlT)
	m = credPaste(m, "a\rb")
	m = credKey(m, tea.KeyCtrlS)
	m = openVisibleCredText(t, m)
	if !m.credText.keepCR || m.credText.value != "a\rb" || !strings.Contains(m.View(), credentialAsPastedModeNote) {
		t.Fatal("reopening must neither convert a CR draft nor claim LF mode")
	}
	m = credKey(m, tea.KeyEsc)
	m.credDraft = "a\nb"
	m = openVisibleCredText(t, m)
	if m.credText.keepCR {
		t.Fatal("a draft without CRs must reopen in LF mode")
	}
}

// useANSI256 renders with 256 colours for the test, so each rainbow hue is a
// distinct 38;5;N sequence.
func useANSI256(t *testing.T) {
	t.Helper()
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
}

var hue256 = regexp.MustCompile(`38;5;(\d+)`)

// isRainbow reports whether a rendered line is painted in several 256-colour
// hues, which only rainbow does in this UI.
func isRainbow(line string) bool {
	hues := map[string]bool{}
	for _, m := range hue256.FindAllStringSubmatch(line, -1) {
		hues[m[1]] = true
	}
	return len(hues) >= 3
}

// lineWith returns the rendered line whose stripped text contains fragment.
func lineWith(t *testing.T, view, fragment string) string {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(l), fragment) {
			return l
		}
	}
	t.Fatalf("no line contains %q:\n%s", fragment, ansi.Strip(view))
	return ""
}

// A key with no final line break saves fine and fails at ssh time, so its
// warning is rainbow in the editor and on the form; ordinary notes stay dim.
func TestCredentialEndLineWarningIsRainbow(t *testing.T) {
	useANSI256(t)
	admin := newFakeCredAdmin()
	admin.disclosure = "writes to layer acme"
	m := addCredential(credModel(t, admin, nil), credKindFile, "KEY", "")
	m.width, m.height = 60, 24 // narrow enough that the warning wraps
	m = credPaste(openVisibleCredText(t, m), crPastedKey)
	for _, screen := range []string{"editor", "form"} {
		if screen == "form" {
			m = credKey(m, tea.KeyCtrlS)
			if m.mode != modeItem {
				t.Fatal("^s must return to the form")
			}
		}
		view := m.View()
		joined := strings.Join(strings.Fields(ansi.Strip(view)), " ")
		if !strings.Contains(joined, credentialEndNoBreak) || strings.Contains(view, credentialEndNoBreak) {
			t.Fatalf("%s: the warning must be present and painted per character:\n%s", screen, joined)
		}
		first := lineWith(t, view, "No final line break")
		if !isRainbow(first) {
			t.Fatalf("%s: warning line is not rainbow: %q", screen, first)
		}
		// The continuation carries the remedy; it must be as loud.
		cont := lineWith(t, view, "at the end.")
		if cont == first || strings.Contains(ansi.Strip(cont), "No final line break") {
			t.Fatalf("%s: the warning must wrap at width 60 for this check:\n%s", screen, ansi.Strip(view))
		}
		if !isRainbow(cont) {
			t.Fatalf("%s: warning continuation is not rainbow: %q", screen, cont)
		}
	}
	if l := lineWith(t, m.View(), "layer acme"); isRainbow(l) || !strings.Contains(l, "writes to layer acme") {
		t.Fatalf("an ordinary note must stay one dim run: %q", l)
	}
}

// noRainbow fails the test if any line of the view is rainbow.
func noRainbow(t *testing.T, view string) {
	t.Helper()
	for _, l := range strings.Split(view, "\n") {
		if isRainbow(l) {
			t.Fatalf("unexpected rainbow line: %q", ansi.Strip(l))
		}
	}
}

// The default state is one calm line: the picker shows LF chosen, the tail
// says what it does, and nothing warns.
func TestCredentialLFModeIsOneCalmLine(t *testing.T) {
	useANSI256(t)
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	view := openVisibleCredText(t, m).View()
	plain := ansi.Strip(view)
	if strings.Count(plain, "[as pasted]") != 1 || !strings.Contains(plain, credentialLFModeNote) || strings.Contains(plain, credentialAsPastedModeNote) {
		t.Fatalf("LF mode must be one picker line naming LF:\n%s", plain)
	}
	if !strings.Contains(view, selStyle.Render("[LF]")) || strings.Contains(view, selStyle.Render("[as pasted]")) {
		t.Fatal("the LF option must be the highlighted one")
	}
	if strings.Contains(plain, credentialCRsNeedLF) {
		t.Fatal("LF mode must not warn about CRs")
	}
	noRainbow(t, view)
}

// As pasted on a draft with no CRs has nothing to warn about.
func TestCredentialAsPastedEmptyDraftDoesNotWarn(t *testing.T) {
	useANSI256(t)
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	m = credKey(openVisibleCredText(t, m), tea.KeyCtrlT)
	view := m.View()
	plain := ansi.Strip(view)
	if !strings.Contains(plain, credentialAsPastedModeNote) || !strings.Contains(view, selStyle.Render("[as pasted]")) || strings.Contains(view, selStyle.Render("[LF]")) {
		t.Fatalf("as-pasted mode must be the highlighted option:\n%s", plain)
	}
	if strings.Contains(plain, credentialCRsNeedLF) {
		t.Fatal("a draft without CRs must not warn about CRs")
	}
	noRainbow(t, view)
}

// A CR draft saves fine and fails at ssh time, so as-pasted mode warns in
// rainbow with the count, and ^t's conversion clears it.
func TestCredentialAsPastedCRDraftWarnsInRainbow(t *testing.T) {
	useANSI256(t)
	for _, tt := range []struct {
		draft, count string
	}{
		{"a\rb", "1 CR " + credentialCRsNeedLF},
		{"a\r\nb\rc\r\n", "3 CRs " + credentialCRsNeedLF}, // a CRLF counts once
	} {
		m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
		m.width = 60 // narrow, so a wrapped warning would show
		m = credPaste(credKey(openVisibleCredText(t, m), tea.KeyCtrlT), tt.draft)
		view := m.View()
		if !strings.Contains(ansi.Strip(view), tt.count) {
			t.Fatalf("%q: want %q:\n%s", tt.draft, tt.count, ansi.Strip(view))
		}
		for _, l := range strings.Split(view, "\n") {
			if p := ansi.Strip(l); (strings.Contains(p, credentialCRsNeedLF) || strings.Contains(p, "^t converts")) && !isRainbow(l) {
				t.Fatalf("%q: CR warning line is not rainbow: %q", tt.draft, p)
			}
		}
		m = credKey(m, tea.KeyCtrlT)
		if view := ansi.Strip(m.View()); strings.ContainsRune(m.credText.value, '\r') || strings.Contains(view, credentialCRsNeedLF) {
			t.Fatalf("%q: ^t must convert the CRs and clear the warning:\n%s", tt.draft, view)
		}
	}
}

// The form saves without another screen, so it repeats the CR warning.
func TestCredentialFormWarnsAboutCRsInRainbow(t *testing.T) {
	useANSI256(t)
	admin := newFakeCredAdmin()
	admin.disclosure = "writes to layer acme"
	m := addCredential(credModel(t, admin, nil), credKindEnv, "KEY", "")
	m = credPaste(credKey(openVisibleCredText(t, m), tea.KeyCtrlT), "a\rb\rc")
	m = credKey(m, tea.KeyCtrlS)
	view := m.View()
	if m.mode != modeItem || !strings.Contains(ansi.Strip(view), "2 CRs "+credentialCRsNeedLF) {
		t.Fatalf("form must warn about the draft's CRs:\n%s", ansi.Strip(view))
	}
	if l := lineWith(t, view, credentialCRsNeedLF); !isRainbow(l) {
		t.Fatalf("CR note is not rainbow: %q", l)
	}
	if l := lineWith(t, view, "layer acme"); isRainbow(l) {
		t.Fatalf("an ordinary note must stay dim: %q", l)
	}
	m = credKey(openVisibleCredText(t, m), tea.KeyCtrlT) // reopens as pasted; ^t converts
	m = credKey(m, tea.KeyCtrlS)
	if m.mode != modeItem || m.credDraft != "a\nb\nc" || strings.Contains(ansi.Strip(m.View()), credentialCRsNeedLF) {
		t.Fatalf("an LF draft must not warn on the form:\n%s", ansi.Strip(m.View()))
	}
}

// A three-digit count wraps the form note at 60 columns; the remedy on the
// continuation line must be as loud.
func TestCredentialFormCRNoteContinuationIsRainbow(t *testing.T) {
	useANSI256(t)
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	m.width, m.height = 60, 40
	m = credPaste(credKey(openVisibleCredText(t, m), tea.KeyCtrlT), strings.Repeat("\r", 1000))
	m = credKey(m, tea.KeyCtrlS)
	view := m.View()
	first := lineWith(t, view, credentialCRsNeedLF)
	cont := lineWith(t, view, "^t.")
	if m.mode != modeItem || cont == first {
		t.Fatalf("the check needs the CR note wrapped at width 60:\n%s", ansi.Strip(view))
	}
	if !isRainbow(first) || !isRainbow(cont) {
		t.Fatalf("every line of the CR note must be rainbow: %q / %q", first, cont)
	}
}

// The largest count a draft can hold keeps the editor's CR warning on one
// line at 60 columns, which credentialTextMinHeight's budget assumes.
func TestCredentialEditorCRWarningFitsOneLineAtMaxCount(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindEnv, "KEY", "")
	m = openVisibleCredText(t, m)
	m.width, m.height = 60, m.credentialTextMinHeight()
	m.credText = credentialText{value: strings.Repeat("\r", credentials.MaxValue), keepCR: true}
	l := ansi.Strip(lineWith(t, m.View(), credentialCRsNeedLF))
	if !strings.Contains(l, "262144 CRs") || !strings.Contains(l, "^t converts them.") || ansi.StringWidth(l) > 60 {
		t.Fatalf("max-count CR warning must fit one 60-column line: %q", l)
	}
}

// Worst case at the minimum size: the CR warning, the end-line warning, and
// a short error all show, with the text and controls, and nothing clips.
func TestCredentialMinimumEditorFitsEveryWarning(t *testing.T) {
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
	m = openVisibleCredText(t, m)
	m.width, m.height = 60, m.credentialTextMinHeight()
	m = credKey(m, tea.KeyCtrlT)
	m = credPaste(m, crPastedKey)
	m = credKey(m, tea.KeyCtrlV)
	raw := m.View()
	if len(strings.Split(m.viewCredText(), "\n")) > m.height-1 {
		t.Fatalf("worst case does not fit the minimum height:\n%s", ansi.Strip(raw))
	}
	view := strings.Join(strings.Fields(ansi.Strip(raw)), " ")
	for _, fragment := range []string{"VISIBLE", credentialAsPastedModeNote, "3 CRs " + credentialCRsNeedLF, "^t converts", credentialEndNoBreak, "Press Enter at the end", "terminal's paste", "^s blocked", "-----END OPENSSH PRIVATE KEY-----", "discard changes", "line endings"} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("worst-case minimum editor hides %q:\n%s", fragment, ansi.Strip(raw))
		}
	}
	if strings.Contains(view, "more below") || strings.Contains(view, "more above") {
		t.Fatalf("worst-case minimum editor was clipped:\n%s", ansi.Strip(raw))
	}
}

// Without colour every fragment is one plain run, so the text still reads
// and still matches.
func TestCredentialLoudTextDegradesToPlainText(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
	m = credPaste(openVisibleCredText(t, m), crPastedKey)
	view := m.View()
	for _, fragment := range []string{"[LF] [as pasted]  " + credentialLFModeNote, credentialEndNoBreak} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("missing plain %q:\n%s", fragment, view)
		}
	}
	m = credKey(credKey(m, tea.KeyCtrlT), tea.KeyCtrlA)
	m = credPaste(m, "x\r")
	if view := m.View(); !strings.Contains(view, "[LF] [as pasted]  "+credentialAsPastedModeNote) || !strings.Contains(view, "1 CR "+credentialCRsNeedLF) {
		t.Fatalf("as-pasted fragments must be plain runs:\n%s", view)
	}
	m = credKey(m, tea.KeyCtrlS)
	if view := m.View(); m.mode != modeItem || !strings.Contains(view, "1 CR "+credentialCRsNeedLF) || !strings.Contains(view, credentialEndNoBreak) || strings.Contains(view, "\x1b[") {
		t.Fatalf("the form notes must be plain:\n%q", view)
	}
}

// warningNeighbours returns the stripped lines directly above the line
// holding first and directly below the line holding last.
func warningNeighbours(t *testing.T, view, first, last string) (above, below string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	top, bottom := -1, -1
	for i, l := range lines {
		if top < 0 && strings.Contains(l, first) {
			top = i
		}
		if strings.Contains(l, last) {
			bottom = i
		}
	}
	if top < 1 || bottom < top || bottom+1 >= len(lines) {
		t.Fatalf("no warning block %q … %q with neighbours:\n%s", first, last, ansi.Strip(view))
	}
	return strings.TrimSpace(lines[top-1]), strings.TrimSpace(lines[bottom+1])
}

// With room to spare, a blank line sets each warning off from its
// neighbours: one above the CR warning (the blank before the text is below
// it already), one either side of the end-line warning.
func TestCredentialWarningsArePaddedWhenThereIsRoom(t *testing.T) {
	for _, tt := range []struct {
		name     string
		asPasted bool
		draft    string
		endLine  bool
		crs      bool
	}{
		{"end line", false, lfKey, true, false},
		{"CRs", true, "a\rb", false, true},
		{"both", true, crPastedKey, true, true},
	} {
		m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
		m.width, m.height = 80, 30
		m = openVisibleCredText(t, m)
		if tt.asPasted {
			m = credKey(m, tea.KeyCtrlT)
		}
		m = credPaste(m, tt.draft)
		view := m.View()
		if tt.endLine {
			if above, below := warningNeighbours(t, view, "No final line break", "end."); above != "" || below != "" {
				t.Fatalf("%s: end-line warning must be padded, got %q above and %q below:\n%s", tt.name, above, below, ansi.Strip(view))
			}
		}
		if tt.crs {
			above, below := warningNeighbours(t, view, credentialCRsNeedLF, "^t converts them.")
			if above != "" || below != "" {
				t.Fatalf("%s: CR warning must be padded, got %q above and %q below:\n%s", tt.name, above, below, ansi.Strip(view))
			}
			if l := strings.Split(ansi.Strip(view), "\n"); !strings.Contains(strings.Join(l, "\n"), "toggles\n\n"+credentialCRWarning(m.credText.value)) {
				t.Fatalf("%s: the blank above the CR warning must follow the mode line:\n%s", tt.name, ansi.Strip(view))
			}
		}
	}
}

// The padding is all or nothing and spent only when the text keeps two
// rows. At 60 columns with no error the budget reserves two error rows and
// two control rows, so the text has height-13 rows with the end-line
// warning (header 5, position 1, warning 2, renderer 1) and height-14 with
// both warnings (header 6): padding of 2 and 3 rows needs height 17 and 19.
func TestCredentialWarningPaddingThreshold(t *testing.T) {
	for _, tt := range []struct {
		name     string
		asPasted bool
		least    int
	}{
		{"end line", false, 17},
		{"both", true, 19},
	} {
		for _, height := range []int{tt.least, tt.least - 1} {
			m := addCredential(credModel(t, newFakeCredAdmin(), nil), credKindFile, "KEY", "")
			m = openVisibleCredText(t, m)
			m.width, m.height = 60, height
			if tt.asPasted {
				m = credKey(m, tea.KeyCtrlT)
			}
			m = credPaste(m, crPastedKey)
			view := m.View()
			above, below := warningNeighbours(t, view, "No final line break", "end.")
			padded := above == "" && below == ""
			if padded != (height == tt.least) || (padded && !strings.Contains(ansi.Strip(view), "view 3–4/4")) {
				t.Fatalf("%s at 60x%d: padded %v, want %v with two text rows:\n%s", tt.name, height, padded, height == tt.least, ansi.Strip(view))
			}
			if !padded && !strings.Contains(above, "bytes ·") {
				t.Fatalf("%s at 60x%d: unpadded warning must follow the position line, got %q", tt.name, height, above)
			}
		}
	}
}
