// provenance.go owns the editor's read-only view of everything it shows but
// does not edit (ADR 0018): the lower cascade layers and skill-contributed
// runtime state. The editor stays layer-scoped -- these inputs exist so the
// screens can show EFFECTIVE state and attribute each row to its source.
package configui

import (
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
)

// SkillRuntime is one skill's runtime contribution, shown read-only in the
// list screens with a (skill:name) tag. Volumes are omitted: they have their
// own engine-backed screen, which already resolves skills.
type SkillRuntime struct {
	Mounts []config.Mount
	Env    map[string]string
	// EnvDocs documents env vars the skill CONSUMES but does not set (var
	// name -> one-line guidance). The env screen renders each var nothing
	// else provides as a dim suggestion row; enter prefills the add editor.
	// Pure documentation — an unset var is never flagged anywhere.
	EnvDocs map[string]string
	// Files are the skill's [build].files: skill-relative source -> absolute
	// image destination. Read-only here (a skill's payload is the skill's),
	// but VISIBLE, because "what is going into my image and who put it
	// there" is the question the Baked files screen exists to answer -- and
	// files is overwhelmingly a skill's key.
	Files  map[string]string
	Egress []string // functional endpoints, open with enablement (ADR 0019/0020)
	// Offered is the skill's declared-but-CLOSED doors (ADR 0020): shown as
	// switches; opening one writes the entry into the user's own egress.
	Offered []string
	// MCPs are the skill's [[mcp]] declarations (ADR 0033). Shown on the MCP
	// screen attributed skill:<name> — and closable from there: a `!name`
	// closure in this file reaches a skill-declared server.
	MCPs []config.MCP
	// ClaudeSkills are the skill's [[claude_skills]] contributions. Shown on
	// the Claude Skills screen attributed skill:<name>, closable there the
	// same way (the closure semantics are shared with MCP).
	ClaudeSkills []config.ClaudeSkill
	// Posture is the skill's declared network_posture ("" = none). The Egress
	// screen uses it to say whether anything enforces the allowlist.
	Posture string
	// Containment is the skill's declared containment hole one-liner ("" =
	// none). Shown on the skills screen when the skill is enabled.
	Containment string
	// CompanionFor names the agent skill this skill is a companion to
	// (ADR 0017/0034; "" = not a companion) — the resolved pairing fact
	// (companion_for, or the pairing shared_auth_for implies), NOT the
	// offer vouch. The skills screen nests such a skill as an indented
	// child of its agent's row so the pairing is visible at the point of
	// enablement, gate-pending or not.
	CompanionFor string
	// Provenance is the package provenance (bundled/local/installed/...) for
	// dim-row labels; empty when unknown.
	Provenance string
	// ProvLabel is the human label ("bundled 0.2.0", "local", ...).
	ProvLabel string
	// DisabledReason, when set, marks the row disabled-with-reason (INVALID,
	// conflict, LEGACY) rather than selectable.
	DisabledReason string
}

// Inherited is the editor's provenance input. The lower layers ride RAW (not
// pre-merged) so each effective row can name which layer set it; the editor
// merges them itself via config.Merge -- the same op the cascade runs. Zero
// value = show nothing inherited (degrade to the plain layer view).
type Inherited struct {
	// HasLower is false for the --global editor: it IS the base layer, so
	// nothing is inherited regardless of what else is set.
	HasLower bool
	// Default is the raw global default.config layer.
	Default config.Config
	// Templates maps a template name to its raw layer. Consulted per the
	// CURRENTLY selected template -- the template picker is a live form field
	// that flips the lower layers.
	Templates map[string]config.Config
	// Skills maps each discovered skill's name to its runtime contribution,
	// consulted for whatever skill set is currently effective in the form --
	// toggling a skill adds/removes its rows live.
	Skills map[string]SkillRuntime
	// Layers maps every LOADABLE named layer to its raw config (parent
	// pointer included), so the editor can walk the extends chain for the
	// CURRENTLY selected extends value -- the EXTENDS picker is a live form
	// field that flips the lower layers, like the template picker.
	Layers map[string]config.Config
	// LayerNames is the EXTENDS picker's option list (loadable layers; for a
	// --layer editor, minus itself and anything whose chain runs through it).
	LayerNames []string
	// Catalog is optional; when set, skill/template rows can show provenance
	// and disable INVALID/conflict/LEGACY entries.
	Catalog *packages.Catalog
}

// lowerNow is the lower-layer resolved config
// (default ⊕ template ⊕ chain(root … parent)) under the CURRENTLY selected
// template and extends values; zero Config when this editor has no lower.
func (m model) lowerNow() config.Config {
	if !m.inh.HasLower {
		return config.Config{}
	}
	lower := m.inh.Default
	if t := m.templateNow(); t != "" {
		lower = config.Merge(lower, m.inh.Templates[t])
	}
	for _, nl := range m.chainNow() {
		lower = config.Merge(lower, nl.Config)
	}
	return lower
}

// lowerScalar reports what the cascade BELOW this file provides for one
// scalar field, and where it comes from -- the inherit row's content. The
// selected template counts as lower for agent/engine (a template may set
// them); for the template field itself it must not, or the row would report
// the very selection it describes.
func (m model) lowerScalar(get func(config.Config) string, includeTemplate bool) (value, source string) {
	if !m.inh.HasLower {
		return "", ""
	}
	lower := m.inh.Default
	if includeTemplate {
		// templateNow, not the raw row: with the template picker on its own
		// inherit row the effective template is the inherited one, and an
		// agent it sets is genuinely below this file.
		if t := m.templateNow(); t != "" {
			lower = config.Merge(lower, m.inh.Templates[t])
		}
	}
	for _, nl := range m.chainNow() {
		lower = config.Merge(lower, nl.Config)
	}
	v := config.FromNone(get(lower))
	if v == "" {
		return "", ""
	}
	return v, m.lowerSource(func(c config.Config) bool { return config.FromNone(get(c)) == v })
}

// templateNow, agentNow and engineNow are the EFFECTIVE selections: an
// inherit row stands for the value it names, the sentinel row means off.
// Readers that ask "what is in effect" use these; only the writer
// (fromScalar) cares about the difference between inheriting and saying so.
func (m model) templateNow() string {
	return effectiveScalar(m.tmplOpts, m.tmplSel, m.tmplInherit, noneOption)
}

func (m model) agentNow() string {
	return effectiveScalar(m.agentOpts, m.agentSel, m.agentInherit, noneOption)
}

func (m model) engineNow() string {
	return effectiveScalar(m.engineOpts, m.engineSel, m.engineInherit, "auto")
}

// extendsNow is the currently selected parent layer ("" = none). The picker
// list is always non-empty (pickerOpts appends the none row).
func (m model) extendsNow() string {
	if len(m.extOpts) == 0 {
		return ""
	}
	return config.FromNone(m.extOpts[m.extSel])
}

// chainNow is the named-layer chain under the CURRENTLY selected extends
// value, root-first -- walked over the raw Layers map, never disk (the
// picker is a live field). A pointer that leaves the map (layer deleted or
// broken mid-session) or loops just ends the walk: the editor degrades to
// shorter attribution; develop still fails loudly.
func (m model) chainNow() []config.NamedLayer {
	var chain []config.NamedLayer
	seen := map[string]bool{}
	for name := m.extendsNow(); name != "" && !seen[name]; {
		c, ok := m.inh.Layers[name]
		if !ok {
			break
		}
		seen[name] = true
		chain = append([]config.NamedLayer{{Name: name, Config: c}}, chain...)
		name = c.Extends
	}
	return chain
}

// hostEnvNow is the effective env_from_host view at this editor: byre's core
// layer (the shipped git identity) under the lower layers under this file's
// own entries, disabled ("") keys dropped. Read-only in the UI — the rows
// exist so the passthrough is never invisible where env is inspected;
// changing it is a hand edit (`env_from_host` in this file).
func (m model) hostEnvNow() map[string]string {
	merged := config.Merge(config.Merge(config.Config{EnvFromHost: config.CoreEnvFromHost()}, m.lowerNow()), m.base).EnvFromHost
	out := map[string]string{}
	for k, v := range merged {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// lowerSource names the sublayer an inherited entry comes from -- the LATEST
// contributing layer wins, matching merge order: the extends chain (leafmost
// first) over the current template's raw layer over the default. has reports
// whether a raw layer carries the entry.
func (m model) lowerSource(has func(config.Config) bool) string {
	chain := m.chainNow()
	for i := len(chain) - 1; i >= 0; i-- {
		if has(chain[i].Config) {
			return "layer:" + chain[i].Name
		}
	}
	if t := m.templateNow(); t != "" && has(m.inh.Templates[t]) {
		return "template:" + t
	}
	if has(m.inh.Default) {
		return "default"
	}
	return "inherited"
}
