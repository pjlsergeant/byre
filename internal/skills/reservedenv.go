// reservedenv.go owns byre's reserved BYRE_ namespace as a legibility
// question: which of a skill's runtime env keys are byre's own chassis knobs,
// and which claims each one can skew. ADR 0050 tier 2 is the decision behind
// it -- a skill setting one is ACCEPTED (enabling a skill is trusting it, and
// refusing the typed spelling while raw Dockerfile lines stay open would be
// theater), but never silent: it is rendered, attributed, and every claim it
// can skew stops asserting.
//
// This lives in skills, not beside config.Exposure, because it is a fact
// about what a SKILL declares -- ReservedEnvSet is the vocabulary, and config
// cannot import skills (the dependency runs the other way), so a home there
// would either duplicate the type or degrade the predicate to bare strings.
// Three surfaces read this one owner and must agree: `byre status`'s claim
// rows, develop's launch banner, and the config editor's exposure line.
package skills

import (
	"maps"
	"slices"
	"strings"
)

// The claims a reserved key can skew, named once. Callers pass these rather
// than string literals: a misspelled claim would make ReservedEnvTouches
// answer false forever, which is a claim asserting itself unqualified --
// the exact failure this machinery exists to prevent.
const (
	ClaimNetwork         = "network"
	ClaimMCPDelivery     = "MCP delivery"
	ClaimContextDelivery = "context delivery"
	ClaimLaunch          = "launch"
)

// reservedPrefix is byre's protocol vocabulary. Reserved, never a reserved
// capability: the raw tier carries the same intent and degrades the same
// claims (ADR 0050).
const reservedPrefix = "BYRE_"

// ReservedEnvSet is one skill runtime-env key inside byre's reserved
// BYRE_ namespace: the variables that parameterize the chassis scripts.
type ReservedEnvSet struct {
	Skill string
	Key   string
}

// ReservedEnvOf lists one skill's reserved keys, sorted by key so a render is
// deterministic. Both the resolved-at-launch path (Resolved.ReservedEnv) and
// the config editor's live view build their sets through here, so "which keys
// count as byre's" is answered in one place for every surface.
func ReservedEnvOf(skill string, env map[string]string) []ReservedEnvSet {
	var out []ReservedEnvSet
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if strings.HasPrefix(k, reservedPrefix) {
			out = append(out, ReservedEnvSet{Skill: skill, Key: k})
		}
	}
	return out
}

// ReservedEnvClaims names the claims one reserved BYRE_ variable can skew.
// Unknown BYRE_* keys (a future chassis knob this map hasn't met)
// conservatively skew ClaimNetwork -- the claim with the most riding on it --
// plus ClaimLaunch. The chassis-knob inventory itself is pinned by gen's
// TestChassisScriptKnobsRideReservedPrefix.
func ReservedEnvClaims(key string) []string {
	switch key {
	case "BYRE_EGRESS", "BYRE_EGRESS_DENY", "BYRE_LAUNCH_GATE_FILE", "BYRE_LAUNCH_GATE_TIMEOUT":
		return []string{ClaimNetwork}
	case "BYRE_CONTEXT_DIR", "BYRE_AGENT_CONTEXT", "BYRE_SESSION_CONTEXT":
		return []string{ClaimContextDelivery}
	case "BYRE_MCP_CONFIG":
		return []string{ClaimMCPDelivery}
	case "BYRE_ENVD_DIR", "BYRE_FIRSTRUN_DIR":
		// Both run after the gate wait (no network reach) but before the
		// agent execs, and env.d is SOURCED -- a redirected dir can rewrite
		// the delivery vars the agent command consumes, so both delivery
		// claims degrade with it (the review's sibling-controls finding).
		return []string{ClaimContextDelivery, ClaimMCPDelivery, ClaimLaunch}
	case "BYRE_WORKSPACE_DIR",
		"BYRE_IMAGE_PATH_FILE", "BYRE_ASSUME_TTY", "BYRE_GEMINI_DIR",
		"BYRE_IDENTITY_BASE", "BYRE_UID", "BYRE_GID", "BYRE_PROJECT", "BYRE_WORKTREE":
		return []string{ClaimLaunch}
	default:
		return []string{ClaimNetwork, ClaimLaunch}
	}
}

// ReservedEnvTouches reports whether any skill-set reserved variable in sets
// can skew the named claim -- the hedge predicate every claim surface
// consults. It takes the resolved set rather than any one surface's view, so
// status, the launch banner and the editor degrade on one input instead of
// three that can drift apart.
func ReservedEnvTouches(sets []ReservedEnvSet, claim string) bool {
	for _, e := range sets {
		if slices.Contains(ReservedEnvClaims(e.Key), claim) {
			return true
		}
	}
	return false
}
