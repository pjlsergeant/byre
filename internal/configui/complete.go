// complete.go owns the flows that finish an editing session: save/assemble,
// dirty tracking behind the quit confirm, and the $EDITOR round-trip.
package configui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
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
	// Did the editor land a mutation? Compare against the ctrl+e snapshot —
	// savedOnce feeds Run's saved return (and the caller's wrote/unchanged
	// report), so it must track disk, not the round-trip. Checked before the
	// parse: a written-but-invalid file was still written. A file DELETED in
	// the editor is a mutation too — reporting it "unchanged" would tell the
	// user their config is intact when it is gone.
	raw, rerr := os.ReadFile(m.filePath)
	created := rerr == nil && m.preEditorErr != nil
	changed := rerr == nil && m.preEditorErr == nil && !bytes.Equal(raw, m.preEditorRaw)
	deleted := rerr != nil && m.preEditorErr == nil
	if created || changed || deleted {
		m.savedOnce = true
	}
	cfg, perr := config.ParseFile(m.filePath)
	if perr != nil {
		m.errMsg = "file has an error after editing (fix it and ctrl+e again): " + perr.Error()
		return m
	}
	m = m.loadConfig(cfg)
	// The editor may have added (or removed) hand-written comments — recompute
	// the destroys-comments warning so it tracks the file, not the open-time state.
	if rerr == nil {
		m.commentWarn = handComments(string(raw))
	}
	m.errMsg = ""
	m.status = "Reloaded from file"
	if deleted {
		// Say what actually happened — the empty form below is the file's
		// true (absent) state, not a glitch.
		m.status = "Reloaded — the file was deleted in the editor"
	}
	return m
}

// ---- save / assemble / dirty -----------------------------------------------

// savedStatus is the post-save status note; statusNote singles it out (green).
const savedStatus = "Saved ✓"

// runPrepare runs the deferred store setup, shared by every path that is about
// to write filePath (ctrl+s save, the $EDITOR shell-out). A failure lands in
// errMsg and reports false. Deliberately re-run on every write, not once: the
// hook (Bootstrap) is idempotent, and each run re-ensures the store dir AND
// its path record together — a one-shot hook would let a later write's own
// MkdirAll resurrect a concurrently-deleted store without the record.
func (m model) runPrepare() (model, bool) {
	if m.prepare == nil {
		return m, true
	}
	if err := m.prepare(); err != nil {
		m.errMsg = err.Error()
		return m, false
	}
	return m, true
}

func (m model) save() model {
	cfg := m.assemble()
	// A layer file may not select a shape (`template` is parse-banned at
	// load): refuse at save, with the file open, rather than write a file
	// the resolver will refuse. The layer editor has no template picker, so
	// a hand-written key can only be repaired via ^e — say so.
	if m.target == TargetLayer && cfg.Template != "" {
		m.errMsg = "template is not allowed in a layer file (shape selection belongs to the project config) — remove it via ctrl+e"
		m.status = ""
		return m
	}
	// Validate BEFORE the deferred store setup: a save the validator refuses
	// never becomes a write, so it must not enroll anything. (Save re-runs
	// the same check on the way to disk; the duplication buys the ordering.)
	if err := cfg.ValidateLayer(); err != nil {
		m.errMsg = err.Error()
		m.status = ""
		return m
	}
	var ok bool
	if m, ok = m.runPrepare(); !ok {
		m.status = ""
		return m
	}
	if err := Save(m.filePath, cfg); err != nil {
		m.errMsg = err.Error()
		m.status = ""
		return m
	}
	m.errMsg = ""
	m.savedSig = m.sig()
	m.savedOnce = true
	m.status = savedStatus
	m.confirmQuit = false
	// The save re-marshaled the file: any hand-written comments are gone now,
	// so the destroys-comments warning has nothing left to protect.
	m.commentWarn = false
	return m
}

// assemble builds a config from the working state onto a copy of the original,
// so untouched fields (raw blocks, volumes, files) are preserved exactly.
func (m model) assemble() config.Config {
	out := m.base
	out.Base = strings.TrimSpace(m.ti.Value())
	// worktree_base is only editable in the global editor; elsewhere it round-trips
	// via m.base untouched. Sibling checkbox wins; else the base path; else unset.
	if m.target == TargetGlobal {
		if m.wtSibling {
			out.WorktreeBase = "sibling"
		} else {
			out.WorktreeBase = strings.TrimSpace(m.wtBase.Value())
		}
	}
	// extends is only editable where the EXTENDS section shows (project and
	// layer editors); the global editor round-trips it via m.base untouched
	// (the resolver refuses it there — never silently drop what a hand wrote).
	if m.target != TargetGlobal {
		out.Extends = fromNone(m.extOpts[m.extSel])
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
	out.Egress = nilIfEmpty(m.egress)
	out.MCPs = append([]config.MCP{}, m.mcps...)
	if len(out.MCPs) == 0 {
		out.MCPs = nil
	}
	out.ClaudeSkills = append([]config.ClaudeSkill{}, m.claudeSkills...)
	if len(out.ClaudeSkills) == 0 {
		out.ClaudeSkills = nil
	}
	// The primary agent is implied by `agent`, so never write it into `skills`
	// (even if it lingers in m.skills from a config that listed it before it became
	// primary) — the locked row shows it on via the agent, not via this list.
	// EXCEPT in the --global editor: there `agent` is an onboarding favourite
	// that enables nothing, so a skills entry naming it is the user's real
	// (and only) way to enable that skill machine-wide — stripping it made
	// the choice silently impossible (audit finding).
	primaryAgent := fromNone(m.agentOpts[m.agentSel])
	if m.target == TargetGlobal {
		primaryAgent = ""
	}
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
		"ext:" + m.extOpts[m.extSel],
		"apt:" + strings.Join(m.apt, ","),
	}
	for _, kv := range m.env {
		parts = append(parts, "env:"+kv.Key+"="+kv.Value)
	}
	for _, mt := range m.mounts {
		parts = append(parts, "mnt:"+mountLine(mt))
	}
	for _, pt := range m.ports {
		// portLine renders the effective binding, which a removal marker
		// doesn't have — sign the marker distinctly or swapping a marker for
		// the real binding it removes would read as clean (review finding).
		if pt.Remove {
			parts = append(parts, fmt.Sprintf("port:!%d", pt.Container))
		} else {
			parts = append(parts, "port:"+portLine(pt))
		}
	}
	parts = append(parts, "egress:"+strings.Join(m.egress, ","))
	for _, mc := range m.mcps {
		parts = append(parts, "mcp:"+mcpLine(mc))
	}
	for _, cs := range m.claudeSkills {
		parts = append(parts, "cskill:"+claudeSkillLine(cs))
	}
	parts = append(parts, "skills:"+strings.Join(m.skills, ","))
	parts = append(parts, "ra:"+m.runArgs, "pre:"+m.dfPre, "post:"+m.dfPost)
	parts = append(parts, fmt.Sprintf("wt:%v/%s", m.wtSibling, m.wtBase.Value()))
	return strings.Join(parts, "\x00")
}

func (m model) dirty() bool { return m.sig() != m.savedSig }
