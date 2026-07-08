// provenance.go owns the editor's read-only view of everything it shows but
// does not edit (ADR 0018): the lower cascade layers and skill-contributed
// runtime state. The editor stays layer-scoped -- these inputs exist so the
// screens can show EFFECTIVE state and attribute each row to its source.
package configui

import (
	"github.com/pjlsergeant/byre/internal/config"
)

// SkillRuntime is one skill's runtime contribution, shown read-only in the
// list screens with a (skill:name) tag. Volumes are omitted: they have their
// own engine-backed screen, which already resolves skills.
type SkillRuntime struct {
	Mounts []config.Mount
	Env    map[string]string
	Egress []string // declared host[:port] endpoints (ADR 0019)
	// Posture is the skill's declared network_posture ("" = none). The Egress
	// screen uses it to say whether anything enforces the allowlist.
	Posture string
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
}

// lowerNow is the lower-layer resolved config (default ⊕ template) under the
// CURRENTLY selected template; zero Config when this editor has no lower.
func (m model) lowerNow() config.Config {
	if !m.inh.HasLower {
		return config.Config{}
	}
	t := fromNone(m.tmplOpts[m.tmplSel])
	if t == "" {
		return m.inh.Default
	}
	return config.Merge(m.inh.Default, m.inh.Templates[t])
}

// lowerSource names the sublayer an inherited entry comes from -- the current
// template's raw layer wins over the default (it's the later layer), matching
// merge order. has reports whether a raw layer carries the entry.
func (m model) lowerSource(has func(config.Config) bool) string {
	if t := fromNone(m.tmplOpts[m.tmplSel]); t != "" && has(m.inh.Templates[t]) {
		return "template:" + t
	}
	if has(m.inh.Default) {
		return "default"
	}
	return "inherited"
}
