package configui

// A credential draft must not pass through Bubbles' input sanitizers: both
// widgets replace tabs, and the single-line widget also flattens newlines.
// Keep the text independently and escape controls only in the display. There
// is no clipboard command, filesystem handoff, decryption, or write here.
import (
	"fmt"
	"slices"
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
	// marks remember which line breaks the editor converted, so ^t switches
	// the draft between LF and exactly what was pasted. They live as long as
	// the draft: ^s hands them to the form beside the accepted draft
	// (credDraftMarks) and a reopen hands them back. The invariant: marks
	// is empty or exactly as long as the draft's runes; in LF mode a mark sits only on an '\n' (markWasCR,
	// markWasCRLF), and as pasted only on an '\r' that ^t restored from a
	// bare CR (markWasCR: convert it alone, even before an '\n'). Typed and
	// as-pasted input is unmarked.
	marks []uint8
	// cycled is set once ^t has been used on this draft: the user knows the
	// toggle, so the converted notice's rainbow line no longer shows (its
	// plain explanation stays). It lives as long as the draft, like marks
	// (credDraftCycled).
	cycled bool
}

// marksFit returns marks if they satisfy the invariant for value in the
// mode its bytes imply, and nil otherwise, so a mismatch drops the memory
// rather than misreading the draft or indexing past it.
func marksFit(value string, marks []uint8) []uint8 {
	r := []rune(value)
	if len(marks) != len(r) {
		return nil
	}
	asPasted := strings.ContainsRune(value, '\r')
	for i, k := range marks {
		switch {
		case k == markNone:
		case asPasted && r[i] == '\r' && k == markWasCR:
		case !asPasted && r[i] == '\n' && (k == markWasCR || k == markWasCRLF):
		default:
			return nil
		}
	}
	return marks
}

const (
	markNone uint8 = iota
	markWasCR
	markWasCRLF
)

// hasConverted reports whether the LF draft holds a line break the editor
// converted.
func (e credentialText) hasConverted() bool {
	if e.keepCR {
		return false
	}
	for _, k := range e.marks {
		if k != markNone {
			return true
		}
	}
	return false
}

// splice replaces del runes at at with ins, keeping marks in lockstep. A
// nil insMarks inserts unmarked runes.
func (e credentialText) splice(r []rune, at, del int, ins []rune, insMarks []uint8) credentialText {
	e.value = string(r[:at]) + string(ins) + string(r[at+del:])
	if len(e.marks) == 0 && !slices.ContainsFunc(insMarks, func(k uint8) bool { return k != markNone }) {
		e.marks = nil
		return e
	}
	marks := e.marks
	if len(marks) != len(r) {
		marks = make([]uint8, len(r)) // marks that do not fit are not read
	}
	if insMarks == nil {
		insMarks = make([]uint8, len(ins))
	}
	e.marks = slices.Concat(marks[:at], insMarks, marks[at+del:])
	return e
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
const credentialCRsNeedLF = "in the draft: most text needs LF."

// The notice while the LF draft holds converted line breaks: anything byre
// does to a secret is stated, not silent. The first line is rainbow, the
// explanation plain. Shared by the view and its tests.
const (
	credentialConvertedNotice = "Your line breaks were automatically changed. ^t to toggle back"
	credentialConvertedWhy    = "Pasted text almost always wants LF characters instead of CR. Your paste has been converted to use LF. Use ^t to switch back to exactly what you pasted instead"
)

// credentialCRWarning opens the warning for a draft holding CRs: most text,
// and ssh-keygen for one, wants LF, and the draft saves fine, so the
// failure would otherwise surface only when the value is used. A CRLF holds
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

// pairsCRLF reports whether the '\r' at i pairs with the '\n' after it as
// one CRLF line break: only when an '\n' follows and ^t has not marked the
// CR to stand alone. Conversion, display, the editor's line count and
// navigation all ask this one rule, so the same bytes never mean one line
// break on screen and two after ^t. A CR beyond the marks is unmarked.
func pairsCRLF(r []rune, marks []uint8, i int) bool {
	return r[i] == '\r' && i+1 < len(r) && r[i+1] == '\n' && (i >= len(marks) || marks[i] != markWasCR)
}

// toLF rewrites CRLF and lone CR as LF, marking each converted LF. An '\r'
// marked markWasCR converts alone even before an '\n', so a bare CR that ^t
// restored becomes the LF it was. pos maps a rune offset in r to the result;
// a cursor between the halves of a CRLF lands on the LF that replaces the
// pair.
func toLF(r []rune, marks []uint8, pos int) ([]rune, []uint8, int) {
	out, outMarks, outPos := make([]rune, 0, len(r)), make([]uint8, 0, len(r)), -1
	for i := 0; i < len(r); i++ {
		if i == pos {
			outPos = len(out)
		}
		switch {
		case r[i] != '\r':
			out, outMarks = append(out, r[i]), append(outMarks, markNone)
		case pairsCRLF(r, marks, i):
			if i+1 == pos {
				outPos = len(out)
			}
			out, outMarks = append(out, '\n'), append(outMarks, markWasCRLF)
			i++
		default:
			out, outMarks = append(out, '\n'), append(outMarks, markWasCR)
		}
	}
	if outPos < 0 {
		outPos = len(out)
	}
	return out, outMarks, outPos
}

// restorePasted undoes toLF: an LF marked markWasCR becomes an '\r' marked to
// convert alone, one marked markWasCRLF becomes "\r\n", and unmarked runes
// stay. A cursor on a restored LF lands before what replaces it.
func restorePasted(r []rune, marks []uint8, pos int) ([]rune, []uint8, int) {
	out, outMarks, outPos := make([]rune, 0, len(r)), make([]uint8, 0, len(r)), -1
	for i, c := range r {
		if i == pos {
			outPos = len(out)
		}
		switch {
		case i < len(marks) && marks[i] == markWasCR:
			out, outMarks = append(out, '\r'), append(outMarks, markWasCR)
		case i < len(marks) && marks[i] == markWasCRLF:
			out, outMarks = append(out, '\r', '\n'), append(outMarks, markNone, markNone)
		default:
			out, outMarks = append(out, c), append(outMarks, markNone)
		}
	}
	if outPos < 0 {
		outPos = len(out)
	}
	return out, outMarks, outPos
}

// credentialPreEntryLineEndings is the pre-entry notice's line-ending
// sentence, shared by the view and its tests.
const credentialPreEntryLineEndings = "Pasted CR and CRLF line endings become LF; ^t in the editor switches between LF and exactly what you pasted."

// credentialToggleTooLong refuses a switch to as pasted that would grow the
// draft past the cap: each restored CRLF is one byte longer than its LF.
const credentialToggleTooLong = "Switching to as pasted would exceed 256 KiB. Nothing changed; shorten the draft first."

// toggleLineEndings flips the mode and converts the draft in place, both
// ways: entering LF rewrites CR and CRLF as marked LFs, and leaving LF
// restores the marked LFs to exactly what was pasted. The cursor stays on
// the same character, and the draft records that ^t has been used. A switch
// that would exceed the cap is refused with the draft, mode and that record
// unchanged.
func (e credentialText) toggleLineEndings() credentialText {
	r := []rune(e.value)
	var out []rune
	var marks []uint8
	var pos int
	if e.keepCR {
		out, marks, pos = toLF(r, e.marks, e.pos)
	} else {
		out, marks, pos = restorePasted(r, e.marks, e.pos)
		if len(string(out)) > credentials.MaxValue {
			e.err = credentialToggleTooLong
			return e
		}
	}
	e.cycled = true
	e.keepCR = !e.keepCR
	e.value, e.pos, e.marks = "", 0, nil
	e = e.splice(nil, 0, 0, out, marks)
	e.pos = pos
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

// lines is the editor's count, where a CR ^t marked to stand alone is its
// own line break; the form counts the accepted draft the same way.
func (e credentialText) lines() string {
	return countLines([]rune(e.value), e.marks)
}

func countLines(r []rune, marks []uint8) string {
	n := 1
	for i, c := range r {
		if c == '\n' || (c == '\r' && !pairsCRLF(r, marks, i)) {
			n++
		}
	}
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
	m.credDraft, m.credDraftMarks, m.credDraftCycled, m.credInputWarning = "", nil, false, ""
	m.credMultiline, m.credTextVisible = false, false
	m.credText = credentialText{}
	m.credTextTransition, m.credTextKeys = credentialTextSteady, nil
}

func (m model) credentialDraftSummary() string {
	if m.credDraft == "" {
		return "No replacement entered"
	}
	return fmt.Sprintf("Not saved yet · %s · hidden", countLines([]rune(m.credDraft), m.credDraftMarks))
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
			var marks []uint8
			cycled := false
			if m.credMultiline {
				value, marks, cycled = m.credDraft, marksFit(m.credDraft, m.credDraftMarks), m.credDraftCycled
			}
			if m.credInputWarning != "" {
				value, marks, cycled = "", nil, false
			}
			// LF mode unless the draft already holds CRs, which only an
			// as-pasted session can have put there: the mode line must
			// describe the draft on screen, and reopening never converts it.
			// The accepted draft's marks and toggle history come back with it.
			m.credText = credentialText{value: value, pos: utf8.RuneCountInString(value), keepCR: strings.ContainsRune(value, '\r'), marks: slices.Clone(marks), cycled: cycled}
			m.credTextTransition = credentialTextEntering
			return m, tea.Sequence(tea.EnterAltScreen, func() tea.Msg { return credentialRevealMsg{} })
		}
		return m, nil
	}
	if msg.Type == tea.KeyCtrlS {
		if m.credText.err != "" {
			return m, nil
		}
		m.credDraft, m.credDraftMarks, m.credMultiline = m.credText.value, slices.Clone(m.credText.marks), true
		m.credDraftCycled = m.credText.cycled
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
			if r[p-1] == '\n' || (r[p-1] == '\r' && !pairsCRLF(r, e.marks, p-1)) {
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
	var insert []rune
	var insertMarks []uint8
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
			if r[previousEnd] == '\n' && previousEnd > 0 && pairsCRLF(r, e.marks, previousEnd-1) {
				previousEnd--
			}
			e.pos = min(previousEnd, start(previousEnd)+p-s)
		}
	case tea.KeyDown:
		n := end(p)
		if n < len(r) {
			next := n + 1
			if pairsCRLF(r, e.marks, n) {
				next++
			}
			e.pos = min(end(next), next+p-start(p))
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if p > 0 {
			e = e.splice(r, p-1, 1, nil, nil)
			e.pos--
		}
	case tea.KeyDelete, tea.KeyCtrlD:
		if p < len(r) {
			e = e.splice(r, p, 1, nil, nil)
		}
	case tea.KeyEnter, tea.KeyCtrlJ:
		insert = []rune{'\n'}
	case tea.KeyTab:
		insert = []rune{'\t'}
	case tea.KeySpace:
		insert = []rune{' '} // Bubble Tea sends a typed space as KeySpace, not KeyRunes.
	case tea.KeyCtrlV:
		e.err = "Use your terminal's paste command."
		return e
	case tea.KeyCtrlT:
		e.err = ""
		toggled := e.toggleLineEndings()
		if toggled.err != "" {
			return toggled
		}
		e = toggled
	default:
		if msg.Type == tea.KeyRunes {
			insert = msg.Runes
			if !e.keepCR {
				insert, insertMarks, _ = toLF(msg.Runes, nil, 0)
			}
		}
	}
	if len(insert) > 0 {
		if len(e.value)+len(string(insert)) > credentials.MaxValue {
			e.err = "Input exceeds 256 KiB. Nothing inserted; shorten the draft and paste again."
			return e
		}
		e = e.splice(r, p, 0, insert, insertMarks)
		e.pos += len(insert)
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
			credentialPreEntryLineEndings,
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
	// gating on both keeps "^t converts them" true. In LF mode, while the
	// draft holds converted line breaks, it is the rainbow notice that they
	// were changed -- until ^t has been used on the draft, when the user
	// knows -- with the plain explanation under it, which stays; "^t to
	// toggle back" would be false as pasted.
	var headerWarning, why []string
	switch {
	case m.credText.keepCR && strings.ContainsRune(m.credText.value, '\r'):
		headerWarning = wrapLine(credentialCRWarning(m.credText.value)+" ^t converts them.", m.width)
	case m.credText.hasConverted():
		if !m.credText.cycled {
			headerWarning = wrapLine(credentialConvertedNotice, m.width)
		}
		why = wrapLine(credentialConvertedWhy, m.width)
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
	// Two elastic extras follow, each all or none and each spent only when
	// the text still keeps two rows, so neither costs the minimum size: first
	// the converted notice's plain explanation, then blank lines setting the
	// warnings off from their neighbours (one above the header warning, or
	// above the explanation when it shows alone, and one either side of the
	// end-line warning). why and padRows each decide both
	// budget and output.
	headerRows := 3 + 1 + len(headerWarning) + 1
	errRows := max(2, strings.Count(errBlock, "\n"))
	helpRows := strings.Count(help, "\n") + 1
	textRows := m.height - headerRows - 1 - len(warning) - errRows - helpRows - 1
	if textRows-len(why) < 2 {
		why = nil
	}
	textRows -= len(why)
	padRows := 0
	if len(headerWarning)+len(why) > 0 {
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
	if padded && len(headerWarning)+len(why) > 0 {
		b.WriteString("\n")
	}
	for _, l := range headerWarning {
		b.WriteString(rainbow(l) + "\n")
	}
	for _, l := range why {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n" + strings.Join(rows[from:to], "\n"))
	fmt.Fprintf(&b, "\n%d bytes · %s · view %d–%d/%d\n", len(m.credText.value), m.credText.lines(), from+1, to, len(rows))
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
	// Worst case at 60 columns: seven header lines (three fixed, the mode
	// line, the header warning at its tallest -- two rows: the converted
	// notice always, the CR warning from a three-digit count; they never
	// show together -- a blank)
	// + two text rows + the position line + the two-line end-line warning +
	// the error block's two reserved rows (message and "^s blocked") +
	// two-line controls + clipHeight's inline-renderer row = 17. An error
	// that wraps to a third row leaves one text row. The notice's explanation
	// and the warnings' blank padding are spent only when the text keeps two
	// rows besides, so neither raises this minimum.
	return 17
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
				crlf = pairsCRLF(r, e.marks, i)
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
