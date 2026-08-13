// complete.go owns the flows that finish an editing session: save/assemble,
// dirty tracking behind the quit confirm, and the $EDITOR round-trip.
package configui

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/editorcmd"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
)

// ---- $EDITOR shell-out -----------------------------------------------------

type editorClosedMsg struct{ err error }

// openEditor suspends the TUI and runs $EDITOR (falling back to vi) on
// path, through the shared shell-semantics launcher (editorcmd). On exit,
// editorClosedMsg triggers a reload from disk (or the prose round-trip,
// when prosePath is set).
func openEditor(path string, roots hostexec.Roots) tea.Cmd {
	cmd, err := editorcmd.Command(editorcmd.Resolve(), path, roots)
	if err != nil {
		// byre won't launch its own shell out of a directory the box writes.
		// Reported on the same channel an editor that exited badly uses, so
		// the screen stays up and the message lands where the user is looking.
		return func() tea.Msg { return editorClosedMsg{err} }
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
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
	// Absence is only ever fs.ErrNotExist: any OTHER read error (permissions,
	// I/O) proves nothing about a write landing, so it sets none of the flags
	// — the ParseFile below fails on the same unreadable file and surfaces it.
	raw, rerr := hostopen.ReadFileBounded(m.filePath, m.followFile, config.MaxConfigBytes)
	created := rerr == nil && errors.Is(m.preEditorErr, fs.ErrNotExist)
	changed := rerr == nil && m.preEditorErr == nil && !bytes.Equal(raw, m.preEditorRaw)
	deleted := errors.Is(rerr, fs.ErrNotExist) && m.preEditorErr == nil
	if created || changed || deleted {
		m.savedOnce = true
	}
	cfg, perr := config.ParseFile(m.filePath, m.followFile)
	if perr != nil {
		m.errMsg = "file has an error after editing (fix it and ctrl+e again): " + perr.Error()
		return m
	}
	m = m.loadConfig(cfg)
	// The $EDITOR edit came through byre's OWN ctrl+e flow and is now the
	// model's state: it is not another session's drift, so it becomes the
	// save baseline. Without this the next ctrl+s prompted to overwrite the
	// user's own accepted edit.
	m.saveBase, m.saveBaseErr = raw, rerr
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

// reportSaved is Run's saved return: whether the caller should say "wrote
// <path>" (true) or "byre: config unchanged." (false). A ctrl+s save always
// reports true — the user asked for that write. $EDITOR mutations report by
// NET content instead: a round-trip that ends byte-identical to the open-time
// file (a bad line edited in, then fixed back out) leaves nothing changed,
// and reporting "wrote" for it contradicted the on-disk truth (QA playbook
// finding 2026-07-18, fixed 2026-07-22). A file created or deleted while the
// editor was open is a real mutation and still reports true.
func (m model) reportSaved() bool {
	if m.uiWrote {
		return true
	}
	// No savedOnce shortcut here: "unchanged" must be POSITIVELY established
	// from the endpoints, never assumed. An $EDITOR session that changed the
	// file and left it unreadable sets no mutation flag (onEditorClosed can't
	// prove the write), and a shortcut on savedOnce reported that session
	// "unchanged".
	raw, err := hostopen.ReadFileBounded(m.filePath, m.followFile, config.MaxConfigBytes)
	switch {
	case err == nil && m.openErr == nil:
		return !bytes.Equal(raw, m.openRaw)
	case errors.Is(err, fs.ErrNotExist) && errors.Is(m.openErr, fs.ErrNotExist):
		return false // absent at open and at quit alike
	case (err == nil && errors.Is(m.openErr, fs.ErrNotExist)) ||
		(errors.Is(err, fs.ErrNotExist) && m.openErr == nil):
		return true // created or deleted during the session
	default:
		// An endpoint failed to read for a reason OTHER than absence
		// (permissions, I/O): the net comparison can't be trusted, and
		// "unchanged" is claimed ONLY on positive evidence (the two cases
		// above) — so every incomparable shape reports written, no
		// evidence-weighing (weighing lied for double-fault
		// endpoints). The residual false "wrote" needs a file the caller's
		// pre-open ParseFile could read that no longer reads at quit with
		// nothing done — an unreadable endpoint on a session that gate let
		// in means something happened to the file while we were here.
		return true
	}
}

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
	if err := (&m).write(cfg, m.forceSave); err != nil {
		if errors.Is(err, ErrDrift) {
			// Don't lose the session's work: arm the overwrite prompt and
			// leave every edit on screen. Answering y re-saves with force.
			m.confirmOverwrite = true
			m.errMsg = ""
			m.status = ""
			return m
		}
		m.errMsg = err.Error()
		m.status = ""
		return m
	}
	m.confirmOverwrite = false
	m.forceSave = false
	m.errMsg = ""
	m.savedOnce = true
	m.uiWrote = true
	m.confirmQuit = false
	m.savedSig = m.sig()
	m.status = savedStatus
	return m
}

// write runs Save inside whatever lock the caller supplied — the lock that
// guards the FILE, whichever editor reached it (the project store's setup
// lock, shared by concurrent worktree sessions; a named layer's own lock,
// shared with every project that extends it and with `byre credentials
// --layer`). No guard -- the global editor -- writes directly; the drift
// check still applies, it is just not serialized.
// The new baseline is captured INSIDE the guard, immediately after the write
// that established it. Re-reading after the lock releases reopens the very
// hole drift detection closes: another session's write can land in the gap,
// become this session's baseline, and the NEXT save then sees no drift and
// reconciles over it silently (grok, on the round after the open-time
// dual-read fix -- the same bound, one call site over).
func (m *model) write(cfg config.Config, force bool) error {
	do := func() error {
		if err := Save(m.filePath, m.followFile, cfg, m.saveBase, m.saveBaseErr, force); err != nil {
			return err
		}
		m.saveBase, m.saveBaseErr = hostopen.ReadFileBounded(m.filePath, m.followFile, config.MaxConfigBytes)
		return nil
	}
	if m.guard == nil {
		return do()
	}
	return m.guard(do)
}

// assemble builds a config from the working state onto a copy of the original,
// so untouched fields (raw blocks, volumes, files) are preserved exactly.
func (m model) assemble() config.Config {
	out := m.base
	out.Base = strings.TrimSpace(m.ti.Value())
	out.SeedPrefs = seedPrefsValue(m.seedPrefsSel)
	// worktree_base is only editable in the global editor; elsewhere it round-trips
	// via m.base untouched. Sibling checkbox wins; else the base path; else unset.
	if m.target == TargetGlobal {
		out.Defaults.SkipQuestions = m.skipQuestions
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
		out.Extends = config.FromNone(m.extOpts[m.extSel])
	}
	out.Template = fromScalar(m.tmplOpts, m.tmplSel, noneOption, m.tmplStored)
	out.Agent = fromScalar(m.agentOpts, m.agentSel, noneOption, m.agentStored)
	out.Engine = fromScalar(m.engineOpts, m.engineSel, "auto", m.engineStored)
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
	if len(m.hostEnv) == 0 {
		out.EnvFromHost = nil
	} else {
		he := make(map[string]string, len(m.hostEnv))
		for _, kv := range m.hostEnv {
			he[kv.Key] = kv.Value
		}
		out.EnvFromHost = he
	}
	if len(m.files) == 0 {
		out.Files = nil
	} else {
		files := make(map[string]string, len(m.files))
		for _, kv := range m.files {
			files[kv.Key] = kv.Value // last wins on a duplicate source
		}
		out.Files = files
	}
	out.Mounts = append([]config.Mount{}, m.mounts...)
	if len(out.Mounts) == 0 {
		out.Mounts = nil
	}
	out.Volumes = append([]config.Volume{}, m.volumes...)
	if len(out.Volumes) == 0 {
		out.Volumes = nil
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
	out.Contexts = append([]config.ContextDecl{}, m.contexts...)
	if len(out.Contexts) == 0 {
		out.Contexts = nil
	}
	// The primary agent is implied by `agent`, so never write it into `skills`
	// (even if it lingers in m.skills from a config that listed it before it became
	// primary) — the locked row shows it on via the agent, not via this list.
	// EXCEPT in the --global editor: there `agent` is an onboarding favourite
	// that enables nothing, so a skills entry naming it is the user's real
	// (and only) way to enable that skill machine-wide — stripping it made
	// the choice silently impossible.
	primaryAgent := m.agentNow()
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
	return nonEmptyLines(text)
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
	for _, kv := range m.files {
		parts = append(parts, "file:"+kv.Key+"="+kv.Value)
	}
	for _, kv := range m.hostEnv {
		parts = append(parts, "hostenv:"+kv.Key+"="+kv.Value)
	}
	for _, mt := range m.mounts {
		parts = append(parts, "mnt:"+mountLine(mt))
	}
	for _, v := range m.volumes {
		// volumeLine flags scope and seed, so an edit that only preserves them
		// still signs identically -- which is right: nothing changed.
		parts = append(parts, "vol:"+volumeLine(v))
	}
	for _, pt := range m.ports {
		// portLine renders the effective binding, which a removal marker
		// doesn't have — sign the marker distinctly or swapping a marker for
		// the real binding it removes would read as clean.
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
	for _, cd := range m.contexts {
		// The full text signs (not just the summary line): a prose edit via
		// $EDITOR must flip dirty even when the first line didn't change.
		parts = append(parts, "ctx:"+cd.Name+""+cd.File+""+cd.Text)
	}
	parts = append(parts, "skills:"+strings.Join(m.skills, ","))
	parts = append(parts, "ra:"+m.runArgs, "pre:"+m.dfPre, "post:"+m.dfPost)
	parts = append(parts, fmt.Sprintf("wt:%v/%s", m.wtSibling, m.wtBase.Value()))
	// Every checkbox assemble() writes has to sign, or its toggle is invisible
	// to dirty(): esc quits without the discard confirm, ctrl+e reloads the
	// file over the change, and the footer claims saved when nothing was.
	parts = append(parts, fmt.Sprintf("skipq:%v", m.skipQuestions))
	parts = append(parts, "seedprefs:"+seedPrefsOpts[m.seedPrefsSel])
	return strings.Join(parts, "\x00")
}

func (m model) dirty() bool { return m.sig() != m.savedSig }
