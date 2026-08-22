package commands

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// effectiveReview resolves what a preset will EFFECTIVELY run as — the cascade
// (default ⊕ template ⊕ preset) plus the skills it enables — and returns that
// config with the full grant summary (the adoption review machinery, survived
// into preset apply). Best-effort: if the cascade or skills can't be
// expanded, it falls back to the raw layer and says so, so a failure to
// expand never hides grants behind an empty summary.
// effectiveReview is READ-ONLY -- no store-ensure. `preset inspect` must
// mutate nothing (its "Nothing written" is a promise), and apply's caller
// has already ensured the store.
func effectiveReview(paths project.Paths, proposal config.Config) (config.Config, []grantLine) {
	cat, _ := builtins.LoadCatalogRaw(paths.Home)

	effective, err := config.ResolveProposed(proposal)
	if err != nil {
		grants := append(grantSummary(proposal), egressGrantLine(proposal.Egress, "", "", false)...)
		grants = append(grants, mcpGrantLines(configMCPDecls(proposal.MCPs), nil)...)
		grants = append(grants, shadowGrantLines(proposal, skills.Resolved{})...)
		return proposal, sortGrantLines(append(grants,
			grantLine{Text: "could not expand the cascade (" + err.Error() + ") — grants shown are from the raw file only"}))
	}
	grants := grantSummary(effective.Config)
	res, rerr := skills.Resolve(effective.Config, cat)
	if rerr != nil {
		grants = append(grants, egressGrantLine(effective.Egress, "", "", false)...)
		grants = append(grants, mcpGrantLines(configMCPDecls(effective.MCPs), nil)...)
		grants = append(grants, shadowGrantLines(effective.Config, skills.Resolved{})...)
		return effective.Config, sortGrantLines(append(grants,
			grantLine{Text: "could not expand skills (" + rerr.Error() + ") — their grants are NOT shown"}))
	}
	posture, postureSkill := res.NetworkPosture()
	grants = append(grants, egressGrantLine(effective.Egress, posture, postureSkill, true)...)
	grants = append(grants, skillGrantSummary(res)...)
	grants = append(grants, shadowGrantLines(effective.Config, res)...)
	// The EFFECTIVE MCP set — skill contributions included, attributed —
	// so a preset can't enable a skill whose wiring (and carried reach)
	// goes undisclosed at confirm time.
	mcps, merr := skills.MCPSet(effective, res)
	grants = append(grants, mcpGrantLines(mcps, merr)...)
	return effective.Config, sortGrantLines(grants)
}

// shadowGrantLines renders the runtime-shadow disclosure (ADR 0052) at
// containment weight. A proposed mount or volume over a byre-managed path
// disclaims byre's own construction, so the review that asks for consent must
// carry it as such -- as an ordinary volume row it reads like storage, and
// the disclosure would arrive at the first develop, after the answer. The
// fallback paths pass an empty Resolved: /etc/byre and the launcher are known
// without skills, and a hook that resolution never reached names nothing.
func shadowGrantLines(cfg config.Config, res skills.Resolved) []grantLine {
	var out []grantLine
	for _, sh := range managedPathShadows(cfg, res) {
		out = append(out, grantLine{Text: ManagedPathShadowText(sh), Containment: true})
	}
	return out
}

// sortGrantLines puts containment holes first, then credential changes, then
// cross-project reach, then the rest -- stable within each class so enable
// order is preserved.
func sortGrantLines(in []grantLine) []grantLine {
	var contain, cred, cross, rest []grantLine
	for _, g := range in {
		switch {
		case g.Containment:
			contain = append(contain, g)
		case g.Credential:
			cred = append(cred, g)
		case g.CrossProject:
			cross = append(cross, g)
		default:
			rest = append(rest, g)
		}
	}
	return append(append(append(contain, cred...), cross...), rest...)
}

// skillGrantSummary lists the runtime grants the enabled skills contribute, so
// they're shown at grant-review time alongside the config-level grants. Skill
// volumes appear exactly when they reach beyond this box: machine scope
// (cross-project — the shared-credential shape) or a host seed. Per-project
// volumes are the sandbox model itself, not a grant. Containment declarations
// are a separate top-sorted class (above cross-project): a standing host-wide
// hole must not hide below machine volumes.
func skillGrantSummary(res skills.Resolved) []grantLine {
	var contain, cross, rest []grantLine
	for _, c := range res.Containments() {
		contain = append(contain, grantLine{
			Text:        fmt.Sprintf("skill %q: %s", c.Skill, c.Text),
			Containment: true,
		})
	}
	for _, g := range res.Grants() {
		for _, m := range g.Mounts {
			rest = append(rest, grantLine{Text: fmt.Sprintf("skill %q mounts %s -> %s (%s)", g.Skill, m.Host, m.Target, orDefault(m.Mode, "ro"))})
		}
		if len(g.Caps) > 0 {
			rest = append(rest, grantLine{Text: fmt.Sprintf("skill %q adds capabilities: %s", g.Skill, strings.Join(g.Caps, ", "))})
		}
		if len(g.RunArgs) > 0 {
			rest = append(rest, grantLine{Text: fmt.Sprintf("skill %q adds raw docker run args (can grant --privileged, the docker socket, host net): %s", g.Skill, strings.Join(g.RunArgs, " "))})
		}
		for _, p := range g.SockGroups {
			rest = append(rest, grantLine{Text: fmt.Sprintf("skill %q grants sock group access via %s (gid resolved at launch; wider than the named path)", g.Skill, p)})
		}
	}
	for _, v := range res.Volumes() {
		if v.MachineScoped() {
			cross = append(cross, grantLine{Text: fmt.Sprintf("skill volume %q is machine-scoped — shared with every project on this machine; this box can read and write it", v.Name), CrossProject: true})
		}
		if v.Seed != nil && v.Seed.Host != "" {
			rest = append(rest, grantLine{Text: fmt.Sprintf("skill volume %q seeds from host path: %s", v.Name, v.Seed.Host)})
		}
	}
	n := 0
	for _, b := range res.BuildBlocks() {
		n += len(b.Dockerfile)
	}
	if n > 0 {
		rest = append(rest, grantLine{Text: fmt.Sprintf("skills inject %d raw Dockerfile line(s)", n)})
	}
	// Top-sort: containment, then cross-project, then the rest.
	return append(append(contain, cross...), rest...)
}

// grantLine is one ⚠ row of the grant review. Containment marks the
// loudest class (host-wide hole); CrossProject marks reach beyond this box
// (machine-scoped volumes); Credential marks a change to a credential value or
// to the identity that opens one. All three render emphasized; containment
// sorts above cross-project so a docker-host-class grant can't hide below
// shared volumes, and credential lines sort between them -- narrower than a
// host-wide hole, but a value the user goes on USING, so it must not sit below
// a shared volume they can already see.
type grantLine struct {
	Text         string
	Containment  bool
	CrossProject bool
	Credential   bool
}

func plainGrants(texts ...string) []grantLine {
	out := make([]grantLine, len(texts))
	for i, t := range texts {
		out[i] = grantLine{Text: t}
	}
	return out
}

// grantSummary lists the parts of a proposed config that grant power — the
// things a reviewer must see before applying, since they can widen the
// sandbox. It must cover every category the glossary calls a Grant; egress is
// the one exception handled by the caller (its live/inert status needs the
// resolved posture, which needs the skills expanded).
func grantSummary(c config.Config) []grantLine {
	var s []grantLine
	if len(c.Mounts) > 0 {
		var m []string
		for _, x := range c.Mounts {
			mode := orDefault(x.Mode, "ro")
			// A disabled mount grants nothing today, but applying it plants an
			// entry one flip away from a grant — show it, marked, not hidden.
			if x.Disabled {
				mode += ", disabled"
			}
			m = append(m, fmt.Sprintf("%s->%s(%s)", x.Host, x.Target, mode))
		}
		s = append(s, grantLine{Text: "mounts host paths: " + strings.Join(m, ", ")})
	}
	if len(c.RunArgs) > 0 {
		s = append(s, grantLine{Text: "raw docker run args (can grant --privileged, the docker socket, host net): " + strings.Join(c.RunArgs, " ")})
	}
	if n := len(c.DockerfilePre) + len(c.DockerfilePost); n > 0 {
		s = append(s, grantLine{Text: fmt.Sprintf("injects %d raw Dockerfile line(s) (arbitrary build commands)", n)})
	}
	for _, v := range c.Volumes {
		// A machine-scoped volume is cross-project reach — the shared-
		// credential mechanism is exactly this shape — and MUST be the
		// loudest line here, whatever the volume claims to hold.
		if v.MachineScoped() {
			s = append(s, grantLine{Text: fmt.Sprintf("machine-scoped volume %q — shared with every project on this machine; this box can read and write it", v.Name), CrossProject: true})
		}
		if v.Seed != nil && v.Seed.Host != "" {
			s = append(s, grantLine{Text: "seeds a volume from a host path: " + v.Seed.Host})
		}
	}
	if ports := portGrantList(c.Ports); len(ports) > 0 {
		s = append(s, grantLine{Text: "binds host ports: " + strings.Join(ports, ", ")})
	}
	// env_from_host beyond byre's own shipped git-identity defaults is a
	// proposal asking for HOST values — exactly what this summary exists to
	// surface. The core entries are every box's baseline (visible in status),
	// not this proposal's ask, so they don't cry wolf here.
	if extra := extraHostEnv(c.EnvFromHost); len(extra) > 0 {
		s = append(s, grantLine{Text: "passes host values into the box's env: " + strings.Join(extra, ", ")})
	}
	// Config [env] is not a host grant, but it reaches every box process
	// and bakes into the image -- content you consented to without
	// authoring deserves the same ⚠ weight whichever table it rides in
	// (the review's preset-vector finding: an [env] line rendered as one
	// unremarkable TOML line in a body a user may skim). Reserved BYRE_*
	// keys never get this far: validation refuses the apply outright.
	if len(c.Env) > 0 {
		s = append(s, grantLine{Text: "sets env in every box process, baked into the image: " + strings.Join(slices.Sorted(maps.Keys(c.Env)), ", ")})
	}
	if len(c.Skills) > 0 {
		s = append(s, grantLine{Text: "enables skills (each can add mounts/caps/run_args/volumes): " + strings.Join(c.Skills, ", ")})
	}
	return s
}

// configMCPDecls wraps a config-layer [[mcp]] list as config-attributed
// declarations for the fallback review paths (cascade or skills failed to
// expand — the resolved path forms the real set via skills.MCPSet). `!name`
// closure markers remove wiring and grant nothing; skipped like port
// removal markers.
func configMCPDecls(mcps []config.MCP) []skills.MCPDecl {
	var out []skills.MCPDecl
	for _, m := range mcps {
		// IsRemoval, not a bare prefix test: a bare "!" is an invalid entry, not
		// a closure, and this is the raw fallback -- omitting it would hide its
		// grants from the summary exactly when resolution already failed.
		if config.IsRemoval(m.Name) {
			continue
		}
		out = append(out, skills.MCPDecl{Skill: skills.MCPFromConfig, MCP: m})
	}
	return out
}

// mcpGrantLines renders MCP declarations for the preset-apply review.
// Wiring, not grants (ADR 0033) — but the carried reach must be spelled out
// per entry before confirm: the endpoint a remote url implies, declared
// extra egress, and the env names the server consumes. setErr is MCPSet's
// cross-source duplicate reject: apply would write a config develop then
// refuses, so the review says so instead of hiding the conflict.
func mcpGrantLines(decls []skills.MCPDecl, setErr error) []grantLine {
	var out []grantLine
	for _, d := range decls {
		m := d.MCP
		src := "config"
		if d.Skill != skills.MCPFromConfig {
			src = fmt.Sprintf("skill %q", d.Skill)
		}
		var desc string
		if m.Remote() {
			desc = "remote " + m.URL
			if host, port, ok := m.Endpoint(); ok {
				desc += fmt.Sprintf(" (implies egress to %s:%d)", host, port)
			}
		} else {
			desc = "local process " + strings.Join(m.Command, " ")
		}
		if len(m.Egress) > 0 {
			desc += "; declared egress " + strings.Join(m.Egress, ", ")
		}
		if names := m.HeaderNames(); len(names) > 0 {
			desc += "; sends headers " + strings.Join(names, ", ")
		}
		if consumed := m.ConsumedEnv(); len(consumed) > 0 {
			desc += "; consumes env " + strings.Join(consumed, ", ")
		}
		out = append(out, grantLine{Text: fmt.Sprintf("wires MCP server %s (%s): %s", m.Name, src, desc)})
	}
	if setErr != nil {
		out = append(out, grantLine{Text: "mcp declarations conflict (develop will refuse): " + setErr.Error()})
	}
	return out
}

// extraHostEnv lists env_from_host entries (sorted) that differ from byre's
// shipped CoreEnvFromHost defaults — the additions a proposal is actually
// asking for. Disabled ("") entries grant nothing and are skipped.
func extraHostEnv(m map[string]string) []string {
	core := config.CoreEnvFromHost()
	var keys []string
	for k, src := range m {
		if src != "" && core[k] != src {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		// RenderSource, not the raw value: an encrypted row's payload is a
		// wall of base64 that would bury the rest of this consent gate.
		out[i] = k + " <- " + config.RenderSource(m[k])
	}
	return out
}

// credentialRejectAdvice is the second half of every value-change line. A
// credential value is the one thing in this review a reader CANNOT check by
// looking: the ciphertext elides, and byre cannot tell a rotation the user
// performed from a value swapped in by whatever wrote the file. So the line
// asks the only question that distinguishes them.
const credentialRejectAdvice = " — if you didn't rotate this credential, reject"

// credentialReviewLines is the preset gate's credential annotation: what
// changes about this file's credentials between the version in the store and
// the version being applied.
//
// It exists because the ordinary review cannot show it. Value legibility was
// never a credential row's job — the ciphertext elides everywhere — so a
// swapped blob and a rotation look identical in the diff, and the file-local
// [credentials] block does not reach Config at all (Parse drops it
// deliberately), so an identity replacement is invisible in the grant summary.
// The repo is agent-writable and apply is the reviewed bridge into the trusted
// store, so this is where a minted, swapped, transplanted or replayed value
// gets named. It does not GATE: the review is legibility at the consent gate,
// the way byre answers everything (P1/P2).
//
// Judged over both files' RAW bytes, and over EITHER side being a credential:
// replacing an encrypted row with a plaintext scheme (or with an [env]
// literal, which takes the key out of env_from_host entirely) changes the same
// delivered value, and a classifier that only looked at the new side would
// wave it through.
func credentialReviewLines(store, content []byte) []grantLine {
	before, beforeErr := readCredentialView(store)
	after, afterErr := readCredentialView(content)
	if beforeErr != nil || afterErr != nil {
		// Degrade, never guess: an unreadable side means byre cannot say
		// whether a credential moved, and silence would read as "nothing did".
		err := afterErr
		if err == nil {
			err = beforeErr
		}
		return []grantLine{{Text: "could not compare this file's credentials (" + err.Error() + ") — credential changes are NOT shown below", Credential: true}}
	}
	var out []grantLine
	for _, key := range slices.Sorted(maps.Keys(unionKeys(before.sources, after.sources))) {
		old, hadOld := before.sources[key]
		nw, hadNew := after.sources[key]
		if !config.IsCredentialSource(old) && !config.IsCredentialSource(nw) {
			continue
		}
		switch {
		case hadOld && hadNew && old != nw:
			out = append(out, grantLine{Text: fmt.Sprintf("%s: credential value changed (%s -> %s)%s",
				key, before.render(key), after.render(key), credentialRejectAdvice), Credential: true})
		case !hadOld && hadNew:
			// Quieter: a key that was not here is a new row, and the grant
			// summary already lists it. Say what appeared.
			out = append(out, grantLine{Text: fmt.Sprintf("%s: credential row appeared (%s)", key, after.render(key)), Credential: true})
		case hadOld && !hadNew:
			out = append(out, grantLine{Text: fmt.Sprintf("%s: credential row vanished (was %s) — its value is gone from this file", key, before.render(key)), Credential: true})
		}
	}
	return append(out, credentialBlockLine(before.block, before.hasBlock, after.block, after.hasBlock)...)
}

// credentialBlockLine names a change to the file-local [credentials] block.
// The block is what OPENS this file's rows and what future `set`s encrypt to,
// so replacing it is a stronger move than changing any one value: every value
// set afterward answers to the incoming identity's passphrase, not the user's.
func credentialBlockLine(before config.CredentialsBlock, hadBefore bool, after config.CredentialsBlock, hasAfter bool) []grantLine {
	same := string(before.Identity) == string(after.Identity) && before.Recipient == after.Recipient
	switch {
	case hadBefore && hasAfter && !same:
		return []grantLine{{Text: "this preset replaces the file's credentials identity — its rows open under ITS passphrase, and values you set afterward would encrypt to ITS recipient; if you didn't do this, reject", Credential: true}}
	case !hadBefore && hasAfter:
		return []grantLine{{Text: "this preset brings its own credentials identity — its rows open under ITS passphrase, and values you set afterward would encrypt to ITS recipient; if you didn't do this, reject", Credential: true}}
	case hadBefore && !hasAfter:
		return []grantLine{{Text: "this preset removes the file's credentials identity — nothing here can open a credential row afterward; if you didn't do this, reject", Credential: true}}
	}
	return nil
}

// fileCredentialView is one physical config file's credential-relevant
// content: what value each env key carries, and the block that opens the
// encrypted ones.
type fileCredentialView struct {
	// sources maps env key -> its env_from_host source, or envLiteralSource for
	// a key an [env] literal owns. The two tables share a key space (ADR 0026:
	// a literal takes the key out of env_from_host), so a credential replaced
	// by a literal has to compare as a CHANGE to that key, not as a row that
	// vanished next to an unrelated literal that appeared.
	sources  map[string]string
	block    config.CredentialsBlock
	hasBlock bool
}

// envLiteralSource stands for "an [env] literal owns this key". It is not a
// value: [env] literals are baked into the image and the review prints env
// KEYS, never their values (the standing keys-not-values rule), and this side
// only ever has to compare unequal to a credential row.
const envLiteralSource = "[env] literal"

func (v fileCredentialView) render(key string) string {
	src, ok := v.sources[key]
	if !ok {
		return "(unset)"
	}
	if src == "" {
		// The ratified per-project disable idiom, which reads as nothing at all
		// unless it is spelled out.
		return `"" (disabled)`
	}
	return config.RenderSource(src)
}

func readCredentialView(raw []byte) (fileCredentialView, error) {
	v := fileCredentialView{sources: map[string]string{}}
	if len(raw) == 0 {
		// No file on this side: everything the other side carries is new. Not
		// an error -- `preset apply` into a project with no config is the
		// ordinary first-apply.
		return v, nil
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		return v, err
	}
	maps.Copy(v.sources, cfg.EnvFromHost)
	for k := range cfg.Env {
		v.sources[k] = envLiteralSource
	}
	if v.block, v.hasBlock, err = config.ParseCredentialsBlock(raw); err != nil {
		return v, err
	}
	return v, nil
}

func unionKeys(a, b map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// portGrantList renders the effective port bindings compactly (removal
// markers grant nothing and are skipped). PortEffective owns the publish
// defaults; this list must show exactly what the runtime will bind.
func portGrantList(ports []config.Port) []string {
	var out []string
	for _, p := range ports {
		if p.Remove {
			continue
		}
		iface, host := config.PortEffective(p)
		out = append(out, fmt.Sprintf("%s:%d->%d", iface, host, p.Container))
	}
	return out
}

// egressGrantLine renders the config-level egress allowlist entries with
// their honest status: live when a resolved skill declares a restrictive
// posture, inert-until otherwise. postureKnown=false (the cascade or skills
// could not be expanded) falls back to the conditional phrasing — an entry is
// one posture-flip from a grant, so it is never hidden (the disabled-mount
// stance). Skill-declared egress is NOT summarized: those are the skill
// author's vouched functional endpoints, not the proposal's ask.
func egressGrantLine(entries []string, posture, postureSkill string, postureKnown bool) []grantLine {
	if len(entries) == 0 {
		return nil
	}
	list := strings.Join(entries, ", ")
	switch {
	case postureKnown && config.PostureEnforcesAllowlist(posture):
		return plainGrants(fmt.Sprintf("opens firewall egress to: %s (live — skill %q sets posture %q)", list, postureSkill, posture))
	case postureKnown && posture != "":
		// open-denylist: the network is open, so allowlist entries are as
		// unenforced as with no posture at all (ADR 0030) — saying "opens"
		// would dress noise up as a grant.
		return plainGrants(fmt.Sprintf("adds egress allowlist entries: %s (inert — posture %q leaves the network open; live under a restrictive one)", list, posture))
	case postureKnown:
		return plainGrants(fmt.Sprintf("adds egress allowlist entries: %s (inert now — no restrictive posture enabled; live the moment one is)", list))
	default:
		return plainGrants(fmt.Sprintf("adds egress allowlist entries: %s (live under a restrictive network posture)", list))
	}
}
