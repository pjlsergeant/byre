package config

import (
	"fmt"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Defaults is the picker-owned section: state about how the NEXT onboarding
// behaves, never anything a box receives. Cascade-inert by construction --
// resolveWith strips the whole section, so no member of it can ever acquire
// teeth by accident.
type Defaults struct {
	// SharedAuth is the shared-auth favourite (ADR 0025): a preference over
	// future ANSWERS. Dual-shape decode: legacy array ["claude"] =
	// yes-inclination with no companion pick; table
	// { claude = "claude-shared-auth" } = agent -> companion pick.
	SharedAuth SharedAuthPref `toml:"shared_auth,omitempty"`
	// SkipQuestions turns onboarding's picker off: a new project is
	// configured from the stored answers -- template, agent, and the
	// shared-auth pick -- without prompting. The shared-auth pick GRANTS
	// (it writes the companion skill into the new project), so this key is
	// the standing consent for that: hand-set, at machine scope, in the one
	// file P5 blesses for hand-made machine-wide grants. Not a remembered
	// answer becoming a silent default (the 2026-07-12 episode P5 exists
	// for) -- an explicit standing instruction, and develop says out loud
	// when it acted on one.
	SkipQuestions bool `toml:"skip_questions,omitempty"`
}

// StoredSharedAuth is the effective shared-auth preference: the [defaults]
// section when it carries one, else the legacy top-level spelling
// onboarding wrote before 2026-07-28. One accessor so no reader has to know
// there are two homes.
func (c Config) StoredSharedAuth() SharedAuthPref {
	// UNION, not either-or: a config can carry both homes (hand-edited, or
	// mid-migration), and picking one wholesale silently drops the other's
	// agents -- the next write then clones the winner and deletes the loser,
	// losing a preference the user set. Canonical wins per AGENT, which is
	// the only granularity at which the two can actually conflict.
	return c.SharedAuthLegacy.mergedWith(c.Defaults.SharedAuth)
}

// mergedWith overlays o on s per agent: o's pick or yes-inclination replaces
// s's for the same agent, and agents only s knows survive.
func (s SharedAuthPref) mergedWith(o SharedAuthPref) SharedAuthPref {
	if o.Empty() {
		return s.Clone()
	}
	if s.Empty() {
		return o.Clone()
	}
	out := s.Clone()
	for agent, companion := range o.Pick {
		if out.Pick == nil {
			out.Pick = map[string]string{}
		}
		out.Pick[agent] = companion
		out.Yes = removeStringPref(out.Yes, agent) // a pick supersedes a bare yes
	}
	for _, agent := range o.Yes {
		if _, picked := out.Pick[agent]; picked {
			continue
		}
		if !containsPref(out.Yes, agent) {
			out.Yes = append(out.Yes, agent)
		}
	}
	return out
}

func containsPref(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func removeStringPref(list []string, v string) []string {
	out := list[:0:0]
	for _, s := range list {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}

// Empty reports whether the section carries nothing.
func (d Defaults) Empty() bool { return d.SharedAuth.Empty() && !d.SkipQuestions }

// SharedAuthPref is the dual-shape shared_auth favourite (ADR 0025):
//
//	shared_auth = ["claude"]                          # legacy: yes, no pick
//	[shared_auth]
//	claude = "claude-shared-auth"                     # pick prefill
//
// A legacy array entry is a yes-inclination with no companion pick: it
// prefills [Y/n] in the single-claimant case and does nothing in a picker.
// Migration never invents a pick; the next save-as-default rewrites the
// entry in the table shape.
type SharedAuthPref struct {
	// Yes lists agents with a legacy yes-inclination and no pick.
	Yes []string
	// Pick maps agent -> companion id (display or canonical).
	Pick map[string]string
}

// Empty reports whether no preference is stored.
func (s SharedAuthPref) Empty() bool {
	return len(s.Yes) == 0 && len(s.Pick) == 0
}

// Equal reports whether two preferences store the same answers.
func (s SharedAuthPref) Equal(o SharedAuthPref) bool {
	if len(s.Yes) != len(o.Yes) || len(s.Pick) != len(o.Pick) {
		return false
	}
	for i := range s.Yes {
		if s.Yes[i] != o.Yes[i] {
			return false
		}
	}
	for k, v := range s.Pick {
		if o.Pick[k] != v {
			return false
		}
	}
	return true
}

// Clone returns a deep copy.
func (s SharedAuthPref) Clone() SharedAuthPref {
	out := SharedAuthPref{}
	if len(s.Yes) > 0 {
		out.Yes = append([]string{}, s.Yes...)
	}
	if len(s.Pick) > 0 {
		out.Pick = map[string]string{}
		for k, v := range s.Pick {
			out.Pick[k] = v
		}
	}
	return out
}

// HasYes reports a yes-inclination for agent: either a pick is stored or a
// legacy array entry names the agent.
func (s SharedAuthPref) HasYes(agent string) bool {
	if agent == "" {
		return false
	}
	if _, ok := s.Pick[agent]; ok {
		return true
	}
	for _, a := range s.Yes {
		if a == agent {
			return true
		}
	}
	return false
}

// CompanionPick returns the saved companion for agent, or "" when only a
// legacy yes-inclination (or nothing) is stored.
func (s SharedAuthPref) CompanionPick(agent string) string {
	if s.Pick == nil {
		return ""
	}
	return s.Pick[agent]
}

// UnmarshalTOML accepts both array and table shapes. The decoder hands over
// the raw TOML of the relevant portion (unstable.Unmarshaler): value bytes
// for the inline spellings (`["claude"]`, `{ claude = "c" }`), a key-value
// document for the `[shared_auth]` table form. Consuming the whole subtree
// here is also what keeps the strict unknown-key check out of it.
func (s *SharedAuthPref) UnmarshalTOML(data []byte) error {
	t := strings.TrimSpace(string(data))
	switch {
	case strings.HasPrefix(t, "["):
		var v struct {
			V []string `toml:"v"`
		}
		if err := toml.Unmarshal([]byte("v = "+t), &v); err != nil {
			return fmt.Errorf("shared_auth: want an array of agent names: %w", err)
		}
		s.Yes = v.V
		return nil
	case strings.HasPrefix(t, "{"):
		var v struct {
			V map[string]string `toml:"v"`
		}
		if err := toml.Unmarshal([]byte("v = "+t), &v); err != nil {
			return fmt.Errorf("shared_auth: want agent = companion strings: %w", err)
		}
		s.Pick = v.V
		return nil
	default:
		var m map[string]string
		if err := toml.Unmarshal([]byte(t), &m); err != nil {
			return fmt.Errorf("shared_auth: want array or table of agent = companion strings: %w", err)
		}
		s.Pick = m
		return nil
	}
}

// EncodeTOMLValue renders the canonical VALUE for a non-empty preference --
// the single owner of the stored shape, used by every writer (the config
// editor's reconcile and the onboarding save alike). Any pick present ->
// inline table of picks only; Yes-without-pick agents are omitted (they
// re-ask; Save always writes a pick when it knows one). No picks -> the
// legacy array shape.
func (s SharedAuthPref) EncodeTOMLValue() string {
	if len(s.Pick) > 0 {
		keys := make([]string, 0, len(s.Pick))
		for k := range s.Pick {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			// Keys are quoted: installed agents have qualified owner/name
			// IDs, and '/' is illegal in a bare TOML key.
			parts = append(parts, fmt.Sprintf("%q = %q", k, s.Pick[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	quoted := make([]string, len(s.Yes))
	for i, a := range s.Yes {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
