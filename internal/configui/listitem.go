// listitem.go owns the list-field modes: the item browser (modeList) and the
// single-item add/edit editor (modeItem) for apt, env, mounts, and ports.
//
// The per-field operation switches below stay switches, not descriptor
// hooks, on a measured call: after the named-declaration genus extraction
// removed the mcp/claude-skill duplication, each switch is ONE case per
// field -- field-specific behavior with one home each, not copies -- and
// folding them into descriptors adds indirection without deleting code.
// Revisit when a new list field lands and the touch-point count still
// hurts (identity is already one fieldInfos row; behavior is one case per
// operation switch).
package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/skills"
)

// ---- list screen (browse a field's EFFECTIVE rows, ADR 0018) ---------------

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.fieldRows(m.listField)
	addRow := len(rows) // index of the "+ add" pseudo-row
	// A read-only screen renders no add row, so it must not be reachable
	// either: without this the cursor walks one past the last row onto a
	// slot that paints nothing, which is the same paint-vs-state desync
	// this batch already paid for twice.
	reach := addRow + 1
	if isReadOnlyField(m.listField) {
		reach = addRow
	}
	if cur, ok := cursorMove(msg.String(), m.listCur, reach); ok {
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
		if isReadOnlyField(m.listField) {
			m.status = readOnlyFieldNote(m.listField)
			return m, nil
		}
		m.itemHostEnv = false
		return m.startItem(-1), nil
	case "enter":
		if m.listCur == addRow {
			if isReadOnlyField(m.listField) {
				m.status = readOnlyFieldNote(m.listField)
				return m, nil
			}
			m.itemHostEnv = false
			return m.startItem(-1), nil
		}
		r := rows[m.listCur]
		m.status = ""
		// A read-only screen has no row menu either: its rows carry ordinary
		// kinds (a [sources] hint this file set is genuinely local), and
		// rowChoices would offer Edit/Delete on them.
		if isReadOnlyField(m.listField) {
			m.status = readOnlyFieldNote(m.listField)
			return m, nil
		}
		// A skill row usually has no actions (the pointer note explains) — but
		// an MCP skill row is closable (rowChoices offers Remove), so the menu
		// must open for it like any actionable row.
		if r.kind == rowSkill && len(m.rowChoices(m.listField, r)) == 0 {
			m.status = skillRowNote(r)
			return m, nil
		}
		if r.kind == rowEnvDoc {
			// The obvious move on reading a suggestion is "set it": open the
			// add editor with the key prefilled, cursor on the value. An
			// env_docs suggestion is about [env], not the passthrough.
			m.itemHostEnv = false
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
	if isReadOnlyField(m.listField) {
		m.status = readOnlyFieldNote(m.listField)
		return m, nil
	}
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
		case fEnv, fFiles:
			return []menuChoice{{"Override here", "e", actOverride}}
		case fMounts, fVolumes, fMCP, fClaudeSkills, fContext:
			return []menuChoice{
				{"Override here", "e", actOverride},
				{"Remove in this project", "d", actRemoveHere},
			}
		default: // apt, ports: no per-entry override, just the off-switch
			return []menuChoice{{"Remove in this project", "d", actRemoveHere}}
		}
	case rowHostEnv:
		// idx >= 0 means this file sets the key; anything else is inherited
		// (a lower layer, or byre's shipped defaults) and gets the override
		// door rather than a delete that would have nothing to delete.
		if r.idx >= 0 {
			return []menuChoice{{"Edit", "e", actEdit}, {"Delete", "d", actDelete}}
		}
		return []menuChoice{{"Override here", "e", actOverride}}
	case rowRemoved:
		return []menuChoice{{"Restore", "d", actRestore}}
	case rowStaleMarker:
		return []menuChoice{{"Clear marker", "d", actRestore}}
	case rowOffered:
		// Opening a door writes into THIS file's egress — say the real blast
		// radius when this file reaches beyond one project.
		switch m.target {
		case TargetGlobal:
			return []menuChoice{{warnStyle.Render("⚠ Open for every project on this machine"), "o", actOpen}}
		case TargetLayer:
			return []menuChoice{{warnStyle.Render("⚠ Open for every project extending this layer"), "o", actOpen}}
		}
		return []menuChoice{{"Open in this project", "o", actOpen}}
	case rowSkill:
		// MCP and Claude Skill rows are the closable skill contributions: a
		// `!name` closure reaches a skill-declared entry (ADR 0033 — "this
		// skill, minus one of its servers"; claude_skills adopts the same
		// semantics). Rows without an ident (already closed by a lower layer,
		// or a lower closure display row) stay menu-less.
		if (f == fMCP || f == fClaudeSkills) && r.ident != "" {
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
	// [env] literals and env_from_host passthroughs share the Env screen, so
	// the row -- not the field -- decides which item editor opens.
	m.itemHostEnv = r.kind == rowHostEnv
	switch act {
	case actEdit:
		return m.startItem(r.idx), nil
	case actOverride:
		return m.startOverride(r), nil
	case actDelete:
		// Deleting a credential row deletes the VALUE: the ciphertext is in
		// the row and byre keeps no copy of it anywhere else. Read before the
		// delete, said after it — the same sentence leaving the credential
		// kind in the form earns.
		credential := m.itemHostEnv && r.idx >= 0 && r.idx < len(m.hostEnv) &&
			config.IsCredentialSource(m.hostEnv[r.idx].Value)
		m.deleteItem(m.listField, r.idx)
		if credential {
			m.status = r.ident + " — " + credentialUnsetNote + "; ^s writes it"
		}
		if r.also {
			// Same data-vs-terminal rule as the row paint: the echoed value
			// rides the status line, so it is escaped there too.
			m.status = packages.EscapeTerminal(r.text) + " is still inherited — remove again to turn it off here"
		}
		// Deleting an OVERRIDE re-inherits the lower layer's entry — that's
		// the cascade working, but "delete" must not read as "gone"
		// (mounts/env/mcp share the shape). Env has no Remove action
		// (inherited vars can't be unset from this layer), so its note
		// must not advertise one.
		if r.kind == rowOverride && r.source != "" {
			if m.listField == fEnv {
				m.status = "override removed — the " + packages.EscapeTerminal(r.source) + " value is back in effect (an inherited var can't be unset from this layer)"
			} else {
				m.status = "override removed — the " + packages.EscapeTerminal(r.source) + " entry is back in effect; use its Remove action to turn it off here"
			}
		}
	case actRemoveHere:
		m.removeHere(r)
	case actRestore:
		m.deleteItem(m.listField, r.idx)
	case actOpen:
		// The opened door becomes THIS layer's own egress entry: user-authored,
		// user-attributed, closable like any other (ADR 0020).
		m.egress = append(m.egress, r.ident)
		// Beyond one project, say the scope of what just happened, where to
		// undo it (delete the entry here), and how one project opts back out.
		switch m.target {
		case TargetGlobal:
			m.status = packages.EscapeTerminal(r.ident) + " opened for every project on this machine (entry in default.config; delete it here to close, or \"Remove in this project\" in a project's editor to opt one box out)"
		case TargetLayer:
			m.status = packages.EscapeTerminal(r.ident) + " opened for every project extending this layer (entry in this layer file; delete it here to close, or \"Remove in this project\" in a project's editor to opt one box out)"
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
	case fVolumes:
		m.volumes = append(m.volumes, config.Volume{Name: "!" + r.ident})
	case fMCP:
		m.mcps = append(m.mcps, config.MCP{Name: "!" + r.ident})
	case fClaudeSkills:
		m.claudeSkills = append(m.claudeSkills, config.ClaudeSkill{Name: "!" + r.ident})
	case fContext:
		m.contexts = append(m.contexts, config.ContextDecl{Name: "!" + r.ident})
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
	if m.itemHostEnv {
		// Prefilled with what is being overridden, so "pin what I already
		// have" is one keypress and any change is deliberate.
		scheme, arg := hostEnvScheme(r.vals[1])
		next.itemMode = scheme
		next = next.syncHostEnvLabel()
		next.inputs[0].SetValue(r.vals[0])
		next.inputs[1].SetValue(arg)
		return next
	}
	switch m.listField {
	case fEnv, fFiles:
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
	case fVolumes:
		// vals: name, target, role, sharing (volumeVals). The inherited
		// DECLARATION rides along too: an override opens the add editor, and
		// the scope and seed this form does not author have to survive it --
		// shadowing a machine-scoped volume must not quietly rescope it to
		// this project.
		next.inputs[0].SetValue(r.vals[0])
		next.inputs[1].SetValue(r.vals[1])
		if r.vals[2] == "cache" {
			next.itemMode = 1
		}
		if r.vals[3] == "exclusive" {
			next.itemMode2 = 1
		}
		for _, v := range m.lowerNow().Volumes {
			if v.Name == r.ident {
				v := v
				next.itemVolume = &v
				break
			}
		}
	case fMCP:
		// vals: name, url, command(argv form), env, egress, headers (mcpVals).
		next.inputs[0].SetValue(r.vals[0])
		if r.vals[1] != "" {
			next.itemMode = 1
			next.inputs[1].SetValue(r.vals[1])
		} else {
			next.inputs[1].SetValue(r.vals[2])
		}
		next.inputs[2].SetValue(r.vals[3])
		next.inputs[3].SetValue(r.vals[4])
		next.inputs[4].SetValue(r.vals[5])
	case fContext:
		// vals: name, file, text (contextVals). The override starts as a copy
		// of the inherited declaration; saving shadows it by name.
		next.inputs[0].SetValue(r.vals[0])
		next.inputs[1].SetValue(r.vals[1])
		next.itemProse = r.vals[2]
		if r.vals[1] != "" {
			next.itemMode = 1
		}
	case fClaudeSkills:
		// vals: name, path (claudeSkillVals). An inherited skill contribution
		// has no config path; the override starts with the name prefilled and
		// the path to be supplied (a config override must point at a host dir).
		next.inputs[0].SetValue(r.vals[0])
		next.inputs[1].SetValue(r.vals[1])
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
// deadEndNote explains a keypress the cascade can't honor for this row.
func deadEndNote(f fieldID, r listRow) string {
	if f == fEnv && r.kind == rowInherited {
		return "can't unset an inherited var from this layer — override its value here, or edit the " + r.source + " config"
	}
	if r.kind == rowSkill {
		return skillRowNote(r)
	}
	if r.kind == rowEnvDoc {
		return "a suggestion from " + r.source + " — press enter to set it here"
	}
	return ""
}

func (m *model) deleteItem(f fieldID, i int) {
	// A passthrough delete removes this file's PIN, so the cascade's own
	// value applies again. It does NOT turn the key off: that is the
	// picker's `disabled` scheme, which writes KEY = "" -- a value, not an
	// absence. Delete is the only un-pin route (the picker has no `inherit`
	// option, because Delete already means exactly that on every list field).
	if m.itemHostEnv {
		m.hostEnv = append(m.hostEnv[:i], m.hostEnv[i+1:]...)
		return
	}
	switch f {
	case fApt:
		m.apt = append(m.apt[:i], m.apt[i+1:]...)
	case fEnv:
		m.env = append(m.env[:i], m.env[i+1:]...)
	case fFiles:
		m.files = append(m.files[:i], m.files[i+1:]...)
	case fMounts:
		m.mounts = append(m.mounts[:i], m.mounts[i+1:]...)
	case fVolumes:
		m.volumes = append(m.volumes[:i], m.volumes[i+1:]...)
	case fPorts:
		m.ports = append(m.ports[:i], m.ports[i+1:]...)
	case fEgress:
		m.egress = append(m.egress[:i], m.egress[i+1:]...)
	case fMCP:
		m.mcps = append(m.mcps[:i], m.mcps[i+1:]...)
	case fClaudeSkills:
		m.claudeSkills = append(m.claudeSkills[:i], m.claudeSkills[i+1:]...)
	case fContext:
		m.contexts = append(m.contexts[:i], m.contexts[i+1:]...)
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
	m.itemModeOpts = nil
	m.itemModeLabel = ""
	m.itemModeFirst = false
	m.itemHasMode2 = false
	m.itemMode2 = 0
	m.itemMode2Opts = nil
	m.itemMode2Label = ""
	m.itemVolume = nil
	if m.listField == fEnv {
		// A WELL-FORMED credential row opens into the form like any other
		// passthrough: the picker carries its kind, the Value box is masked
		// and empty (empty means unchanged), and accepting a new value rides
		// the same write path `byre credentials set` rides.
		//
		// A row that names a credential scheme and CANNOT be used -- damaged
		// base64, or the reserved `manifest` key -- still refuses. Opening it
		// would offer to re-encrypt over a row whose problem is not the value:
		// the picker's other schemes would write "" or a host source over the
		// ciphertext, and a new value would write a good row on a key that
		// cannot deliver. That is not repair; `byre credentials unset` is.
		// An editor with no credential write path at all (--global) refuses
		// every credential row for the same reason it offers no credential
		// kinds: nothing here can write one.
		if idx >= 0 && m.itemHostEnv {
			src := m.hostEnv[idx].Value
			key := m.hostEnv[idx].Key
			_, usable, perr := config.ParseEncryptedRow(key, src)
			if config.IsCredentialSource(src) && (!usable || perr != nil || !m.canWriteCredentials()) {
				m.status = key + " is a credential — change it with `byre credentials set " + key + "` (this screen would write over the ciphertext)"
				return m
			}
		}
		// One picker for the whole screen: an [env] literal and an
		// env_from_host passthrough answer the same question ("where does
		// this variable's value come from"), and asking it once is what makes
		// ADDING a passthrough possible -- the add key previously built a
		// literal editor and nothing else.
		key, arg := "", ""
		m.itemMode = schemeValue
		if idx >= 0 {
			if m.itemHostEnv {
				key = m.hostEnv[idx].Key
				m.itemMode, arg = hostEnvScheme(m.hostEnv[idx].Value)
			} else {
				key, arg = m.env[idx].Key, m.env[idx].Value
			}
		}
		m.itemHasMode = true
		m.itemModeOpts = hostEnvPickerOpts(m.canWriteCredentials())
		m.itemModeLabel = "Source"
		m.itemModeFirst = true
		m.inputLabels = []string{"Key", hostEnvArgLabel(m.itemMode)}
		argIn := newInput(arg)
		argIn.Placeholder = hostEnvArgHint(m.itemMode)
		m.inputs = []textinput.Model{newInput(key), argIn}
		m.maskCredentialInput()
		m.probeCredentialIdentity()
		// Focus the KEY, not the picker: `value` is the common answer, so the
		// old flow (type key, tab, type value, enter) must not grow a step.
		m.focusItem(1)
		m.mode = modeItem
		return m
	}
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
		// Kind picker FIRST — it drives the form: one Endpoint input whose
		// meaning (local argv / remote url) follows the picker, so the
		// url-XOR-command rule is structural instead of a validation error
		// (Pete's review of the first form: five undifferentiated inputs,
		// unclear requiredness, unstated lowercase rule, implied egress
		// invisible). The endpoint's live label + the derived-egress footer
		// render in viewItem; the name lowercases itself on commit.
		m.itemHasMode = true
		m.itemModeOpts = []string{"local", "remote"}
		m.itemModeLabel = "Kind"
		m.itemModeFirst = true
		m.inputLabels = []string{
			"Name (required)", // viewItem appends the lowercase hint
			"Endpoint",        // viewItem swaps in the kind-specific label
			"Env var names (optional)",
			"Extra egress (optional)",
			"Headers (optional)", // remote only; validated by ValidateMCP
		}
		name, endpoint, env, egress, headers := "", "", "", "", ""
		if idx >= 0 {
			mc := m.mcps[idx]
			name, env, egress = mc.Name, strings.Join(mc.Env, " "), strings.Join(mc.Egress, " ")
			headers = joinHeaders(mc.Headers)
			if mc.Remote() {
				m.itemMode = 1
				endpoint = mc.URL
			} else {
				endpoint = joinArgv(mc.Command)
			}
		}
		m.inputs = []textinput.Model{newInput(name), newInput(endpoint), newInput(env), newInput(egress), newInput(headers)}
	case fClaudeSkills:
		// Two inputs: the name (frontmatter identity) and the host source dir.
		// Content checks (SKILL.md, frontmatter, bounds) remain the bake's to
		// ENFORCE; the editor holds the declaration to config's shape rules
		// only, plus a warn-only build-will-fail note (claudeSkillDirNote) so
		// a typo'd path is visible now instead of at the next develop.
		m.inputLabels = []string{
			"Name (required)", // viewItem appends the lowercase hint
			"Directory (host path, ~/… or absolute)",
		}
		name, path := "", ""
		if idx >= 0 {
			name, path = m.claudeSkills[idx].Name, m.claudeSkills[idx].Path
		}
		m.inputs = []textinput.Model{newInput(name), newInput(path)}
	case fContext:
		// Name + (file-mode) path. The prose itself is NOT a form input: it is
		// edited in $EDITOR (^e — the suspend/reload shape the whole-file ^e
		// already uses), held in m.itemProse until commit.
		m.inputLabels = []string{
			"Name (required)",
			"File (host path, ~/… or absolute — file mode only)",
		}
		name, file := "", ""
		m.itemProse = ""
		if idx >= 0 {
			cd := m.contexts[idx]
			name, file = cd.Name, cd.File
			m.itemProse = cd.Text
			if cd.File != "" {
				m.itemMode = 1
			}
		}
		m.inputs = []textinput.Model{newInput(name), newInput(file)}
		m.itemHasMode = true
		m.itemModeOpts = []string{"inline text", "host file"}
		m.itemModeLabel = "Source"
	case fFiles:
		// The labels carry the two rules planFiles enforces at build time, so
		// a refusal is not the first place a user learns them.
		m.inputLabels = []string{"Source (in project)", "Destination (absolute, in image)"}
		src, dest := "", ""
		if idx >= 0 {
			src, dest = m.files[idx].Key, m.files[idx].Value
		}
		m.inputs = []textinput.Model{newInput(src), newInput(dest)}
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
		m.itemModeOpts = []string{"ro", "rw", "disabled"}
		m.itemModeLabel = "Mode"
	case fVolumes:
		// Name + target + role + sharing. Scope and seed are NOT form
		// controls: both are declared shapes with consequences a two-word
		// picker can't carry (a machine scope is one volume shared by every
		// project; a seed is a one-time host->volume copy with its own
		// grammar), and both are overwhelmingly skill-authored. An edit
		// preserves whatever the entry already declares (commitItem) and
		// itemNotes says so. Sharing IS a control: it is two words, it is the
		// author's own claim about their data, and it changes what starting a
		// second worktree box does.
		m.inputLabels = []string{"Name", "Target (in box)"}
		name, target := "", ""
		if idx >= 0 {
			v := m.volumes[idx]
			name, target = v.Name, v.Target
			if v.Role == "cache" {
				m.itemMode = 1
			}
			if v.Exclusive() {
				m.itemMode2 = 1
			}
		}
		m.inputs = []textinput.Model{newInput(name), newInput(target)}
		m.itemHasMode = true
		m.itemModeOpts = []string{"state", "cache"}
		m.itemModeLabel = "Role"
		m.itemHasMode2 = true
		m.itemMode2Opts = []string{"shared", "exclusive"}
		m.itemMode2Label = "Sharing"
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

// itemFocusables is the number of focusable controls in the item editor (the
// inputs, plus the segmented picker when the field has one).
func (m model) itemFocusables() int {
	n := len(m.inputs)
	if m.itemHasMode {
		n++
	}
	if m.itemHasMode2 {
		n++
	}
	return n
}

// mode2Control is the second picker's control index: after every input and
// after the first picker, wherever that one sits. -1 when the form has none.
func (m model) mode2Control() int {
	if !m.itemHasMode2 {
		return -1
	}
	n := len(m.inputs)
	if m.itemHasMode {
		n++
	}
	return n
}

// itemInputIndex maps the control index to an input index, or -1 when a
// picker holds focus. With itemModeFirst the first picker is control 0 and the
// inputs shift up one.
func (m model) itemInputIndex() int {
	if c := m.mode2Control(); c >= 0 && m.itemFocus == c {
		return -1
	}
	if m.itemHasMode && m.itemModeFirst {
		return m.itemFocus - 1 // control 0 = picker; -1 flags it
	}
	if m.itemHasMode && m.itemFocus == len(m.inputs) {
		return -1
	}
	return m.itemFocus
}

func (m *model) focusItem(i int) {
	m.itemFocus = wrap(i, m.itemFocusables())
	fi := m.itemInputIndex()
	for j := range m.inputs {
		if j == fi {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
}

func (m *model) onModePicker() bool {
	return m.itemHasMode && !m.onMode2Picker() && m.itemInputIndex() == -1
}

func (m *model) onMode2Picker() bool { return m.itemHasMode2 && m.itemFocus == m.mode2Control() }

func (m model) updateItem(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeList
		return m, nil
	case "enter":
		return m.commitItem(), nil
	case "ctrl+e":
		// [[context]] inline prose is edited in a real editor, not a form
		// input: write the draft to a temp file, suspend to $EDITOR, and
		// editorClosedMsg routes back here via prosePath (form.go).
		if m.listField == fContext && m.itemMode == 0 {
			f, err := hostopen.PlainCreateTemp("", "byre-context-*.md", hostopen.ProcessTemp)
			if err != nil {
				m.itemErr = err.Error()
				return m, nil
			}
			if _, err := f.WriteString(m.itemProse); err != nil {
				f.Close()
				hostopen.PlainRemove(f.Name(), hostopen.ByreCreated)
				m.itemErr = err.Error()
				return m, nil
			}
			if err := f.Close(); err != nil {
				hostopen.PlainRemove(f.Name(), hostopen.ByreCreated)
				m.itemErr = err.Error()
				return m, nil
			}
			m.prosePath = f.Name()
			return m, openEditor(m.prosePath, m.editorRoots)
		}
	case "ctrl+s":
		// Global save: accept the open item first — a ^s that silently dropped
		// the row being typed would be lossy — then write the file. An invalid
		// item keeps the editor open with its error and saves nothing.
		next := m.commitItem()
		if next.itemErr != "" {
			return next, nil
		}
		if next.mode == modeCredPass {
			// The item was accepted and is now waiting on a passphrase; the
			// save belongs after that decision, not underneath its modal.
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
		if m.onMode2Picker() {
			m.itemMode2 = wrap(m.itemMode2-1, len(m.itemMode2Opts))
			return m, nil
		}
		if m.onModePicker() {
			m.itemMode = wrap(m.itemMode-1, len(m.itemModeOpts))
			return m.syncHostEnvLabel(), nil
		}
	case "right":
		if m.onMode2Picker() {
			m.itemMode2 = wrap(m.itemMode2+1, len(m.itemMode2Opts))
			return m, nil
		}
		if m.onModePicker() {
			m.itemMode = wrap(m.itemMode+1, len(m.itemModeOpts))
			return m.syncHostEnvLabel(), nil
		}
		// At the end of an input with a live suggestion, → accepts it (host-path
		// completion or the derived target); otherwise it's a normal cursor move.
		if full := m.suggestion(); full != "" && m.atInputEnd() {
			m.inputs[m.itemInputIndex()].SetValue(full)
			m.inputs[m.itemInputIndex()].CursorEnd()
			return m, nil
		}
	}
	if fi := m.itemInputIndex(); fi >= 0 && fi < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[fi], cmd = m.inputs[fi].Update(msg)
		return m, cmd
	}
	return m, nil
}

// atInputEnd reports whether the focused input's cursor is at the end, so → can
// mean "accept suggestion" rather than "move cursor right".
func (m model) atInputEnd() bool {
	fi := m.itemInputIndex()
	if fi < 0 || fi >= len(m.inputs) {
		return false
	}
	in := m.inputs[fi]
	return in.Position() >= len([]rune(in.Value()))
}

// suggestion returns the full suggested value for the focused input (the ghost is
// the part beyond what's typed). Mounts only: the host input gets filesystem
// completion; the target input, while empty, gets a path derived from the host.
func (m model) suggestion() string {
	if m.listField != fMounts || m.onModePicker() {
		return ""
	}
	switch m.itemInputIndex() {
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
	fi := m.itemInputIndex()
	if fi < 0 || fi >= len(m.inputs) {
		return ""
	}
	cur := m.inputs[fi].Value()
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
	entries, err := hostopen.PlainReadDir(dir, hostopen.HostUserOwned)
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
			return skills.DevHome + "/" + filepath.ToSlash(rel)
		}
	}
	return skills.DevHome + "/" + base
}

// expandTilde degrades on failure (returns p unchanged): this is the TUI's
// display/completion path, where a missing home means fewer suggestions, not
// an error.
func expandTilde(p string) string {
	if exp, err := config.ExpandTilde(p); err == nil {
		return exp
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
	if m.listField == fEnv {
		return m.commitEnvRow(orig)
	}
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
		// The Kind picker decides what the Endpoint input means; shape rules
		// stay config's (ValidateMCP — the same check the layer gate runs).
		// The name lowercases itself (the grammar is lowercase-only, and
		// "GitHub" means github); a local endpoint round-trips through the
		// quote-aware argv form so spaced args survive an open-and-commit.
		mc := config.MCP{
			Name:   strings.ToLower(strings.TrimSpace(m.inputs[0].Value())),
			Env:    strings.Fields(m.inputs[2].Value()),
			Egress: strings.Fields(m.inputs[3].Value()),
		}
		hdrs, herr := splitHeaders(m.inputs[4].Value())
		if herr != nil {
			m.itemErr = "headers: " + herr.Error()
			return m
		}
		mc.Headers = hdrs
		endpoint := strings.TrimSpace(m.inputs[1].Value())
		if m.itemMode == 1 {
			mc.URL = endpoint
		} else {
			cmd, err := splitArgv(endpoint)
			if err != nil {
				m.itemErr = "command: " + err.Error()
				return m
			}
			mc.Command = cmd
		}
		if err := config.ValidateMCP(mc); err != nil {
			m.itemErr = err.Error()
			return m
		}
		m.mcps = putAt(m.mcps, m.editIndex, mc)
	case fContext:
		cd := config.ContextDecl{
			Name: strings.ToLower(strings.TrimSpace(m.inputs[0].Value())),
		}
		if m.itemMode == 1 {
			cd.File = strings.TrimSpace(m.inputs[1].Value())
		} else {
			cd.Text = m.itemProse
			if strings.TrimSpace(cd.Text) == "" {
				m.itemErr = "no text yet — ^e opens $EDITOR to write it"
				return m
			}
		}
		if err := config.ValidateContextDecl(cd); err != nil {
			m.itemErr = err.Error()
			return m
		}
		m.contexts = putAt(m.contexts, m.editIndex, cd)
	case fClaudeSkills:
		// Shape rules stay config's (ValidateClaudeSkill — the same check the
		// layer gate runs); the name lowercases itself like MCP names.
		cs := config.ClaudeSkill{
			Name: strings.ToLower(strings.TrimSpace(m.inputs[0].Value())),
			Path: strings.TrimSpace(m.inputs[1].Value()),
		}
		if err := config.ValidateClaudeSkill(cs, false); err != nil {
			m.itemErr = err.Error()
			return m
		}
		m.claudeSkills = putAt(m.claudeSkills, m.editIndex, cs)
	case fFiles:
		src := strings.TrimSpace(m.inputs[0].Value())
		dest := strings.TrimSpace(m.inputs[1].Value())
		if src == "" || dest == "" {
			m.itemErr = "source and destination are both required"
			return m
		}
		// Shapes belong to config, not here: this calls the same validator
		// ValidateLayer runs, so the editor cannot drift from what a layer
		// will accept (the composition rule above).
		if err := config.ValidateFiles(map[string]string{src: dest}); err != nil {
			m.itemErr = err.Error()
			return m
		}
		// files is a map on disk, so two rows sharing a source would silently
		// collapse on save -- and planFiles refuses two spellings of one
		// source outright, since which survives would be map-iteration order.
		clean := filepath.Clean(src)
		for i, kv := range m.files {
			if i != m.editIndex && filepath.Clean(kv.Key) == clean {
				m.itemErr = "duplicate source " + src
				return m
			}
		}
		m.files = putAt(m.files, m.editIndex, kvItem{Key: src, Value: dest})
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
	case fVolumes:
		name := strings.TrimSpace(m.inputs[0].Value())
		target := strings.TrimSpace(m.inputs[1].Value())
		if name == "" || target == "" {
			m.itemErr = "name and target are both required"
			return m
		}
		v := config.Volume{Name: name, Target: target, Role: "state"}
		if m.itemMode == 1 {
			v.Role = "cache"
		}
		// Written only when it is the non-default answer: `sharing = "shared"`
		// in every volume block would be noise in a file people hand-edit,
		// and the empty spelling means exactly the same thing.
		if m.itemMode2 == 1 {
			v.Sharing = "exclusive"
		}
		// Carry the declared scope and seed through: this form authors neither,
		// so dropping them would silently un-share a machine volume (or un-seed
		// a state one) as a side effect of retyping a target. Editing carries
		// them from the entry being edited, overriding from the inherited
		// declaration being shadowed -- both are "the entry this row is about".
		if base := m.volumeBase(); base != nil {
			v.Scope, v.Seed = base.Scope, base.Seed
		}
		m.volumes = putAt(m.volumes, m.editIndex, v)
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

// volumeBase is the declaration whose scope and seed the open volume editor
// must carry: the entry being EDITED, or -- for an override, which opens the
// ADD editor with no index -- the inherited declaration being shadowed. nil
// for a plain add, which is project-scoped and unseeded by construction.
func (m model) volumeBase() *config.Volume {
	if m.editIndex >= 0 && m.editIndex < len(m.volumes) {
		return &m.volumes[m.editIndex]
	}
	return m.itemVolume
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

// volumeLine renders one [[volumes]] declaration: name, mount point, role, and
// the properties that change what the entry MEANS -- a machine scope (one
// volume shared by every project of this user), an exclusive sharing contract
// (develop refuses a second live box that would mount it), and a seed (a
// one-time copy into a fresh volume). Each is flagged because a row that
// omitted it would read as an ordinary, freely shared per-project volume.
func volumeLine(v config.Volume) string {
	role := v.Role
	if role == "" {
		role = "state"
	}
	s := fmt.Sprintf("%s -> %s (%s)", v.Name, v.Target, role)
	if v.MachineScoped() {
		s += " [machine — shared by all your projects]"
	}
	if v.Exclusive() {
		s += " [exclusive — one live box at a time]"
	}
	if v.Seed != nil {
		s += " [seeded]"
	}
	return s
}

func portLine(p config.Port) string {
	iface, host := config.PortEffective(p)
	return fmt.Sprintf("%s:%d -> %d", iface, host, p.Container)
}

// filesSourceNote is the Build files editor's live missing-source check --
// the claudeSkillDirNote shape on the [build].files vocabulary. Warn-only,
// never a gate; empty or shape-invalid sources return "" (the required-field
// and ValidateFiles checks own those), and so does an editor with no project
// dir to resolve against.
func filesSourceNote(projectDir, src string) string {
	s := strings.TrimSpace(src)
	if projectDir == "" || s == "" || filepath.IsAbs(s) {
		return ""
	}
	clean := filepath.Clean(s)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "" // the escape refusal owns this shape
	}
	if _, err := hostopen.PlainStat(filepath.Join(projectDir, clean), hostopen.UserNamed); err != nil {
		return "source not in the project — build will fail"
	}
	return ""
}

// claudeSkillDirNote is the live legibility check on a declared host dir:
// the editor accepted a nonexistent path silently, deferring the failure to
// the next develop. skills.ValidateClaudeSkillDir — the exact check the bake
// runs — decides WHETHER the build would fail (so editor and develop can never
// disagree); the label
// here only classifies it briefly. Warn-only, never a gate: the path may be
// created later, and byre doesn't nanny. Empty paths return "" (the
// required-field check owns those; skill-contributed `from` entries resolve
// inside their package and aren't host paths at all).
func claudeSkillDirNote(name, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	dir := expandTilde(p)
	if skills.ValidateClaudeSkillDir(dir, strings.ToLower(strings.TrimSpace(name))) == nil {
		return ""
	}
	if fi, err := hostopen.PlainStat(dir, hostopen.UserNamed); err != nil {
		return "path missing — build will fail"
	} else if !fi.IsDir() {
		return "not a directory — build will fail"
	}
	if _, err := hostopen.PlainStat(filepath.Join(dir, "SKILL.md"), hostopen.UserNamed); err != nil {
		return "no SKILL.md — build will fail"
	}
	return "SKILL.md invalid or name mismatch — build will fail"
}

// claudeSkillLine renders one Claude Skill declaration: name plus whichever
// source spelling its home carries (a config path or a skill-relative from).
func claudeSkillLine(cs config.ClaudeSkill) string {
	src := cs.Path
	if src == "" {
		src = cs.From
	}
	if src == "" {
		return cs.Name
	}
	return cs.Name + " — " + src
}

// contextDeclLine is one [[context]] declaration's row: its source at a
// glance (file path, or the prose's first line and extent).
func contextDeclLine(cd config.ContextDecl) string {
	if cd.File != "" {
		return cd.Name + " — file: " + cd.File
	}
	first, _, _ := strings.Cut(strings.TrimSpace(cd.Text), "\n")
	if len(first) > 48 {
		first = first[:47] + "…"
	}
	if lines := strings.Count(strings.TrimRight(cd.Text, "\n"), "\n"); lines > 0 {
		return fmt.Sprintf("%s — %q +%d lines", cd.Name, first, lines)
	}
	return fmt.Sprintf("%s — %q", cd.Name, first)
}

// onProseEditorClosed completes the [[context]] prose round-trip: read the
// temp file back into the item draft and return to the item editor. The
// whole-file ^e reload (onEditorClosed) is untouched — prosePath is what
// routed here.
func (m model) onProseEditorClosed(err error) model {
	path := m.prosePath
	m.prosePath = ""
	defer hostopen.PlainRemove(path, hostopen.ByreCreated)
	if err != nil {
		m.itemErr = "$EDITOR: " + err.Error()
		return m
	}
	b, rerr := hostopen.PlainReadFile(path, hostopen.ByreCreated)
	if rerr != nil {
		m.itemErr = rerr.Error()
		return m
	}
	m.itemProse = string(b)
	m.itemErr = ""
	return m
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
	// Headers WITH values: the row is also the dirty signature (sig), so a
	// header edit must change this string — and the env screen shows values
	// too, so no new exposure class.
	if len(mc.Headers) > 0 {
		fmt.Fprintf(&b, " (headers: %s)", joinHeaders(mc.Headers))
	}
	return b.String()
}

// joinArgv/splitArgv are the editor's REVERSIBLE argv text form: elements
// join on spaces; an element containing whitespace or a double quote renders
// double-quoted, with `\\` and `\"` escapes inside the quotes (backslash
// first, or a quoted arg ENDING in `\` would swallow its own closing quote).
// splitArgv parses exactly that back. Round-trip
// property: splitArgv(joinArgv(x)) == x for every argv config validation
// admits (no control characters). Not a shell: no single quotes, no
// variable expansion — just enough to keep `["--label", "hello world"]`
// intact through an open-and-commit.
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

// joinHeaders/splitHeaders are the headers input's text form, riding the
// reversible argv codec: each header is ONE quoted `"Name: value"` token, so
// values with spaces/quotes survive an open-and-commit unchanged and multiple
// headers stay representable.
func joinHeaders(h map[string]string) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + ": " + h[k]
	}
	return joinArgv(pairs)
}

func splitHeaders(s string) (map[string]string, error) {
	tokens, err := splitArgv(s)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, tok := range tokens {
		k, v, ok := strings.Cut(tok, ":")
		if !ok {
			return nil, fmt.Errorf("%q: want \"Name: value\"", tok)
		}
		k = strings.TrimSpace(k)
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("header %s given twice", k)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
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
	fmt.Fprintf(&b, "%s\n\n", m.crumb(fieldLabel(m.listField)))
	// Before the rows, not after: this says what the screen IS, and a reader
	// who learns that at the bottom has already spent the list wondering
	// which of these they were meant to act on.
	if ex := fieldExplain(m.listField); ex != "" {
		b.WriteString(dimStyle.Render("  "+ex) + "\n\n")
	}
	rows := m.fieldRows(m.listField)
	if len(rows) == 0 {
		// Newline OUTSIDE the Render: lipgloss pads multi-line renders to
		// equal width, so an embedded \n minted a phantom line of spaces
		// that indented whatever printed next (field-report 2026-07-17).
		b.WriteString(dimStyle.Render("  (none yet)") + "\n")
	}
	// Skill contributions get their own heading. They are already LAST in
	// cascade order on every screen, so this is decoration, not a reordering
	// -- and it stays out of the rows slice on purpose: the cursor indexes
	// that slice, so a header row would shift every selection by one.
	//
	// It matters most where a skill dominates the screen: baked files are
	// overwhelmingly skill payloads, and an unheaded list reads as though the
	// user wrote every line of it.
	skillHeaderShown := false
	for i, r := range rows {
		if r.kind == rowSkill && !skillHeaderShown && !isReadOnlyField(m.listField) {
			skillHeaderShown = true
			b.WriteString("\n" + dimStyle.Render("  — from skills —") + "\n")
		}
		// Row text and annotation are DATA on this screen, and data does not
		// get to drive the terminal: a config value carrying \r or an SGR
		// escape (via TOML's \uXXXX form -- literal control bytes never parse) could
		// overwrite its own row with a forged one, or terminate byre's
		// styling mid-line. The grants screens are where the user audits what
		// the agent wrote, so what is painted must be what is stored. Escaped
		// BEFORE byre's own styling wraps it -- after, the same strip would
		// eat the dim codes -- and display-only: r.vals prefills the editor
		// and must stay raw, or a save would write back a mangled value.
		line := packages.EscapeTerminal(r.text)
		if r.kind == rowRemoved || r.kind == rowStaleMarker || r.kind == rowOffered || r.kind == rowEnvDoc {
			line = dimStyle.Render(line)
		}
		if ann := packages.EscapeTerminal(rowAnnotation(r)); ann != "" {
			line += dimStyle.Render(ann)
		}
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.listCur, line))
	}
	// No add row: there is nothing on this screen for a user to write, and
	// the header above already said why.
	if isReadOnlyField(m.listField) {
		if note := m.subFooterNote(); note != "" {
			b.WriteString("\n" + note)
		}
		b.WriteString("\n" + helpLine("↑/↓", "move", "^s", "save", "esc", "back"))
		return b.String()
	}

	// The "+ add" row.
	addLine := "+ add " + fieldLabel(m.listField)
	if m.listCur == len(rows) {
		fmt.Fprintf(&b, "%s\n", cursorLine(true, addLine))
	} else {
		fmt.Fprintf(&b, "%s\n", cursorLine(false, dimStyle.Render(addLine)))
	}

	if note := m.subFooterNote(); note != "" {
		b.WriteString("\n" + note)
	}
	b.WriteString("\n" + helpLine("↑/↓", "move", "enter", "actions", "a", "add", "^s", "save", "esc", "back"))
	return b.String()
}

// rowAnnotation is the dim provenance tail after a row's value (ADR 0018).
func rowAnnotation(r listRow) string {
	switch r.kind {
	case rowLocal:
		if r.closed {
			// A port this file binds twice: the later binding replaces this
			// one, so the row is config that publishes nothing.
			return "  (replaced by a later entry in this file)"
		}
		if r.also {
			return "  (also " + r.source + ")"
		}
	case rowOverride:
		return "  (overrides " + r.source + ")"
	case rowInherited:
		if r.closed {
			return "  (" + r.source + " — replaced by this file's entry)"
		}
		return "  (" + r.source + ")"
	case rowRemoved:
		if r.source == "" {
			return "  (removed here)" // this layer's own entry, killed by its own marker
		}
		return "  (" + r.source + " — removed here)"
	case rowStaleMarker:
		return "  (removes nothing — stale marker)"
	case rowSkill:
		if r.skews != "" {
			// A reserved BYRE_ key costs claims (a known knob names them; an
			// unknown key gets the qualified wording -- never "byre control",
			// which would claim knowledge byre lacks). Without the annotation
			// the attribution reads like any other skill env var and the cost
			// was legible only in `byre status`.
			return "  (" + r.source + " — " + r.skews + ")"
		}
		return "  (" + r.source + ")"
	case rowHostEnv:
		// Without this the six keys byre ships rendered bare, which on a
		// screen where every OTHER row carries its provenance reads as "you
		// set this here". idx >= 0 is the only case that actually is local.
		who := r.source
		if r.idx >= 0 {
			who = "set here"
		}
		if r.closed {
			return "  (" + who + " — overridden by [env], not passed)"
		}
		return "  (" + who + ")"
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

// proseBlock renders stored instruction text read-only: soft-wrapped to the
// available width, gutter-marked, capped, with a tail count naming the way
// to the rest. Shared by the item editor and the row menu — reading the
// instructions never requires opening an editor, wherever the row lives
// (maintainer calls, 2026-07-25). Wrapping happens HERE because the view's
// clipLines truncates long rendered lines with an ellipsis — prose written
// as one long line (the natural shape of an instruction sentence) showed
// only its first screen-width otherwise.
func proseBlock(text, more string, width int) string {
	// Wrap to the REAL remaining width (never a floor above it — a floor
	// hands lines back to the clip's ellipsis on a narrow terminal), in
	// display CELLS, the unit clipLines truncates by (CJK and emoji are two
	// cells wide; rune counting re-clipped them — codex pre-ship review).
	w := width - 4 // two-space indent + gutter
	if w < 1 {
		w = 1
	}
	var lines []string
	// Prose is data: standing instructions are exactly what a self-edit
	// agent writes, so the reader strips control sequences like every other
	// display surface. Display-only (^e edits the stored text, not this) and
	// PER LINE, after the split and the tab expansion -- newlines and tabs
	// are structure in a multiline surface, and a whole-text strip ate them
	// (caught by the tail-count test, not by me).
	for _, src := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		lines = append(lines, wrapLine(packages.EscapeTerminal(expandTabs(src)), w)...)
	}
	const proseview = 12
	shown := lines
	if len(shown) > proseview {
		shown = shown[:proseview]
	}
	var b strings.Builder
	for _, l := range shown {
		b.WriteString("  " + dimStyle.Render("│ ") + l + "\n")
	}
	if extra := len(lines) - len(shown); extra > 0 {
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("│ … +%d more lines (%s)", extra, more)) + "\n")
	}
	return b.String()
}

// expandTabs replaces tabs with spaces to the next 8-column stop, measured
// in display cells — ansi.StringWidth counts a tab as ZERO cells while a
// real terminal expands it, so tab-indented prose (a code sample in the
// instructions, which validation deliberately permits) measured as fitting
// while visually blowing the width, past both the wrap and the clip (grok
// pre-ship probe). Display-only: the stored prose keeps its tabs.
func expandTabs(src string) string {
	if !strings.ContainsRune(src, '\t') {
		return src
	}
	var b strings.Builder
	col := 0
	for _, r := range src {
		if r == '\t' {
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += ansi.StringWidth(string(r))
	}
	return b.String()
}

// wrapLine wraps one source line to w display cells, FAITHFULLY: a line
// that fits is returned verbatim (indentation, aligned spacing, and all —
// showing the stored prose means showing it, not a re-flowed paraphrase);
// a longer line breaks at the last space inside the window, consuming only
// that one break space, with the line's own leading indent carried onto
// continuations. A space-less stretch wider than the window is hard-cut at
// the cell boundary (grapheme-aware via ansi.Truncate), so nothing ever
// reaches the view's ellipsis clip.
func wrapLine(src string, w int) []string {
	if ansi.StringWidth(src) <= w {
		return []string{src}
	}
	indent := src[:len(src)-len(strings.TrimLeft(src, " \t"))]
	if ansi.StringWidth(indent) >= w {
		indent = "" // pathological indent wider than the window
	}
	chunkw := w - ansi.StringWidth(indent)
	rest := src[len(indent):]
	var out []string
	for rest != "" {
		if ansi.StringWidth(rest) <= chunkw {
			out = append(out, indent+rest)
			break
		}
		head := ansi.Truncate(rest, chunkw, "")
		if head == "" {
			// No complete grapheme fits the window (a two-cell glyph in a
			// one-cell window): emit the next grapheme anyway — progress
			// beats the nominal width, and the view clip absorbs the
			// one-glyph overflow (the unchanged rest looped forever and
			// froze the TUI).
			for take := 1; head == ""; take++ {
				head = ansi.Truncate(rest, take, "")
			}
			out = append(out, indent+head)
			rest = rest[len(head):]
			continue
		}
		if i := strings.LastIndexByte(head, ' '); i > 0 {
			head = head[:i]
			rest = rest[i+1:] // consume the one break space, keep any others
		} else {
			rest = rest[len(head):]
		}
		out = append(out, indent+head)
	}
	return out
}

// viewMenu renders the per-row action menu: the row, where it's set, and the
// actions it supports -- terse labels, accelerator keys beside them.
func (m model) viewMenu() string {
	var b strings.Builder
	// The menu re-paints the selected row, so it is the list funnel's closest
	// sibling: the same value neutralized there must not come back to life on
	// Enter (review catch -- the strip stopped one screen short).
	fmt.Fprintf(&b, "%s\n", focusStyle.Render(packages.EscapeTerminal(m.menuRow.text)))
	b.WriteString(dimStyle.Render("Set in: "+packages.EscapeTerminal(setIn(m.menuRow))) + "\n\n")
	// An INHERITED instructions row is readable right here, in full — the
	// user can't edit another layer's snippet from this file, but seeing
	// what their agent will be told never requires leaving the screen.
	if m.listField == fContext && m.menuRow.kind == rowInherited && len(m.menuRow.vals) > 2 && strings.TrimSpace(m.menuRow.vals[2]) != "" {
		b.WriteString(proseBlock(m.menuRow.vals[2], "full text in "+m.menuRow.source+"'s own editor", m.width) + "\n")
	}
	choices := m.rowChoices(m.listField, m.menuRow)
	for i, c := range choices {
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.menuCur, c.label+dimStyle.Render("  "+c.key)))
	}
	if m.listField == fEnv && m.menuRow.kind == rowInherited {
		b.WriteString("\n" + dimStyle.Render("(can't unset from this layer — edit the "+packages.EscapeTerminal(m.menuRow.source)+" config to remove)"))
	}
	if note := m.subFooterNote(); note != "" {
		b.WriteString("\n" + note)
	}
	b.WriteString("\n" + helpLine("↑/↓", "move", "enter", "apply", "^s", "save", "esc", "back"))
	return b.String()
}

// setIn names where the row under the menu is set, in cascade vocabulary.
func setIn(r listRow) string {
	switch r.kind {
	case rowOverride:
		return "this file, overriding " + r.source
	case rowInherited, rowSkill:
		return r.source
	case rowHostEnv:
		if r.idx >= 0 {
			return "this file"
		}
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
	// One title for both kinds on the Env screen: a passthrough IS an
	// environment variable, and the Source picker directly below says where
	// its value comes from. A kind-dependent title would restate the picker
	// and go stale the moment the picker moved.
	fmt.Fprintf(&b, "%s\n\n", m.crumb(verb+" "+itemTitle(m.listField)))

	// Label column sized to the widest label this form shows, so optional/
	// required annotations don't push the colons out of line.
	pad := 16
	if m.itemHasMode && len(m.itemModeLabel) > pad {
		pad = len(m.itemModeLabel)
	}
	if m.itemHasMode2 && len(m.itemMode2Label) > pad {
		pad = len(m.itemMode2Label)
	}
	for i := range m.inputs {
		if l := len([]rune(m.itemLabel(i))); l > pad {
			pad = l
		}
	}
	seg := func(label string, opts []string, sel int, focused bool) {
		cursor := "  "
		if focused {
			cursor = cursorStyle.Render("▸ ")
		}
		fmt.Fprintf(&b, "%s%s: %s\n", cursor, fmt.Sprintf("%-*s", pad, label), renderSeg(opts, sel, focused))
	}
	picker := func() {
		seg(m.itemModeLabel, m.itemModeOpts, m.itemMode, m.onModePicker())
	}
	if m.itemHasMode && m.itemModeFirst {
		picker()
	}
	for i, in := range m.inputs {
		cursor := "  "
		val := in.View()
		if i == m.itemInputIndex() {
			cursor = cursorStyle.Render("▸ ")
			val += dimStyle.Render(m.ghostSuffix()) // autocomplete/suggestion ghost
		}
		fmt.Fprintf(&b, "%s%-*s: %s\n", cursor, pad, m.itemLabel(i), val)
	}
	if m.itemHasMode && !m.itemModeFirst {
		picker()
	}
	if m.itemHasMode2 {
		seg(m.itemMode2Label, m.itemMode2Opts, m.itemMode2, m.onMode2Picker())
	}
	for _, note := range m.itemNotes() {
		b.WriteString(dimStyle.Render("  "+note) + "\n")
	}
	// The stored prose, read-only: the editing path is ^e, but READING the
	// standing instructions must not require launching a second program
	// (PRINCIPLES.md §4; the $EDITOR ruling was about not building a worse
	// text editor, never about hiding the text).
	if m.listField == fContext && m.itemMode == 0 && strings.TrimSpace(m.itemProse) != "" {
		b.WriteString("\n" + proseBlock(m.itemProse, "^e to view and edit", m.width))
	}

	if m.itemErr != "" {
		b.WriteString("\n" + m.errLine(m.itemErr))
	}
	hint := helpLine("tab", "next", "enter", "accept", "^s", "save", "esc", "cancel")
	switch {
	// "accept" would understate it on a credential: enter is the write.
	case m.listField == fEnv && isCredentialScheme(m.itemMode):
		hint = helpLine("tab", "next", "←/→", "source", "enter", "encrypt + write", "^s", "save", "esc", "cancel")
	case m.listField == fMounts:
		hint = helpLine("tab", "next", "→", "accept suggestion", "←/→", "mode", "enter", "accept", "^s", "save", "esc", "cancel")
	case m.itemHasMode2:
		// Two pickers, so the hint names the action rather than one of them.
		hint = helpLine("tab", "next", "←/→", "choose", "enter", "accept", "^s", "save", "esc", "cancel")
	case m.itemHasMode:
		hint = helpLine("tab", "next", "←/→", strings.ToLower(m.itemModeLabel), "enter", "accept", "^s", "save", "esc", "cancel")
	}
	b.WriteString("\n\n" + hint)
	return b.String()
}

// itemLabel is the display label for input i — MCP's endpoint label follows
// the Kind picker live, so requiredness and meaning are never ambiguous.
func (m model) itemLabel(i int) string {
	if m.listField == fMCP {
		switch i {
		case 0:
			return "Name (required)"
		case 1:
			if m.itemMode == 1 {
				return "URL (required)"
			}
			return "Command (required)"
		}
	}
	return m.inputLabels[i]
}

// nameNotes is the name-input guidance shared by the named-declaration item
// editors: the grammar line, plus a LIVE warning the moment the current
// value can't become valid — flagged while typing, not first at commit
// (maintainer review, 2026-07-25). The check runs on the lowercased trim,
// because that transform is what save applies; a name that only needs
// lowercasing draws no warning.
func nameNotes(raw string, valid func(string) bool) []string {
	notes := []string{"name: lowercase a-z 0-9 - (auto-lowercased on save)"}
	name := strings.ToLower(strings.TrimSpace(raw))
	if name != "" && !valid(name) {
		notes = append(notes, "⚠ this name won't save — lowercase a-z 0-9 - only, starting with a letter or digit, max 64")
	}
	return notes
}

// itemNotes are the dim guidance lines under the editor — the form explains
// itself instead of failing at commit (Pete's review of the first form).
func (m model) itemNotes() []string {
	if m.listField == fEnv {
		return m.envItemNotes()
	}
	if m.listField == fClaudeSkills {
		notes := nameNotes(m.inputs[0].Value(), config.ValidClaudeSkillName)
		if n := claudeSkillDirNote(m.inputs[0].Value(), m.inputs[1].Value()); n != "" {
			notes = append(notes, "⚠ "+n+" (accepted anyway — the dir can be created later)")
		}
		return notes
	}
	if m.listField == fVolumes {
		// The two declared properties this form does not author are named
		// here, at the moment of editing: silence would read as "this entry
		// has neither", and both change what saving the row does.
		notes := []string{"state = precious (auth, history, scratch); cache = disposable. New volumes are project-scoped."}
		// The sharing note follows the picker: worktree boxes of one project
		// run concurrently and mount the same volumes, so what "exclusive"
		// costs has to be readable at the moment of choosing it.
		if m.itemMode2 == 1 {
			notes = append(notes, "exclusive: byre develop REFUSES to start a second box of this project while another holds this volume — for data that cannot take two writers.")
		} else {
			notes = append(notes, "shared: every box of this project — worktrees included — may hold this volume at once.")
		}
		if v := m.volumeBase(); v != nil {
			if v.MachineScoped() {
				notes = append(notes, "⚠ scope: machine — ONE volume shared by ALL your projects (kept as declared; ^e to change)")
			}
			if v.Seed != nil {
				notes = append(notes, "seed: filled once when the volume is first created (kept as declared; ^e to change)")
			}
		}
		return notes
	}
	if m.listField == fFiles {
		// Same affordance the Claude Skills editor has: a source that is not
		// on disk is accepted (it can be created before the next develop) but
		// never silently -- deferring the failure to the build hands the user
		// a raw lstat error long after the editor could have said so. "" for
		// the global/layer editors: no project, so no tree to ask.
		if n := filesSourceNote(m.inh.ProjectDir, m.inputs[0].Value()); n != "" {
			return []string{"⚠ " + n + " (accepted anyway — the file can be created later)"}
		}
		return nil
	}
	if m.listField == fContext {
		notes := nameNotes(m.inputs[0].Value(), config.ValidContextName)
		if m.itemMode == 0 {
			if strings.TrimSpace(m.itemProse) == "" {
				notes = append(notes, "text: empty — ^e opens $EDITOR to write it")
			} else {
				// The text itself renders below (viewItem's prose block).
				notes = append(notes, "text — ^e edits in $EDITOR")
			}
		} else {
			notes = append(notes, "file: read at bake; machine-local (won't ride a preset — inline text does)")
		}
		return notes
	}
	if m.listField != fMCP {
		return nil
	}
	notes := nameNotes(m.inputs[0].Value(), config.ValidMCPName)
	if m.itemMode == 1 {
		probe := config.MCP{Name: "x", URL: strings.TrimSpace(m.inputs[1].Value())}
		if host, port, ok := probe.Endpoint(); ok {
			notes = append(notes, fmt.Sprintf("url host implies egress to %s:%d — opened automatically under a firewall;", host, port))
			notes = append(notes, "extra egress is only for side-hosts (e.g. an OAuth endpoint)")
		} else {
			notes = append(notes, "remote server: byre opens the url's host automatically under a firewall")
		}
		notes = append(notes, `headers: quoted "Name: value" each — tokens by name: "Authorization: Bearer ${TOKEN}"`)
	} else {
		notes = append(notes, `command is an argv — "quote args with spaces"; ship the binary via a skill/apt`,
			"local servers reach nothing a firewall doesn't allow: declare their hosts in extra egress")
	}
	return notes
}

// commitEnvRow writes an Env-screen entry back into the working state. The
// picker decides WHICH map it lands in, so switching a row from `value` to a
// scheme (or back) moves it between [env] and env_from_host rather than
// leaving a stale twin in the one it came from.
//
// Removing an entry entirely is Delete on the row, not an option here: that
// is what Delete means on every list field, and a second spelling of it would
// be a concept this screen does not need.
func (m model) commitEnvRow(orig model) model {
	key := strings.TrimSpace(m.inputs[0].Value())
	if key == "" {
		orig.itemErr = "key is required"
		return orig
	}
	now := isPassthrough(m.itemMode)
	// moving: the picker changed which MAP this row belongs in, so the entry
	// leaves one and joins the other.
	moving := m.editIndex >= 0 && m.itemHostEnv != now

	// Every refusal returns ORIG, and the duplicate check runs BEFORE the
	// entry is removed from the map it is leaving. Checking after the removal
	// meant a conversion onto an occupied key reported "duplicate" while
	// having already deleted the row it was converting -- escape, and the
	// deletion was still in the working state to be saved.
	dest := m.env
	if now {
		dest = m.hostEnv
	}
	skip := -1
	if m.editIndex >= 0 && !moving {
		skip = m.editIndex
	}
	for i, kv := range dest {
		if i != skip && kv.Key == key {
			orig.itemErr = "duplicate key " + key
			return orig
		}
	}

	// A credential value is not a source this form can encode: it is encrypted
	// and written by the credential path, on accept. That path does its own
	// buffer surgery (after the write lands), so it branches before the moves
	// below.
	if isCredentialScheme(m.itemMode) {
		return m.commitCredentialRow(orig, key, moving)
	}
	// Leaving a credential kind is an UNSET of that row, and the ciphertext is
	// the only copy of the value: say so as it happens, since the row is about
	// to become an ordinary source with nothing to undo it.
	leavingCredential := m.itemHostEnv && m.editIndex >= 0 && config.IsCredentialSource(m.hostEnv[m.editIndex].Value)

	if moving {
		if m.itemHostEnv {
			m.hostEnv = append(append([]kvItem{}, m.hostEnv[:m.editIndex]...), m.hostEnv[m.editIndex+1:]...)
		} else {
			m.env = append(append([]kvItem{}, m.env[:m.editIndex]...), m.env[m.editIndex+1:]...)
		}
		m.editIndex = -1
	}
	if now {
		m.hostEnv = putAt(m.hostEnv, m.editIndex, kvItem{Key: key, Value: hostEnvSource(m.itemMode, m.inputs[1].Value())})
	} else {
		m.env = putAt(m.env, m.editIndex, kvItem{Key: key, Value: m.inputs[1].Value()})
	}
	// The same validator Save runs: key grammar, the closed scheme set, and
	// the BYRE_ namespace rule all belong to config, not here.
	if err := m.assemble().ValidateLayer(); err != nil {
		orig.itemErr = err.Error()
		return orig
	}
	m.itemErr = ""
	m.mode = modeList
	if leavingCredential {
		m.status = key + " — " + credentialUnsetNote + "; ^s writes it"
	}
	return m
}

// syncHostEnvLabel keeps the argument input's label matching the selected
// scheme. The picker decides what the second field MEANS -- a git config key,
// a host variable name, or nothing at all -- so a fixed label would be wrong
// for three of the five options.
func (m model) syncHostEnvLabel() model {
	if m.listField == fEnv && len(m.inputLabels) == 2 && len(m.inputs) == 2 {
		m.inputLabels = []string{m.inputLabels[0], hostEnvArgLabel(m.itemMode)}
		m.inputs[1].Placeholder = hostEnvArgHint(m.itemMode)
		m.maskCredentialInput()
	}
	return m
}

// maskCredentialInput keeps the value box's echo matching the selected scheme,
// and CLEARS the box whenever the selection crosses the credential boundary: a
// literal typed in the open must not become an encrypted value through a
// picker move, and a value typed hidden must not appear in the box the moment
// the picker leaves the credential kinds. The echo mode is itself the record
// of which side the box was on.
func (m *model) maskCredentialInput() {
	if m.listField != fEnv || len(m.inputs) != 2 {
		return
	}
	want := isCredentialScheme(m.itemMode)
	if was := m.inputs[1].EchoMode == textinput.EchoPassword; want == was {
		return
	}
	m.inputs[1].SetValue("")
	if want {
		m.inputs[1].EchoMode = textinput.EchoPassword
		m.inputs[1].EchoCharacter = '•'
		return
	}
	m.inputs[1].EchoMode = textinput.EchoNormal
}

// readOnlyFieldNote answers a keypress a read-only screen cannot honor, with
// the route that does work rather than a bare refusal. Per field, because the
// route differs: a skill's payload is changed by forking the skill, while
// [sources] is written by the one flow that takes a human's consent for it.
func readOnlyFieldNote(f fieldID) string {
	if f == fSources {
		return "read-only — [sources] hints are recorded by `byre preset apply` when you accept a preset's packages; ^e edits the file by hand"
	}
	return "read-only — a skill's payload is the skill's; fork the skill (byre skill fork <id> <new-id>) to change what it bakes in"
}
