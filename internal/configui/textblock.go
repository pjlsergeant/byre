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
// into the working field (still not written to disk until the form's ctrl+s),
// esc/ctrl+c cancel and discard. As with the item editor, ctrl+c here only backs
// out of this edit — it never quits the whole editor.
func (m model) updateText(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		m.setText(m.textField, m.ta.Value())
		m.mode = modeForm
		m.ta.Blur()
		return m, nil
	case "esc", "ctrl+c":
		m.mode = modeForm
		m.ta.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m model) viewText() string {
	hint := "one per line"
	title := fieldLabel[m.textField]
	if key := rawFieldKey[m.textField]; key != "" {
		title += dimStyle.Render("  (" + key + ")") // keep the TOML key discoverable
	}
	return fmt.Sprintf("%s\n\n%s — ctrl+s accept · esc cancel\n%s\n\n%s\n",
		title,
		dimStyle.Render("edit "+fieldLabel[m.textField]),
		dimStyle.Render("("+hint+")"),
		m.ta.View())
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
