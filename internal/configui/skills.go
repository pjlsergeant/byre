// skills.go owns the skills multi-select screen (modeSkills) and the option
// helpers behind the template/agent/engine pickers.
package configui

import (
	"fmt"
	"github.com/pjlsergeant/byre/internal/config"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// skillEntry is one row of the skills multi-select.
type skillEntry struct {
	name   string
	agent  bool // an agent skill (grouped separately)
	locked bool // the primary agent — shown ticked, can't be toggled here
}

// skillEntries builds the multi-select rows: non-agent skills first, then agent
// skills. The set is the discovered skills plus any enabled or primary-agent
// skill not already in it (so nothing an existing config references disappears).
// The primary agent (from the Pri. Agent picker) is marked locked+on.
func (m model) skillEntries() []skillEntry {
	agentSet := map[string]bool{}
	for _, a := range m.agents {
		agentSet[a] = true
	}
	primary := fromNone(m.agentOpts[m.agentSel])

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
	for _, n := range m.skills {
		add(n)
	}
	add(primary)

	var nonAgent, agent []skillEntry
	for _, n := range names {
		if agentSet[n] || n == primary {
			agent = append(agent, skillEntry{name: n, agent: true, locked: n == primary})
		} else {
			nonAgent = append(nonAgent, skillEntry{name: n})
		}
	}
	return append(nonAgent, agent...)
}

func (m model) updateSkills(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := m.skillEntries()
	if cur, ok := cursorMove(msg.String(), m.skillCur, len(entries)); ok {
		m.skillCur = cur
		m.status = ""
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeForm
		return m, nil
	case " ", "x", "enter":
		if m.skillCur < len(entries) {
			e := entries[m.skillCur]
			if e.locked {
				m.status = "that's the primary agent — change it in Pri. Agent"
			} else {
				m.skills = toggle(m.skills, e.name)
			}
		}
		return m, nil
	}
	return m, nil
}

// toggle adds name to s if absent, else removes it (preserving order).
func toggle(s []string, name string) []string {
	for i, v := range s {
		if v == name {
			return append(s[:i], s[i+1:]...)
		}
	}
	return append(s, name)
}

func (m model) viewSkills() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render("Skills"))
	entries := m.skillEntries()
	if len(entries) == 0 {
		b.WriteString(dimStyle.Render("  (no skills discovered)\n"))
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
		if e.locked || contains(m.skills, e.name) {
			box = "[x]"
		}
		line := box + " " + e.name
		if e.locked {
			line += dimStyle.Render("  (primary agent)")
		}
		if d := m.skillDescs[e.name]; d != "" {
			line += dimStyle.Render("  — " + d)
		}
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.skillCur, line))
	}

	if m.status != "" {
		b.WriteString("\n" + dimStyle.Render(m.status))
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ move · space toggle · esc back"))
	return b.String()
}

// ---- option/value helpers --------------------------------------------------

const noneOption = config.NoneLabel

// pickerOpts builds the option list for a template/agent picker: the discovered
// items, then a configured-but-not-discovered value (preserved so it round-trips
// instead of being silently dropped), then the "none" sentinel.
func pickerOpts(discovered []string, current string) []string {
	opts := append([]string{}, discovered...)
	if current != "" && !contains(opts, current) {
		opts = append(opts, current)
	}
	return append(opts, noneOption)
}

// orNone/fromNone delegate to the shared sentinel vocabulary in config.
func orNone(v string) string   { return config.OrNone(v) }
func fromNone(v string) string { return config.FromNone(v) }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// fromAuto maps the "auto" engine selection back to "" so it's omitted from the
// written config (auto is the default).
func fromAuto(v string) string {
	if v == "auto" {
		return ""
	}
	return v
}
