// skills.go owns the skills multi-select screen (modeSkills) and the option
// helpers behind the template/agent/engine pickers.
package configui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// skillEntry is one row of the skills multi-select.
type skillEntry struct {
	name        string
	agent       bool // rendered in the agent-skills section (agent skills + their nested companions)
	child       bool // a shared-auth companion nested under its agent's row (indented)
	locked      bool // the primary agent — shown ticked, can't be toggled here
	enabledHere bool // this layer's own `skills` list names it
	inherited   bool // a LOWER cascade layer (default/template) enables it
	removedHere bool // this layer carries a `!name` removal marker for it
	// Provenance label (dimmed) and optional disabled reason (INVALID/
	// conflict/protected-name). disabled rows are not toggleable.
	provLabel string
	disabled  string // non-empty => disabled-with-reason
}

// on reports the entry's EFFECTIVE state — what the resolved cascade enables —
// which is what the checkbox must show (an unchecked box for an inherited-on
// skill is a lie; found live 2026-07-07). A removal marker trumps a same-layer
// enable: Merge applies removals after additions, so ["x", "!x"] resolves OFF.
func (e skillEntry) on() bool {
	return e.locked || (!e.removedHere && (e.enabledHere || e.inherited))
}

// splitSkillLayer parses one layer's skills list into plain enables and
// `!name` removal markers (the cascade's off-switch for inherited entries).
func splitSkillLayer(entries []string) (enabled []string, removed map[string]bool) {
	removed = map[string]bool{}
	for _, s := range entries {
		if n, ok := config.CutRemoval(s); ok {
			removed[n] = true
		} else {
			enabled = append(enabled, s)
		}
	}
	return enabled, removed
}

// inheritedNow is the lower-layer skill set under the CURRENTLY selected
// template — the template picker is a live form field, so the inherited set
// follows it.
func (m model) inheritedNow() []string {
	return m.lowerNow().Skills
}

// skillEntries builds the multi-select rows: non-agent skills first, then agent
// skills. The set is the discovered skills, plus anything this layer enables or
// removes, plus anything a lower layer enables, plus the primary agent (so
// nothing an existing config references disappears). The primary agent (from
// the Agent picker) is marked locked+on.
func (m model) skillEntries() []skillEntry {
	agentSet := map[string]bool{}
	for _, a := range m.agents {
		agentSet[a] = true
	}
	primary := m.agentNow()
	// In the --global editor the agent picker is an onboarding FAVOURITE —
	// it enables nothing anywhere — so there is no primary agent to lock on:
	// a "[x] (primary agent)" row would claim a machine-wide enable that
	// isn't happening, and the lock would silently prevent enabling that
	// agent's skill machine-wide via this screen.
	if m.target == TargetGlobal {
		primary = ""
	}
	enabledHere, removedHere := splitSkillLayer(m.skills)
	inherited := map[string]bool{}
	for _, n := range m.inheritedNow() {
		inherited[n] = true
	}

	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, n := range m.skillOpts {
		add(n)
	}
	for _, n := range enabledHere {
		add(n)
	}
	for _, n := range m.inheritedNow() {
		add(n)
	}
	for n := range removedHere {
		add(n) // a stale removal marker stays visible + editable
	}
	add(primary)

	enabledSet := map[string]bool{}
	for _, n := range enabledHere {
		enabledSet[n] = true
	}

	var nonAgent, agent []skillEntry
	for _, n := range names {
		e := skillEntry{
			name:        n,
			locked:      n == primary,
			enabledHere: enabledSet[n],
			inherited:   inherited[n],
			removedHere: removedHere[n],
		}
		if rt, ok := m.inh.Skills[n]; ok {
			e.provLabel = rt.ProvLabel
			e.disabled = rt.DisabledReason
		}
		// A name the config already carries is in `seen`, so the problem-row
		// append below skips it -- the reason has to be MERGED into the row
		// the config built, which also keeps that row's enabled/inherited/
		// locked state and its agent-section placement (a fresh appended row
		// would lose all four). Same catalog lookup the pickers read, so an
		// enabled skill that broke cannot read normal here while `skill list`
		// and the agent picker both name the reason.
		if e.disabled == "" {
			if d := m.optDisabled(n); d != "" {
				e.disabled = d
				if p := m.optProv(n); p != "" {
					e.provLabel = p
				}
			}
		}
		if agentSet[n] || n == primary {
			e.agent = true
			agent = append(agent, e)
		} else {
			nonAgent = append(nonAgent, e)
		}
	}
	// Problem rows from the catalog (INVALID/conflict/LEGACY) appear
	// disabled-with-reason rather than vanishing.
	if m.inh.Catalog != nil {
		for _, ent := range m.inh.Catalog.ListProblemRows(packages.KindSkill) {
			name := ent.DisplayName()
			if name == "" {
				name = ent.ID
			}
			if seen[name] || seen[ent.ID] {
				continue
			}
			seen[name] = true
			e := skillEntry{
				name:      name,
				provLabel: ent.ProvenanceLabel(),
				disabled:  ent.Reason,
			}
			if ent.Reason == "" {
				e.disabled = string(ent.Provenance)
			}
			nonAgent = append(nonAgent, e)
		}
	}
	nonAgent, agent = m.nestCompanions(nonAgent, agent)
	return append(nonAgent, agent...)
}

// nestCompanions moves each companion skill (one paired to an agent via
// companion_for, or via the pairing shared_auth_for implies — ADR 0034) out
// of the flat skills list and inserts it as an indented child directly under
// its agent's row, so the pairing is visible at the point of enablement.
// Nesting rides the pairing FACT alone — a gate-pending companion nests the
// same as a vouched one; readiness gates the onboarding offer, never the
// display. Pairing is by canonical ID (alias expansion when a catalog is
// present). A companion whose agent has no row stays a plain skill. Only
// non-agent rows nest: a skill that is itself agent-capable keeps its
// top-level agent row (nesting one agent — possibly the locked primary —
// under another would misstate what it is), a shape no real companion has
// anyway.
func (m model) nestCompanions(nonAgent, agent []skillEntry) ([]skillEntry, []skillEntry) {
	canon := func(n string) string {
		if m.inh.Catalog != nil {
			return m.inh.Catalog.ExpandAlias(n)
		}
		return n
	}
	agentIdx := map[string]int{}
	for i, e := range agent {
		agentIdx[canon(e.name)] = i
	}
	children := map[int][]skillEntry{}
	var rest []skillEntry
	for _, e := range nonAgent {
		if claim := m.inh.Skills[e.name].CompanionFor; claim != "" {
			if i, ok := agentIdx[canon(claim)]; ok {
				e.agent = true // renders inside the agent section, under its agent
				e.child = true
				children[i] = append(children[i], e)
				continue
			}
		}
		rest = append(rest, e)
	}
	if len(children) == 0 {
		return nonAgent, agent
	}
	nested := make([]skillEntry, 0, len(agent))
	for i, e := range agent {
		nested = append(nested, e)
		nested = append(nested, children[i]...)
	}
	return rest, nested
}

func (m model) updateSkills(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := m.skillEntries()
	if cur, ok := cursorMove(msg.String(), m.skillCur, len(entries)); ok {
		m.skillCur = cur
		m.status = ""
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeForm
		return m, nil
	case "ctrl+s":
		return m.save(), nil
	case " ", "x", "enter":
		if m.skillCur < len(entries) {
			e := entries[m.skillCur]
			m.status = "" // every press restates or clears the guidance; never stale
			if e.disabled != "" {
				m.status = e.disabled
				return m, nil
			}
			// Toggling peels one layer of state at a time, so every press has
			// exactly one legible effect on THIS layer's list:
			//   enabled here            -> drop the local entry
			//   removed here            -> drop the `!name` marker (re-inherit)
			//   inherited (effectively on) -> add `!name` (the cascade's off-switch)
			//   otherwise               -> enable here
			switch {
			// removedHere outranks locked: a stale `!primary` marker must stay
			// visible and removable, or it silently suppresses the inherited
			// skill the moment the primary agent changes.
			case e.removedHere:
				m.skills = removeString(m.skills, "!"+e.name)
			case e.locked:
				m.status = "that's the primary agent — change it in Agent"
			case e.enabledHere:
				m.skills = removeString(m.skills, e.name)
				if e.inherited {
					m.status = fmt.Sprintf("%s still inherited — toggle again to remove it here", e.name)
				}
			case e.inherited:
				m.skills = append(m.skills, "!"+e.name)
			default:
				m.skills = append(m.skills, e.name)
			}
		}
		return m, nil
	}
	return m, nil
}

// removeString returns s without v. Non-mutating (the caller's backing array is
// left alone) and removes every match -- the same contract the config and
// onboard copies have. It used to splice in place and stop at the first match,
// which made one name mean two things depending on the package.
func removeString(s []string, v string) []string {
	out := s[:0:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func (m model) viewSkills() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", m.crumb("Skills"))
	entries := m.skillEntries()
	if len(entries) == 0 {
		// \n outside the Render — see viewList's empty line for why.
		b.WriteString(dimStyle.Render("  (no skills discovered)") + "\n")
	}
	prevAgent := false
	for i, e := range entries {
		if i == 0 || e.agent != prevAgent {
			header := "Skills"
			if e.agent {
				header = "Agent skills"
			}
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(dimStyle.Render(header) + "\n")
		}
		prevAgent = e.agent

		box := "[ ]"
		if e.disabled != "" {
			box = "[-]"
		} else if e.on() {
			box = "[x]"
		}
		// Name, provenance label and disabled reason are catalog/manifest
		// text -- the same trust class as the description below.
		line := box + " " + packages.EscapeTerminal(e.name)
		if e.child {
			line = "  └ " + line
		}
		if e.provLabel != "" {
			line += dimStyle.Render("  " + packages.EscapeTerminal(e.provLabel))
		}
		switch {
		case e.disabled != "":
			line += dimStyle.Render("  (" + packages.EscapeTerminal(e.disabled) + ")")
		case e.locked && e.removedHere:
			line += dimStyle.Render("  (primary agent — stale !" + e.name + " marker, toggle to clear)")
		case e.locked:
			line += dimStyle.Render("  (primary agent)")
		case e.inherited && e.removedHere:
			line += dimStyle.Render("  (inherited — removed here)")
		case e.inherited && !e.enabledHere:
			line += dimStyle.Render("  (inherited)")
		case e.removedHere:
			line += dimStyle.Render("  (removes an inherited skill)")
		}
		if d := m.skillDescs[e.name]; d != "" {
			// A description is manifest text -- local and installed skills
			// are untrusted input on this box -- so it is data here too.
			line += dimStyle.Render("  — " + packages.EscapeTerminal(d))
		}
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.skillCur, line))
		// Containment hole: same skill-owned one-liner as status and preset
		// apply's grant review, so
		// enabling the skill in the GRANTS-adjacent skills view never hides
		// the warranty disclaimer.
		if e.on() && e.disabled == "" {
			// A vouched shared-auth companion enabled in THIS file is the one
			// suppression ADR 0025 allows: onboarding stops offering it,
			// because the cascade already grants it and an "n" would do
			// nothing. That is a defensible rule and an invisible one -- the
			// effect lands on projects that do not exist yet, so the place to
			// say it is where the switch is thrown. Global editor only: in a
			// project or layer file this skill grants the box it is in, and
			// suppresses no question.
			if m.target == TargetGlobal && m.inh.Skills[e.name].SharedAuthFor != "" {
				fmt.Fprintf(&b, "%s\n", dimStyle.Render("      → every new project gets this, and is no longer asked about shared credentials"))
			}
			if c := m.inh.Skills[e.name].Containment; c != "" {
				fmt.Fprintf(&b, "%s\n", dimStyle.Render("      🛑 "+packages.EscapeTerminal(c)))
			}
		}
	}

	if note := m.subFooterNote(); note != "" {
		b.WriteString("\n" + note)
	}
	// The inherited-toggle nuance rides its own line: inside the key help it
	// pushed "esc back" past 80 columns and got clipped.
	b.WriteString("\n" + dimStyle.Render("(toggling an inherited skill adds/removes a !name override in this file)"))
	b.WriteString("\n" + helpLine("↑/↓", "move", "space", "toggle", "^s", "save", "esc", "back"))
	return b.String()
}

const noneOption = config.NoneLabel

// pickerOpts builds the option list for a template/agent picker: the discovered
// items, then a configured-but-not-discovered value (preserved so it round-trips
// instead of being silently dropped), then the "none" sentinel. A configured
// "none" IS the sentinel (wizard-onboarded agentless configs store it
// literally), not a preserved value — appending it here would render the
// sentinel twice.
func pickerOpts(discovered []string, current string) []string {
	opts := append([]string{}, discovered...)
	if current != "" && current != noneOption && !contains(opts, current) {
		opts = append(opts, current)
	}
	return append(opts, noneOption)
}

// Scalar pickers distinguish two states an absent key cannot tell apart on
// its own: INHERIT (say nothing, take what the cascade below provides) and
// the explicit off-switch (`agent = "none"`, `engine = "auto"`), which is a
// value that BEATS the lower layer in the merge. Rendering both through one
// row is what let a zero-edit save delete an off-switch and silently hand an
// agentless project the layer's agent.
//
// The inherit row appears ONLY when a lower layer actually provides a value.
// With nothing below, absence and the sentinel are genuinely the same thing,
// so the picker doesn't grow a choice that means nothing -- and the file byre
// writes is unchanged from before (the sentinel row keeps mapping to absent).
const inheritPrefix = "(inherit: "

func inheritRow(value string) string {
	return inheritPrefix + value + ")"
}

func isInheritRow(s string) bool { return strings.HasPrefix(s, inheritPrefix) }

func hasInheritRow(opts []string) bool {
	return indexOfInherit(opts) >= 0
}

func indexOfInherit(opts []string) int {
	for i, o := range opts {
		if isInheritRow(o) {
			return i
		}
	}
	return -1
}

// scalarOpts is pickerOpts plus the inherit row, for the fields that carry an
// off-switch sentinel. lowerValue is what the cascade below provides ("" =
// nothing below, so no inherit row).
// sentinelFirst keeps each picker's historical sentinel position (engine leads
// with "auto"; template/agent end with "none"). For the latter, inherit comes
// last: it is the exceptional meta-choice, not one of the concrete values.
func scalarOpts(discovered []string, current, lowerValue, sentinel string, sentinelFirst bool) []string {
	var opts []string
	if sentinelFirst {
		opts = append(opts, sentinel)
		if lowerValue != "" {
			opts = append(opts, inheritRow(lowerValue))
		}
	}
	for _, d := range discovered {
		if d != sentinel {
			opts = append(opts, d)
		}
	}
	if current != "" && current != sentinel && !contains(opts, current) {
		opts = append(opts, current)
	}
	if !sentinelFirst {
		opts = append(opts, sentinel)
		if lowerValue != "" {
			opts = append(opts, inheritRow(lowerValue))
		}
	}
	return opts
}

// scalarSel picks the row for a stored value: the sentinel selects the
// sentinel row, absent selects inherit where one exists, anything else
// selects itself.
func scalarSel(opts []string, current, sentinel string) int {
	if current == "" {
		if i := indexOfInherit(opts); i >= 0 {
			return i
		}
		return indexOf(opts, sentinel)
	}
	return indexOf(opts, current)
}

// fromScalar maps a selected row back to what gets WRITTEN. Inherit writes
// nothing. The sentinel writes ITSELF when there is an inherit row to be
// told apart from, or when it is a deliberate answer (storedSentinel: the
// file said it, or the user's last move on the picker landed on it) --
// onboarding records both axes explicitly for exactly this reason ("none"
// must win over a layer's choice if one is added later), so dropping it on
// save would destroy a deliberate answer. Otherwise the sentinel writes
// absent: with nothing below and nothing written, the two mean the same and
// an ordinary save must not churn the file.
func fromScalar(opts []string, sel int, sentinel string, storedSentinel bool) string {
	if sel < 0 || sel >= len(opts) {
		return ""
	}
	v := opts[sel]
	switch {
	case isInheritRow(v):
		return ""
	case v == sentinel:
		if hasInheritRow(opts) || storedSentinel {
			return sentinel
		}
		return ""
	}
	return v
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// effectiveScalar is what a picker's SELECTION means for resolution: the
// inherit row stands for the value it names (that is what the box gets), the
// sentinel means off, anything else is itself. Distinct from fromScalar,
// which is what gets WRITTEN -- inherit writes nothing precisely so the
// value it stands for keeps coming from below.
func effectiveScalar(opts []string, sel int, inherit, sentinel string) string {
	if sel < 0 || sel >= len(opts) {
		return ""
	}
	v := opts[sel]
	switch {
	case isInheritRow(v):
		return inherit
	case v == sentinel:
		return ""
	}
	return v
}
