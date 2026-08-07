package configui

// The inline vault-creation modal (modeCredPass): a vault-less project's
// first ^s with staged credential values asks for the new passphrase right
// there — the brief's "inline on the first editor ^s" rule. Everything is
// masked; esc cancels back to the form with the values still staged (the
// config half of the save has not happened either — the modal runs before
// any write, so cancel leaves the disk untouched).

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/credentials"
)

// openCredPass arms the modal with two fresh masked inputs.
func (m model) openCredPass() model {
	for i := range m.credPassInputs {
		in := textinput.New()
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
		in.CharLimit = 0
		in.Prompt = ""
		m.credPassInputs[i] = in
	}
	m.credPassInputs[0].Focus()
	m.credPassFocus = 0
	m.credPassErr = ""
	m.mode = modeCredPass
	m.status = ""
	m.errMsg = ""
	return m
}

func (m model) updateCredPass(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+q", "ctrl+c":
		// Back to the form, values still staged, nothing written — the
		// footer's unsaved marker keeps saying so.
		m.mode = modeForm
		m.credPassphrase = ""
		m.status = "Not saved — the staged values need a vault; ctrl+s asks again"
		return m, nil
	case "tab", "down", "shift+tab", "up":
		next := 1 - m.credPassFocus
		m.credPassInputs[m.credPassFocus].Blur()
		m.credPassInputs[next].Focus()
		m.credPassFocus = next
		return m, nil
	case "enter":
		if m.credPassFocus == 0 {
			m.credPassInputs[0].Blur()
			m.credPassInputs[1].Focus()
			m.credPassFocus = 1
			return m, nil
		}
		pw := m.credPassInputs[0].Value()
		if pw == "" {
			m.credPassErr = credentials.EmptyPassphraseWorthless
			return m, nil
		}
		if pw != m.credPassInputs[1].Value() {
			m.credPassErr = "passphrases do not match"
			return m, nil
		}
		// Hand the answer to the save that opened this modal and re-run it:
		// config write, vault creation, and the value flush ride the normal
		// ^s path (locks, drift check, status line included).
		m.credPassphrase = pw
		m.credPassInputs[0].SetValue("")
		m.credPassInputs[1].SetValue("")
		m.mode = modeForm
		return m.save(), nil
	}
	var cmd tea.Cmd
	m.credPassInputs[m.credPassFocus], cmd = m.credPassInputs[m.credPassFocus].Update(msg)
	return m, cmd
}

func (m model) viewCredPass() string {
	var b strings.Builder
	b.WriteString(focusStyle.Render("Create the credentials vault") + "\n\n")
	b.WriteString("This project has no vault yet. The passphrase encrypts the staged\nvalues at rest in the host-side store; byre asks for it once per launch\nto deliver them. There is no recovery — a forgotten passphrase means a\nnew vault.\n\n")
	labels := [2]string{"New passphrase", "Confirm"}
	for i, in := range m.credPassInputs {
		cursor := "  "
		if m.credPassFocus == i {
			cursor = cursorStyle.Render("▸ ")
		}
		b.WriteString(cursor + labels[i] + ": " + in.View() + "\n")
	}
	if m.credPassErr != "" {
		b.WriteString("\n" + m.errLine(m.credPassErr) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("enter create + save   ·   esc back (values stay staged, unsaved)") + "\n")
	return b.String()
}
