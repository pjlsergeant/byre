package config

// mergestate.go is the cascade's merge bookkeeping, produced BESIDE the
// merged Config rather than on it (the 2026-08-23 lifecycle-split stage,
// ADR 0049 "staged work"). A raw layer expresses a closure as a `!name`
// marker INSIDE its own lists; the fold extracts the markers into a
// Closures accumulator that threads through the cascade. Config therefore
// never carries merge output — a raw layer is bookkeeping-free by TYPE,
// and MergeStep never has to tolerate its own merged Config as input.

// Closures is the `!name` closure bookkeeping one cascade fold produces:
// the closures that survived, per genus, stored as stripped names (no
// '!'). Egress closures subtract from the DERIVED allowlist after skill
// egress unions in; MCP and Claude Skill closures subtract skill-declared
// entries after their unions (ADR 0030 semantics, wholesale); context
// closures are extracted for genus uniformity and are inert (contexts
// have no skill home to subtract from). Egress closures are matched by
// EgressClosureMatches — PORTLESS closes every port of the host; ported
// closes just that one — the deliberate asymmetry with the open grammar,
// where portless means :443: addition is never greedy, subtraction may
// be. The other genera match by exact name.
type Closures struct {
	Egress       []string
	MCP          []string
	ClaudeSkills []string
	Contexts     []string
}

// Merged is a resolved cascade: the effective Config, plus the closures
// resolution extracted along the way. Load and ResolveProposed return it;
// everything downstream that subtracts by closure reads Closures from
// here, never from a Config field.
type Merged struct {
	Config
	Closures Closures
}

// MergeStep folds one raw layer (over) onto the accumulated cascade state
// (base + baseCl) and returns the new accumulation. over is always a RAW
// layer — its closures live as `!name` markers inside its own lists, and
// the fold extracts them — so merged output re-entering as `over` is a
// caller bug the type system now makes hard to write. Precedence per
// genus is documented on the helpers (mergeEgress, mergeNamedDecls).
func MergeStep(base Config, baseCl Closures, over Config) (Config, Closures) {
	return mergeStep(base, baseCl, over)
}
