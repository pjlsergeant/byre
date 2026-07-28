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
	"fmt"
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

// reservedEnvKnobs is the chassis-knob inventory: every reserved variable
// byre itself reads, mapped to the claims setting it can skew. A key absent
// from this map is one byre has no knowledge of -- it wears byre's reserved
// spelling and byre cannot say what it does -- which is what
// ReservedEnvClaims and ReservedEnvNote below answer for, each in its own
// register. The inventory is pinned against the scripts by gen's
// TestChassisScriptKnobsRideReservedPrefix.
var reservedEnvKnobs = map[string][]string{
	"BYRE_EGRESS":              {ClaimNetwork},
	"BYRE_EGRESS_DENY":         {ClaimNetwork},
	"BYRE_LAUNCH_GATE_FILE":    {ClaimNetwork},
	"BYRE_LAUNCH_GATE_TIMEOUT": {ClaimNetwork},

	"BYRE_CONTEXT_DIR":     {ClaimContextDelivery},
	"BYRE_AGENT_CONTEXT":   {ClaimContextDelivery},
	"BYRE_SESSION_CONTEXT": {ClaimContextDelivery},

	"BYRE_MCP_CONFIG": {ClaimMCPDelivery},

	// Both run after the gate wait (no network reach) but before the agent
	// execs, and env.d is SOURCED -- a redirected dir can rewrite the
	// delivery vars the agent command consumes, so both delivery claims
	// degrade with it (the review's sibling-controls finding).
	"BYRE_ENVD_DIR":     {ClaimContextDelivery, ClaimMCPDelivery, ClaimLaunch},
	"BYRE_FIRSTRUN_DIR": {ClaimContextDelivery, ClaimMCPDelivery, ClaimLaunch},

	"BYRE_WORKSPACE_DIR":   {ClaimLaunch},
	"BYRE_IMAGE_PATH_FILE": {ClaimLaunch},
	"BYRE_ASSUME_TTY":      {ClaimLaunch},
	"BYRE_GEMINI_DIR":      {ClaimLaunch},
	"BYRE_IDENTITY_BASE":   {ClaimLaunch},
	"BYRE_UID":             {ClaimLaunch},
	"BYRE_GID":             {ClaimLaunch},
	"BYRE_PROJECT":         {ClaimLaunch},
	"BYRE_WORKTREE":        {ClaimLaunch},
}

// ReservedEnvClaims names the claims one reserved BYRE_ variable can skew.
// A key byre does not recognize (a future chassis knob this inventory hasn't
// met, or a skill's own variable wearing the prefix) conservatively skews
// ClaimNetwork -- the claim with the most riding on it -- plus ClaimLaunch.
func ReservedEnvClaims(key string) []string {
	if claims, ok := reservedEnvKnobs[key]; ok {
		return claims
	}
	return []string{ClaimNetwork, ClaimLaunch}
}

// ReservedEnvKnown reports whether key is a chassis knob byre itself reads.
// It is the honesty half of the conservative default: the DEGRADATION above
// is the same either way, but what byre may SAY about the key is not.
func ReservedEnvKnown(key string) bool {
	_, ok := reservedEnvKnobs[key]
	return ok
}

// ReservedEnvNote is the sentence status's Reserved env row prints for a
// skill-set reserved key; the config editor's Env row annotates via
// ReservedEnvSkew, the row-sized register off the same inventory.
//
// The two registers are the point. For a knob byre reads, byre knows what
// the skill took over and says so. For a key byre does not recognize it says
// only what it can defend: the prefix is byre's, byre cannot tell what the
// key does, and the claims are qualified for that reason. Announcing "byre
// runtime control" over a key byre has never heard of claims knowledge byre
// lacks -- a skill's own `BYRE_`-prefixed scratch path is not a control of
// byre's, and a row saying it is, is wrong in effect while being right by
// rule.
func ReservedEnvNote(e ReservedEnvSet) string {
	claims := strings.Join(ReservedEnvClaims(e.Key), " + ")
	if ReservedEnvKnown(e.Key) {
		return fmt.Sprintf("%s sets %s — byre runtime control; the %s claim(s) ride it", e.Skill, e.Key, claims)
	}
	return fmt.Sprintf("%s sets %s — not a control this byre recognizes; treated cautiously, so the %s claim(s) ride it", e.Skill, e.Key, claims)
}

// ReservedEnvSkew is ReservedEnvNote's row-sized register, for a surface with
// a line rather than a page: the config editor's Env row already prints the
// key and its skill attribution, so the annotation carries only what the row
// itself cannot say -- which claims stop being warranted, and, for a key byre
// does not read, that byre cannot tell what the key does.
//
// It sits beside ReservedEnvNote rather than inside the editor so the two
// registers answer off one inventory: a key status calls unrecognized can
// never read as a byre control one screen over.
func ReservedEnvSkew(key string) string {
	claims := "skews: " + strings.Join(ReservedEnvClaims(key), " + ")
	if ReservedEnvKnown(key) {
		return claims
	}
	return "not a control byre recognizes; " + claims
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
