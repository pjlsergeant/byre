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
	// Terminals send pasted line breaks as CR, which key parsers such as
	// ssh-keygen reject, so pastes become LF unless the user keeps them.
	keepCR bool
	// converted counts the line breaks this editor session has rewritten to
	// LF, so the editor can say what it did to the secret. Later edits do
	// not undo it; the editor rebuilds the struct, and so the count, on open.
	converted int
}

// The mode line's notes say in words which mode is on: the picker's reverse
// video vanishes without colour and in a text capture. Neither appears on
// the other mode's line. Shared by the view and its tests.
const (
	credentialLFModeNote       = "recommended"
	credentialAsPastedModeNote = "CR/CRLF kept"
)

// credentialCRsNeedLF follows the count in the CR warning, shared by the
// editor, the form note, and their tests.
const credentialCRsNeedLF = "in the draft: keys need LF."

// credentialChangedToLF follows the count in the notice of line breaks the
// editor converted, shared by the view and its tests.
const credentialChangedToLF = "changed to LF."

// credentialConvertedNotice says how many pasted line breaks the editor
// rewrote to LF: anything byre does to a secret is stated, not silent. A
// CRLF holds one CR and counts once.
func credentialConvertedNotice(n int) string {
	noun := "line breaks"
	if n == 1 {
		noun = "line break"
	}
	return fmt.Sprintf("⚠ %d pasted %s %s", n, noun, credentialChangedToLF)
}

// credentialCRWarning opens the warning for a draft holding CRs: ssh-keygen
// rejects a key whose line breaks are CR, and the draft saves fine, so the
// failure would otherwise surface only when ssh reads the key. A CRLF holds
// one CR and counts once.
func credentialCRWarning(draft string) string {
	n := strings.Count(draft, "\r")
	noun := "CRs"
	if n == 1 {
		noun = "CR"
	}
	return fmt.Sprintf("⚠ %d %s %s", n, noun, credentialCRsNeedLF)
}

// credentialEndNoBreak opens the warning for a draft ending in a PEM END
// line: OpenSSH refuses a private key file whose END line has no final line
// break, and a terminal paste carries none. Shared by the editor, the form
// note, and their tests.
const credentialEndNoBreak = "No final line break: -----END----- lines usually need one."

// endsInBareEndLine reports whether the draft's last line is a PEM END line
// ("-----END ...-----") with nothing after it. The match is exact: leading
// whitespace, or any trailing character including CR, is no match.
func endsInBareEndLine(draft string) bool {
	last := draft[strings.LastIndexAny(draft, "\r\n")+1:]
	return strings.HasPrefix(last, "-----END ") && strings.HasSuffix(last, "-----")
}

// toLF rewrites CRLF and lone CR as LF.
func toLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// toggleLineEndings flips the mode. Entering LF converts the draft's CRs in
// place, keeping the cursor on the same character; a cursor between the
// halves of a CRLF lands on the LF that replaces the pair. Leaving LF only
// changes later pastes: a converted break cannot be told from a typed one.
func (e credentialText) toggleLineEndings() credentialText {
	e.keepCR = !e.keepCR
	if e.keepCR {
		return e
	}
	r := []rune(e.value)
	prefix := r[:e.pos]
	if e.pos > 0 && e.pos < len(r) && r[e.pos-1] == '\r' && r[e.pos] == '\n' {
		prefix = r[:e.pos-1]
	}
	e.pos = utf8.RuneCountInString(toLF(string(prefix)))
	e.converted += strings.Count(e.value, "\r")
	e.value = toLF(e.value)
	return e
}

const credentialTabWidth = 8

var credentialMarkerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

// credentialModeLine is the line-ending picker, unfocused, followed by a dim
// note naming the active mode and its key, drawn as a controls-line entry
// so ^t looks the same in both places.
func (e credentialText) credentialModeLine() string {
	sel, note := 0, credentialLFModeNote
	if e.keepCR {
		sel, note = 1, credentialAsPastedModeNote
	}
	return "Line endings: " + renderSeg([]string{"LF", "as pasted"}, sel, false) + "  " +
		dimStyle.Render(note) + dimStyle.Render(" · ") + helpLine("^t", "toggles")
}

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
			// LF mode unless the draft already holds CRs, which only an
			// as-pasted session can have put there: the mode line must
			// describe the draft on screen, and reopening never converts it.
			m.credText = credentialText{value: value, pos: utf8.RuneCountInString(value), keepCR: strings.ContainsRune(value, '\r')}
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
	converts := 0 // CRs the insert rewrites to LF
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
	case tea.KeyCtrlT:
		e = e.toggleLineEndings()
	default:
		if msg.Type == tea.KeyRunes {
			insert = string(msg.Runes)
			if !e.keepCR {
				converts = strings.Count(insert, "\r")
				insert = toLF(insert)
			}
		}
	}
	if insert != "" {
		if len(e.value)+len(insert) > credentials.MaxValue {
			e.err = "Input exceeds 256 KiB. Nothing inserted; shorten the draft and paste again."
			return e
		}
		e.value = string(r[:p]) + insert + string(r[p:])
		e.pos += utf8.RuneCountInString(insert)
		e.converted += converts
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
			"Pasted CR and CRLF line endings become LF; ^t in the editor keeps them as pasted.",
		} {
			paragraphs = append(paragraphs, strings.Join(wrapLine(note, m.width), "\n"))
		}
		return strings.Join(paragraphs, "\n\n") + "\n\n" + helpLine("^e", "show draft + open editor", "esc", "cancel")
	}
	help := packedHelp(m.width, "enter", "newline", "^s", "use draft (NOT save)", "esc", "discard changes", "^t", "line endings")
	var warning []string
	if endsInBareEndLine(m.credText.value) {
		warning = wrapLine("⚠ "+credentialEndNoBreak+" Press Enter at the end.", m.width)
	}
	// The header warning sits under the mode line and depends on the mode,
	// so at most one shows. As pasted, it is the CR warning: only that mode
	// can hold CRs (LF mode converts on entry and on every insert), and
	// gating on both keeps "^t converts them" true. In LF mode, it is the
	// notice of line breaks this session converted; "^t stops this" would
	// be false as pasted.
	var headerWarning []string
	switch {
	case m.credText.keepCR && strings.ContainsRune(m.credText.value, '\r'):
		headerWarning = wrapLine(credentialCRWarning(m.credText.value)+" ^t converts them.", m.width)
	case !m.credText.keepCR && m.credText.converted > 0:
		headerWarning = wrapLine(credentialConvertedNotice(m.credText.converted)+" ^t stops this.", m.width)
	}
	// The error block is rendered once, so the rows it is budgeted and the
	// rows it prints cannot disagree.
	var errBlock string
	if m.credText.err != "" {
		errBlock = m.errLine(m.credText.err) + "\n" +
			strings.Join(wrapLine("^s blocked until another edit or cursor move. Esc cancels.", m.width), "\n") + "\n"
	}
	width := max(8, m.width-2)
	rows, cursorRow := m.credText.rows(width)
	// The text gets what is left after the header (three fixed lines, the
	// mode line, the header warning when it shows, a blank line), the position
	// line, the end-line warning when it shows, the error block, the
	// controls, and the row clipHeight keeps for the inline renderer. The
	// error block always reserves at least two rows, so the text does not
	// jump when a short error appears; a taller one takes rows from the text.
	// Blank lines set the warnings off from their neighbours (one above the
	// header warning, one either side of the end-line warning), all or none:
	// they are spent only when the text still keeps two rows, so they never
	// cost the minimum size. The one padRows decides budget and output.
	headerRows := 3 + 1 + len(headerWarning) + 1
	errRows := max(2, strings.Count(errBlock, "\n"))
	helpRows := strings.Count(help, "\n") + 1
	textRows := m.height - headerRows - 1 - len(warning) - errRows - helpRows - 1
	padRows := 0
	if len(headerWarning) > 0 {
		padRows++
	}
	if len(warning) > 0 {
		padRows += 2
	}
	if textRows-padRows < 2 {
		padRows = 0
	}
	padded := padRows > 0
	height := max(1, textRows-padRows)
	from := max(0, cursorRow-height+1)
	to := min(len(rows), from+height)
	var b strings.Builder
	b.WriteString("VISIBLE replacement — not saved\n")
	b.WriteString("Stored credential NOT loaded.\n")
	b.WriteString(credentialMarkerStyle.Render("Tabs: ⇥ (8 cols)  CR: ␍  LF: ↵  End: ∎") + " (display only)\n")
	b.WriteString(m.credText.credentialModeLine() + "\n")
	// Warnings are rainbow, wrapped first so each painted line fits the width.
	if padded && len(headerWarning) > 0 {
		b.WriteString("\n")
	}
	for _, l := range headerWarning {
		b.WriteString(rainbow(l) + "\n")
	}
	b.WriteString("\n" + strings.Join(rows[from:to], "\n"))
	fmt.Fprintf(&b, "\n%d bytes · %s · view %d–%d/%d\n", len(m.credText.value), credentialLines(m.credText.value), from+1, to, len(rows))
	if padded && len(warning) > 0 {
		b.WriteString("\n")
	}
	for _, l := range warning {
		b.WriteString(rainbow(l) + "\n")
	}
	if padded && len(warning) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(errBlock)
	b.WriteString(help)
	return b.String()
}

func (m model) credentialTextMinHeight() int {
	if !m.credTextVisible {
		return 16 // room for the pre-entry disclosure
	}
	// Worst case at 60 columns: six header lines (three fixed, the mode
	// line, the one-line header warning -- the CR warning or the converted
	// notice, never both -- a blank) + two text rows + the position
	// line + the two-line end-line warning + the error block's two reserved
	// rows (message and "^s blocked") + two-line controls + clipHeight's
	// inline-renderer row = 16. An error that wraps to a third row leaves one
	// text row. The warnings' blank padding is spent only when the text keeps
	// two rows besides, so it never raises this minimum.
	return 16
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
