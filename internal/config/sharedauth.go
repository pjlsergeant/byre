package config

import (
	"fmt"
	"sort"
	"strings"
)

// SharedAuthPref is the dual-shape shared_auth favourite (D2c):
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

// UnmarshalTOML accepts both array and table shapes (D2c).
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

// EncodeTOMLLine returns a single-line TOML assignment for surgical writers.
// Prefer the table shape when any pick is present; otherwise the legacy array.
// Empty preference returns "" (caller removes the key).
func (s SharedAuthPref) EncodeTOMLLine() string {
	if s.Empty() {
		return ""
	}
	if len(s.Pick) > 0 {
		// Table shape: any pick present -> table of picks only; Yes-without-
		// pick agents are omitted (they re-ask). Save always writes a pick
		// when it knows one.
		keys := make([]string, 0, len(s.Pick))
		for k := range s.Pick {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s = %q", k, s.Pick[k]))
		}
		return "shared_auth = { " + strings.Join(parts, ", ") + " }"
	}
	// Legacy array shape.
	quoted := make([]string, len(s.Yes))
	for i, a := range s.Yes {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return "shared_auth = [" + strings.Join(quoted, ", ") + "]"
}
