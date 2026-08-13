package configui

// The Env screen's credential workflow: the seam onto byre's ONE credential
// write path, the per-file passphrase modal (modeCredPass), the rekey modal
// (modeCredRekey), and the commit that turns a masked Value into an
// encrypted row.
//
// Two things about this screen are unlike every other field here.
//
// It WRITES ON ACCEPT. Every other edit lands in the working state and reaches
// disk at ^s; a credential value cannot, because encrypting it means holding
// the plaintext until then and the row it becomes is the write path's to
// produce -- compare-and-swap, the file's own lock, and (on a file's first
// credential) the identity landing in the same generation as the row it opens.
// So enter runs the same write `byre credentials set` runs, and the form says
// so before the value is typed. The rest of the screen still saves at ^s.
//
// And the VALUE is never shown. Not in the input (masked), not in the row
// (the ciphertext elides), not in a status line or an error. An existing row
// opens with an empty Value field meaning "unchanged": the stored value is not
// readable from here at all -- this file's identity is passphrase-wrapped, and
// the editor holds no passphrase.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
	"github.com/pjlsergeant/byre/internal/packages"
)

// CredentialAdmin is the editor's route to the credential write path: the same
// target resolution, write-target disclosure and compare-and-swap that `byre
// credentials set` and `byre credentials rekey` use, over the very file this
// editor has open.
//
// nil means this editor cannot write credentials -- the --global editor, whose
// file no credentials verb targets either -- and the Source picker then omits
// the credential kinds rather than offering an option that could only refuse.
type CredentialAdmin interface {
	// Disclosure is the cross-project warning the form shows BEFORE a value is
	// accepted, empty for a file that reaches one project only. Layer changes
	// propagate live (ADR 0035), so a user typing a production key into a
	// shared layer has to know that while they can still stop.
	Disclosure() string
	// HasIdentity reports whether the file already carries a [credentials]
	// block. False means the next Set MINTS one and needs a passphrase, and
	// that Rekey has nothing to wrap — the Env screen hides the rekey
	// surface rather than offering one that can only refuse.
	HasIdentity() (bool, error)
	// Set applies one accept to the file: encrypt the value, write its row,
	// remove the rows the accept replaces, and (on a file's first credential)
	// mint the identity that opens them -- all in ONE compare-and-swap
	// mutation.
	Set(CredentialWrite) (CredentialResult, error)
	// Rekey re-wraps this file's identity under a new passphrase. The
	// identity itself does not rotate, so every value row stays
	// byte-identical. The returned File is the bytes as written, under the
	// lock — the editor's new save baseline, for the same reason Set
	// returns them.
	Rekey(current, newPw string) (CredentialResult, error)
}

// CredentialWrite is one accept's WHOLE change to the file: the value to
// encrypt, and the rows that leave with it.
//
// They travel together because they have to land together. The editor's buffer
// applies the accept's other row surgery immediately -- an [env] literal being
// converted disappears from the screen, a renamed row is replaced -- and that
// surgery only reaches disk at ^s. Splitting them put a quit-without-^s between
// the two: the file kept BOTH rows, and the [env] literal still won the cascade
// (ADR 0026), so the next develop delivered the old plaintext while the status
// said the credential was set.
type CredentialWrite struct {
	Key   string
	Kind  credentials.Kind
	Value []byte
	// Passphrase wraps the identity this write mints on a file's FIRST
	// credential, and is unused on every later one (values encrypt to the
	// file's cleartext recipient).
	Passphrase string
	// RemoveEnv is the [env] literal this row converts FROM, empty when the
	// accept converts nothing. The literal must go in the same write: while it
	// is there it takes the key out of env_from_host entirely.
	RemoveEnv string
	// RemoveEnvFromHost is the env_from_host row this write REPLACES under a
	// different key -- an ordinary source re-authored as a credential and
	// renamed in one edit. Empty when the key is unchanged (a row that is
	// ALREADY a credential cannot be renamed at all: the payload is stamped
	// with its key).
	RemoveEnvFromHost string
}

// CredentialResult is what one write left behind.
type CredentialResult struct {
	// Row is the row source that landed, for the editor's working state.
	Row string
	// File is the file's bytes AS WRITTEN, captured under the lock that wrote
	// them. It is the editor's new save baseline: re-reading the file after
	// the lock releases would take whatever a concurrent writer landed in the
	// window as this session's baseline, and the next ^s would then see no
	// drift and reconcile over a change it never held.
	File []byte
}

// pendingCredential is a value the form has accepted and not yet written: the
// passphrase modal sits between the two on a file's first credential. The
// plaintext is held HERE and nowhere else -- never in a field the view renders
// once the modal is up, never in a status line, never in an error.
type pendingCredential struct {
	key   string
	kind  credentials.Kind
	value []byte
	// idx is the env_from_host row this replaces (-1 = a new row), and envIdx
	// the [env] literal the row is converting FROM (-1 = none). Both are
	// applied only after the write lands, so a refused write leaves the
	// working state exactly as the user left it.
	idx    int
	envIdx int
}

// credentialWriteNote is what enter does here, stated before the value is
// typed: this one field does not wait for ^s.
const credentialWriteNote = "enter encrypts and writes this value now (the rest of the screen still saves with ^s)"

// canWriteCredentials reports whether this editor has a credential write path.
// In production that is false for exactly one target: --global, whose
// default.config no credentials verb can name.
func (m model) canWriteCredentials() bool { return m.creds != nil }

// credentialNoWritePathNote is what an editor with no credential write path
// says about a credential row — the notes and the row's own refusal both. It
// names no CLI verb ON PURPOSE: `byre credentials set` cannot target this
// file, so naming it would send a user to a command that writes a SHADOWING
// row in another file instead of repairing the one they are looking at.
const credentialNoWritePathNote = "nothing here can write a credential to this file — set credentials in a project config or a layer"

// commitCredentialRow is the credential arm of commitEnvRow: hold the key to
// the same rules a save would, decide what an empty Value means for this row,
// and either write it or open the passphrase modal that must precede the first
// write to this file. orig is the pre-commit model every refusal returns.
func (m model) commitCredentialRow(orig model, key string, moving bool) model {
	if !m.canWriteCredentials() {
		// Unreachable today (the picker omits the credential scheme and
		// existing rows refuse to open without a write path), and held to
		// the same rule as those gates: the note names no CLI verb, because
		// `byre credentials set` cannot target this file.
		orig.itemErr = credentialNoWritePathNote
		return orig
	}
	// The key rules ValidateLayer would apply, applied BEFORE the write rather
	// than after it: the credential path writes on accept, so a save-time
	// check would find the row already on disk. The reserved-key rule is the
	// extra one a credential carries (config.ValidateCredentialKey).
	if err := config.ValidateEnvFromHostKey(key); err != nil {
		orig.itemErr = err.Error()
		return orig
	}
	if err := config.ValidateCredentialKey(key); err != nil {
		orig.itemErr = err.Error()
		return orig
	}

	// The row being edited, when it is a credential this file already holds.
	var was string
	if m.itemHostEnv && m.editIndex >= 0 {
		was = m.hostEnv[m.editIndex].Value
	}
	if config.IsCredentialSource(was) {
		if key != m.hostEnv[m.editIndex].Key {
			// The payload is stamped with the key it was set for, so a renamed
			// row would decrypt to a mismatch refusal at the next develop.
			// Re-encrypting under the new name is a set; there is no rename.
			orig.itemErr = "a credential's value is bound to its key — delete this row and add the value under the new key"
			return orig
		}
	}

	value := []byte(m.inputs[1].Value())
	if len(value) == 0 {
		return m.commitCredentialUnchanged(orig, was)
	}

	p := pendingCredential{key: key, kind: credentialKind(m.itemMode2), value: value, idx: -1, envIdx: -1}
	switch {
	case moving:
		// Converting an [env] literal: the literal leaves the buffer, the
		// credential row joins env_from_host. Applied after the write.
		p.envIdx = m.editIndex
	case m.itemHostEnv && m.editIndex >= 0:
		p.idx = m.editIndex
	}

	has, err := m.creds.HasIdentity()
	if err != nil {
		orig.itemErr = err.Error()
		return orig
	}
	if !has {
		// A file's FIRST credential mints its identity, and the passphrase
		// that wraps it is a decision, not a form field: the modal explains
		// what it opens and what forgetting it costs.
		m.credPending = &p
		return m.openCredPass()
	}
	return m.writeCredential(p, "")
}

// commitCredentialUnchanged answers an empty Value box. On the row that
// already holds a credential that means "keep it" -- the stored value is not
// readable here, so requiring it to be retyped would be asking for a secret
// the editor could not have shown. Anywhere else an empty value is nothing to
// encrypt.
func (m model) commitCredentialUnchanged(orig model, was string) model {
	if !config.IsCredentialSource(was) {
		orig.itemErr = "a credential needs a value — type it (input hidden), or pick another source"
		return orig
	}
	if credKindSel(was) != m.itemMode2 {
		// env->file (or back) re-encrypts: the payload is stamped with the
		// kind it was set for, and this editor cannot read the old value to
		// re-stamp it.
		orig.itemErr = "changing a credential's kind re-encrypts its value — type the value again"
		return orig
	}
	orig.itemErr = ""
	orig.mode = modeList
	orig.status = orig.hostEnv[orig.editIndex].Key + " — value unchanged (nothing was written)"
	return orig
}

// writeCredential runs the write and folds the result into the working state.
// The accept's WHOLE change is on disk when this returns -- the row, and the
// rows it replaces -- so the working state takes the ciphertext too (a later ^s
// writes the same value back, unchanged, instead of reconciling it away) and
// the buffer surgery below only mirrors what the file already says.
func (m model) writeCredential(p pendingCredential, passphrase string) model {
	w := CredentialWrite{Key: p.key, Kind: p.kind, Value: p.value, Passphrase: passphrase}
	if p.envIdx >= 0 {
		w.RemoveEnv = m.env[p.envIdx].Key
	}
	if p.idx >= 0 && m.hostEnv[p.idx].Key != p.key {
		// The row being re-authored moved key: on disk that is a removal plus
		// an add, or the old row outlives the edit that replaced it.
		w.RemoveEnvFromHost = m.hostEnv[p.idx].Key
	}
	res, err := m.creds.Set(w)
	if err != nil {
		// The refusal is the write path's own -- a value over its kind's cap,
		// a NUL in an env value, a file that moved under the compare-and-swap.
		// It names the rule and the offending size; it never carries the value.
		// The pending value dies with the attempt: the form is where a retry
		// re-reads it, and this field is meant to hold a plaintext only while a
		// modal is the reason the editor is elsewhere.
		m.credPending = nil
		m.itemErr = err.Error()
		m.mode = modeItem
		return m
	}
	clean := !m.dirty()
	if p.envIdx >= 0 {
		m.env = append(append([]kvItem{}, m.env[:p.envIdx]...), m.env[p.envIdx+1:]...)
	}
	m.hostEnv = putAt(m.hostEnv, p.idx, kvItem{Key: p.key, Value: res.Row})
	// The file just changed, by this editor's own hand: re-baseline, or the
	// next ^s sees another session's write where its own is (drift, and the
	// overwrite prompt with it). The bytes come from the write itself, taken
	// under its lock -- a read from here would happen after the lock released,
	// and a concurrent writer's bytes would become this session's baseline
	// without ever being in its buffer.
	m.saveBase, m.saveBaseErr = res.File, nil
	m.savedOnce = true
	// A successful Set always leaves the file with an identity (minted or
	// already there), so the Env list's rekey surface can appear without
	// another probe.
	m.credHasIdentity = true
	if clean {
		// A buffer that was clean stays clean: the one change it just took is
		// already on disk, WHOLE (the row and the rows it replaced), and a
		// dirty-quit confirm over it would be a lie. A buffer that was already
		// dirty stays dirty -- its OTHER edits are still unsaved.
		m.savedSig = m.sig()
	}
	// The plaintext leaves the model with the form: the value input is not
	// re-rendered after this, and holding it would keep a secret alive in a
	// screen the user has left.
	m.inputs[1].SetValue("")
	m.credPending = nil
	m.itemErr = ""
	m.errMsg = ""
	m.mode = modeList
	m.status = p.key + " — credential set in " + packages.DisplayPath(m.filePath) + "; applies at the next develop"
	return m
}

// envItemNotes is the Env item editor's guidance. The ordinary schemes explain
// themselves through the picker and the placeholder (hostEnvArgHint); a
// credential carries consequences a placeholder cannot hold — where the write
// lands, that enter writes it now, what an empty box means, and the caps its
// kind enforces — so the form states them BEFORE a value is typed, the way the
// CLI prints them before it prompts.
func (m model) envItemNotes() []string {
	editingCredential := m.itemHostEnv && m.editIndex >= 0 &&
		m.editIndex < len(m.hostEnv) && config.IsCredentialSource(m.hostEnv[m.editIndex].Value)
	if !isCredentialScheme(m.itemMode) {
		if editingCredential {
			return []string{"⚠ " + credentialUnsetNote}
		}
		return nil
	}
	if !m.canWriteCredentials() {
		return []string{"⚠ " + credentialNoWritePathNote}
	}
	var notes []string
	if d := m.creds.Disclosure(); d != "" {
		notes = append(notes, "⚠ "+d)
	}
	notes = append(notes, credentialWriteNote)
	switch {
	case m.credProbeErr != "":
		notes = append(notes, "⚠ "+m.credProbeErr)
	case !m.credHasIdentity && m.orphanCredentialRows() > 0:
		// Same falsehood the modal refuses to tell, one screen earlier: rows
		// are listed and the file has no identity for them.
		notes = append(notes, "⚠ this file's credential rows have no identity — enter asks for a new passphrase, which will not open them")
	case !m.credHasIdentity:
		notes = append(notes, "this file has no credentials yet — enter asks for a new passphrase")
	}
	if editingCredential {
		notes = append(notes, "the stored value is never shown — empty keeps it, a new one replaces it")
	}
	return append(notes, credentialKindNote(m.itemMode2))
}

// credentialKindNote is what a kind delivers and the cap it carries — the
// rules the write path enforces (credentials.ValidateValue), stated where the
// value is typed instead of first in a refusal.
func credentialKindNote(sel int) string {
	if sel == credKindFile {
		return fmt.Sprintf("file: written to the box's session tmpfs, its path in the key; up to %d KiB", credentials.MaxValue>>10)
	}
	return fmt.Sprintf("env var: exported under this key; no NUL bytes, up to %d KiB", credentials.MaxEnvValue>>10)
}

// canRekey reports whether the Env list should offer the rekey surface: a
// write path AND a file that already carries an identity. Absent either, the
// binding and its help are gone rather than present-and-refusing — a file
// with no identity has nothing to wrap, and --global has no write path.
func (m model) canRekey() bool {
	return m.listField == fEnv && m.canWriteCredentials() && m.credHasIdentity
}

// probeCredentialIdentity asks, once per opening of the Env list or item
// editor, whether this file already has an identity — the answer the notes
// and the rekey surface need. Once per OPEN and not per frame: the honest
// answer costs a file read and a parse, and the decision that matters (does
// this write mint an identity) is taken again, freshly, at accept.
func (m *model) probeCredentialIdentity() {
	m.credHasIdentity, m.credProbeErr = false, ""
	if !m.canWriteCredentials() {
		return
	}
	has, err := m.creds.HasIdentity()
	if err != nil {
		m.credProbeErr = err.Error()
		return
	}
	m.credHasIdentity = has
}

// orphanCredentialRows counts the credential rows this file carries that
// NOTHING here can open: asked only where the file has no [credentials] block,
// which makes every such row an orphan (its identity was deleted, or the row
// was copied out of the file that owned it). The count comes from the buffer
// because the buffer is what the screen shows — a modal claiming "no
// credentials yet" over a list of them is the falsehood this closes.
func (m model) orphanCredentialRows() int {
	n := 0
	for _, kv := range m.hostEnv {
		if config.IsCredentialSource(kv.Value) {
			n++
		}
	}
	return n
}

// credentialUnsetNote is what leaving a credential kind does, said at the
// moment it is chosen: the row's ciphertext is the only copy of that value.
const credentialUnsetNote = "this replaces the credential — the ciphertext goes with the row, and no copy is kept"

// ---- the passphrase modal (modeCredPass) -----------------------------------

// openCredPass arms the modal with two fresh masked inputs.
func (m model) openCredPass() model {
	for i := range m.credPassInputs {
		in := textinput.New()
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
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
		// Back to the form with the value still in its (masked) input and
		// nothing written: no identity, no row, no file touched.
		m.credPending = nil
		m.credPassInputs[0].SetValue("")
		m.credPassInputs[1].SetValue("")
		m.mode = modeItem
		m.itemErr = "nothing was written — a credential needs this file's passphrase"
		return m, nil
	case "tab", "down", "shift+tab", "up":
		return m.focusCredPass(1 - m.credPassFocus), nil
	case "ctrl+s":
		// ^s saves everywhere else in this editor; here it would write the
		// file underneath an open decision. Say that instead of swallowing
		// the keystroke.
		m.credPassErr = "finish this passphrase, or esc to cancel it — ^s saves the rest of the screen after that"
		return m, nil
	case "enter":
		if m.credPassFocus == 0 {
			return m.focusCredPass(1), nil
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
		if m.credPending == nil {
			// Nothing to write; the modal is only ever opened with a value in
			// hand, so this is a shape, not a path a user reaches.
			m.mode = modeItem
			m.itemErr = "no credential value is waiting to be written"
			return m, nil
		}
		p := *m.credPending
		m.credPassInputs[0].SetValue("")
		m.credPassInputs[1].SetValue("")
		return m.writeCredential(p, pw), nil
	}
	var cmd tea.Cmd
	m.credPassInputs[m.credPassFocus], cmd = m.credPassInputs[m.credPassFocus].Update(msg)
	return m, cmd
}

func (m model) focusCredPass(i int) model {
	m.credPassInputs[m.credPassFocus].Blur()
	m.credPassFocus = i
	m.credPassInputs[i].Focus()
	return m
}

func (m model) viewCredPass() string {
	var b strings.Builder
	b.WriteString(focusStyle.Render("Choose this file's credentials passphrase") + "\n\n")
	if n := m.orphanCredentialRows(); n > 0 {
		// NOT "holds no credentials yet": the screen behind this modal lists
		// those rows. They are orphans — the identity that opened them is gone
		// — and the passphrase being chosen here does not bring them back.
		for _, l := range wrapLine("⚠ "+credentials.OrphanRowsWarning(n), m.width) {
			b.WriteString(warnStyle.Render(l) + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("This file holds no credentials yet.\n\n")
	}
	b.WriteString("The passphrase you choose here wraps the key that opens every\n" +
		"credential in THIS file, and byre asks for it when a box that needs\n" +
		"one launches.\n\n" +
		"There is no recovery: a forgotten passphrase means unsetting each row\n" +
		"and setting its value again.\n\n")
	if d := m.creds.Disclosure(); d != "" {
		b.WriteString(warnStyle.Render("⚠ "+d) + "\n\n")
	}
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
	b.WriteString("\n" + helpLine("tab", "next", "enter", "create + write", "esc", "cancel (nothing written)"))
	return b.String()
}

// ---- the rekey modal (modeCredRekey) ---------------------------------------

// openCredRekey arms the modal with three fresh masked inputs: current
// passphrase, new, confirm. Separate from the mint modal — different
// inputs, different stakes.
func (m model) openCredRekey() model {
	for i := range m.credRekeyInputs {
		in := textinput.New()
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
		in.Prompt = ""
		m.credRekeyInputs[i] = in
	}
	m.credRekeyInputs[0].Focus()
	m.credRekeyFocus = 0
	m.credRekeyErr = ""
	m.mode = modeCredRekey
	m.status = ""
	m.errMsg = ""
	return m
}

func (m model) updateCredRekey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+q", "ctrl+c":
		// Back to the list with nothing written and no passphrase retained.
		m.clearCredRekeyInputs()
		m.mode = modeList
		m.status = "nothing was written — the passphrase is unchanged"
		return m, nil
	case "tab", "down":
		return m.focusCredRekey((m.credRekeyFocus + 1) % 3), nil
	case "shift+tab", "up":
		return m.focusCredRekey((m.credRekeyFocus + 2) % 3), nil
	case "ctrl+s":
		// ^s saves everywhere else in this editor; here it would write the
		// file underneath an open decision. Say that instead of swallowing
		// the keystroke.
		m.credRekeyErr = "finish this passphrase, or esc to cancel it — ^s saves the rest of the screen after that"
		return m, nil
	case "enter":
		if m.credRekeyFocus < 2 {
			return m.focusCredRekey(m.credRekeyFocus + 1), nil
		}
		current := m.credRekeyInputs[0].Value()
		newPw := m.credRekeyInputs[1].Value()
		if newPw == "" {
			m.credRekeyErr = credentials.EmptyPassphraseWorthless
			return m, nil
		}
		if newPw != m.credRekeyInputs[2].Value() {
			m.credRekeyErr = "passphrases do not match"
			return m, nil
		}
		return m.writeRekey(current, newPw), nil
	}
	var cmd tea.Cmd
	m.credRekeyInputs[m.credRekeyFocus], cmd = m.credRekeyInputs[m.credRekeyFocus].Update(msg)
	return m, cmd
}

func (m model) focusCredRekey(i int) model {
	m.credRekeyInputs[m.credRekeyFocus].Blur()
	m.credRekeyFocus = i
	m.credRekeyInputs[i].Focus()
	return m
}

func (m *model) clearCredRekeyInputs() {
	for i := range m.credRekeyInputs {
		m.credRekeyInputs[i].SetValue("")
	}
}

// writeRekey runs the rekey and folds the result into the working state. A
// refused write (wrong passphrase, a file that moved) leaves the list
// untouched; a successful one re-baselines saveBase from the bytes the
// write itself produced.
func (m model) writeRekey(current, newPw string) model {
	// The passphrases leave the inputs with the attempt: success or
	// refusal, they are not held in a field the view can render.
	m.clearCredRekeyInputs()
	res, err := m.creds.Rekey(current, newPw)
	if err != nil {
		// The retry starts over — with the inputs cleared, focus anywhere
		// but the first field would have the user typing a fresh current
		// passphrase into Confirm.
		m.credRekeyErr = err.Error()
		return m.focusCredRekey(0)
	}
	clean := !m.dirty()
	// The file just changed, by this editor's own hand: re-baseline, or
	// the next ^s sees this write as another session's drift. The bytes
	// come from the write itself, taken under its lock.
	m.saveBase, m.saveBaseErr = res.File, nil
	m.savedOnce = true
	if clean {
		// A buffer that was clean stays clean: the [credentials] block is
		// not part of the buffer's rows, so the sig is unaffected, but the
		// baseline still has to move or the next ^s sees this write as
		// drift.
		m.savedSig = m.sig()
	}
	m.credRekeyErr = ""
	m.errMsg = ""
	m.mode = modeList
	m.status = "passphrase rotated in " + packages.DisplayPath(m.filePath) + "; the old one no longer works"
	return m
}

func (m model) viewCredRekey() string {
	var b strings.Builder
	b.WriteString(focusStyle.Render("Change this file's credentials passphrase") + "\n\n")
	b.WriteString("This changes the passphrase only. Every credential value row stays\n" +
		"byte-identical — that is what keeps drift honest. The old passphrase\n" +
		"stops working.\n\n" +
		"There is no recovery: a forgotten passphrase means unsetting each row\n" +
		"and setting its value again.\n\n")
	if d := m.creds.Disclosure(); d != "" {
		b.WriteString(warnStyle.Render("⚠ "+d) + "\n\n")
	}
	labels := [3]string{"Current passphrase", "New passphrase", "Confirm"}
	for i, in := range m.credRekeyInputs {
		cursor := "  "
		if m.credRekeyFocus == i {
			cursor = cursorStyle.Render("▸ ")
		}
		b.WriteString(cursor + labels[i] + ": " + in.View() + "\n")
	}
	if m.credRekeyErr != "" {
		b.WriteString("\n" + m.errLine(m.credRekeyErr) + "\n")
	}
	b.WriteString("\n" + helpLine("tab", "next", "enter", "rekey", "esc", "cancel (nothing written)"))
	return b.String()
}
