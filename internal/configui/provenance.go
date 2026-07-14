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
	Egress  []string // functional endpoints, open with enablement (ADR 0019/0020)
	// Offered is the skill's declared-but-CLOSED doors (ADR 0020): shown as
	// switches; opening one writes the entry into the user's own egress.
	Offered []string
	// MCPs are the skill's [[mcp]] declarations (ADR 0033). Shown on the MCP
	// screen attributed skill:<name> — and closable from there: a `!name`
	// closure in this file reaches a skill-declared server.
	MCPs []config.MCP
	// Posture is the skill's declared network_posture ("" = none). The Egress
	// screen uses it to say whether anything enforces the allowlist.
	Posture string
	// Containment is the skill's declared containment hole one-liner ("" =
	// none). Shown on the skills screen when the skill is enabled.
	Containment string
	// SharedAuthFor names the agent skill this skill is the shared-auth
	// companion for (ADR 0017/0025; "" = not a companion). The skills screen
	// nests such a skill as an indented child of its agent's row so the
	// pairing is visible at the point of enablement.
	SharedAuthFor string
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
	// Catalog is optional; when set, skill/template rows can show provenance
	// and disable INVALID/conflict/LEGACY entries.
	Catalog *packages.Catalog
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
