package config

import (
	"fmt"
	"sort"
	"strings"
)

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

// UnmarshalTOML accepts both array and table shapes.
func (s *SharedAuthPref) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case []interface{}:
		s.Yes = s.Yes[:0]
		for i, item := range v {
			str, ok := item.(string)
			if !ok {
				return fmt.Errorf("shared_auth[%d]: want string, got %T", i, item)
			}
			s.Yes = append(s.Yes, str)
		}
		return nil
	case map[string]interface{}:
		s.Pick = map[string]string{}
		for k, val := range v {
			str, ok := val.(string)
			if !ok {
				return fmt.Errorf("shared_auth.%s: want string, got %T", k, val)
			}
			s.Pick[k] = str
		}
		return nil
	default:
		return fmt.Errorf("shared_auth: want array or table, got %T", data)
	}
}

// Agents returns every agent with any stored preference, sorted (for writers
// that need a stable list).
func (s SharedAuthPref) Agents() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range s.Yes {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for a := range s.Pick {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
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
