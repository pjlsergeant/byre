package commands

// claims.go is the claim-classification registry: every config.Config field
// answers, in code, the question "does this field bear on a status claim,
// and where is that rendered?" -- or carries the argument that it cannot.
// This is a GROWTH GUARD, not a proof: TestEveryConfigFieldHasClaimClassification
// fails on any unclassified field, so a new config key cannot ship without
// this answer appearing in a reviewable diff; whether a rendered() note is
// TRUE is the review's job (and the per-claim tests'). P4 made status the
// mechanism -- the 2026-07 outside review found four independent claim-
// surface holes whose common cause was that nothing ever forced this
// question to be answered.

// claimClass is one field's answer: rendered somewhere nameable, or
// argued inert. The note is load-bearing either way -- a reviewer greps
// the renderer, or attacks the argument.
type claimClass struct {
	rendered bool
	note     string
}

func rendered(where string) claimClass { return claimClass{rendered: true, note: where} }
func inert(why string) claimClass      { return claimClass{rendered: false, note: why} }

// claimSurface maps every config.Config field (the toml:"-" derived sets
// included -- they subtract from claims, which makes them claim inputs) to
// its classification. Keyed by Go field name; the guard test fails on a
// missing field AND on a ghost entry.
var claimSurface = map[string]claimClass{
	"Engine":   rendered("the Engine row"),
	"Template": rendered("the Template row; anything it pulls in renders as the layers/skills it contributes"),
	"Agent":    rendered("the Agent row; the agent skill's grants ride the skill/grant rows"),
	"Base": inert("build input: selects image contents, and box contents are not a status claim -- " +
		"grants ride typed fields, raw blocks degrade (P3)"),
	"Extends":      rendered("the Extends chain row (read off the raw layer; resolution consumes the key)"),
	"SeedPrefs":    inert("one-time copy of the skill-curated, structurally secret-free pref allowlist into a FRESH volume (ADR 0013); no standing grant"),
	"WorktreeBase": inert("host-side workflow preference (where worktree checkouts land); not part of the box"),
	"Defaults": inert("picker-owned section: state about how the NEXT onboarding runs. Stripped whole by resolution, so no member can acquire teeth. " +
		"skip_questions changes whether byre ASKS, and develop says out loud when it acted on it; the shared-auth pick it applies lands as a skills entry, which renders like any other"),
	"SharedAuthLegacy": inert("the pre-2026-07-28 top-level shared_auth spelling; read for upgrades, migrated into [defaults] on the next write"),
	"Sources":          inert("acquisition hints, never auto-fetched (ADR 0029); their only surface is remedy text"),
	"Apt":              inert("build input: package contents of the box, not a grant claim; anti-injection grammar at validation"),
	"Env": rendered("the Env row (keys); reserved BYRE_* refused at validation, BYRE_EGRESS " +
		"re-asserted at run so no key skews what the box is told byre enforces"),
	"EnvFromHost": rendered("the Host env row, from resolveHostEnv outcomes (delivered / NOT passed / overridden); its encrypted: rows are the Credentials rows and the exposure tally's credentials segment, values nowhere"),
	"Files": rendered("guard-collision warnings when an entry covers a security path (warnGuardCollisions); " +
		"artifact shadows (mcp.json, agent context, claude-skills) degrade their delivery lines; otherwise build input"),
	"Skills": rendered("the Skills row and every attributed grant, egress, posture, and containment row"),
	"Mounts": rendered("bind rows; a target covering a byre-managed path renders the blanket runtime-shadow " +
		"disclosure in the Containment register, on status, develop and the apply review (managedPathShadows, ADR 0052)"),
	"Volumes": rendered("volume rows (state/cache/machine split); an exclusive volume is marked in its row and qualifies the " +
		"Worktrees row (ADR 0054); a target covering a byre-managed path renders " +
		"the same runtime-shadow disclosure as Mounts"),
	"Ports":        rendered("port rows via portStatusLine -- the runtime's own normalization, so the row can't lie about defaults"),
	"Egress":       rendered("egress rows + networkLine; config entries marked unenforced when no posture arms them (ADR 0019)"),
	"EgressClosed": rendered("closure rows (closureLine); subtracts from the allowlist, the summary counts, and networkLine's blocked tally"),
	"EgressOffered": inert("declared-but-CLOSED doors (ADR 0020): always inert at enforcement; opening one writes a plain " +
		"egress entry, which then renders like any other"),
	"MCPs":               rendered("mcp rows + the delivery line; carried egress rides the Egress rows attributed mcp:<name>"),
	"MCPClosed":          rendered("mcp closures; a closed declaration renders closed-by, not vanished"),
	"ClaudeSkills":       rendered("claude-skill rows + the delivery line; zero exposure contribution by design"),
	"ClaudeSkillsClosed": rendered("claude-skill closures, mirroring the MCP trio"),
	"Contexts":           rendered("context rows + the delivery line"),
	"ContextsClosed":     rendered("subtracts from the rendered context set at merge (genus uniformity)"),
	"DockerfilePre":      rendered("Raw build rows, verbatim + not-introspected note; presence degrades the network claim"),
	"DockerfilePost":     rendered("Raw build rows, same treatment as DockerfilePre"),
	"RunArgs":            rendered("the Raw run args row, verbatim; presence degrades the network claim"),
}
