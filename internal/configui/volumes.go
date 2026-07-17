// volumes.go owns the volumes screen (modeVolumes): listing a project's
// volumes and clearing them ad hoc through the VolumeAdmin.
package configui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// VolumeAdmin lets the editor show a project's volumes and clear them ad hoc.
// It's engine-backed, so the commands layer supplies it; a nil VolumeAdmin hides
// the Volumes section (e.g. the global config, which has no project volumes).
type VolumeAdmin interface {
	// List returns one row per volume per engine, plus loud degrade notes
	// for engines that could not be queried (installed but unreachable —
	// e.g. a stopped podman machine). An unreachable engine must narrow the
	// view, not kill it: its copies aren't shown (the note says so) while
	// reachable engines list normally. The error return is for failures
	// that leave nothing to show (config resolution), not engine trouble.
	List() ([]VolumeStatus, []string, error)
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
	list, notes, err := m.vols.List()
	if err != nil {
		m.errMsg = "listing volumes: " + err.Error()
		return m
	}
	m.volList = list
	m.volNotes = notes
	m.volCur = 0
	m.volPendClear = -1
	m.volErr = ""
	m.mode = modeVolumes
	m.status = ""
	m.errMsg = ""
	return m
}

func (m model) updateVolumes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A clear is destructive, so it takes an explicit y/n confirm.
	if m.volPendClear >= 0 {
		switch msg.String() {
		case "ctrl+s":
			// ctrl+s means save on every screen, including mid-confirm — but it
			// is not a "y": the pending destructive question resolves in the
			// safe direction (cancelled) so the Saved ✓ note has room to show.
			m.volPendClear = -1
			m.volErr = ""
			return m.save(), nil
		case "y", "Y":
			vol := m.volList[m.volPendClear]
			m.volPendClear = -1
			if err := m.vols.Clear(vol); err != nil {
				m.volErr = err.Error()
			} else {
				m.volErr = ""
				if list, notes, lerr := m.vols.List(); lerr == nil {
					m.volList = list
					m.volNotes = notes
					if m.volCur >= len(list) && m.volCur > 0 {
						m.volCur = len(list) - 1
					}
				}
			}
		case "n", "N", "esc", "ctrl+c", "ctrl+q":
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
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeForm
		return m, nil
	case "ctrl+s":
		return m.save(), nil
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
	fmt.Fprintf(&b, "%s\n\n", m.crumb("Volumes"))
	if len(m.volList) == 0 && len(m.volNotes) == 0 {
		// \n outside the Render — see viewList's empty line for why. Gated
		// on no degrade notes: with every engine unreachable an empty list
		// proves nothing about declarations, and "(no volumes declared)"
		// beside "copies aren't shown" would be a contradiction — the notes
		// alone tell the true story there (codereview finding).
		b.WriteString(dimStyle.Render("  (no volumes declared for this project)") + "\n")
	}
	// Engine degrade notes: loud (bold, not dim) — an unreachable engine
	// means this view's claims are narrowed, and that must not blend into
	// the furniture. Not yellow: warnStyle stays cross-project reach's.
	for _, n := range m.volNotes {
		b.WriteString(errStyle.Render("  ⚠ "+n) + "\n")
	}
	multiEngine := volEngines(m.volList) > 1
	// Column widths from the content: fixed widths (14/6/24) shattered on
	// real rows — "opencode-identity" is 17, an identity target is 30+, and
	// orphan rows have no role/target at all, so "present" floated to a
	// different column per row (live field report, 2026-07-17).
	// Display cells, not bytes/runes: a target path with accented or wide
	// characters would mis-size byte- or rune-counted columns (codereview);
	// ansi.StringWidth + manual padding is the cell-true pair.
	nameW, roleW, targetW := 0, 0, 0
	for _, v := range m.volList {
		nameW = max(nameW, ansi.StringWidth(v.Name))
		roleW = max(roleW, ansi.StringWidth(v.Role))
		targetW = max(targetW, ansi.StringWidth(v.Target))
	}
	pad := func(s string, w int) string {
		if d := w - ansi.StringWidth(s); d > 0 {
			return s + strings.Repeat(" ", d)
		}
		return s
	}
	for i, v := range m.volList {
		state := dimStyle.Render("empty")
		if v.Exists {
			state = "present"
		}
		line := pad(v.Name, nameW) + "  " + pad(v.Role, roleW) + "  " + pad(v.Target, targetW) + "  " + state
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
		b.WriteString(m.errLine(m.volErr))
	default:
		b.WriteString(m.subFooterNote())
	}

	b.WriteString("\n\n" + helpLine("↑/↓", "move", "c", "clear", "^s", "save", "esc", "back"))
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
