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

// ---- list screen (browse a field's items) ----------------------------------

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.itemLines(m.listField)
	addRow := len(items) // index of the "+ add" pseudo-row
	if cur, ok := cursorMove(msg.String(), m.listCur, addRow+1); ok {
		m.listCur = cur
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeForm
		return m, nil
	case "a":
		return m.startItem(-1), nil
	case "d", "x":
		if m.listCur < addRow {
			m.deleteItem(m.listField, m.listCur)
			if m.listCur > 0 && m.listCur >= len(m.itemLines(m.listField)) {
				m.listCur--
			}
		}
		return m, nil
	case "enter":
		if m.listCur == addRow {
			return m.startItem(-1), nil
		}
		return m.startItem(m.listCur), nil
	}
	return m, nil
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
		m.inputLabels = []string{"Container port", "Host port (blank = same)", "Interface (blank = 127.0.0.1)"}
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
	case "esc", "ctrl+c":
		m.mode = modeList
		return m, nil
	case "enter", "ctrl+s":
		return m.commitItem(), nil
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
// the working slice (append when adding, replace when editing). A validation
// error — per-field, or the same layer validation Save runs (so cross-item
// problems like duplicate mount targets surface while the offending item is
// still open, not at save time) — keeps the editor open with a message.
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
	case fEnv:
		k := strings.TrimSpace(m.inputs[0].Value())
		if !envKeyRe.MatchString(k) {
			m.itemErr = "key must match [A-Za-z_][A-Za-z0-9_]*"
			return m
		}
		// Reject a duplicate key (other than the row being edited): env is a map on
		// disk, so two rows with the same key would silently collapse on save.
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
		if !strings.HasPrefix(target, "/") {
			m.itemErr = "target must be an absolute path in the box (start with /)"
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
		container, err := strconv.Atoi(strings.TrimSpace(m.inputs[0].Value()))
		if err != nil || container < 1 || container > 65535 {
			m.itemErr = "container port must be a number 1-65535"
			return m
		}
		host := 0
		if hs := strings.TrimSpace(m.inputs[1].Value()); hs != "" {
			h, herr := strconv.Atoi(hs)
			if herr != nil || h < 1 || h > 65535 {
				m.itemErr = "host port must be blank (any) or a number 1-65535"
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

// ---- item lines (display form of a field's items) ---------------------------

func (m model) itemLines(f fieldID) []string {
	switch f {
	case fApt:
		return m.apt
	case fEnv:
		out := make([]string, len(m.env))
		for i, kv := range m.env {
			out[i] = kv.Key + "=" + kv.Value
		}
		return out
	case fMounts:
		out := make([]string, len(m.mounts))
		for i, mt := range m.mounts {
			out[i] = mountLine(mt)
		}
		return out
	case fPorts:
		out := make([]string, len(m.ports))
		for i, pt := range m.ports {
			out[i] = portLine(pt)
		}
		return out
	}
	return nil
}

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
	iface := p.Interface
	if iface == "" {
		iface = "127.0.0.1"
	}
	host := p.Host
	if host == 0 {
		host = p.Container // blank host mirrors the container port
	}
	return fmt.Sprintf("%s:%d -> %d", iface, host, p.Container)
}

// ---- rendering ---------------------------------------------------------------

func (m model) viewList() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(fieldLabel[m.listField]))
	items := m.itemLines(m.listField)
	if len(items) == 0 {
		b.WriteString(dimStyle.Render("  (no items yet)\n"))
	}
	for i, line := range items {
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.listCur, line))
	}
	// The "+ add" row.
	addLine := "+ add " + fieldLabel[m.listField]
	if m.listCur == len(items) {
		fmt.Fprintf(&b, "%s\n", cursorLine(true, addLine))
	} else {
		fmt.Fprintf(&b, "%s\n", cursorLine(false, dimStyle.Render(addLine)))
	}

	b.WriteString("\n" + dimStyle.Render("↑/↓ move · enter edit · a add · d delete · esc back"))
	return b.String()
}

func (m model) viewItem() string {
	var b strings.Builder
	verb := "Edit"
	if m.editIndex < 0 {
		verb = "Add"
	}
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(verb+" "+strings.TrimSuffix(fieldLabel[m.listField], "s")))

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
	hint := "tab next · enter save · esc cancel"
	if m.listField == fMounts {
		hint = "tab next · → accept suggestion · ←/→ mode · enter save · esc cancel"
	}
	b.WriteString("\n\n" + dimStyle.Render(hint))
	return b.String()
}
