package commands

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// The picker platform adapter (ADR 0021): when deliver's cascade lands on
// "several boxes, no explicit pick", the interactive picker is chosen by
// capability, each axis on its own — a TTY on stdin gets a Bubble Tea
// list; an OCCUPIED stdin (a pipe, a tar stream) with a controlling
// terminal gets the same list through /dev/tty (ssh's own contract:
// prompts survive a busy stdin — adopted 2026-07-16, closing ADR 0038's
// open question); a graphical launch (no terminal, but a GUI session)
// gets a system dialog (osascript / zenity / kdialog, shelled out per
// ADR 0002); none of these yields nil, and deliver degrades to the
// candidates-listing error. byre stays one Go binary — no GUI toolkit
// dependency.

// hostPicker picks the adapter for this invocation.
func hostPicker(s Streams) func([]deliver.Session) (deliver.Session, bool, error) {
	if s.TTY {
		return func(sessions []deliver.Session) (deliver.Session, bool, error) {
			return ttyPick(s, sessions)
		}
	}
	if tty := openControllingTTY(); tty != nil {
		return func(sessions []deliver.Session) (deliver.Session, bool, error) {
			defer tty.Close()
			// Render where the human is: stderr normally; when stderr is
			// redirected too, the terminal device itself (beat.go's rule —
			// bubbletea's arm/cleanup sequences must reach the terminal).
			out := io.Writer(s.Err)
			if ef, ok := s.Err.(*os.File); !ok || !isTTY(ef) {
				out = tty
			}
			return runPick(tty, out, sessions)
		}
	}
	if tool := graphicalPickTool(runtime.GOOS, os.Getenv); tool != nil {
		return tool
	}
	return nil
}

// openControllingTTY reaches the controlling terminal for interaction while
// stdin carries data. Nil when the process has none (cron, a detached
// launch) — the capability probe the adapter order needs. Seam for tests.
var openControllingTTY = func() *os.File {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	return f
}

// --- TTY picker (Bubble Tea, rendered to stderr — stdout is the contract) ---

type pickModel struct {
	sessions []deliver.Session
	cursor   int
	choice   int // -1 until chosen
	quit     bool
}

func (m pickModel) Init() tea.Cmd { return nil }

func (m pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.sessions)-1 {
				m.cursor++
			}
		case "enter":
			m.choice = m.cursor
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
	}
	return m, nil
}

var (
	pickTitleStyle  = lipgloss.NewStyle().Bold(true)
	pickCursorStyle = lipgloss.NewStyle().Bold(true)
	pickDimStyle    = lipgloss.NewStyle().Faint(true)
)

func (m pickModel) View() string {
	var b strings.Builder
	b.WriteString(pickTitleStyle.Render("deliver to which box?") + "\n")
	for i, s := range m.sessions {
		row := pickRow(s)
		if i == m.cursor {
			b.WriteString(pickCursorStyle.Render("> "+row) + "\n")
		} else {
			b.WriteString("  " + row + "\n")
		}
	}
	b.WriteString(pickDimStyle.Render("↑/↓ move · enter deliver · q cancel") + "\n")
	return b.String()
}

// pickRow shows what discovery honestly has: id, engine, ownership.
func pickRow(s deliver.Session) string {
	row := fmt.Sprintf("%s (%s)", pickLabel(s), s.EngineName)
	if s.Foreign {
		row += fmt.Sprintf(" — owned by uid %d, not you", s.UID)
	}
	return row
}

// pickLabel is the session's display name: the workdir id (distinct for
// worktree sessions), else the project id, else the container id.
func pickLabel(s deliver.Session) string {
	if s.WorkdirID != "" {
		return s.WorkdirID
	}
	if s.ProjectID != "" {
		return s.ProjectID
	}
	return s.ID
}

func ttyPick(s Streams, sessions []deliver.Session) (deliver.Session, bool, error) {
	var in *os.File
	if f, ok := s.In.(*os.File); ok {
		in = f
	}
	// Render to stderr: stdout is the delivered-paths contract and must stay
	// clean even through an interactive pick.
	return runPick(in, s.Err, sessions)
}

// runPick runs the Bubble Tea list on the given terminal input and output.
func runPick(in *os.File, out io.Writer, sessions []deliver.Session) (deliver.Session, bool, error) {
	m := pickModel{sessions: sessions, choice: -1}
	opts := []tea.ProgramOption{tea.WithOutput(out)}
	if in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	res, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return deliver.Session{}, false, fmt.Errorf("picker: %w", err)
	}
	final := res.(pickModel)
	if final.quit || final.choice < 0 {
		return deliver.Session{}, false, nil
	}
	return sessions[final.choice], true, nil
}

// --- graphical picker (osascript / zenity / kdialog) ---

// graphicalPickTool probes for a dialog tool; each returned func shows the
// sessions and maps the answer back. Labels are matched by pickLabel, which
// is unique per session (workdir ids are unique; container ids break ties).
func graphicalPickTool(goos string, getenv func(string) string) func([]deliver.Session) (deliver.Session, bool, error) {
	switch goos {
	case "darwin":
		if getenv("SSH_CONNECTION") != "" {
			return nil // remote shell: no local WindowServer to draw on
		}
		if _, err := clipLookPath("osascript"); err != nil {
			return nil
		}
		return func(sessions []deliver.Session) (deliver.Session, bool, error) {
			labels := make([]string, len(sessions))
			for i, s := range sessions {
				labels[i] = `"` + strings.ReplaceAll(pickRow(s), `"`, `\"`) + `"`
			}
			script := fmt.Sprintf(`choose from list {%s} with prompt "Deliver to which box?"`, strings.Join(labels, ", "))
			out, err := clipRunOut("osascript", "-e", script)
			if err != nil {
				return deliver.Session{}, false, fmt.Errorf("picker dialog: %w", err)
			}
			return matchPick(sessions, strings.TrimSpace(string(out)))
		}
	default:
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return nil
		}
		if _, err := clipLookPath("zenity"); err == nil {
			return func(sessions []deliver.Session) (deliver.Session, bool, error) {
				args := []string{"--list", "--title", "byre deliver", "--text", "Deliver to which box?", "--column", "box"}
				for _, s := range sessions {
					args = append(args, pickRow(s))
				}
				out, err := clipRunOut("zenity", args...)
				if err != nil {
					// Exit 1 is the user pressing Cancel; anything else is a
					// broken dialog and must not masquerade as a choice.
					if exitCode(err) == 1 {
						return deliver.Session{}, false, nil
					}
					return deliver.Session{}, false, fmt.Errorf("picker dialog: %w", err)
				}
				return matchPick(sessions, strings.TrimSpace(string(out)))
			}
		}
		if _, err := clipLookPath("kdialog"); err == nil {
			return func(sessions []deliver.Session) (deliver.Session, bool, error) {
				args := []string{"--menu", "Deliver to which box?"}
				for _, s := range sessions {
					args = append(args, pickRow(s), pickRow(s)) // tag, label
				}
				out, err := clipRunOut("kdialog", args...)
				if err != nil {
					if exitCode(err) == 1 { // cancel
						return deliver.Session{}, false, nil
					}
					return deliver.Session{}, false, fmt.Errorf("picker dialog: %w", err)
				}
				return matchPick(sessions, strings.TrimSpace(string(out)))
			}
		}
		return nil
	}
}

// matchPick maps a dialog's answer back to its session. "false" is
// osascript's cancel; an empty answer is a dismissed dialog.
func matchPick(sessions []deliver.Session, answer string) (deliver.Session, bool, error) {
	if answer == "" || answer == "false" {
		return deliver.Session{}, false, nil
	}
	for _, s := range sessions {
		if answer == pickRow(s) {
			return s, true, nil
		}
	}
	return deliver.Session{}, false, fmt.Errorf("picker returned an unknown choice %q", answer)
}
