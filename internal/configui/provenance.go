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
}

// Inherited is the editor's provenance input. Zero value = show nothing
// inherited (degrade to the plain layer view, never error).
type Inherited struct {
	// Lower maps a template name ("" = none selected) to the resolved lower
	// cascade (default ⊕ that template). Keyed by template because the
	// template picker is a live form field that flips the lower layers.
	// nil for the --global editor: it IS the base layer.
	Lower map[string]config.Config
	// Skills maps each discovered skill's name to its runtime contribution,
	// consulted for whatever skill set is currently effective in the form --
	// toggling a skill adds/removes its rows live.
	Skills map[string]SkillRuntime
}

// lowerNow is the lower-layer resolved config under the CURRENTLY selected
// template (zero Config when there is none -- the --global editor).
func (m model) lowerNow() config.Config {
	if m.inh.Lower == nil {
		return config.Config{}
	}
	return m.inh.Lower[fromNone(m.tmplOpts[m.tmplSel])]
}
