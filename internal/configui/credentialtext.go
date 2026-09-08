package configui

// A credential draft must not pass through Bubbles' input sanitizers: both
// widgets replace tabs, and the single-line widget also flattens newlines.
// Keep the text independently and escape controls only in the display. There
// is no clipboard command, filesystem handoff, decryption, or write here.
import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/pjlsergeant/byre/internal/credentials"
)

type credentialText struct {
	value string
	pos   int // rune offset, never a byte offset
	err   string
}

const credentialTabWidth = 8

var credentialMarkerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

func credentialLines(value string) string {
	n := strings.Count(value, "\n") + strings.Count(value, "\r") - strings.Count(value, "\r\n") + 1
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

type credentialTextTransition int

const (
	credentialTextSteady credentialTextTransition = iota
	credentialTextEntering
	credentialTextHiding
	credentialTextExiting
)

type credentialRevealMsg struct{}
type credentialHiddenMsg struct{}
type credentialClosedMsg struct{}

// Commands run asynchronously to View. Reveal only AFTER entering the alternate
// screen; exit only AFTER a hidden View has replaced the renderer's buffer.
// Queue typing while entering. Closing keys must NOT cross into the form's
// different keymap: a repeated editor ^s is not consent to persist a draft.
func (m model) credentialTextTransitionMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case credentialRevealMsg:
		if m.credTextTransition != credentialTextEntering {
			return m, nil
		}
		m.credTextTransition, m.credTextVisible = credentialTextSteady, true
	case credentialHiddenMsg:
		if m.credTextTransition != credentialTextHiding {
			return m, nil
		}
		m.credTextTransition = credentialTextExiting
		return m, tea.Sequence(tea.ExitAltScreen, func() tea.Msg { return credentialClosedMsg{} })
	case credentialClosedMsg:
		if m.credTextTransition != credentialTextExiting {
			return m, nil
		}
		m.credTextTransition, m.mode = credentialTextSteady, modeItem
		m.credTextKeys = nil
		// The inline cursor was restored to the pre-overlay frame, but the
		// renderer counts the last alternate-screen frame. Reset its origin
		// as on resize so different frame heights cannot shift the form.
		return m, tea.ClearScreen
	}
	keys := m.credTextKeys
	m.credTextKeys = nil
	var cmds []tea.Cmd
	for _, key := range keys {
		next, cmd := m.Update(key)
		m = next.(model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m model) hideCredText() (tea.Model, tea.Cmd) {
	m.credText = credentialText{}
	m.credTextVisible = false
	m.credTextTransition = credentialTextHiding
	return m, func() tea.Msg { return credentialHiddenMsg{} }
}

func (m model) credentialItem() bool {
	return m.listField == fEnv && isCredentialScheme(m.itemMode)
}

func (m *model) clearCredentialDraft() {
	m.credDraft, m.credInputWarning = "", ""
	m.credMultiline, m.credTextVisible = false, false
	m.credText = credentialText{}
	m.credTextTransition, m.credTextKeys = credentialTextSteady, nil
}

func (m model) credentialDraftSummary() string {
	if m.credDraft == "" {
		return "No replacement entered"
	}
	return fmt.Sprintf("Not saved yet · %s · hidden", credentialLines(m.credDraft))
}

const credentialSingleLineWarning = "This field cannot accept newlines or control characters.\nNothing saved. Use ^e to re-enter the whole value."

// Refuse before any widget can echo/sanitize a paste. Latch Value's newline
// refusal so later unbracketed chunks cannot leave a saveable partial value.
func (m model) credentialItemKey(msg tea.KeyMsg) (model, tea.Cmd, bool) {
	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if unicode.IsControl(r) || r == utf8.RuneError {
				if m.itemInputIndex() == 1 && !m.credMultiline {
					m.credInputWarning = credentialSingleLineWarning
				} else {
					m.itemErr = "Paste rejected: use ^e to edit a multiline replacement."
				}
				return m, nil, true
			}
		}
	}
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+q":
		m.inputs[1].SetValue("")
		m.clearCredentialDraft()
		m.mode = modeList
		return m, nil, true
	case "enter", "ctrl+j":
		if m.credMultiline || m.itemInputIndex() != 1 {
			m.itemErr = "Enter never saves. Use ^e to edit the replacement, or ^s to save."
			return m, nil, true
		}
		m.credInputWarning = credentialSingleLineWarning
		return m, nil, true
	case "ctrl+e":
		m.mode, m.credTextVisible = modeCredText, false
		return m, nil, true
	case "ctrl+v":
		m.itemErr = "Use your terminal's paste command; ^e opens the multiline editor."
		return m, nil, true
	case "ctrl+s":
		if m.credInputWarning != "" {
			m.itemErr = "Not saved: the rejected input must be re-entered with ^e, or Esc to cancel."
			return m, nil, true
		}
	}
	if m.itemInputIndex() != 1 {
		return m, nil, false
	}
	if m.credMultiline || m.credInputWarning != "" {
		switch msg.String() {
		case "tab", "shift+tab", "up", "down", "ctrl+s":
			return m, nil, false
		}
		m.itemErr = "Use ^e to edit the replacement; it will be visible."
		return m, nil, true
	}
	if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
		// Bound the draft in memory; the writer enforces the kind-specific cap.
		if len(m.inputs[1].Value())+len(string(msg.Runes)) > credentials.MaxValue {
			m.credInputWarning = "Input exceeds 256 KiB. Nothing inserted; open ^e to enter the replacement again."
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m model) updateCredText(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.credTextTransition != credentialTextSteady {
		if m.credTextTransition == credentialTextEntering {
			m.credTextKeys = append(m.credTextKeys, msg)
		}
		return m, nil
	}
	if msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyCtrlQ {
		if m.credTextVisible {
			return m.hideCredText()
		}
		m.mode = modeItem
		return m, nil
	}
	if m.width < 60 || m.height < m.credentialTextMinHeight() {
		return m, nil // never edit text the user cannot see with its warning
	}
	if !m.credTextVisible {
		if msg.Type == tea.KeyCtrlE {
			value := m.inputs[1].Value()
			if m.credMultiline {
				value = m.credDraft
			}
			if m.credInputWarning != "" {
				value = ""
			}
			m.credText = credentialText{value: value, pos: utf8.RuneCountInString(value)}
			m.credTextTransition = credentialTextEntering
			return m, tea.Sequence(tea.EnterAltScreen, func() tea.Msg { return credentialRevealMsg{} })
		}
		return m, nil
	}
	if msg.Type == tea.KeyCtrlS {
		if m.credText.err != "" {
			return m, nil
		}
		m.credDraft, m.credMultiline = m.credText.value, true
		m.inputs[1].SetValue("")
		m.credInputWarning, m.itemErr = "", ""
		return m.hideCredText()
	}
	m.credText = m.credText.update(msg)
	return m, nil
}

func (e credentialText) update(msg tea.KeyMsg) credentialText {
	r := []rune(e.value)
	p := e.pos
	start := func(p int) int {
		for p > 0 {
			if r[p-1] == '\n' || (r[p-1] == '\r' && (p == len(r) || r[p] != '\n')) {
				break
			}
			p--
		}
		return p
	}
	end := func(p int) int {
		for p < len(r) && r[p] != '\n' && r[p] != '\r' {
			p++
		}
		return p
	}
	var insert string
	switch msg.Type {
	case tea.KeyLeft:
		e.pos = max(0, p-1)
	case tea.KeyRight:
		e.pos = min(len(r), p+1)
	case tea.KeyHome, tea.KeyCtrlA:
		e.pos = start(p)
	case tea.KeyEnd, tea.KeyCtrlE:
		e.pos = end(p)
	case tea.KeyUp:
		s := start(p)
		if s > 0 {
			previousEnd := s - 1
			if r[previousEnd] == '\n' && previousEnd > 0 && r[previousEnd-1] == '\r' {
				previousEnd--
			}
			e.pos = min(previousEnd, start(previousEnd)+p-s)
		}
	case tea.KeyDown:
		n := end(p)
		if n < len(r) {
			next := n + 1
			if r[n] == '\r' && next < len(r) && r[next] == '\n' {
				next++
			}
			e.pos = min(end(next), next+p-start(p))
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if p > 0 {
			e.value = string(r[:p-1]) + string(r[p:])
			e.pos--
		}
	case tea.KeyDelete, tea.KeyCtrlD:
		if p < len(r) {
			e.value = string(r[:p]) + string(r[p+1:])
		}
	case tea.KeyEnter, tea.KeyCtrlJ:
		insert = "\n"
	case tea.KeyTab:
		insert = "\t"
	case tea.KeySpace:
		insert = " " // Bubble Tea sends a typed space as KeySpace, not KeyRunes.
	case tea.KeyCtrlV:
		e.err = "Use your terminal's paste command."
		return e
	default:
		if msg.Type == tea.KeyRunes {
			insert = string(msg.Runes)
		}
	}
	if insert != "" {
		if len(e.value)+len(insert) > credentials.MaxValue {
			e.err = "Input exceeds 256 KiB. Nothing inserted; shorten the draft and paste again."
			return e
		}
		e.value = string(r[:p]) + insert + string(r[p:])
		e.pos += utf8.RuneCountInString(insert)
	}
	e.err = ""
	return e
}

func (m model) viewCredText() string {
	if m.credTextTransition == credentialTextHiding || m.credTextTransition == credentialTextExiting {
		return m.viewItem()
	}
	if m.width < 60 || m.height < m.credentialTextMinHeight() {
		return fmt.Sprintf("Editor paused: enlarge to 60×%d.\nEsc cancels. Draft not saved.", m.credentialTextMinHeight())
	}
	if !m.credTextVisible {
		var paragraphs []string
		for _, note := range []string{
			"Visible replacement",
			"Your draft will be shown without masking: anyone watching or recording can read it. Stored credentials are never loaded; no plaintext editor file is created.",
			"Use bracketed paste. Invalid UTF-8/U+FFFD can be lost; binary files need CLI input and an existing identity (see configuration reference).",
		} {
			paragraphs = append(paragraphs, strings.Join(wrapLine(note, m.width), "\n"))
		}
		return strings.Join(paragraphs, "\n\n") + "\n\n" + helpLine("^e", "show draft + open editor", "esc", "cancel")
	}
	width := max(8, m.width-2)
	rows, cursorRow := m.credText.rows(width)
	// Reserve space for the persistent warning, position, errors and controls.
	height := max(1, m.height-9)
	from := max(0, cursorRow-height+1)
	to := min(len(rows), from+height)
	var b strings.Builder
	b.WriteString("VISIBLE replacement — not saved\n")
	b.WriteString("Stored credential NOT loaded.\n")
	b.WriteString(credentialMarkerStyle.Render("Tabs: ⇥ (8 cols)  CR: ␍  LF: ↵  End: ∎") + " (display only)\n\n")
	b.WriteString(strings.Join(rows[from:to], "\n"))
	fmt.Fprintf(&b, "\n%d bytes · %s · view %d–%d/%d\n", len(m.credText.value), credentialLines(m.credText.value), from+1, to, len(rows))
	if m.credText.err != "" {
		b.WriteString(m.errLine(m.credText.err) + "\n")
		b.WriteString("^s blocked until another edit or cursor move. Esc cancels.\n")
	}
	b.WriteString(helpLine("enter", "newline", "^s", "use draft (NOT save)", "esc", "discard changes"))
	return b.String()
}

func (m model) credentialTextMinHeight() int {
	if !m.credTextVisible {
		return 16 // room for the pre-entry disclosure
	}
	return 12
}

// Soft-wrap the display, not the value. Controls are inert visible notation,
// so a pasted escape sequence cannot instruct the terminal. The cursor is a
// styled cell; no secret enters the ordinary form or its status/error strings.
func (e credentialText) rows(width int) ([]string, int) {
	rows := []string{""}
	col, cursorRow := 0, 0
	r := []rune(e.value)
	for i := 0; i <= len(r); i++ {
		cell := "∎"
		newline, marker, crlf := false, true, false
		if i < len(r) {
			switch r[i] {
			case '\n':
				cell, newline = "↵", true
			case '\r':
				cell = "␍"
				crlf = i+1 < len(r) && r[i+1] == '\n'
				newline = !crlf
			case '\t':
				cell = strings.Repeat("─", credentialTabWidth-col%credentialTabWidth-1) + "⇥"
			default:
				cell = string(r[i])
				marker = !unicode.IsPrint(r[i])
				if marker {
					cell = strings.Trim(strconv.QuoteRune(r[i]), "'")
				}
			}
		}
		w := ansi.StringWidth(cell)
		wrapWidth := w
		if crlf {
			wrapWidth++ // keep both CRLF markers on the same display row
		}
		if col+wrapWidth > width {
			rows = append(rows, "")
			col = 0
			if i < len(r) && r[i] == '\t' {
				cell = strings.Repeat("─", credentialTabWidth-1) + "⇥"
				w = credentialTabWidth
			}
		}
		if marker || i == e.pos {
			style := credentialMarkerStyle
			if !marker {
				style = lipgloss.NewStyle()
			}
			if i == e.pos {
				cursorRow = len(rows) - 1
				style = style.Reverse(true)
			}
			cell = style.Render(cell)
		}
		rows[len(rows)-1] += cell
		col += w
		if newline {
			rows = append(rows, "")
			col = 0
		}
	}
	return rows, cursorRow
}
