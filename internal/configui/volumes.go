// volumes.go owns the volumes screen (modeVolumes): listing what volumes hold
// on disk and deleting or clearing them ad hoc through the VolumeAdmin.
package configui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/pjlsergeant/byre/internal/packages"
)

// VolumeAdmin lets the editor show volumes' data and delete or clear it ad hoc.
// It's engine-backed, so the commands layer supplies it: a project's volumes in
// a project editor, the machine volumes in the --global one. A nil VolumeAdmin
// hides the Volume data row (a layer editor, or no usable engine).
type VolumeAdmin interface {
	// List returns one row per volume per engine, plus loud degrade notes
	// for engines that could not be queried (installed but unreachable —
	// e.g. a stopped podman machine). An unreachable engine must narrow the
	// view, not kill it: its copies aren't shown (the note says so) while
	// reachable engines list normally. The error return is for failures
	// that leave nothing to show (config resolution), not engine trouble.
	List() ([]VolumeStatus, []string, error)
	// Delete removes the volume from the engine, refusing while a container
	// mounts it. It takes the full row, not just the name: scope decides
	// which Docker volume the logical name maps to, and an orphaned machine
	// volume may share a logical name with a declared project one.
	Delete(v VolumeStatus) error
	// Empty is the editor's "clear": everything in the volume goes and the
	// volume stays, so it works while boxes are running with it. The note
	// says what happened (a volume nothing mounts is removed instead).
	Empty(v VolumeStatus) (note string, err error)
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
	m.volList = groupVolumes(list)
	m.volNotes = notes
	m.volCur = 0
	m.volPendClear = -1
	m.volErr = ""
	m.volNote = ""
	m.volReturn = modeForm
	m.mode = modeVolumes
	m.status = ""
	m.errMsg = ""
	return m
}

func (m model) updateVolumes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Delete and clear are destructive, so each takes an explicit y/n confirm.
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
			var err error
			if m.volPendEmpty {
				var note string
				note, err = m.vols.Empty(vol)
				m.volNote = packages.EscapeTerminal(vol.Name + ": " + note)
			} else {
				err = m.vols.Delete(vol)
				m.volNote = packages.EscapeTerminal(vol.Name) + ": deleted"
			}
			if err != nil {
				m.volErr = err.Error()
				m.volNote = ""
			} else {
				m.volErr = ""
			}
			// Re-list either way: a failed action can still have changed
			// the disk (a clear whose re-create failed left the volume gone).
			if list, notes, lerr := m.vols.List(); lerr == nil {
				m.volList = groupVolumes(list)
				m.volNotes = notes
				if m.volCur >= len(list) && m.volCur > 0 {
					m.volCur = len(list) - 1
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
		m.mode = m.volReturn
		return m, nil
	case "ctrl+s":
		return m.save(), nil
	case "d", "x", "c":
		if len(m.volList) == 0 {
			return m, nil
		}
		return m.askVolumeAction(m.volCur, msg.String() == "c"), nil
	}
	return m, nil
}

// askVolumeAction puts row i up for a delete (or, with empty, a clear)
// confirm.
func (m model) askVolumeAction(i int, empty bool) model {
	m.volNote = ""
	if !m.volList[i].Exists {
		m.volErr = "that volume isn't present on disk — nothing to delete or clear"
		return m
	}
	m.volPendClear = i
	m.volPendEmpty = empty
	m.volErr = ""
	return m
}

// openVolumeAction is a declaration row's Delete/Clear: it opens the data
// screen on that volume's copy with the confirm already asked, and esc
// brings the person back to the list they came from. Several engine copies
// are several rows; the screen shows them all and the confirm names which
// one it acts on.
func (m model) openVolumeAction(name string, machine, empty bool) model {
	back := m.mode
	m = m.openVolumes()
	if m.mode != modeVolumes {
		return m // listing failed; openVolumes put the reason on the form
	}
	m.volReturn = back
	// The first copy ON DISK: with two engines a declared volume lists once
	// per engine, and the first row may be the copy that is not there.
	found := -1
	for i, v := range m.volList {
		if v.Name == name && v.Machine == machine {
			if found < 0 {
				found = i
			}
			if v.Exists {
				m.volCur = i
				return m.askVolumeAction(i, empty)
			}
		}
	}
	if found >= 0 {
		m.volCur = found
	}
	m.volErr = "that volume isn't present on disk — nothing to delete or clear"
	return m
}

func (m model) viewVolumes() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", m.crumb("Volumes"))
	if len(m.volList) == 0 && len(m.volNotes) == 0 {
		// \n outside the Render — see viewList's empty line for why. Gated
		// on no degrade notes: with every engine unreachable an empty list
		// proves nothing about declarations, and "(no volumes declared)"
		// beside "copies aren't shown" would be a contradiction — the notes
		// alone tell the true story there.
		empty := "  (no volumes declared for this project)"
		if m.target == TargetGlobal {
			empty = "  (no machine volumes on disk)"
		}
		b.WriteString(dimStyle.Render(empty) + "\n")
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
	// characters would mis-size byte- or rune-counted columns;
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
	// Cells an orphan row can't fill (no declaration → no role/target) get a
	// dim "-": a visible n/a, not a gap that reads as a rendering fault.
	cell := func(s string) string {
		if s == "" {
			return dimStyle.Render("-")
		}
		return s
	}
	// Scope grouping: rows arrive project-first (groupVolumes), and dim
	// section headers carry the shared-ness once — the per-row
	// "(shared: all your projects)" tags they replace repeated it on every
	// machine row. Headers only appear when machine rows exist: an all-project
	// list has nothing to distinguish, and a lone header is furniture.
	hasMachine := false
	for _, v := range m.volList {
		if v.Machine {
			hasMachine = true
		}
	}
	for i, v := range m.volList {
		if hasMachine && (i == 0 || v.Machine != m.volList[i-1].Machine) {
			if i > 0 {
				b.WriteString("\n")
			}
			header := "Project volumes"
			if v.Machine {
				header = "Machine volumes — shared by all your projects"
			}
			b.WriteString(dimStyle.Render(header) + "\n")
		}
		state := dimStyle.Render("empty")
		if v.Exists {
			state = "present"
		}
		// State is padded too ("empty" is 5 cells, "present" 7 — pad works
		// through the ANSI styling), or the engine suffix and shared/orphan
		// annotations drift on mixed-state rows.
		line := pad(packages.EscapeTerminal(v.Name), nameW) + "  " + pad(cell(v.Role), roleW) + "  " + pad(packages.EscapeTerminal(cell(v.Target)), targetW) + "  " + pad(state, len("present"))
		if multiEngine {
			line += " [" + v.Engine + "]"
		}
		if v.Orphan {
			// The full story (still on disk, what clearing costs) lives in the
			// highlighted row's description — the row tag stays a flag.
			line += dimStyle.Render("  (no longer declared)")
		}
		fmt.Fprintf(&b, "%s\n", cursorLine(i == m.volCur, line))
	}
	// Description of the highlighted volume. Wrapped, not clipped: the
	// frame-wide clip ends a long line in "…", and on this string the part
	// past the ellipsis was the consequence ("logged out everywhere") --
	// exactly what the reader is deciding with, one row above the actions it
	// warns about.
	if m.volCur < len(m.volList) {
		if d := volDescription(m.volList[m.volCur]); d != "" {
			b.WriteString("\n")
			w := m.width
			if w <= 0 {
				w = 80
			}
			for _, l := range wrapLine(d, w) {
				b.WriteString(dimStyle.Render(l) + "\n")
			}
		}
	}

	b.WriteString("\n")
	switch {
	case m.volPendClear >= 0:
		pend := m.volList[m.volPendClear]
		msg := fmt.Sprintf("Delete %q? This removes the volume and everything in it.", packages.EscapeTerminal(pend.Name))
		if m.volPendEmpty {
			msg = fmt.Sprintf("Clear %q? Everything in it is deleted and the volume stays — boxes running with it now see it empty.", packages.EscapeTerminal(pend.Name))
		}
		if pend.Machine {
			msg += "\nShared by ALL your projects — this affects every one (a shared agent login is gone for all of them)."
		}
		// With several engines installed the action is engine-local — say so,
		// or "logged out everywhere" overclaims while the other engine's
		// same-named copy keeps the login alive.
		if multiEngine {
			msg += fmt.Sprintf("\nThis acts on the %s copy only — a same-named volume on another engine is its own row.", pend.Engine)
		}
		if m.vols != nil {
			if note := m.vols.SharedNote(); note != "" {
				msg += "\n" + note
			}
		}
		b.WriteString(m.wrapStyled(errStyle, msg+" [y/n]"))
	case m.volErr != "":
		b.WriteString(m.errLine(m.volErr))
	case m.volNote != "":
		b.WriteString(m.wrapStyled(okStyle, m.volNote))
	default:
		b.WriteString(m.subFooterNote())
	}

	b.WriteString("\n\n" + helpLine("↑/↓", "move", "d", "delete", "c", "clear", "^s", "save", "esc", "back"))
	return b.String()
}

// wrapStyled wraps each line of msg to the terminal width before styling it:
// the view clips every line to the width, and the end of these messages
// (the remedy, the [y/n]) is the part that must not be lost.
func (m model) wrapStyled(st lipgloss.Style, msg string) string {
	if m.width <= 0 {
		return st.Render(msg)
	}
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		for _, l := range wrapLine(line, m.width) {
			out = append(out, st.Render(l))
		}
	}
	return strings.Join(out, "\n")
}

// groupVolumes orders rows for display: project-scoped first, then
// machine-scoped, orphans last within the shared group — the view renders one
// section header per scope, so the scopes must be contiguous. The partition is
// stable: the admin's per-engine order survives inside each group (the [engine]
// suffix keeps interleaved engines legible).
func groupVolumes(rows []VolumeStatus) []VolumeStatus {
	var project, shared, orphans []VolumeStatus
	for _, v := range rows {
		switch {
		case v.Orphan:
			orphans = append(orphans, v)
		case v.Machine:
			shared = append(shared, v)
		default:
			project = append(project, v)
		}
	}
	out := make([]VolumeStatus, 0, len(rows))
	out = append(out, project...)
	out = append(out, shared...)
	return append(out, orphans...)
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
		return "machine-scoped, ORPHANED — still on disk but no enabled skill declares it (e.g. shared-auth was disabled). Deleting or clearing it destroys the shared data (a shared agent login: logged out everywhere)."
	}
	if v.Machine {
		return "machine-scoped — ONE volume shared by all your projects (ADR 0017). Deleting or clearing it affects every project (e.g. a shared agent login: logged out everywhere)."
	}
	switch v.Role {
	case "state":
		return "state — persists across rebuilds (agent auth, history, config). Deleting or clearing it forces a re-login / re-init."
	case "cache":
		return "cache — disposable, rebuilt on demand (e.g. node_modules). Deleting it just frees space."
	}
	return ""
}
