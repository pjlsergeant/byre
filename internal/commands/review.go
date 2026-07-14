package commands

import (
	"fmt"
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
		return proposal, append(grants,
			grantLine{Text: "could not expand the cascade (" + err.Error() + ") — grants shown are from the raw file only"})
	}
	grants := grantSummary(effective)
	res, rerr := skills.Resolve(effective, cat)
	if rerr != nil {
		grants = append(grants, egressGrantLine(effective.Egress, "", "", false)...)
		return effective, append(grants,
			grantLine{Text: "could not expand skills (" + rerr.Error() + ") — their grants are NOT shown"})
	}
	posture, postureSkill := res.NetworkPosture()
	grants = append(grants, egressGrantLine(effective.Egress, posture, postureSkill, true)...)
	grants = append(grants, skillGrantSummary(res)...)
	return effective, sortGrantLines(grants)
}

// sortGrantLines puts containment holes first, then cross-project reach, then
// the rest -- stable within each class so enable order is preserved.
func sortGrantLines(in []grantLine) []grantLine {
	var contain, cross, rest []grantLine
	for _, g := range in {
		switch {
		case g.Containment:
			contain = append(contain, g)
		case g.CrossProject:
			cross = append(cross, g)
		default:
			rest = append(rest, g)
		}
	}
	return append(append(contain, cross...), rest...)
}

// skillGrantSummary lists the runtime grants the enabled skills contribute, so
// they're shown at adoption time alongside the config-level grants. Skill
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

// grantLine is one ⚠ row of the adoption review. Containment marks the
// loudest class (host-wide hole); CrossProject marks reach beyond this box
// (machine-scoped volumes). Both render emphasized; containment sorts above
// cross-project so a docker-host-class grant can't hide below shared volumes.
type grantLine struct {
	Text         string
	Containment  bool
	CrossProject bool
}

func plainGrants(texts ...string) []grantLine {
	out := make([]grantLine, len(texts))
	for i, t := range texts {
		out[i] = grantLine{Text: t}
	}
	return out
}

// grantSummary lists the parts of a proposed config that grant power — the
// things a reviewer must see before adopting, since they can widen the
// sandbox. It must cover every category the glossary calls a Grant; egress is
// the one exception handled by the caller (its live/inert status needs the
// resolved posture, which needs the skills expanded).
func grantSummary(c config.Config) []grantLine {
	var s []grantLine
	if len(c.Mounts) > 0 {
		var m []string
		for _, x := range c.Mounts {
			mode := orDefault(x.Mode, "ro")
			// A disabled mount grants nothing today, but adopting it plants an
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
	if len(c.Skills) > 0 {
		s = append(s, grantLine{Text: "enables skills (each can add mounts/caps/run_args/volumes): " + strings.Join(c.Skills, ", ")})
	}
	return s
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
		out[i] = k + " <- " + m[k]
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
	case postureKnown && posture != "":
		return plainGrants(fmt.Sprintf("opens firewall egress to: %s (live — skill %q sets posture %q)", list, postureSkill, posture))
	case postureKnown:
		return plainGrants(fmt.Sprintf("adds egress allowlist entries: %s (inert now — no restrictive posture enabled; live the moment one is)", list))
	default:
		return plainGrants(fmt.Sprintf("adds egress allowlist entries: %s (live under a restrictive network posture)", list))
	}
}
