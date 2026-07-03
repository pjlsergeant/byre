package configui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"byre/internal/config"
	"byre/internal/gen"
)

// Run shows the interactive editor for cfg and returns whether the config was
// saved to filePath at least once (false = the user quit without saving, so the
// file is untouched). templates and agents populate the pickers. Saving happens
// inside the UI (explicit ctrl+s), so the user can edit, save, and keep editing;
// quitting never writes.
func Run(title, filePath string, cfg config.Config, templates, agents, skillOpts []string, vols VolumeAdmin) (bool, error) {
	m := newModel(title, filePath, cfg, templates, agents, skillOpts, vols)
	fm, err := tea.NewProgram(m).Run()
	if err != nil {
		return false, err
	}
	return fm.(model).savedOnce, nil
}

// fieldID identifies one editable row, in focus order.
type fieldID int

const (
	fBase fieldID = iota
	fTemplate
	fAgent
	fEngine
	fApt
	fEnv
	fMounts
	fVolumes
	fRunArgs
	fDockerfilePre
	fDockerfilePost
	fDockerfile
	fPorts
	fSkills
	fWorktreeSibling // checkbox: worktrees beside the repo
	fWorktreeBase    // text: base dir for worktrees (when not sibling)
)

// section groups fields under a header in the form (grants foregrounded).
type section struct {
	title  string
	fields []fieldID
}

// textFields are edited in the multi-line textarea overlay (freeform text).
func isTextField(f fieldID) bool {
	return f == fRunArgs || f == fDockerfilePre || f == fDockerfilePost || f == fDockerfile
}

func isListField(f fieldID) bool { return f == fApt || f == fEnv || f == fMounts || f == fPorts }

// Labels are human/display names (not the raw TOML keys); the underlying key is
// shown as a hint when editing the raw text blocks.
var fieldLabel = map[fieldID]string{
	fBase:            "Base image",
	fTemplate:        "Template",
	fAgent:           "Pri. Agent",
	fEngine:          "Engine",
	fApt:             "Packages",
	fEnv:             "Env vars",
	fMounts:          "Extra mounts",
	fPorts:           "Ports",
	fVolumes:         "Volumes",
	fRunArgs:         "Run args",
	fDockerfilePre:   "Dockerfile before",
	fDockerfilePost:  "Dockerfile after",
	fDockerfile:      "Dockerfile",
	fSkills:          "Skills",
	fWorktreeSibling: "Worktree loc",
	fWorktreeBase:    "Base path",
}

// rawFieldKey is the TOML key behind a raw text field, shown as a hint in its
// editor so the human label stays discoverable as a config key.
var rawFieldKey = map[fieldID]string{
	fRunArgs:        "run_args",
	fDockerfilePre:  "dockerfile_pre",
	fDockerfilePost: "dockerfile_post",
	fDockerfile:     "dockerfile",
}

// labelWidth is the padded width of the label column ("Dockerfile before" is longest).
const labelWidth = 17

// envKeyRe is the accepted shape of an env var name (POSIX-ish).
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	focusStyle = lipgloss.NewStyle().Bold(true)
	selStyle   = lipgloss.NewStyle().Reverse(true)            // chosen option, unfocused row
	selFocus   = lipgloss.NewStyle().Reverse(true).Bold(true) // chosen option, focused row
	dimStyle   = lipgloss.NewStyle().Faint(true)
	errStyle   = lipgloss.NewStyle().Bold(true)
)

// uiMode is the current screen: the field form, a list field's item browser, or
// a single-item add/edit editor.
type uiMode int

const (
	modeForm uiMode = iota
	modeList
	modeItem
	modeVolumes
	modeText
	modeSkills
)

type kvItem struct{ Key, Value string }

// VolumeAdmin lets the editor show a project's volumes and clear them ad hoc.
// It's engine-backed, so the commands layer supplies it; a nil VolumeAdmin hides
// the Volumes section (e.g. the global config, which has no project volumes).
type VolumeAdmin interface {
	List() ([]VolumeStatus, error)
	Clear(name string) error // remove the volume from the engine (refuses if a session is live)
	// SharedNote returns a blast-radius warning to show before a clear (e.g. a
	// worktree whose volumes are shared across the repo family), or "" if none.
	SharedNote() string
}

// VolumeStatus is one project volume for display.
type VolumeStatus struct {
	Name   string // logical name (e.g. ".claude")
	Role   string // "state" | "cache"
	Target string // mount point inside the box
	Exists bool   // whether the engine volume currently exists on disk
}

type model struct {
	title, filePath string
	base            config.Config // original, so untouched fields round-trip

	// The discovered lists, kept so state can be rebuilt after an external
	// ($EDITOR) edit reloads the file.
	templates, agents, skillOpts []string

	vols     VolumeAdmin // nil = no Volumes section
	sections []section   // rendered groups (Grants / Build / Advanced)
	order    []fieldID   // flattened focus order across all sections

	ti        textinput.Model // base image editor
	wtBase    textinput.Model // worktree base-path editor (fWorktreeBase)
	wtSibling bool            // fWorktreeSibling checkbox: worktrees beside the repo

	tmplOpts, agentOpts, engineOpts []string
	tmplSel, agentSel, engineSel    int

	// Structured working state for the list fields.
	apt    []string
	env    []kvItem
	mounts []config.Mount
	ports  []config.Port
	skills []string // enabled skill names (multi-select)

	// Freeform raw-tier working state (edited as text blocks).
	runArgs    string // one arg per line
	dfPre      string // dockerfile_pre lines
	dfPost     string // dockerfile_post lines
	dockerfile string // full-Dockerfile opt-out path

	savedSig  string
	savedOnce bool

	mode  uiMode
	focus int // form row (modeForm)

	// modeList
	listField fieldID
	listCur   int // 0..len(items); the last index is the "+ add" row

	// modeVolumes
	volList      []VolumeStatus
	volCur       int
	volPendClear int // index awaiting a clear-confirm, or -1
	volErr       string

	// modeSkills (multi-select)
	skillCur int

	// modeText (freeform text-block editor)
	ta        textarea.Model
	textField fieldID

	// modeItem
	inputs      []textinput.Model
	inputLabels []string
	itemFocus   int  // 0..len(inputs)-1, then the mode picker if itemHasMode
	itemHasMode bool // mounts: a ro/rw picker after the inputs
	itemMode    int  // 0 = ro, 1 = rw
	editIndex   int  // -1 = adding a new item
	itemErr     string

	width       int
	errMsg      string
	status      string
	confirmQuit bool
}

func newModel(title, filePath string, cfg config.Config, templates, agents, skillOpts []string, vols VolumeAdmin) model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	wtBase := textinput.New()
	wtBase.Prompt = ""
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.SetWidth(76)
	ta.SetHeight(10)

	// Grants lead (security-weighty: what the box can reach), then Build, then the
	// Advanced escape hatches. Volumes sits in Advanced, and only when engine-backed.
	// The full-Dockerfile opt-out (config `dockerfile`) is deliberately NOT here —
	// it's a rare, whole-image escape hatch, edited in the file / via ctrl+e; its
	// value still round-trips untouched through save.
	advanced := []fieldID{fRunArgs, fDockerfilePre, fDockerfilePost}
	if vols != nil {
		advanced = append(advanced, fVolumes)
	}
	sections := []section{
		{"GRANTS — what this box can reach", []fieldID{fMounts, fPorts, fEnv}},
		{"BUILD — how the box is made", []fieldID{fBase, fTemplate, fAgent, fEngine, fApt, fSkills}},
		{"WORKTREES — where `byre worktree` creates them", []fieldID{fWorktreeSibling, fWorktreeBase}},
		{"ADVANCED", advanced},
	}
	var order []fieldID
	for _, s := range sections {
		order = append(order, s.fields...)
	}

	m := model{
		title:        title,
		filePath:     filePath,
		templates:    templates,
		agents:       agents,
		skillOpts:    skillOpts,
		vols:         vols,
		sections:     sections,
		order:        order,
		ti:           ti,
		wtBase:       wtBase,
		ta:           ta,
		width:        80,
		volPendClear: -1,
	}
	return m.loadConfig(cfg)
}

// loadConfig (re)initializes the editable working state from cfg, preserving the
// discovered template/agent lists. Used both at open and after an external
// ($EDITOR) edit reloads the file from disk.
//
// A configured value that isn't in the discovered/known set (a not-installed
// template/agent, an unusual engine) is preserved as an option, so opening the
// editor and saving unrelated edits never silently rewrites it; a truly invalid
// value surfaces via Save's validation rather than being coerced.
func (m model) loadConfig(cfg config.Config) model {
	m.base = cfg
	m.ti.SetValue(cfg.Base)
	m.tmplOpts = pickerOpts(m.templates, cfg.Template)
	m.agentOpts = pickerOpts(m.agents, cfg.Agent)
	m.engineOpts = []string{"auto", "docker", "podman"}
	if cfg.Engine != "" && !contains(m.engineOpts, cfg.Engine) {
		m.engineOpts = append(m.engineOpts, cfg.Engine)
	}
	m.tmplSel = indexOf(m.tmplOpts, orNone(cfg.Template))
	m.agentSel = indexOf(m.agentOpts, orNone(cfg.Agent))
	m.engineSel = indexOf(m.engineOpts, orDefault(cfg.Engine, "auto"))
	m.apt = append([]string{}, cfg.Apt...)
	m.env = envItems(cfg.Env)
	m.mounts = append([]config.Mount{}, cfg.Mounts...)
	m.ports = append([]config.Port{}, cfg.Ports...)
	m.skills = append([]string{}, cfg.Skills...)
	m.runArgs = strings.Join(cfg.RunArgs, "\n")
	m.dfPre = strings.Join(cfg.DockerfilePre, "\n")
	m.dfPost = strings.Join(cfg.DockerfilePost, "\n")
	m.dockerfile = cfg.Dockerfile
	// worktree_base is a 3-state choice: "sibling" (checkbox on), a path (checkbox
	// off, path set), or unset (checkbox off, path empty -> byre worktree refuses).
	switch v := strings.TrimSpace(cfg.WorktreeBase); v {
	case "sibling":
		m.wtSibling = true
		m.wtBase.SetValue("")
	case "":
		m.wtSibling = false
		m.wtBase.SetValue("")
	default:
		m.wtSibling = false
		m.wtBase.SetValue(v)
	}
	m.savedSig = m.sig()
	return m
}

// envItems converts the config env map into a stable, sorted-by-key slice for
// ordered editing.
func envItems(m map[string]string) []kvItem {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kvItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, kvItem{Key: k, Value: m[k]})
	}
	return out
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case editorClosedMsg:
		return m.onEditorClosed(msg.err), nil
	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeItem:
			return m.updateItem(msg)
		case modeVolumes:
			return m.updateVolumes(msg)
		case modeText:
			return m.updateText(msg)
		case modeSkills:
			return m.updateSkills(msg)
		default:
			return m.updateForm(msg)
		}
	}
	// Non-key messages (cursor blink) go to whichever editor is live.
	var cmd tea.Cmd
	switch {
	case m.mode == modeText:
		m.ta, cmd = m.ta.Update(msg)
	case m.mode == modeItem && len(m.inputs) > 0 && m.itemFocus < len(m.inputs):
		m.inputs[m.itemFocus], cmd = m.inputs[m.itemFocus].Update(msg)
	case m.mode == modeForm && m.field() == fBase:
		m.ti, cmd = m.ti.Update(msg)
	case m.mode == modeForm && m.field() == fWorktreeBase:
		m.wtBase, cmd = m.wtBase.Update(msg)
	}
	return m, cmd
}

// ---- form screen -----------------------------------------------------------

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key != "esc" {
		m.confirmQuit = false
	}
	switch key {
	case "ctrl+c", "esc":
		if m.dirty() && !m.confirmQuit {
			m.confirmQuit = true // View shows the confirm prompt
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+s":
		return m.save(), nil
	case "ctrl+e":
		// Drop into $EDITOR on the whole config file (the way to reach the raw
		// tier the UI doesn't edit). Require a clean state first: $EDITOR sees the
		// on-disk file, so unsaved structured edits would be lost or clobbered.
		if m.dirty() {
			m.errMsg = "save (ctrl+s) or discard changes before editing the file in $EDITOR"
			m.confirmQuit = false
			return m, nil
		}
		m.errMsg = ""
		return m, openEditor(m.filePath)
	case "up", "shift+tab":
		m.setFocus(m.focus - 1)
		m.status = ""
		return m, nil
	case "down", "tab":
		m.setFocus(m.focus + 1)
		m.status = ""
		return m, nil
	case "left":
		m.cycle(-1)
		return m, nil
	case "right":
		m.cycle(1)
		return m, nil
	case "enter":
		switch f := m.field(); {
		case isListField(f):
			m.listField = f
			m.listCur = 0
			m.mode = modeList
			m.status = ""
		case f == fVolumes:
			return m.openVolumes(), nil
		case f == fSkills:
			m.skillCur = 0
			m.mode = modeSkills
			m.status = ""
		case isTextField(f):
			return m.openText(f), textarea.Blink
		case f == fWorktreeSibling:
			m.wtSibling = !m.wtSibling
		}
		return m, nil
	}
	if m.field() == fBase {
		var cmd tea.Cmd
		m.ti, cmd = m.ti.Update(msg)
		return m, cmd
	}
	if m.field() == fWorktreeBase {
		var cmd tea.Cmd
		m.wtBase, cmd = m.wtBase.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) cycle(dir int) {
	switch m.field() {
	case fWorktreeSibling:
		m.wtSibling = !m.wtSibling
	case fBase:
		m.ti, _ = m.ti.Update(tea.KeyMsg{Type: keyArrow(dir)})
	case fTemplate:
		m.tmplSel = wrap(m.tmplSel+dir, len(m.tmplOpts))
	case fAgent:
		m.agentSel = wrap(m.agentSel+dir, len(m.agentOpts))
	case fEngine:
		m.engineSel = wrap(m.engineSel+dir, len(m.engineOpts))
	}
}

// ---- list screen (browse a field's items) ----------------------------------

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.itemLines(m.listField)
	addRow := len(items) // index of the "+ add" pseudo-row
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeForm
		return m, nil
	case "up", "shift+tab":
		m.listCur = wrap(m.listCur-1, addRow+1)
		return m, nil
	case "down", "tab":
		m.listCur = wrap(m.listCur+1, addRow+1)
		return m, nil
	case "a":
		return m.startItem(-1), nil
	case "d", "x":
		if m.listCur < addRow {
			m.deleteItem(m.listField, m.listCur)
			if m.listCur > 0 && m.listCur >= len(m.itemLines(m.listField)) {
				m.listCur--
			}
		}
		return m, nil
	case "enter":
		if m.listCur == addRow {
			return m.startItem(-1), nil
		}
		return m.startItem(m.listCur), nil
	}
	return m, nil
}

func (m *model) deleteItem(f fieldID, i int) {
	switch f {
	case fApt:
		m.apt = append(m.apt[:i], m.apt[i+1:]...)
	case fEnv:
		m.env = append(m.env[:i], m.env[i+1:]...)
	case fMounts:
		m.mounts = append(m.mounts[:i], m.mounts[i+1:]...)
	case fPorts:
		m.ports = append(m.ports[:i], m.ports[i+1:]...)
	}
}

// ---- item screen (add / edit one item) -------------------------------------

// startItem opens the item editor for the current list field. idx < 0 adds a new
// item; otherwise it edits the existing one at idx.
func (m model) startItem(idx int) model {
	m.editIndex = idx
	m.itemErr = ""
	m.itemFocus = 0
	m.itemHasMode = false
	m.itemMode = 0
	switch m.listField {
	case fApt:
		m.inputLabels = []string{"Package"}
		v := ""
		if idx >= 0 {
			v = m.apt[idx]
		}
		m.inputs = []textinput.Model{newInput(v)}
	case fEnv:
		m.inputLabels = []string{"Key", "Value"}
		k, val := "", ""
		if idx >= 0 {
			k, val = m.env[idx].Key, m.env[idx].Value
		}
		m.inputs = []textinput.Model{newInput(k), newInput(val)}
	case fMounts:
		m.inputLabels = []string{"Host path", "Target (in box)"}
		host, target := "", ""
		if idx >= 0 {
			host, target = m.mounts[idx].Host, m.mounts[idx].Target
			if m.mounts[idx].Mode == "rw" {
				m.itemMode = 1
			}
		}
		m.inputs = []textinput.Model{newInput(host), newInput(target)}
		m.itemHasMode = true
	case fPorts:
		m.inputLabels = []string{"Container port", "Host port (blank = same)", "Interface (blank = 127.0.0.1)"}
		container, host, iface := "", "", ""
		if idx >= 0 {
			p := m.ports[idx]
			container = strconv.Itoa(p.Container)
			if p.Host != 0 {
				host = strconv.Itoa(p.Host)
			}
			iface = p.Interface
		}
		m.inputs = []textinput.Model{newInput(container), newInput(host), newInput(iface)}
	}
	m.focusItem(0)
	m.mode = modeItem
	return m
}

func newInput(v string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(v)
	return ti
}

// itemFocusables is the number of focusable controls in the item editor (inputs,
// plus the ro/rw picker for mounts).
func (m model) itemFocusables() int {
	n := len(m.inputs)
	if m.itemHasMode {
		n++
	}
	return n
}

func (m *model) focusItem(i int) {
	m.itemFocus = wrap(i, m.itemFocusables())
	for j := range m.inputs {
		if j == m.itemFocus {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
}

func (m *model) onModePicker() bool { return m.itemHasMode && m.itemFocus == len(m.inputs) }

func (m model) updateItem(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeList
		return m, nil
	case "enter", "ctrl+s":
		return m.commitItem(), nil
	case "tab", "down":
		m.focusItem(m.itemFocus + 1)
		return m, nil
	case "shift+tab", "up":
		m.focusItem(m.itemFocus - 1)
		return m, nil
	case "left":
		if m.onModePicker() {
			m.itemMode = wrap(m.itemMode-1, 2)
			return m, nil
		}
	case "right":
		if m.onModePicker() {
			m.itemMode = wrap(m.itemMode+1, 2)
			return m, nil
		}
		// At the end of an input with a live suggestion, → accepts it (host-path
		// completion or the derived target); otherwise it's a normal cursor move.
		if full := m.suggestion(); full != "" && m.atInputEnd() {
			m.inputs[m.itemFocus].SetValue(full)
			m.inputs[m.itemFocus].CursorEnd()
			return m, nil
		}
	}
	if !m.onModePicker() && m.itemFocus < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.itemFocus], cmd = m.inputs[m.itemFocus].Update(msg)
		return m, cmd
	}
	return m, nil
}

// atInputEnd reports whether the focused input's cursor is at the end, so → can
// mean "accept suggestion" rather than "move cursor right".
func (m model) atInputEnd() bool {
	if m.itemFocus >= len(m.inputs) {
		return false
	}
	in := m.inputs[m.itemFocus]
	return in.Position() >= len([]rune(in.Value()))
}

// suggestion returns the full suggested value for the focused input (the ghost is
// the part beyond what's typed). Mounts only: the host input gets filesystem
// completion; the target input, while empty, gets a path derived from the host.
func (m model) suggestion() string {
	if m.listField != fMounts || m.onModePicker() || m.itemFocus >= len(m.inputs) {
		return ""
	}
	switch m.itemFocus {
	case 0:
		return completeHostPath(m.inputs[0].Value())
	case 1:
		if strings.TrimSpace(m.inputs[1].Value()) != "" {
			return ""
		}
		return suggestTarget(m.inputs[0].Value())
	}
	return ""
}

// ghostSuffix is the un-typed remainder of the current suggestion, shown dimmed
// after the focused input.
func (m model) ghostSuffix() string {
	full := m.suggestion()
	cur := m.inputs[m.itemFocus].Value()
	if full != "" && strings.HasPrefix(full, cur) {
		return full[len(cur):]
	}
	return ""
}

// completeHostPath returns val extended to the longest unambiguous host-filesystem
// completion (dir-aware; a sole directory match gains a trailing "/"), or "" when
// there's nothing to add. Runs on the host, where byre config is launched, so the
// paths it completes are the real mount sources.
func completeHostPath(val string) string {
	if val == "" {
		return ""
	}
	if val == "~" {
		return "~/"
	}
	exp := expandTilde(val)
	var dir, prefix string
	if strings.HasSuffix(val, "/") {
		dir, prefix = exp, ""
	} else {
		dir, prefix = filepath.Dir(exp), filepath.Base(exp)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	var sole os.DirEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
			sole = e
		}
	}
	if len(names) == 0 {
		return ""
	}
	common := longestCommonPrefix(names)
	if len(common) < len(prefix) {
		return ""
	}
	completed := val + common[len(prefix):]
	if len(names) == 1 && sole.IsDir() && !strings.HasSuffix(completed, "/") {
		completed += "/"
	}
	if completed == val {
		return ""
	}
	return completed
}

// suggestTarget proposes an in-box mount target from a host path: a home-relative
// source mirrors under /home/dev (so dotfiles/config land where the agent looks),
// anything else goes to /home/dev/<basename>.
func suggestTarget(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	exp := filepath.Clean(expandTilde(host))
	base := filepath.Base(exp)
	if base == "" || base == "/" || base == "." {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, exp); err == nil &&
			rel != "." && !strings.HasPrefix(rel, "..") {
			return "/home/dev/" + filepath.ToSlash(rel)
		}
	}
	return "/home/dev/" + base
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// commitItem validates the item editor's inputs and writes the item back into
// the working slice (append when adding, replace when editing). A validation
// error keeps the editor open with a message.
func (m model) commitItem() model {
	switch m.listField {
	case fApt:
		pkg := strings.TrimSpace(m.inputs[0].Value())
		if pkg == "" {
			m.itemErr = "package name can't be empty"
			return m
		}
		m.apt = putAt(m.apt, m.editIndex, pkg)
	case fEnv:
		k := strings.TrimSpace(m.inputs[0].Value())
		if !envKeyRe.MatchString(k) {
			m.itemErr = "key must match [A-Za-z_][A-Za-z0-9_]*"
			return m
		}
		// Reject a duplicate key (other than the row being edited): env is a map on
		// disk, so two rows with the same key would silently collapse on save.
		for i, kv := range m.env {
			if i != m.editIndex && kv.Key == k {
				m.itemErr = "duplicate key " + k
				return m
			}
		}
		m.env = putAt(m.env, m.editIndex, kvItem{Key: k, Value: m.inputs[1].Value()})
	case fMounts:
		host := strings.TrimSpace(m.inputs[0].Value())
		target := strings.TrimSpace(m.inputs[1].Value())
		if host == "" || target == "" {
			m.itemErr = "host and target are both required"
			return m
		}
		if !strings.HasPrefix(target, "/") {
			m.itemErr = "target must be an absolute path in the box (start with /)"
			return m
		}
		mode := "ro"
		if m.itemMode == 1 {
			mode = "rw"
		}
		m.mounts = putAt(m.mounts, m.editIndex, config.Mount{Host: host, Target: target, Mode: mode})
	case fPorts:
		container, err := strconv.Atoi(strings.TrimSpace(m.inputs[0].Value()))
		if err != nil || container < 1 || container > 65535 {
			m.itemErr = "container port must be a number 1-65535"
			return m
		}
		host := 0
		if hs := strings.TrimSpace(m.inputs[1].Value()); hs != "" {
			h, herr := strconv.Atoi(hs)
			if herr != nil || h < 1 || h > 65535 {
				m.itemErr = "host port must be blank (any) or a number 1-65535"
				return m
			}
			host = h
		}
		m.ports = putAt(m.ports, m.editIndex, config.Port{
			Container: container,
			Host:      host,
			Interface: strings.TrimSpace(m.inputs[2].Value()),
		})
	}
	m.mode = modeList
	return m
}

// putAt appends v when idx < 0, else replaces the element at idx.
func putAt[T any](s []T, idx int, v T) []T {
	if idx < 0 {
		return append(s, v)
	}
	s[idx] = v
	return s
}

// ---- volumes screen (show + ad-hoc clear) ----------------------------------

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
			name := m.volList[m.volPendClear].Name
			m.volPendClear = -1
			if err := m.vols.Clear(name); err != nil {
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

	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeForm
		return m, nil
	case "up", "shift+tab":
		if len(m.volList) > 0 {
			m.volCur = wrap(m.volCur-1, len(m.volList))
		}
		return m, nil
	case "down", "tab":
		if len(m.volList) > 0 {
			m.volCur = wrap(m.volCur+1, len(m.volList))
		}
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
	for i, v := range m.volList {
		cursor := "  "
		if i == m.volCur {
			cursor = focusStyle.Render("▸ ")
		}
		state := dimStyle.Render("empty")
		if v.Exists {
			state = "present"
		}
		line := fmt.Sprintf("%-14s %-6s %-24s %s", v.Name, v.Role, v.Target, state)
		if i == m.volCur {
			line = focusStyle.Render(line)
		}
		fmt.Fprintf(&b, "%s%s\n", cursor, line)
	}
	// Description of the highlighted volume.
	if m.volCur < len(m.volList) {
		if d := volDescription(m.volList[m.volCur].Role); d != "" {
			b.WriteString("\n" + dimStyle.Render(d) + "\n")
		}
	}

	b.WriteString("\n")
	switch {
	case m.volPendClear >= 0:
		msg := fmt.Sprintf("Clear %q? This deletes the volume and its data.", m.volList[m.volPendClear].Name)
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

func volDescription(role string) string {
	switch role {
	case "state":
		return "state — persists across rebuilds (agent auth, history, config). Clearing forces a re-login / re-init on the next develop."
	case "cache":
		return "cache — disposable, rebuilt on demand (e.g. node_modules). Clearing just frees space."
	}
	return ""
}

// ---- text-block screen (raw freeform fields) -------------------------------

// openText opens the multi-line editor for a raw text field (run_args /
// dockerfile_pre|post as one-per-line, or the Dockerfile opt-out path).
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
	case fDockerfile:
		return m.dockerfile
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
	case fDockerfile:
		m.dockerfile = strings.TrimSpace(v)
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
	if m.textField == fDockerfile {
		hint = "a project-relative path; byre stops generating and builds this file"
	}
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

// ---- skills screen (multi-select) ------------------------------------------

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
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeForm
		return m, nil
	case "up", "shift+tab":
		if len(entries) > 0 {
			m.skillCur = wrap(m.skillCur-1, len(entries))
		}
		m.status = ""
		return m, nil
	case "down", "tab":
		if len(entries) > 0 {
			m.skillCur = wrap(m.skillCur+1, len(entries))
		}
		m.status = ""
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
		cursor := "  "
		if i == m.skillCur {
			cursor = focusStyle.Render("▸ ")
			line = focusStyle.Render(line)
		}
		fmt.Fprintf(&b, "%s%s\n", cursor, line)
	}

	if m.status != "" {
		b.WriteString("\n" + dimStyle.Render(m.status))
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ move · space toggle · esc back"))
	return b.String()
}

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
	// worktree_base: sibling checkbox wins; else the base path; else unset (refuse).
	switch {
	case m.wtSibling:
		out.WorktreeBase = "sibling"
	default:
		out.WorktreeBase = strings.TrimSpace(m.wtBase.Value())
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
	if strings.TrimSpace(m.dockerfile) == m.base.Dockerfile {
		out.Dockerfile = m.base.Dockerfile
	} else {
		out.Dockerfile = strings.TrimSpace(m.dockerfile)
	}
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
	parts = append(parts, "ra:"+m.runArgs, "pre:"+m.dfPre, "post:"+m.dfPost, "df:"+m.dockerfile)
	parts = append(parts, fmt.Sprintf("wt:%v/%s", m.wtSibling, m.wtBase.Value()))
	return strings.Join(parts, "\x00")
}

func (m model) dirty() bool { return m.sig() != m.savedSig }

func (m model) field() fieldID { return m.order[m.focus] }

func (m *model) setFocus(i int) {
	m.focus = wrap(i, len(m.order))
	f := m.field()
	if f == fBase {
		m.ti.Focus()
	} else {
		m.ti.Blur()
	}
	if f == fWorktreeBase {
		m.wtBase.Focus()
	} else {
		m.wtBase.Blur()
	}
}

func (m model) itemLines(f fieldID) []string {
	switch f {
	case fApt:
		return m.apt
	case fEnv:
		out := make([]string, len(m.env))
		for i, kv := range m.env {
			out[i] = kv.Key + "=" + kv.Value
		}
		return out
	case fMounts:
		out := make([]string, len(m.mounts))
		for i, mt := range m.mounts {
			out[i] = mountLine(mt)
		}
		return out
	case fPorts:
		out := make([]string, len(m.ports))
		for i, pt := range m.ports {
			out[i] = portLine(pt)
		}
		return out
	}
	return nil
}

func mountLine(mt config.Mount) string {
	mode := mt.Mode
	if mode == "" {
		mode = "ro"
	}
	return fmt.Sprintf("%s -> %s (%s)", mt.Host, mt.Target, mode)
}

func portLine(p config.Port) string {
	iface := p.Interface
	if iface == "" {
		iface = "127.0.0.1"
	}
	host := p.Host
	if host == 0 {
		host = p.Container // blank host mirrors the container port
	}
	return fmt.Sprintf("%s:%d -> %d", iface, host, p.Container)
}

// ---- rendering -------------------------------------------------------------

func (m model) View() string {
	switch m.mode {
	case modeList:
		return m.viewList()
	case modeItem:
		return m.viewItem()
	case modeVolumes:
		return m.viewVolumes()
	case modeText:
		return m.viewText()
	case modeSkills:
		return m.viewSkills()
	default:
		return m.viewForm()
	}
}

func (m model) viewForm() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(m.title))

	focusedField := m.field()
	for _, s := range m.sections {
		fmt.Fprintf(&b, "%s\n", dimStyle.Render(s.title))
		for _, f := range s.fields {
			focused := f == focusedField
			cursor := "  "
			if focused {
				cursor = focusStyle.Render("▸ ")
			}
			label := fmt.Sprintf("%-*s", labelWidth, fieldLabel[f])
			if focused {
				label = focusStyle.Render(label)
			}
			fmt.Fprintf(&b, "%s%s : %s\n", cursor, label, m.renderValue(f, focused))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	switch {
	case m.confirmQuit:
		b.WriteString(errStyle.Render("● Unsaved changes — press esc again to discard, or ctrl+s to save"))
	case m.errMsg != "":
		b.WriteString(errStyle.Render("✗ " + m.errMsg))
	case m.dirty():
		b.WriteString(errStyle.Render("● Unsaved changes") + dimStyle.Render("  (ctrl+s to save)"))
	case m.status != "":
		b.WriteString(dimStyle.Render(m.status))
	default:
		b.WriteString(dimStyle.Render("No unsaved changes"))
	}
	b.WriteString("\n")

	b.WriteString("\n" + dimStyle.Render("Saves to: "+m.filePath))
	b.WriteString("\n" + dimStyle.Render("↑↓ move · ←→ change · ↵ open · ^s save · ^e $EDITOR · esc quit"))
	return b.String()
}

func (m model) renderValue(f fieldID, focused bool) string {
	switch f {
	case fBase:
		if focused {
			return m.ti.View()
		}
		if v := strings.TrimSpace(m.ti.Value()); v != "" {
			return v
		}
		return dimStyle.Render("(defaults to " + gen.DefaultBase + ")")
	case fWorktreeSibling:
		box := "[ ]"
		if m.wtSibling {
			box = "[x]"
		}
		s := box + " sibling of repo"
		if focused {
			s += dimStyle.Render("  (←/→ or enter to toggle)")
		}
		return s
	case fWorktreeBase:
		if m.wtSibling {
			return dimStyle.Render("(using sibling)")
		}
		if focused {
			return m.wtBase.View()
		}
		if v := strings.TrimSpace(m.wtBase.Value()); v != "" {
			return v
		}
		return dimStyle.Render("(unset — byre worktree will refuse)")
	case fTemplate:
		return renderSeg(m.tmplOpts, m.tmplSel, focused)
	case fAgent:
		return renderSeg(m.agentOpts, m.agentSel, focused)
	case fEngine:
		return renderSeg(m.engineOpts, m.engineSel, focused)
	case fVolumes:
		s := "view / clear" // an action row, not an empty value — don't dim it
		if focused {
			s += dimStyle.Render("  (enter)")
		}
		return s
	case fSkills:
		s := dimStyle.Render("(none)")
		if n := len(m.skills); n > 0 {
			s = fmt.Sprintf("%d enabled", n)
		}
		if focused {
			s += dimStyle.Render("  (enter to choose)")
		}
		return s
	case fDockerfile:
		if v := strings.TrimSpace(m.dockerfile); v != "" {
			return v
		}
		s := dimStyle.Render("(none — byre generates the Dockerfile)")
		if focused {
			s += dimStyle.Render("  (enter to edit)")
		}
		return s
	case fRunArgs, fDockerfilePre, fDockerfilePost:
		n := len(splitLines(m.textValue(f)))
		s := dimStyle.Render("(none)")
		if n == 1 {
			s = "1 line"
		} else if n > 1 {
			s = fmt.Sprintf("%d lines", n)
		}
		if focused {
			s += dimStyle.Render("  (enter to edit)")
		}
		return s
	default:
		n := len(m.itemLines(f))
		s := dimStyle.Render("(none)")
		if n == 1 {
			s = "1 item"
		} else if n > 1 {
			s = fmt.Sprintf("%d items", n)
		}
		if focused {
			s += dimStyle.Render("  (enter to edit)")
		}
		return s
	}
}

func (m model) viewList() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(fieldLabel[m.listField]))
	items := m.itemLines(m.listField)
	if len(items) == 0 {
		b.WriteString(dimStyle.Render("  (no items yet)\n"))
	}
	for i, line := range items {
		cursor := "  "
		if i == m.listCur {
			cursor = focusStyle.Render("▸ ")
			line = focusStyle.Render(line)
		}
		fmt.Fprintf(&b, "%s%s\n", cursor, line)
	}
	// The "+ add" row.
	addLine := "+ add " + fieldLabel[m.listField]
	if m.listCur == len(items) {
		fmt.Fprintf(&b, "%s%s\n", focusStyle.Render("▸ "), focusStyle.Render(addLine))
	} else {
		fmt.Fprintf(&b, "  %s\n", dimStyle.Render(addLine))
	}

	b.WriteString("\n" + dimStyle.Render("↑/↓ move · enter edit · a add · d delete · esc back"))
	return b.String()
}

func (m model) viewItem() string {
	var b strings.Builder
	verb := "Edit"
	if m.editIndex < 0 {
		verb = "Add"
	}
	fmt.Fprintf(&b, "%s\n\n", focusStyle.Render(verb+" "+strings.TrimSuffix(fieldLabel[m.listField], "s")))

	for i, in := range m.inputs {
		cursor := "  "
		val := in.View()
		if i == m.itemFocus {
			cursor = focusStyle.Render("▸ ")
			val += dimStyle.Render(m.ghostSuffix()) // autocomplete/suggestion ghost
		}
		label := fmt.Sprintf("%-*s", 16, m.inputLabels[i])
		fmt.Fprintf(&b, "%s%s: %s\n", cursor, label, val)
	}
	if m.itemHasMode {
		cursor := "  "
		if m.onModePicker() {
			cursor = focusStyle.Render("▸ ")
		}
		label := fmt.Sprintf("%-*s", 16, "Mode")
		fmt.Fprintf(&b, "%s%s: %s\n", cursor, label, renderSeg([]string{"ro", "rw"}, m.itemMode, m.onModePicker()))
	}

	if m.itemErr != "" {
		b.WriteString("\n" + errStyle.Render("✗ "+m.itemErr))
	}
	hint := "tab next · enter save · esc cancel"
	if m.listField == fMounts {
		hint = "tab next · → accept suggestion · ←/→ mode · enter save · esc cancel"
	}
	b.WriteString("\n\n" + dimStyle.Render(hint))
	return b.String()
}

// renderSeg renders a segmented picker: every option is bracketed, the chosen
// one is reverse-video (a monochrome-safe, non-color emphasis).
func renderSeg(opts []string, sel int, focused bool) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		seg := "[" + o + "]"
		if i == sel {
			if focused {
				seg = selFocus.Render(seg)
			} else {
				seg = selStyle.Render(seg)
			}
		}
		parts[i] = seg
	}
	return strings.Join(parts, " ")
}

// ---- option/value helpers --------------------------------------------------

const noneOption = "none"

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

func contains(opts []string, v string) bool {
	for _, o := range opts {
		if o == v {
			return true
		}
	}
	return false
}

func indexOf(opts []string, v string) int {
	for i, o := range opts {
		if o == v {
			return i
		}
	}
	return 0 // unreachable once the value is preserved as an option; safe default
}

func orNone(v string) string {
	if v == "" {
		return noneOption
	}
	return v
}

func fromNone(v string) string {
	if v == noneOption {
		return ""
	}
	return v
}

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

func keyArrow(dir int) tea.KeyType {
	if dir < 0 {
		return tea.KeyLeft
	}
	return tea.KeyRight
}

func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}
