// textblock.go owns the raw text-block overlay (modeText): the multi-line
// editor for run_args, dockerfile_pre, and dockerfile_post.
package configui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// openText opens the multi-line editor for a raw text field (run_args /
// dockerfile_pre|post, one item per line).
func (m model) openText(f fieldID) model {
	m.textField = f
	m.ta.SetValue(m.textValue(f))
	m.ta.CursorEnd()
	m.ta.Focus() // else the textarea ignores typing
	m.mode = modeText
	m.status = ""
	// Same rule as the other screen entries: a stale form error must not ride
	// in (the overlay doesn't render errMsg, so it would lurk invisibly and
	// reappear on cancel).
	m.errMsg = ""
	return m
}

func (m model) textValue(f fieldID) string {
	switch f {
	case fRunArgs:
		return m.runArgs
	case fDockerfilePre:
		return m.dfPre
	case fDockerfilePost:
		return m.dfPost
	}
	return ""
}

func (m *model) setText(f fieldID, v string) {
	switch f {
	case fRunArgs:
		m.runArgs = v
	case fDockerfilePre:
		m.dfPre = v
	case fDockerfilePost:
		m.dfPost = v
	}
}

// updateText routes keys to the text-block overlay: ctrl+s accepts the buffer
// into the working field and saves the file (^s means SAVE on every screen;
// enter is a newline here, so there's no separate accept-only key — staging a
// text edit without writing goes esc + re-open), esc/ctrl+c/ctrl+q cancel and
// discard. As with the item editor, ctrl+c here only backs out of this edit —
// it never quits the whole editor.
func (m model) updateText(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		m.setText(m.textField, m.ta.Value())
		m.mode = modeForm
		m.ta.Blur()
		return m.save(), nil
	case "esc", "ctrl+c", "ctrl+q":
		m.mode = modeForm
		m.ta.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m model) viewText() string {
	title := focusStyle.Render(fieldLabel[m.textField])
	if key := rawFieldKey[m.textField]; key != "" {
		title += dimStyle.Render("  (" + key + ")") // keep the TOML key discoverable
	}
	title += dimStyle.Render("  ·  " + m.title) // crumb, hand-built around the key hint
	return fmt.Sprintf("%s\n%s\n\n%s\n\n%s\n",
		title,
		dimStyle.Render("(one per line)"),
		m.ta.View(),
		helpLine("^s", "accept + save", "esc", "cancel"))
}

// splitLines parses one item per line, dropping blanks/whitespace.
func splitLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}
