// volumes.go owns the volumes screen (modeVolumes): listing a project's
// volumes and clearing them ad hoc through the VolumeAdmin.
package configui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// VolumeAdmin lets the editor show a project's volumes and clear them ad hoc.
// It's engine-backed, so the commands layer supplies it; a nil VolumeAdmin hides
// the Volumes section (e.g. the global config, which has no project volumes).
type VolumeAdmin interface {
	List() ([]VolumeStatus, error)
	// Clear removes the volume from the engine (refuses if a session is
	// live). It takes the full row, not just the name: scope decides which
	// Docker volume the logical name maps to, and an orphaned machine volume
	// may share a logical name with a declared project one.
	Clear(v VolumeStatus) error
	// SharedNote returns a blast-radius warning to show before a clear (e.g. a
	// worktree whose volumes are shared across the project's worktrees), or "" if none.
	SharedNote() string
}

// VolumeStatus is one project volume for display — one row PER ENGINE when
// more than one is installed: a volume can exist on docker and podman at
// once, each copy its own row, or "cleared" would be a per-engine truth
// wearing machine-wide words.
type VolumeStatus struct {
	Name    string // logical name (e.g. ".claude")
	Role    string // "state" | "cache"
	Target  string // mount point inside the box
	Exists  bool   // whether the engine volume currently exists on disk
	Machine bool   // machine-scoped: shared across ALL the user's projects
	Orphan  bool   // machine-scoped volume no longer declared by any enabled skill/config
	Engine  string // the engine this row was listed from (labels rows when several are installed)
}

// openVolumes loads the project's volumes and enters the volumes screen. A list
// error is surfaced on the form rather than opening an empty screen.
func (m model) openVolumes() model {
	list, err := m.vols.List()
	if err != nil {
		m.errMsg = "listing volumes: " + err.Error()
		return m
	}
	m.volList = list
	m.volCur = 0
	m.volPendClear = -1
	m.volErr = ""
	m.mode = modeVolumes
	m.status = ""
	return m
}

func (m model) updateVolumes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A clear is destructive, so it takes an explicit y/n confirm.
	if m.volPendClear >= 0 {
		switch msg.String() {
		case "y", "Y":
			vol := m.volList[m.volPendClear]
			m.volPendClear = -1
			if err := m.vols.Clear(vol); err != nil {
				m.volErr = err.Error()
			} else {
				m.volErr = ""
				if list, lerr := m.vols.List(); lerr == nil {
					m.volList = list
					if m.volCur >= len(list) && m.volCur > 0 {
						m.volCur = len(list) - 1
					}
				}
			}
		case "n", "N", "esc", "ctrl+c":
			m.volPendClear = -1
			m.volErr = ""
		}
		return m, nil
	}

	if cur, ok := cursorMove(msg.String(), m.volCur, len(m.volList)); ok {
		m.volCur = cur
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeForm
		return m, nil
	case "c", "d", "x":
		if len(m.volList) == 0 {
			return m, nil
		}
		if !m.volList[m.volCur].Exists {
			m.volErr = "that volume isn't present on disk — nothing to clear"
			return m, nil
		}
		m.volPendClear = m.volCur
		m.volErr = ""
		return m, nil
	}
	return m, nil
}

func (m model) viewVolumes() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render("Volumes"))
	if len(m.volList) == 0 {
		b.WriteString(dimStyle.Render("  (no volumes declared for this project)\n"))
	}
	multiEngine := volEngines(m.volList) > 1
	for i, v := range m.volList {
		state := dimStyle.Render("empty")
		if v.Exists {
			state = "present"
		}
		line := fmt.Sprintf("%-14s %-6s %-24s %s", v.Name, v.Role, v.Target, state)
		if multiEngine {
			line += " [" + v.Engine + "]"
		}
		switch {
		case v.Orphan:
			line += dimStyle.Render("  (shared: all your projects — no longer declared)")
		case v.Machine:
			line += dimStyle.Render("  (shared: all your projects)")
		}
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.volCur, line))
	}
	// Description of the highlighted volume.
	if m.volCur < len(m.volList) {
		if d := volDescription(m.volList[m.volCur]); d != "" {
			b.WriteString("\n" + dimStyle.Render(d) + "\n")
		}
	}

	b.WriteString("\n")
	switch {
	case m.volPendClear >= 0:
		pend := m.volList[m.volPendClear]
		msg := fmt.Sprintf("Clear %q? This deletes the volume and its data.", pend.Name)
		if pend.Machine {
			msg += "\nShared by ALL your projects — clearing it affects every one (a shared agent login logs out everywhere)."
		}
		// With several engines installed the clear is engine-local — say so,
		// or "logged out everywhere" overclaims while the other engine's
		// same-named copy keeps the login alive.
		if multiEngine {
			msg += fmt.Sprintf("\nThis clears the %s copy only — a same-named volume on another engine is its own row.", pend.Engine)
		}
		if m.vols != nil {
			if note := m.vols.SharedNote(); note != "" {
				msg += "\n" + note
			}
		}
		b.WriteString(errStyle.Render(msg + " [y/n]"))
	case m.volErr != "":
		b.WriteString(errStyle.Render("✗ " + m.volErr))
	}

	b.WriteString("\n\n" + dimStyle.Render("↑/↓ move · c clear · esc back"))
	return b.String()
}

// volEngines counts the distinct engines among the rows — the label and the
// engine-local clear note only appear when there is more than one.
func volEngines(rows []VolumeStatus) int {
	set := map[string]bool{}
	for _, v := range rows {
		set[v.Engine] = true
	}
	return len(set)
}

func volDescription(v VolumeStatus) string {
	if v.Orphan {
		return "machine-scoped, ORPHANED — still on disk but no enabled skill declares it (e.g. shared-auth was disabled). Clearing deletes the shared data (a shared agent login: logged out everywhere)."
	}
	if v.Machine {
		return "machine-scoped — ONE volume shared by all your projects (ADR 0017). Clearing it affects every project (e.g. a shared agent login: clearing = logging out everywhere)."
	}
	switch v.Role {
	case "state":
		return "state — persists across rebuilds (agent auth, history, config). Clearing forces a re-login / re-init on the next develop."
	case "cache":
		return "cache — disposable, rebuilt on demand (e.g. node_modules). Clearing just frees space."
	}
	return ""
}
