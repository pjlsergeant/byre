package configui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
	for _, fragment := range []string{"without masking", "^e"} {
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
	m.width, m.height = 60, 12
	if !strings.Contains(m.View(), "VISIBLE") || !strings.Contains(m.View(), "NOT loaded") || !strings.Contains(m.View(), "NOT save") {
		t.Fatal("minimum live editor hides visibility or save-boundary notice")
	}
	m = credKey(m, tea.KeyCtrlV)
	if !strings.Contains(m.View(), "^s blocked") {
		t.Fatal("editor error must explain why the draft cannot be accepted")
	}
	m = credKey(m, tea.KeyLeft)
	m = credKey(m, tea.KeyCtrlS)
	if !strings.Contains(m.View(), "Not saved yet") || !strings.Contains(m.View(), "hidden") {
		t.Fatal("narrow form must keep the unsaved/hidden draft summary visible")
	}
}
