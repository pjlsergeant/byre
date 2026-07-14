// listitem.go owns the list-field modes: the item browser (modeList) and the
// single-item add/edit editor (modeItem) for apt, env, mounts, and ports.
package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
)

// ---- list screen (browse a field's EFFECTIVE rows, ADR 0018) ---------------

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.fieldRows(m.listField)
	addRow := len(rows) // index of the "+ add" pseudo-row
	if cur, ok := cursorMove(msg.String(), m.listCur, addRow+1); ok {
		m.listCur = cur
		m.status = ""
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeForm
		m.status = ""
		return m, nil
	case "ctrl+s":
		return m.save(), nil // global save-in-place; feedback via subFooterNote
	case "a":
		return m.startItem(-1), nil
	case "enter":
		if m.listCur == addRow {
			return m.startItem(-1), nil
		}
		r := rows[m.listCur]
		m.status = ""
		// A skill row usually has no actions (the pointer note explains) — but
		// an MCP skill row is closable (rowChoices offers Remove), so the menu
		// must open for it like any actionable row.
		if r.kind == rowSkill && len(m.rowChoices(m.listField, r)) == 0 {
			m.status = skillRowNote(r)
			return m, nil
		}
		if r.kind == rowHostEnv {
			m.status = hostEnvRowNote()
			return m, nil
		}
		if r.kind == rowEnvDoc {
			// The obvious move on reading a suggestion is "set it": open the
			// add editor with the key prefilled, cursor on the value.
			next := m.startItem(-1)
			next.inputs[0].SetValue(r.ident)
			next.focusItem(1)
			return next, nil
		}
		m.menuRow = r
		m.menuCur = 0
		m.mode = modeMenu
		return m, nil
	// Accelerators: the same actions the menu offers, keyed identically.
	case "e":
		if m.listCur < addRow {
			return m.accelerate(rows[m.listCur], "e")
		}
	case "d", "x":
		if m.listCur < addRow {
			return m.accelerate(rows[m.listCur], "d")
		}
	case "o":
		if m.listCur < addRow {
			return m.accelerate(rows[m.listCur], "o")
		}
	}
	return m, nil
}

// accelerate applies the row's menu action bound to key, or explains why the
// row has none (the dead-ends read as information, not errors).
func (m model) accelerate(r listRow, key string) (tea.Model, tea.Cmd) {
	m.status = ""
	for _, c := range m.rowChoices(m.listField, r) {
		if c.key == key {
			return m.applyRowAct(c.act, r)
		}
	}
	m.status = deadEndNote(m.listField, r)
	return m, nil
}

// ---- per-row action menu (modeMenu) -----------------------------------------

// rowAct is one action a list row supports; the menu and the accelerator keys
// dispatch to the same set.
type rowAct int

const (
	actEdit rowAct = iota
	actDelete
	actOverride   // add a local entry shadowing the inherited one
	actRemoveHere // write this layer's removal marker for the inherited entry
	actRestore    // drop this layer's marker (re-inherit / clear stale)
	actOpen       // open an offered egress door: write the entry into this layer (ADR 0020)
)

type menuChoice struct {
	label string
	key   string // accelerator, shown dimmed beside the label
	act   rowAct
}

// rowChoices is what the menu offers for a row: exactly what the cascade
// supports for that field and kind, nothing refused after the fact. A method
// because the offered-door action's label must state the scope of the write:
// in the --global editor "this project" would be a lie — the entry lands in
// default.config, i.e. every project on this machine (the wording-equals-
// write rule; the action itself is legitimate, ADR 0020's hand-grant path).
func (m model) rowChoices(f fieldID, r listRow) []menuChoice {
	switch r.kind {
	case rowLocal, rowOverride:
		return []menuChoice{{"Edit", "e", actEdit}, {"Delete", "d", actDelete}}
	case rowInherited:
		switch f {
		case fEnv:
			return []menuChoice{{"Override here", "e", actOverride}}
		case fMounts, fMCP:
			return []menuChoice{
				{"Override here", "e", actOverride},
				{"Remove in this project", "d", actRemoveHere},
			}
		default: // apt, ports: no per-entry override, just the off-switch
			return []menuChoice{{"Remove in this project", "d", actRemoveHere}}
		}
	case rowRemoved:
		return []menuChoice{{"Restore", "d", actRestore}}
	case rowStaleMarker:
		return []menuChoice{{"Clear marker", "d", actRestore}}
	case rowOffered:
		if m.global {
			return []menuChoice{{warnStyle.Render("⚠ Open for every project on this machine"), "o", actOpen}}
		}
		return []menuChoice{{"Open in this project", "o", actOpen}}
	case rowSkill:
		// MCP skill rows are the one closable skill contribution: a `!name`
		// closure reaches a skill-declared server (ADR 0033 — "this skill,
		// minus one of its servers"). Rows without an ident (already closed
		// by a lower layer, or a lower closure display row) stay menu-less.
		if f == fMCP && r.ident != "" {
			return []menuChoice{{"Remove in this project", "d", actRemoveHere}}
		}
	}
	return nil // other rowSkill rows: no menu; the list screen shows a pointer
}

func (m model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.rowChoices(m.listField, m.menuRow)
	if cur, ok := cursorMove(msg.String(), m.menuCur, len(choices)); ok {
		m.menuCur = cur
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeList
		return m, nil
	case "ctrl+s":
		return m.save(), nil
	case "enter", " ":
		if m.menuCur < len(choices) {
			m.mode = modeList
			return m.applyRowAct(choices[m.menuCur].act, m.menuRow)
		}
	default:
		for _, c := range choices {
			if msg.String() == c.key {
				m.mode = modeList
				return m.applyRowAct(c.act, m.menuRow)
			}
		}
	}
	return m, nil
}

// applyRowAct performs one row action against THIS layer's working state --
// every action is a single legible change to the open file (ADR 0018).
func (m model) applyRowAct(act rowAct, r listRow) (tea.Model, tea.Cmd) {
	m.status = ""
	switch act {
	case actEdit:
		return m.startItem(r.idx), nil
	case actOverride:
		return m.startOverride(r), nil
	case actDelete:
		m.deleteItem(m.listField, r.idx)
		if r.also {
			m.status = r.text + " is still inherited — remove again to turn it off here"
		}
	case actRemoveHere:
		m.removeHere(r)
	case actRestore:
		m.deleteItem(m.listField, r.idx)
	case actOpen:
		// The opened door becomes THIS layer's own egress entry: user-authored,
		// user-attributed, closable like any other (ADR 0020).
		m.egress = append(m.egress, r.ident)
		// In the --global editor that layer is default.config: say the scope
		// of what just happened, where to undo it (delete the entry here),
		// and how a single project opts back out.
		if m.global {
			m.status = r.ident + " opened for every project on this machine (entry in default.config; delete it here to close, or \"Remove in this project\" in a project's editor to opt one box out)"
		}
	}
	if n := len(m.fieldRows(m.listField)); m.listCur > n {
		m.listCur = n
	}
	return m, nil
}

// removeHere writes this layer's removal marker for an inherited entry: the
// cascade's off-switch, spelled per field (ADR 0018).
func (m *model) removeHere(r listRow) {
	switch m.listField {
	case fApt:
		m.apt = append(m.apt, "!"+r.ident)
	case fEgress:
		m.egress = append(m.egress, "!"+r.ident)
	case fMounts:
		m.mounts = append(m.mounts, config.Mount{Target: "!" + r.ident})
	case fMCP:
		m.mcps = append(m.mcps, config.MCP{Name: "!" + r.ident})
	case fPorts:
		if c, err := strconv.Atoi(r.ident); err == nil {
			m.ports = append(m.ports, config.Port{Container: c, Remove: true})
		}
	}
}

// startOverride opens the add editor prefilled with an inherited entry's
// values; saving writes a local entry that shadows it (env by key, mounts by
// target -- Merge's replace rules do the shadowing).
func (m model) startOverride(r listRow) model {
	next := m.startItem(-1)
	switch m.listField {
	case fEnv:
		next.inputs[0].SetValue(r.vals[0])
		next.inputs[1].SetValue(r.vals[1])
	case fMounts:
		next.inputs[0].SetValue(r.vals[0])
		next.inputs[1].SetValue(r.vals[1])
		switch r.vals[2] {
		case "rw":
			next.itemMode = 1
		case "disabled":
			next.itemMode = 2
		}
	case fMCP:
		for i := range next.inputs {
			next.inputs[i].SetValue(r.vals[i])
		}
	}
	return next
}

// skillRowNote points at the one place a skill-contributed row can be turned
// off: the skill itself.
func skillRowNote(r listRow) string {
	return "granted by " + r.source + " — disable it in Skills to remove"
}

// hostEnvRowNote points at the two hand edits that change the passthrough
// (ADR 0026): disabling the key, or shadowing it with an explicit env value.
func hostEnvRowNote() string {
	return "host passthrough — set KEY = \"\" under env_from_host in this file to disable, or an [env] KEY to override"
}

// deadEndNote explains a keypress the cascade can't honor for this row.
func deadEndNote(f fieldID, r listRow) string {
	if f == fEnv && r.kind == rowInherited {
		return "can't unset an inherited var from this layer — override its value here, or edit the " + r.source + " config"
	}
	if r.kind == rowSkill {
		return skillRowNote(r)
	}
	if r.kind == rowHostEnv {
		return hostEnvRowNote()
	}
	if r.kind == rowEnvDoc {
		return "a suggestion from " + r.source + " — press enter to set it here"
	}
	return ""
}

func (m *model) deleteItem(f fieldID, i int) {
	switch f {
	case fApt:
		m.apt = append(m.apt[:i], m.apt[i+1:]...)
	case fEnv:
		m.env = append(m.env[:i], m.env[i+1:]...)
	case fMounts:
		m.mounts = append(m.mounts[:i], m.mounts[i+1:]...)
	case fPorts:
		m.ports = append(m.ports[:i], m.ports[i+1:]...)
	case fEgress:
		m.egress = append(m.egress[:i], m.egress[i+1:]...)
	case fMCP:
		m.mcps = append(m.mcps[:i], m.mcps[i+1:]...)
	}
}

// ---- item screen (add / edit one item) -------------------------------------

// startItem opens the item editor for the current list field. idx < 0 adds a new
// item; otherwise it edits the existing one at idx.
func (m model) startItem(idx int) model {
	m.editIndex = idx
	m.itemErr = ""
	m.itemFocus = 0
	m.itemHasMode = false
	m.itemMode = 0
	switch m.listField {
	case fApt:
		m.inputLabels = []string{"Package"}
		v := ""
		if idx >= 0 {
			v = m.apt[idx]
		}
		m.inputs = []textinput.Model{newInput(v)}
	case fEgress:
		m.inputLabels = []string{"Host[:port]"}
		v := ""
		if idx >= 0 {
			v = m.egress[idx]
		}
		m.inputs = []textinput.Model{newInput(v)}
	case fMCP:
		// url XOR command discriminates remote vs local (no transport field —
		// ADR 0033). The command field is the reversible argv form: space-
		// separated, "double quotes" grouping an arg with spaces.
		m.inputLabels = []string{
			"Name",
			"URL (remote server; blank = local)",
			`Command (argv; "quote args with spaces")`,
			"Env names it consumes (space-separated)",
			"Extra egress host[:port] (space-separated)",
		}
		vals := []string{"", "", "", "", ""}
		if idx >= 0 {
			vals = mcpVals(m.mcps[idx])
		}
		m.inputs = make([]textinput.Model, len(vals))
		for i, v := range vals {
			m.inputs[i] = newInput(v)
		}
	case fEnv:
		m.inputLabels = []string{"Key", "Value"}
		k, val := "", ""
		if idx >= 0 {
			k, val = m.env[idx].Key, m.env[idx].Value
		}
		m.inputs = []textinput.Model{newInput(k), newInput(val)}
	case fMounts:
		m.inputLabels = []string{"Host path", "Target (in box)"}
		host, target := "", ""
		if idx >= 0 {
			host, target = m.mounts[idx].Host, m.mounts[idx].Target
			if m.mounts[idx].Mode == "rw" {
				m.itemMode = 1
			}
			// Disabled wins the picker display; the ro/rw underneath survives in
			// the entry (commitItem preserves it) so re-enabling restores it.
			if m.mounts[idx].Disabled {
				m.itemMode = 2
			}
		}
		m.inputs = []textinput.Model{newInput(host), newInput(target)}
		m.itemHasMode = true
	case fPorts:
		m.inputLabels = []string{"Container port", "Host port (blank = same)", "Interface (blank = " + config.DefaultPortInterface + ")"}
		container, host, iface := "", "", ""
		if idx >= 0 {
			p := m.ports[idx]
			container = strconv.Itoa(p.Container)
			if p.Host != 0 {
				host = strconv.Itoa(p.Host)
			}
			iface = p.Interface
		}
		m.inputs = []textinput.Model{newInput(container), newInput(host), newInput(iface)}
	}
	m.focusItem(0)
	m.mode = modeItem
	return m
}

func newInput(v string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(v)
	return ti
}

// itemFocusables is the number of focusable controls in the item editor (inputs,
// plus the ro/rw picker for mounts).
func (m model) itemFocusables() int {
	n := len(m.inputs)
	if m.itemHasMode {
		n++
	}
	return n
}

func (m *model) focusItem(i int) {
	m.itemFocus = wrap(i, m.itemFocusables())
	for j := range m.inputs {
		if j == m.itemFocus {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
}

func (m *model) onModePicker() bool { return m.itemHasMode && m.itemFocus == len(m.inputs) }

func (m model) updateItem(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeList
		return m, nil
	case "enter":
		return m.commitItem(), nil
	case "ctrl+s":
		// Global save: accept the open item first — a ^s that silently dropped
		// the row being typed would be lossy — then write the file. An invalid
		// item keeps the editor open with its error and saves nothing.
		next := m.commitItem()
		if next.itemErr != "" {
			return next, nil
		}
		return next.save(), nil
	case "tab", "down":
		m.focusItem(m.itemFocus + 1)
		return m, nil
	case "shift+tab", "up":
		m.focusItem(m.itemFocus - 1)
		return m, nil
	case "left":
		if m.onModePicker() {
			m.itemMode = wrap(m.itemMode-1, 3)
			return m, nil
		}
	case "right":
		if m.onModePicker() {
			m.itemMode = wrap(m.itemMode+1, 3)
			return m, nil
		}
		// At the end of an input with a live suggestion, → accepts it (host-path
		// completion or the derived target); otherwise it's a normal cursor move.
		if full := m.suggestion(); full != "" && m.atInputEnd() {
			m.inputs[m.itemFocus].SetValue(full)
			m.inputs[m.itemFocus].CursorEnd()
			return m, nil
		}
	}
	if !m.onModePicker() && m.itemFocus < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.itemFocus], cmd = m.inputs[m.itemFocus].Update(msg)
		return m, cmd
	}
	return m, nil
}

// atInputEnd reports whether the focused input's cursor is at the end, so → can
// mean "accept suggestion" rather than "move cursor right".
func (m model) atInputEnd() bool {
	if m.itemFocus >= len(m.inputs) {
		return false
	}
	in := m.inputs[m.itemFocus]
	return in.Position() >= len([]rune(in.Value()))
}

// suggestion returns the full suggested value for the focused input (the ghost is
// the part beyond what's typed). Mounts only: the host input gets filesystem
// completion; the target input, while empty, gets a path derived from the host.
func (m model) suggestion() string {
	if m.listField != fMounts || m.onModePicker() || m.itemFocus >= len(m.inputs) {
		return ""
	}
	switch m.itemFocus {
	case 0:
		return completeHostPath(m.inputs[0].Value())
	case 1:
		if strings.TrimSpace(m.inputs[1].Value()) != "" {
			return ""
		}
		return suggestTarget(m.inputs[0].Value())
	}
	return ""
}

// ghostSuffix is the un-typed remainder of the current suggestion, shown dimmed
// after the focused input.
func (m model) ghostSuffix() string {
	full := m.suggestion()
	cur := m.inputs[m.itemFocus].Value()
	if full != "" && strings.HasPrefix(full, cur) {
		return full[len(cur):]
	}
	return ""
}

// completeHostPath returns val extended to the longest unambiguous host-filesystem
// completion (dir-aware; a sole directory match gains a trailing "/"), or "" when
// there's nothing to add. Runs on the host, where byre config is launched, so the
// paths it completes are the real mount sources.
func completeHostPath(val string) string {
	if val == "" {
		return ""
	}
	if val == "~" {
		return "~/"
	}
	exp := expandTilde(val)
	var dir, prefix string
	if strings.HasSuffix(val, "/") {
		dir, prefix = exp, ""
	} else {
		dir, prefix = filepath.Dir(exp), filepath.Base(exp)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	var sole os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
			sole = e
		}
	}
	if len(names) == 0 {
		return ""
	}
	common := longestCommonPrefix(names)
	if len(common) < len(prefix) {
		return ""
	}
	completed := val + common[len(prefix):]
	if len(names) == 1 && sole.IsDir() && !strings.HasSuffix(completed, "/") {
		completed += "/"
	}
	if completed == val {
		return ""
	}
	return completed
}

// suggestTarget proposes an in-box mount target from a host path: a home-relative
// source mirrors under /home/dev (so dotfiles/config land where the agent looks),
// anything else goes to /home/dev/<basename>.
func suggestTarget(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	exp := filepath.Clean(expandTilde(host))
	base := filepath.Base(exp)
	if base == "" || base == "/" || base == "." {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, exp); err == nil &&
			rel != "." && !strings.HasPrefix(rel, "..") {
			return "/home/dev/" + filepath.ToSlash(rel)
		}
	}
	return "/home/dev/" + base
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// commitItem validates the item editor's inputs and writes the item back into
// the working slice (append when adding, replace when editing). Pre-checks are
// limited to what the layer gate can't own: parsing string inputs, friendlier
// wording for empty/partial input, and editor-only rules (duplicate env rows
// collapse in assemble() before validation could see them). Field shapes,
// ranges, and cross-item collisions are all caught by the same ValidateLayer
// call Save runs — against the assembled layer, while the offending item is
// still open, not at save time. Any failure keeps the editor open with a
// message. (Composition rule: never restate a config rule here — config owns
// the shapes, and a pre-check may only call what its validators call, like
// fEgress's ParseEgress.)
func (m model) commitItem() model {
	orig := m
	switch m.listField {
	case fApt:
		pkg := strings.TrimSpace(m.inputs[0].Value())
		if pkg == "" {
			m.itemErr = "package name can't be empty"
			return m
		}
		m.apt = putAt(m.apt, m.editIndex, pkg)
	case fEgress:
		entry := strings.TrimSpace(m.inputs[0].Value())
		if _, _, err := config.ParseEgress(entry); err != nil {
			m.itemErr = err.Error()
			return m
		}
		m.egress = putAt(m.egress, m.editIndex, entry)
	case fMCP:
		// Shape rules are config's (ValidateMCP — the same check the layer
		// gate runs); the editor only parses its string inputs apart. The
		// command field round-trips through the quote-aware argv form
		// (splitArgv/joinArgv), so an arg with embedded spaces survives an
		// open-and-commit unchanged.
		cmd, err := splitArgv(m.inputs[2].Value())
		if err != nil {
			m.itemErr = "command: " + err.Error()
			return m
		}
		mc := config.MCP{
			Name:    strings.TrimSpace(m.inputs[0].Value()),
			URL:     strings.TrimSpace(m.inputs[1].Value()),
			Command: cmd,
			Env:     strings.Fields(m.inputs[3].Value()),
			Egress:  strings.Fields(m.inputs[4].Value()),
		}
		if err := config.ValidateMCP(mc); err != nil {
			m.itemErr = err.Error()
			return m
		}
		m.mcps = putAt(m.mcps, m.editIndex, mc)
	case fEnv:
		k := strings.TrimSpace(m.inputs[0].Value())
		// Key shape is the layer check's job. Duplicates are the editor's: env is
		// a map on disk, so two rows with the same key would silently collapse in
		// assemble() before ValidateLayer could reject them.
		for i, kv := range m.env {
			if i != m.editIndex && kv.Key == k {
				m.itemErr = "duplicate key " + k
				return m
			}
		}
		m.env = putAt(m.env, m.editIndex, kvItem{Key: k, Value: m.inputs[1].Value()})
	case fMounts:
		host := strings.TrimSpace(m.inputs[0].Value())
		target := strings.TrimSpace(m.inputs[1].Value())
		if host == "" || target == "" {
			m.itemErr = "host and target are both required"
			return m
		}
		mt := config.Mount{Host: host, Target: target, Mode: "ro"}
		switch m.itemMode {
		case 1:
			mt.Mode = "rw"
		case 2:
			mt.Disabled = true
			// Keep the entry's stored ro/rw while it's off, so flipping it back
			// on restores the mode instead of resetting to ro.
			if m.editIndex >= 0 {
				mt.Mode = m.mounts[m.editIndex].Mode
			}
		}
		m.mounts = putAt(m.mounts, m.editIndex, mt)
	case fPorts:
		// The inputs are strings, so the numeric parse happens here; ranges and
		// collisions are the layer check's (validatePorts).
		container, err := strconv.Atoi(strings.TrimSpace(m.inputs[0].Value()))
		if err != nil {
			m.itemErr = "container port must be a number"
			return m
		}
		host := 0
		if hs := strings.TrimSpace(m.inputs[1].Value()); hs != "" {
			h, herr := strconv.Atoi(hs)
			if herr != nil {
				m.itemErr = "host port must be a number (blank = same as container)"
				return m
			}
			host = h
		}
		m.ports = putAt(m.ports, m.editIndex, config.Port{
			Container: container,
			Host:      host,
			Interface: strings.TrimSpace(m.inputs[2].Value()),
		})
	}
	// The same check Save applies, run against the assembled layer now that the
	// item is in it. putAt copies, so backing out is just returning the
	// pre-commit model with the message.
	if err := m.assemble().ValidateLayer(); err != nil {
		orig.itemErr = err.Error()
		return orig
	}
	m.itemErr = ""
	m.mode = modeList
	return m
}

// putAt appends v when idx < 0 else replaces the element at idx — always into
// a fresh slice, so a rejected commit can't have mutated the caller's backing
// array through a shared model copy.
func putAt[T any](s []T, idx int, v T) []T {
	out := append([]T{}, s...)
	if idx < 0 {
		return append(out, v)
	}
	out[idx] = v
	return out
}

// itemTitle is the singular noun the item editor's title uses. Explicit per
// field: naive de-pluralizing turned "Egress" into "Egres" (found live
// 2026-07-08).
func itemTitle(f fieldID) string {
	switch f {
	case fApt:
		return "Package"
	case fEnv:
		return "Env var"
	case fMounts:
		return "Extra mount"
	case fPorts:
		return "Port"
	case fEgress:
		return "Egress host"
	case fMCP:
		return "MCP server"
	}
	return strings.TrimSuffix(fieldLabel[f], "s")
}

// ---- display helpers ---------------------------------------------------------

func mountLine(mt config.Mount) string {
	mode := mt.Mode
	if mode == "" {
		mode = "ro"
	}
	if mt.Disabled {
		mode += ", disabled"
	}
	return fmt.Sprintf("%s -> %s (%s)", mt.Host, mt.Target, mode)
}

func portLine(p config.Port) string {
	iface, host := config.PortEffective(p)
	return fmt.Sprintf("%s:%d -> %d", iface, host, p.Container)
}

// mcpLine renders one [[mcp]] declaration for rows and the dirty signature:
// the same local/remote vocabulary status prints, plus the carried env names.
// The command renders in the argv form the editor parses (joinArgv), so a
// spaced arg reads as it round-trips.
func mcpLine(mc config.MCP) string {
	var b strings.Builder
	if mc.Remote() {
		fmt.Fprintf(&b, "%s — remote: %s", mc.Name, mc.URL)
	} else {
		fmt.Fprintf(&b, "%s — local: %s", mc.Name, joinArgv(mc.Command))
	}
	if len(mc.Env) > 0 {
		fmt.Fprintf(&b, " (env: %s)", strings.Join(mc.Env, ", "))
	}
	if len(mc.Egress) > 0 {
		fmt.Fprintf(&b, " (+egress: %s)", strings.Join(mc.Egress, ", "))
	}
	return b.String()
}

// joinArgv/splitArgv are the editor's REVERSIBLE argv text form: elements
// join on spaces; an element containing whitespace or a double quote renders
// double-quoted, with `\\` and `\"` escapes inside the quotes (backslash
// first, or a quoted arg ENDING in `\` would swallow its own closing quote —
// codex review round 5). splitArgv parses exactly that back. Round-trip
// property: splitArgv(joinArgv(x)) == x for every argv config validation
// admits (no control characters). Not a shell: no single quotes, no
// variable expansion — just enough to keep `["--label", "hello world"]`
// intact through an open-and-commit (codex review round 4).
func joinArgv(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			q := strings.ReplaceAll(a, `\`, `\\`)
			q = strings.ReplaceAll(q, `"`, `\"`)
			parts[i] = `"` + q + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func splitArgv(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote, started := false, false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case inQuote && r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
			cur.WriteRune(rs[i+1])
			i++
		case r == '"':
			inQuote = !inQuote
			started = true // "" is a deliberate empty element
		case !inQuote && (r == ' ' || r == '\t'):
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if inQuote {
		return nil, fmt.Errorf(`unterminated " quote`)
	}
	if started {
		out = append(out, cur.String())
	}
	return out, nil
}

// ---- rendering ---------------------------------------------------------------

func (m model) viewList() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(fieldLabel[m.listField]))
	rows := m.fieldRows(m.listField)
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("  (no items yet)\n"))
	}
	for i, r := range rows {
		line := r.text
		if r.kind == rowRemoved || r.kind == rowStaleMarker || r.kind == rowOffered || r.kind == rowEnvDoc {
			line = dimStyle.Render(line)
		}
		if ann := rowAnnotation(r); ann != "" {
			line += dimStyle.Render(ann)
		}
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.listCur, line))
	}
	// The "+ add" row.
	addLine := "+ add " + fieldLabel[m.listField]
	if m.listCur == len(rows) {
		fmt.Fprintf(&b, "%s\n", cursorLine(true, addLine))
	} else {
		fmt.Fprintf(&b, "%s\n", cursorLine(false, dimStyle.Render(addLine)))
	}

	if note := m.subFooterNote(); note != "" {
		b.WriteString("\n" + note)
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ move · enter actions · a add · ^s save · esc back"))
	return b.String()
}

// rowAnnotation is the dim provenance tail after a row's value (ADR 0018).
func rowAnnotation(r listRow) string {
	switch r.kind {
	case rowLocal:
		if r.also {
			return "  (also " + r.source + ")"
		}
	case rowOverride:
		return "  (overrides " + r.source + ")"
	case rowInherited:
		return "  (" + r.source + ")"
	case rowRemoved:
		if r.source == "" {
			return "  (removed here)" // this layer's own entry, killed by its own marker
		}
		return "  (" + r.source + " — removed here)"
	case rowStaleMarker:
		return "  (removes nothing — stale marker)"
	case rowSkill:
		return "  (" + r.source + ")"
	case rowOffered:
		if r.source == "" {
			return "  (offered here — closed)"
		}
		return "  (offered by " + r.source + " — closed)"
	case rowEnvDoc:
		guidance := ""
		if len(r.vals) > 0 {
			guidance = r.vals[0]
		}
		return " — " + guidance + "  (suggested by " + r.source + ")"
	}
	return ""
}

// viewMenu renders the per-row action menu: the row, where it's set, and the
// actions it supports -- terse labels, accelerator keys beside them.
func (m model) viewMenu() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", focusStyle.Render(m.menuRow.text))
	b.WriteString(dimStyle.Render("Set in: "+setIn(m.menuRow)) + "\n\n")
	choices := m.rowChoices(m.listField, m.menuRow)
	for i, c := range choices {
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.menuCur, c.label+dimStyle.Render("  "+c.key)))
	}
	if m.listField == fEnv && m.menuRow.kind == rowInherited {
		b.WriteString("\n" + dimStyle.Render("(can't unset from this layer — edit the "+m.menuRow.source+" config to remove)"))
	}
	if note := m.subFooterNote(); note != "" {
		b.WriteString("\n" + note)
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ move · enter apply · ^s save · esc back"))
	return b.String()
}

// setIn names where the row under the menu is set, in cascade vocabulary.
func setIn(r listRow) string {
	switch r.kind {
	case rowOverride:
		return "this file, overriding " + r.source
	case rowInherited, rowSkill:
		return r.source
	case rowRemoved:
		if r.source == "" {
			return "this file — removed by its own marker"
		}
		return r.source + " — removed by this file"
	case rowStaleMarker:
		return "this file (marker matches nothing)"
	case rowOffered:
		if r.source == "" {
			return "offered by this file — closed until opened"
		}
		return "offered by " + r.source + " — closed until opened"
	}
	if r.also {
		return "this file — also in " + r.source
	}
	return "this file"
}

func (m model) viewItem() string {
	var b strings.Builder
	verb := "Edit"
	if m.editIndex < 0 {
		verb = "Add"
	}
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(verb+" "+itemTitle(m.listField)))

	for i, in := range m.inputs {
		cursor := "  "
		val := in.View()
		if i == m.itemFocus {
			cursor = focusStyle.Render("▸ ")
			val += dimStyle.Render(m.ghostSuffix()) // autocomplete/suggestion ghost
		}
		label := fmt.Sprintf("%-*s", 16, m.inputLabels[i])
		fmt.Fprintf(&b, "%s%s: %s\n", cursor, label, val)
	}
	if m.itemHasMode {
		cursor := "  "
		if m.onModePicker() {
			cursor = focusStyle.Render("▸ ")
		}
		label := fmt.Sprintf("%-*s", 16, "Mode")
		fmt.Fprintf(&b, "%s%s: %s\n", cursor, label, renderSeg([]string{"ro", "rw", "disabled"}, m.itemMode, m.onModePicker()))
	}

	if m.itemErr != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.itemErr))
	}
	hint := "tab next · enter accept · ^s save · esc cancel"
	if m.listField == fMounts {
		hint = "tab next · → accept suggestion · ←/→ mode · enter accept · ^s save · esc cancel"
	}
	b.WriteString("\n\n" + dimStyle.Render(hint))
	return b.String()
}
