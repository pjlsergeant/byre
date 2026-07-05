// complete.go owns the flows that finish an editing session: save/assemble,
// dirty tracking behind the quit confirm, and the $EDITOR round-trip.
package configui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"byre/internal/config"
)

// ---- $EDITOR shell-out -----------------------------------------------------

type editorClosedMsg struct{ err error }

// openEditor suspends the TUI and runs $EDITOR (falling back to vi) on path. The
// editor value is split on spaces so "code -w" / "emacsclient -nw"-style values
// work. On exit, editorClosedMsg triggers a reload from disk.
func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	args := append(parts[1:], path)
	return tea.ExecProcess(exec.Command(parts[0], args...), func(err error) tea.Msg {
		return editorClosedMsg{err}
	})
}

// onEditorClosed reloads the config from disk after $EDITOR exits, so any raw-tier
// edits the user made by hand are reflected. A parse error (they left the file
// invalid) is surfaced without discarding what's on screen.
func (m model) onEditorClosed(err error) model {
	m.mode = modeForm
	if err != nil {
		m.errMsg = "editor: " + err.Error()
		return m
	}
	cfg, perr := config.ParseFile(m.filePath)
	if perr != nil {
		m.errMsg = "file has an error after editing (fix it and ctrl+e again): " + perr.Error()
		return m
	}
	m = m.loadConfig(cfg)
	// The editor may have added (or removed) hand-written comments — recompute
	// the destroys-comments warning so it tracks the file, not the open-time state.
	if raw, rerr := os.ReadFile(m.filePath); rerr == nil {
		m.commentWarn = handComments(string(raw))
	}
	m.errMsg = ""
	m.status = "Reloaded from file"
	return m
}

// ---- save / assemble / dirty -----------------------------------------------

func (m model) save() model {
	cfg := m.assemble()
	if err := Save(m.filePath, cfg); err != nil {
		m.errMsg = err.Error()
		m.status = ""
		return m
	}
	m.errMsg = ""
	m.savedSig = m.sig()
	m.savedOnce = true
	m.status = "Saved ✓"
	m.confirmQuit = false
	return m
}

// assemble builds a config from the working state onto a copy of the original,
// so untouched fields (raw blocks, volumes, files) are preserved exactly.
func (m model) assemble() config.Config {
	out := m.base
	out.Base = strings.TrimSpace(m.ti.Value())
	// worktree_base is only editable in the global editor; elsewhere it round-trips
	// via m.base untouched. Sibling checkbox wins; else the base path; else unset.
	if m.global {
		if m.wtSibling {
			out.WorktreeBase = "sibling"
		} else {
			out.WorktreeBase = strings.TrimSpace(m.wtBase.Value())
		}
	}
	out.Template = fromNone(m.tmplOpts[m.tmplSel])
	out.Agent = fromNone(m.agentOpts[m.agentSel])
	out.Engine = fromAuto(m.engineOpts[m.engineSel])
	out.Apt = nilIfEmpty(m.apt)
	if len(m.env) == 0 {
		out.Env = nil
	} else {
		env := make(map[string]string, len(m.env))
		for _, kv := range m.env {
			env[kv.Key] = kv.Value // last wins on a duplicate key
		}
		out.Env = env
	}
	out.Mounts = append([]config.Mount{}, m.mounts...)
	if len(out.Mounts) == 0 {
		out.Mounts = nil
	}
	out.Ports = append([]config.Port{}, m.ports...)
	if len(out.Ports) == 0 {
		out.Ports = nil
	}
	// The primary agent is implied by `agent`, so never write it into `skills`
	// (even if it lingers in m.skills from a config that listed it before it became
	// primary) — the locked row shows it on via the agent, not via this list.
	primaryAgent := fromNone(m.agentOpts[m.agentSel])
	out.Skills = nil
	for _, s := range m.skills {
		if s != primaryAgent {
			out.Skills = append(out.Skills, s)
		}
	}
	// Raw blocks round-trip VERBATIM when untouched (preserving hand-formatting —
	// indented Dockerfile continuations, blank lines); only a block the user
	// actually edited gets normalized via splitLines.
	out.RunArgs = rawSlice(m.runArgs, m.base.RunArgs)
	out.DockerfilePre = rawSlice(m.dfPre, m.base.DockerfilePre)
	out.DockerfilePost = rawSlice(m.dfPost, m.base.DockerfilePost)
	return out
}

// rawSlice keeps the original slice verbatim when the edited text still matches
// it (untouched), else re-parses the edited text one item per line.
func rawSlice(text string, orig []string) []string {
	if text == strings.Join(orig, "\n") {
		return orig
	}
	return splitLines(text)
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// sig is a signature of the working values, for dirty detection.
func (m model) sig() string {
	parts := []string{
		m.ti.Value(),
		m.tmplOpts[m.tmplSel], m.agentOpts[m.agentSel], m.engineOpts[m.engineSel],
		"apt:" + strings.Join(m.apt, ","),
	}
	for _, kv := range m.env {
		parts = append(parts, "env:"+kv.Key+"="+kv.Value)
	}
	for _, mt := range m.mounts {
		parts = append(parts, "mnt:"+mountLine(mt))
	}
	for _, pt := range m.ports {
		parts = append(parts, "port:"+portLine(pt))
	}
	parts = append(parts, "skills:"+strings.Join(m.skills, ","))
	parts = append(parts, "ra:"+m.runArgs, "pre:"+m.dfPre, "post:"+m.dfPost)
	parts = append(parts, fmt.Sprintf("wt:%v/%s", m.wtSibling, m.wtBase.Value()))
	return strings.Join(parts, "\x00")
}

func (m model) dirty() bool { return m.sig() != m.savedSig }
