// form.go owns the editor core: the model, field/mode enums, Run, the Update
// dispatch, and the main form screen (modeForm); the other modes live in their
// own files (listitem.go, volumes.go, skills.go, textblock.go, complete.go).
package configui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/packages"
)

// Run shows the interactive editor for cfg and returns whether the config was
// saved to filePath at least once (false = the user quit without saving, so the
// file is untouched). templates and agents populate the pickers. Saving happens
// inside the UI (explicit ctrl+s), so the user can edit, save, and keep editing;
// quitting never writes. prepare (nil = no-op) runs before the first write can
// happen — an explicit save or the $EDITOR round-trip — so the caller can defer
// creating the target's directory until the user actually commits: opening the
// editor and quitting must leave no trace.
func Run(title, filePath string, cfg config.Config, templates, agents, skillOpts []string, skillDescs map[string]string, inh Inherited, vols VolumeAdmin, target Target, prepare func() error) (bool, error) {
	m := newModel(title, filePath, cfg, templates, agents, skillOpts, skillDescs, inh, vols, target)
	m.prepare = prepare
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
	fEgress
	fMounts
	fVolumes
	fRunArgs
	fDockerfilePre
	fDockerfilePost
	fPorts
	fSkills
	fMCP             // [[mcp]] declarations (wiring, not grants — ADR 0033)
	fClaudeSkills    // [[claude_skills]] declarations (wiring, the MCP genus)
	fWorktreeSibling // checkbox: worktrees beside the repo
	fWorktreeBase    // text: base dir for worktrees (when not sibling)
	fExtends         // parent named layer (the extends chain pointer)
)

// Target says which kind of file this editor session edits: a project's
// byre.config, the global default.config, or a named layer's layer.config.
// The layer target is the project form minus the template picker (shape
// selection has one owner, the project config) and minus project volumes.
type Target int

const (
	TargetProject Target = iota
	TargetGlobal
	TargetLayer
)

// section groups fields under a header in the form (grants foregrounded).
type section struct {
	title  string
	fields []fieldID
}

// textFields are edited in the multi-line textarea overlay (freeform text).
func isTextField(f fieldID) bool {
	return f == fRunArgs || f == fDockerfilePre || f == fDockerfilePost
}

func isListField(f fieldID) bool {
	return f == fApt || f == fEnv || f == fMounts || f == fPorts || f == fEgress || f == fMCP || f == fClaudeSkills
}

// Labels are human/display names (not the raw TOML keys); the underlying key is
// shown as a hint when editing the raw text blocks.
var fieldLabel = map[fieldID]string{
	fBase:            "Base image",
	fTemplate:        "Template",
	fAgent:           "Pri. Agent",
	fEngine:          "Engine",
	fApt:             "Packages",
	fEnv:             "Env vars",
	fEgress:          "Egress",
	fMounts:          "Extra mounts",
	fPorts:           "Ports",
	fMCP:             "MCP servers",
	fClaudeSkills:    "Claude Skills",
	fVolumes:         "Volumes",
	fRunArgs:         "Run args",
	fDockerfilePre:   "Dockerfile before",
	fDockerfilePost:  "Dockerfile after",
	fSkills:          "Skills",
	fWorktreeSibling: "Worktree loc",
	fWorktreeBase:    "Base path",
	fExtends:         "Extends",
}

// rawFieldKey is the TOML key behind a raw text field, shown as a hint in its
// editor so the human label stays discoverable as a config key.
var rawFieldKey = map[fieldID]string{
	fRunArgs:        "run_args",
	fDockerfilePre:  "dockerfile_pre",
	fDockerfilePost: "dockerfile_post",
}

// labelWidth is the padded width of the label column ("Dockerfile before" is longest).
const labelWidth = 17

// accentColor is the ONE structural color: the cursor, section headers, and
// the focused picker selection. ANSI 4-bit, so the terminal theme picks the
// shade and monochrome terminals drop it cleanly. The other colors carry
// fixed semantics — red = errors, green = saved — and yellow stays reserved
// for warnStyle alone (cross-project reach must never blend in).
var accentColor = lipgloss.Color("6")

var (
	focusStyle  = lipgloss.NewStyle().Bold(true)
	accentStyle = lipgloss.NewStyle().Foreground(accentColor)
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	selStyle    = lipgloss.NewStyle().Reverse(true)                                    // chosen option, unfocused row
	selFocus    = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(accentColor) // chosen option, focused row
	dimStyle    = lipgloss.NewStyle().Faint(true)
	// errStyle is for BANNERS — the unsaved dot, destructive confirms, the
	// comment warning. Actual error messages get errTextStyle (bold red, via
	// errLine); on a monochrome terminal both degrade to the same bold.
	errStyle     = lipgloss.NewStyle().Bold(true)
	errTextStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	// warnStyle marks cross-project reach — the one thing in this UI that
	// escapes the current scope must not blend in (ANSI yellow, bold).
	warnStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
)

// uiMode is the current screen: the field form, a list field's item browser, or
// a single-item add/edit editor.
type uiMode int

const (
	modeForm uiMode = iota
	modeList
	modeItem
	modeMenu // per-row action menu over a list row (ADR 0018)
	modeVolumes
	modeText
	modeSkills
)

type kvItem struct{ Key, Value string }

type model struct {
	title, filePath string
	base            config.Config // original, so untouched fields round-trip

	// The discovered lists, kept so state can be rebuilt after an external
	// ($EDITOR) edit reloads the file.
	templates, agents, skillOpts []string
	// skillDescs maps a skill name to its one-line description (skill.toml
	// `description`), shown dimmed beside the name in the skills screen so
	// near-namesakes (claude vs claude-shared-auth) are tellable apart at the
	// point of choice. Missing entries render as just the name.
	skillDescs map[string]string
	// inh is the read-only provenance input (ADR 0018): the resolved lower
	// cascade per template plus skill runtime contributions, so every screen
	// can show effective state instead of lying with this layer's raw entries
	// (the editor edits one layer; effect is cascade-wide).
	inh Inherited

	vols     VolumeAdmin // nil = no Volumes section
	sections []section   // rendered groups (Grants / Build / Advanced)
	order    []fieldID   // flattened focus order across all sections

	// commentWarn: the loaded file has hand-written comments that a ^s
	// re-marshal would drop; shown persistently in the form footer (Q7).
	commentWarn bool

	ti        textinput.Model // base image editor
	wtBase    textinput.Model // worktree base-path editor (fWorktreeBase)
	wtSibling bool            // fWorktreeSibling checkbox: worktrees beside the repo
	target    Target          // which kind of file this session edits

	tmplOpts, agentOpts, engineOpts []string
	tmplSel, agentSel, engineSel    int
	extOpts                         []string // EXTENDS picker (named layers + none)
	extSel                          int

	// Structured working state for the list fields.
	apt          []string
	env          []kvItem
	mounts       []config.Mount
	ports        []config.Port
	egress       []string             // firewall-allowlist extensions, host[:port] (ADR 0019)
	mcps         []config.MCP         // [[mcp]] declarations incl. `!name` closure markers
	claudeSkills []config.ClaudeSkill // [[claude_skills]] declarations incl. `!name` closure markers
	skills       []string             // enabled skill names (multi-select)

	// Freeform raw-tier working state (edited as text blocks).
	runArgs string // one arg per line
	dfPre   string // dockerfile_pre lines
	dfPost  string // dockerfile_post lines

	savedSig  string
	savedOnce bool

	// prepare runs before anything can write filePath (ctrl+s save, $EDITOR);
	// nil = no-op. The project editor passes Bootstrap here so an uninitialized
	// project is enrolled in ~/.byre/projects only when a write actually lands
	// (and re-runs it per write — see runPrepare).
	prepare func() error

	// preEditorRaw/preEditorErr snapshot filePath as ctrl+e hands it to
	// $EDITOR (Err non-nil = it didn't exist); onEditorClosed compares to
	// mark savedOnce only when the editor actually wrote.
	preEditorRaw []byte
	preEditorErr error

	mode  uiMode
	focus int // form row (modeForm)

	// modeList
	listField fieldID
	listCur   int // 0..len(rows); the last index is the "+ add" row

	// modeMenu (per-row actions over the list row under the cursor)
	menuRow listRow
	menuCur int

	// modeVolumes
	volList      []VolumeStatus
	volNotes     []string // engine degrade notes (unreachable engine → copies not shown)
	volCur       int
	volPendClear int // index awaiting a clear-confirm, or -1
	volErr       string

	// modeSkills (multi-select)
	skillCur int

	// modeText (freeform text-block editor)
	ta        textarea.Model
	textField fieldID

	// modeItem
	inputs        []textinput.Model
	inputLabels   []string
	itemFocus     int      // control index; inputMap below says what it means
	itemHasMode   bool     // the editor carries a segmented picker
	itemMode      int      // selected picker option
	itemModeOpts  []string // picker options (mounts: ro/rw/disabled; mcp: local/remote)
	itemModeLabel string   // picker row label ("Mode", "Kind")
	itemModeFirst bool     // picker renders/focuses BEFORE the inputs (mcp: Kind drives the form)
	editIndex     int      // -1 = adding a new item
	itemErr       string

	width       int
	height      int
	errMsg      string
	status      string
	confirmQuit bool
}

func newModel(title, filePath string, cfg config.Config, templates, agents, skillOpts []string, skillDescs map[string]string, inh Inherited, vols VolumeAdmin, target Target) model {
	// Q7: saving re-marshals the whole file, so a hand-commented config would
	// lose its comments — say so on LOAD, while the user can still bail to ^e.
	commentWarn := false
	if raw, err := os.ReadFile(filePath); err == nil {
		commentWarn = handComments(string(raw))
	}
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
	advanced := []fieldID{fRunArgs, fDockerfilePre, fDockerfilePost}
	if vols != nil {
		advanced = append(advanced, fVolumes)
	}
	// MCP servers sit in BUILD, not GRANTS: declarations are wiring, like
	// packages (ADR 0033) — their CARRIED egress/env show in the grant rows.
	sections := []section{
		{"GRANTS — what this box can reach", []fieldID{fMounts, fPorts, fEgress, fEnv}},
		{"BUILD — how the box is made", []fieldID{fBase, fTemplate, fAgent, fEngine, fApt, fSkills, fMCP, fClaudeSkills}},
	}
	switch target {
	case TargetGlobal:
		// In default.config, template/agent are the first-run picker's
		// PRE-SELECTIONS — the resolver strips them from every resolved config,
		// so filing them under BUILD would claim they shape boxes. Their own
		// section says what they actually do (audit finding: the global editor
		// presented inert favourites as live machine-wide config).
		sections = []section{
			{"GRANTS — what every box can reach (defaults for all projects)", []fieldID{fMounts, fPorts, fEgress, fEnv}},
			{"ONBOARDING FAVOURITES — pre-selected in the first-run picker; applies nothing to any box", []fieldID{fTemplate, fAgent}},
			{"BUILD — defaults for how boxes are made", []fieldID{fBase, fEngine, fApt, fSkills, fMCP, fClaudeSkills}},
			// worktree_base is a global/host preference; only the --global editor
			// shows it (in a project editor it would falsely read "unset — will
			// refuse" whenever a global default is actually inherited).
			{"WORKTREES — where `byre worktree` creates them", []fieldID{fWorktreeSibling, fWorktreeBase}},
		}
	case TargetLayer:
		// A layer carries the full vocabulary EXCEPT template (shape selection
		// has one owner, the project config) — same form, no template picker.
		sections = []section{
			{"GRANTS — what boxes built on this layer can reach", []fieldID{fMounts, fPorts, fEgress, fEnv}},
			{"BUILD — what this layer adds to boxes", []fieldID{fBase, fAgent, fEngine, fApt, fSkills, fMCP, fClaudeSkills}},
		}
	}
	// The chain pointer: project configs and layers may name a parent layer;
	// default.config has no chain slot (the resolver refuses one).
	if target != TargetGlobal {
		sections = append(sections, section{"EXTENDS — a named layer this config builds on", []fieldID{fExtends}})
	}
	sections = append(sections, section{"ADVANCED", advanced})
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
		skillDescs:   skillDescs,
		inh:          inh,
		vols:         vols,
		sections:     sections,
		order:        order,
		ti:           ti,
		wtBase:       wtBase,
		target:       target,
		ta:           ta,
		width:        80,
		volPendClear: -1,
		commentWarn:  commentWarn,
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
	// Problem rows appear in pickers disabled-with-reason.
	m.tmplOpts = appendPickerProblems(m.tmplOpts, m.inh.Catalog, packages.KindTemplate, false)
	m.agentOpts = appendPickerProblems(m.agentOpts, m.inh.Catalog, packages.KindSkill, true)
	m.engineOpts = []string{"auto", "docker", "podman"}
	if cfg.Engine != "" && !contains(m.engineOpts, cfg.Engine) {
		m.engineOpts = append(m.engineOpts, cfg.Engine)
	}
	// The initial selection stays on the stored value EVEN when it is a
	// disabled problem row: the form writes whatever is selected on save, so
	// moving the selection here would silently swap the user's template/agent
	// just by opening the editor. Cycling away skips disabled rows (and can't
	// come back) — changing off a broken value is deliberate, keeping it isn't
	// a choice the editor makes for you.
	m.tmplSel = indexOf(m.tmplOpts, orNone(cfg.Template))
	m.agentSel = indexOf(m.agentOpts, orNone(cfg.Agent))
	m.engineSel = indexOf(m.engineOpts, orDefault(cfg.Engine, "auto"))
	// The EXTENDS picker: loadable layers plus, like every picker, the stored
	// value even when it isn't offerable (a dangling extends must survive an
	// unrelated open-and-save, and fail loudly at develop instead).
	m.extOpts = pickerOpts(m.inh.LayerNames, cfg.Extends)
	m.extSel = indexOf(m.extOpts, orNone(cfg.Extends))
	m.apt = append([]string{}, cfg.Apt...)
	m.env = envItems(cfg.Env)
	m.mounts = append([]config.Mount{}, cfg.Mounts...)
	m.ports = append([]config.Port{}, cfg.Ports...)
	m.egress = append([]string{}, cfg.Egress...)
	m.mcps = append([]config.MCP{}, cfg.MCPs...)
	m.claudeSkills = append([]config.ClaudeSkill{}, cfg.ClaudeSkills...)
	m.skills = append([]string{}, cfg.Skills...)
	m.runArgs = strings.Join(cfg.RunArgs, "\n")
	m.dfPre = strings.Join(cfg.DockerfilePre, "\n")
	m.dfPost = strings.Join(cfg.DockerfilePost, "\n")
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
		m.height = msg.Height
		// Full clear on resize: the inline renderer repaints only the lines
		// it knows it drew, but a SHRINK makes previously-drawn (old-width)
		// lines wrap in the terminal, occupying more physical rows than the
		// renderer's count — the overflow lingers as stale fragments above
		// the frame (live field report, 2026-07-17: duplicated rows after
		// resizing a few times).
		return m, tea.ClearScreen
	case editorClosedMsg:
		return m.onEditorClosed(msg.err), nil
	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeItem:
			return m.updateItem(msg)
		case modeMenu:
			return m.updateMenu(msg)
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
	case m.mode == modeForm:
		if in := m.focusedInput(); in != nil {
			*in, cmd = in.Update(msg)
		}
	}
	return m, cmd
}

// ---- form screen -----------------------------------------------------------

// isQuitKey reports whether a key both arms and confirms the dirty-quit
// prompt on the form screen. Any key that quits must also be excluded from
// clearing confirmQuit, or a repeat press re-arms forever instead of quitting.
func isQuitKey(k string) bool {
	switch k {
	case "esc", "ctrl+c", "ctrl+q":
		return true
	}
	return false
}

func (m model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if isQuitKey(key) {
		if m.dirty() && !m.confirmQuit {
			m.confirmQuit = true // View shows the confirm prompt
			return m, nil
		}
		return m, tea.Quit
	}
	m.confirmQuit = false
	switch key {
	case "ctrl+s":
		return m.save(), nil
	case "ctrl+e":
		// Drop into $EDITOR on the whole config file (the way to reach the raw
		// tier the UI doesn't edit). Require a clean state first: $EDITOR sees the
		// on-disk file, so unsaved structured edits would be lost or clobbered.
		if m.dirty() {
			m.errMsg = "save (ctrl+s) or discard changes before editing the file in $EDITOR"
			return m, nil
		}
		// $EDITOR writes the file directly, so the deferred store setup must
		// land BEFORE it opens — vi can't create a file in a missing dir. The
		// accepted cost: opening the raw editor and quitting without writing
		// still enrolls; the alternative (a half-created store the editor
		// can't write into) helps nobody.
		var ok bool
		if m, ok = m.runPrepare(); !ok {
			return m, nil
		}
		// Snapshot the on-disk state so onEditorClosed can tell a real
		// $EDITOR write from a look-and-quit — savedOnce must track writes
		// that actually landed, not editor round-trips.
		m.preEditorRaw, m.preEditorErr = os.ReadFile(m.filePath)
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
		// Entering a screen clears the form's transient status AND its error:
		// sub-screens render errMsg too (ctrl+s works everywhere), so a stale
		// form error must not follow the user in.
		switch f := m.field(); {
		case isListField(f):
			m.listField = f
			m.listCur = 0
			m.mode = modeList
			m.status = ""
			m.errMsg = ""
		case f == fVolumes:
			return m.openVolumes(), nil
		case f == fSkills:
			m.skillCur = 0
			m.mode = modeSkills
			m.status = ""
			m.errMsg = ""
		case isTextField(f):
			return m.openText(f), textarea.Blink
		case f == fWorktreeSibling:
			m.wtSibling = !m.wtSibling
		}
		return m, nil
	}
	if in := m.focusedInput(); in != nil {
		var cmd tea.Cmd
		*in, cmd = in.Update(msg)
		return m, cmd
	}
	return m, nil
}

// focusedInput returns a pointer to the textinput.Model backing the currently
// focused field, or nil when the focused field isn't a single-line text input.
// This is the one place that maps "focused field" to "the textinput.Model to
// route keys/cursor-movement to" — everything that needs to drive a text input
// (arrow-key cycling, non-key routing in Update, the form's key fallback) goes
// through it so fBase and fWorktreeBase behave identically.
func (m *model) focusedInput() *textinput.Model {
	switch m.field() {
	case fBase:
		return &m.ti
	case fWorktreeBase:
		return &m.wtBase
	default:
		return nil
	}
}

func (m *model) cycle(dir int) {
	switch m.field() {
	case fWorktreeSibling:
		m.wtSibling = !m.wtSibling
	case fTemplate:
		m.tmplSel = m.skipDisabled(m.tmplOpts, wrap(m.tmplSel+dir, len(m.tmplOpts)), dir)
	case fExtends:
		m.extSel = wrap(m.extSel+dir, len(m.extOpts))
	case fAgent:
		m.agentSel = m.skipDisabled(m.agentOpts, wrap(m.agentSel+dir, len(m.agentOpts)), dir)
	case fEngine:
		m.engineSel = wrap(m.engineSel+dir, len(m.engineOpts))
	default:
		if in := m.focusedInput(); in != nil {
			*in, _ = in.Update(tea.KeyMsg{Type: keyArrow(dir)})
		}
	}
}

// skipDisabled advances from start along dir until a non-disabled option or
// a full wrap (then returns start). "none" is always selectable.
func (m model) skipDisabled(opts []string, start, dir int) int {
	if len(opts) == 0 {
		return start
	}
	if dir == 0 {
		dir = 1
	}
	i := start
	for n := 0; n < len(opts); n++ {
		name := opts[i]
		if name == noneOption || m.optDisabled(name) == "" {
			return i
		}
		i = wrap(i+dir, len(opts))
	}
	return start
}

// optProv / optDisabled look up catalog provenance for template/agent options.
// Both go through Lookup, which expands aliases: a name that resolves to a
// loadable package is NEVER disabled, even when a same-named problem row
// exists — a LEGACY materialized `claude` dir must not grey out the valid
// bundled `claude` option (sibling-keyed problem rows shadow nothing).
func (m model) optProv(name string) string {
	if m.inh.Catalog == nil || name == "" || name == noneOption {
		return ""
	}
	if ent, ok := m.inh.Catalog.Lookup(name); ok {
		// Bundled is the unmarked default — suffixing every stock option
		// "bundled (devel)" adds words, not information. Only exceptional
		// provenance (a fork, a local path, an installed package) changes
		// the trust posture enough to earn a label on the picker line; the
		// skills screen and `byre skill inspect` keep full provenance.
		if ent.Provenance == packages.ProvBundled {
			return ""
		}
		return ent.ProvenanceLabel()
	}
	return ""
}

func (m model) optDisabled(name string) string {
	if m.inh.Catalog == nil || name == "" || name == noneOption {
		return ""
	}
	ent, ok := m.inh.Catalog.Lookup(name)
	if !ok {
		return ""
	}
	switch ent.Provenance {
	case packages.ProvInvalid, packages.ProvLegacy, packages.ProvConflict:
		if ent.Reason != "" {
			return ent.Reason
		}
		return string(ent.Provenance)
	}
	return ""
}

func (m model) field() fieldID { return m.order[m.focus] }

func (m *model) setFocus(i int) {
	m.focus = wrap(i, len(m.order))
	m.ti.Blur()
	m.wtBase.Blur()
	if in := m.focusedInput(); in != nil {
		in.Focus()
	}
}

// ---- rendering -------------------------------------------------------------

func (m model) View() string {
	var v string
	switch m.mode {
	case modeList:
		v = m.viewList()
	case modeItem:
		v = m.viewItem()
	case modeMenu:
		v = m.viewMenu()
	case modeVolumes:
		v = m.viewVolumes()
	case modeText:
		v = m.viewText()
	case modeSkills:
		v = m.viewSkills()
	default:
		v = m.viewForm()
	}
	return clipLines(clipHeight(v, m.height), m.width)
}

// clipHeight windows the view vertically when it exceeds the terminal,
// keeping the ▸ cursor row AND the frame's footer on screen. The inline
// bubbletea renderer can't scroll: a frame taller than the terminal silently
// pushes the TOP rows off (found live 2026-07-12: the --global form's extra
// section cropped the title on short terminals). Every screen ends with its
// status/confirm banner and key help — the dirty-quit confirmation lives
// there (the clear-volume confirm too), so the footer is pinned visible and
// only the body above it scrolls. Clipped content is never silent: a dim marker row
// names each hidden direction, and moving the cursor scrolls the window.
func clipHeight(s string, height int) string {
	max := height - 1 // the inline renderer keeps one row for itself
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	pin := len(lines) - footerStart(lines)
	bodyMax := max - pin
	if bodyMax < 4 {
		return s // unknown or absurd height: let the terminal cope
	}
	tail := lines[len(lines)-pin:]
	body := lines[:len(lines)-pin]

	focus := 0
	for i, l := range body {
		if strings.Contains(l, "▸") {
			focus = i
			break
		}
	}
	start := 0
	if focus > start+bodyMax-3 {
		start = focus - (bodyMax - 3) // keep the cursor clear of the bottom edge
	}
	if start+bodyMax > len(body) {
		start = len(body) - bodyMax
	}
	if start < 0 {
		start = 0
	}
	out := append([]string{}, body[start:start+bodyMax]...)
	if start > 0 {
		out[0] = dimStyle.Render("··· (more above)")
	}
	if start+bodyMax < len(body) {
		out[len(out)-1] = dimStyle.Render("··· (more below)")
	}
	return strings.Join(append(out, tail...), "\n")
}

// footerStart is where the pinned footer begins: the first blank separator
// within the frame's final rows (every screen sets its footer — status or
// confirm banner, optional warnings, path, key help — off with a blank
// line; a multi-line confirm makes the footer taller, so the pin is sized
// by looking, not by a fixed count). Capped so a pathological tail can't
// eat the whole window; no separator found falls back to the last 4 rows.
func footerStart(lines []string) int {
	const maxPin = 8
	from := len(lines) - maxPin
	if from < 0 {
		from = 0
	}
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			return i // include the separator: the visual gap survives
		}
	}
	if len(lines) > 4 {
		return len(lines) - 4
	}
	return 0
}

// errLine renders an error/validation message wrapped to the terminal width
// as REAL newline-separated lines. clipLines truncates any longer line
// (deliberately: soft-wrapped lines break the row accounting), which cut
// long messages off mid-word at the pane edge — and error messages echo
// user input, so their length is unbounded (field-QA 2026-07-17, finding 5).
func (m model) errLine(msg string) string {
	if m.width > 0 {
		return errTextStyle.Render(ansi.Wrap("✗ "+msg, m.width, ""))
	}
	return errTextStyle.Render("✗ " + msg)
}

// clipLines truncates every rendered line to the terminal width (ANSI-aware).
// The inline bubbletea renderer counts the lines it drew to repaint them; a
// line that WRAPS breaks that accounting and strands stale rows from the
// previous frame on screen (found live 2026-07-08: a long Egress summary row
// left the form row above it behind on the item-editor screen). The "…" tail
// makes the clipping visible — same language as clipHeight's "··· (more
// above)" markers — instead of content silently ending at the edge.
func clipLines(s string, width int) string {
	if width <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m model) viewForm() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", focusStyle.Render(m.title))
	// The one-line total-exposure summary: what the box actually gets across
	// all layers + skills, in the same words develop prints at launch.
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render("exposure: "+m.exposureNow().Line()))

	focusedField := m.field()
	for _, s := range m.sections {
		fmt.Fprintf(&b, "%s\n", m.sectionRule(s.title))
		for _, f := range s.fields {
			focused := f == focusedField
			cursor := "  "
			if focused {
				cursor = cursorStyle.Render("▸ ")
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
		b.WriteString(errStyle.Render("● Unsaved changes — press esc/^q/^c again to discard, or ctrl+s to save"))
	case m.errMsg != "":
		b.WriteString(m.errLine(m.errMsg))
	case m.dirty():
		b.WriteString(errStyle.Render("● Unsaved changes") + dimStyle.Render("  (ctrl+s to save)"))
	case m.status != "":
		b.WriteString(statusNote(m.status))
	default:
		b.WriteString(dimStyle.Render("No unsaved changes"))
	}
	b.WriteString("\n")

	if m.commentWarn {
		b.WriteString("\n" + errStyle.Render("⚠ this file has hand-written comments — ^s rewrites it and DROPS them (raw blocks survive; use ^e to edit without losing comments)"))
	}
	b.WriteString("\n" + dimStyle.Render("Saves to: "+m.filePath))
	b.WriteString("\n" + helpLine("↑↓", "move", "←→", "change", "↵", "open", "^s", "save", "^e", "$EDITOR", "^q", "quit"))
	return b.String()
}

// sectionRule renders a section header as a rule filled toward the terminal
// width — the section name in the accent color, its description and the
// dashes dim. Structure without borders: full boxes would eat width and
// fight clipHeight's blank-separator footer detection.
func (m model) sectionRule(title string) string {
	name, desc, cut := strings.Cut(title, " — ")
	t := accentStyle.Render(name)
	if cut {
		t += dimStyle.Render(" — " + desc)
	}
	// "─ " (one dash) puts the section name at column 2, flush with the
	// field labels below — a two-dash prefix left the name one column off
	// the keys, which read as misalignment (field-report 2026-07-17).
	line := dimStyle.Render("─ ") + t
	w := m.width
	if w > 76 {
		w = 76 // a full-width rule on an ultrawide terminal is a smear, not structure
	}
	if fill := w - ansi.StringWidth("─ "+title+" "); fill > 0 {
		line += " " + dimStyle.Render(strings.Repeat("─", fill))
	}
	return line
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
		return m.renderPick(m.tmplOpts, m.tmplSel, focused)
	case fExtends:
		s := m.renderPick(m.extOpts, m.extSel, focused)
		// The full resolved chain, root-first, whenever picking a parent pulls
		// in ancestors — the pointer names one layer; the merge takes them all.
		if chain := m.chainNow(); len(chain) > 1 {
			names := make([]string, len(chain))
			for i, nl := range chain {
				names[i] = nl.Name
			}
			s += dimStyle.Render("  (chain: " + strings.Join(names, " -> ") + ")")
		} else if len(m.inh.LayerNames) == 0 && m.extendsNow() == "" {
			s += dimStyle.Render("  (no layers on this machine — byre layer new <name>)")
		}
		return s
	case fAgent:
		return m.renderPick(m.agentOpts, m.agentSel, focused)
	case fEngine:
		return renderSeg(m.engineOpts, m.engineSel, focused)
	case fVolumes:
		s := "view / clear" // an action row, not an empty value — don't dim it
		if focused {
			s += dimStyle.Render("  (enter)")
		}
		return s
	case fSkills:
		// Count EFFECTIVE state, same as the skills screen's checkboxes: raw
		// layer entries include `!name` removal markers (not enabled skills)
		// and miss inherited-on skills entirely.
		n := 0
		for _, e := range m.skillEntries() {
			if e.on() {
				n++
			}
		}
		s := dimStyle.Render("(none)")
		if n > 0 {
			s = fmt.Sprintf("%d enabled", n)
		}
		if focused {
			s += dimStyle.Render("  (enter to choose)")
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
		// List fields count EFFECTIVE state, like the Skills summary: what the
		// box actually gets, with the inherited/skill share dimmed beside it.
		eff, inherited, fromSkills, offered := rowCounts(m.fieldRows(f))
		s := dimStyle.Render("(none)")
		if eff > 0 {
			s = fmt.Sprintf("%d %s", eff, fieldNoun(f, eff))
			var parts []string
			if inherited > 0 {
				parts = append(parts, fmt.Sprintf("%d inherited", inherited))
			}
			if fromSkills > 0 {
				parts = append(parts, fmt.Sprintf("%d from skills", fromSkills))
			}
			if len(parts) > 0 {
				s += dimStyle.Render("  (" + strings.Join(parts, ", ") + ")")
			}
		}
		// Offered doors are closed, so they never count as effective — but
		// discovery must not depend on entering the screen (ADR 0020).
		if offered > 0 {
			s += dimStyle.Render(fmt.Sprintf("  — %d offered", offered))
		}
		// Egress is declarative: unless a posture arms the allowlist, nothing
		// enforces it — config must not look armed when it isn't (ADR 0019).
		// open-denylist counts as unarmed here: its network is open and only
		// the closures bite (ADR 0030).
		if f == fEgress && eff > 0 && !config.PostureEnforcesAllowlist(m.postureNow()) {
			if m.postureNow() == "" {
				s += dimStyle.Render("  — unenforced (no firewall skill)")
			} else {
				s += dimStyle.Render("  — unenforced (network open; !closures still bite)")
			}
		}
		if focused {
			s += dimStyle.Render("  (enter to edit)")
		}
		return s
	}
}

// fieldNoun is the summary noun for a list field, pluralized.
func fieldNoun(f fieldID, n int) string {
	noun := map[fieldID]string{fApt: "package", fEnv: "var", fMounts: "mount", fPorts: "port", fEgress: "host", fMCP: "server", fClaudeSkills: "skill"}[f]
	if noun == "" {
		noun = "item"
	}
	if n != 1 {
		noun += "s"
	}
	return noun
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

// renderPick is renderSeg plus provenance dimming and disabled-with-reason
// for the selected option (template/agent pickers).
func (m model) renderPick(opts []string, sel int, focused bool) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		seg := "[" + o + "]"
		if m.optDisabled(o) != "" {
			// Disabled options stay visible but dimmed (not reverse-video even
			// when selected -- cycle skips them so selection should not land).
			seg = dimStyle.Render(seg)
		} else if i == sel {
			if focused {
				seg = selFocus.Render(seg)
			} else {
				seg = selStyle.Render(seg)
			}
		}
		parts[i] = seg
	}
	s := strings.Join(parts, " ")
	if sel >= 0 && sel < len(opts) {
		o := opts[sel]
		if p := m.optProv(o); p != "" {
			s += dimStyle.Render("  " + p)
		}
		if d := m.optDisabled(o); d != "" {
			s += dimStyle.Render("  (" + d + ")")
		}
	}
	return s
}

// appendPickerProblems adds catalog INVALID/conflict/LEGACY names of kind to
// opts (agentsOnly: only LooksLikeAgent skill rows).
func appendPickerProblems(opts []string, cat *packages.Catalog, kind packages.Kind, agentsOnly bool) []string {
	if cat == nil {
		return opts
	}
	seen := map[string]bool{}
	for _, o := range opts {
		seen[o] = true
	}
	for _, ent := range cat.ListProblemRows(kind) {
		// The agent picker only lists skill problem rows whose primary carries
		// an [agent] table (LooksLikeAgent, set at ingest) — a broken plain
		// skill doesn't belong in the agent picker.
		if agentsOnly && !ent.LooksLikeAgent {
			continue
		}
		name := ent.DisplayName()
		if name == "" {
			name = ent.ID
		}
		if seen[name] || seen[ent.ID] {
			continue
		}
		seen[name] = true
		opts = append(opts, name)
	}
	return opts
}

// ---- cursor-list plumbing ----------------------------------------------------
//
// The list-style screens (list fields, volumes, skills) share one cursor idiom:
// up/down move with wraparound, and the selected row gets a "▸ " marker with
// bold emphasis. cursorMove and cursorLine are that idiom, extracted.

// cursorMove applies an up/down navigation key to a cursor over n rows, wrapping
// at the ends. ok reports whether key was a navigation key at all; over zero
// rows the cursor stays put.
func cursorMove(key string, cur, n int) (newCur int, ok bool) {
	switch key {
	case "up", "shift+tab":
		if n > 0 {
			cur = wrap(cur-1, n)
		}
		return cur, true
	case "down", "tab":
		if n > 0 {
			cur = wrap(cur+1, n)
		}
		return cur, true
	}
	return cur, false
}

// cursorLine renders one row of a cursor list: the selected row gets the "▸ "
// marker and bold emphasis, the rest a plain two-space indent.
func cursorLine(selected bool, line string) string {
	if selected {
		return cursorStyle.Render("▸ ") + focusStyle.Render(line)
	}
	return "  " + line
}

// crumb renders a sub-screen title with a dim breadcrumb back to the session
// title (which file this edits) — orientation two screens deep, the same job
// the form's "Saves to:" line does on the root screen.
func (m model) crumb(screen string) string {
	return focusStyle.Render(screen) + dimStyle.Render("  ·  "+m.title)
}

// helpLine renders the footer key help from key/verb pairs: keys at normal
// intensity, verbs faint — scannable, not a uniform grey smear.
func helpLine(pairs ...string) string {
	parts := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, pairs[i]+" "+dimStyle.Render(pairs[i+1]))
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// statusNote renders a transient status line: the save confirmation gets its
// own green (the state the eye checks most often), everything else stays dim.
func statusNote(s string) string {
	if s == savedStatus {
		return okStyle.Render(s)
	}
	return dimStyle.Render(s)
}

// subFooterNote is the status/error line the sub-screens show above their key
// help. The form's footer owns the rich save/dirty banner; ctrl+s works from
// every screen, so its outcome — a save error especially — must be visible
// where it was pressed, not wait for the user to happen back to the form.
func (m model) subFooterNote() string {
	if m.errMsg != "" {
		return m.errLine(m.errMsg)
	}
	if m.status != "" {
		return statusNote(m.status)
	}
	return ""
}

// ---- small shared helpers ----------------------------------------------------

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
