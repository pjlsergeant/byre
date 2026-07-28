package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
)

// statuslaunch.go is the Running / Next-launch split.
//
// The ruling it implements: WHEN A BOX IS RUNNING IT IS ALWAYS THE SUBJECT.
// status's grant rows then render from that box's launch record and say so,
// and a `Next launch` section lists only what DIFFERS in the current config.
// Not a second full block -- doubling the screen for two changed rows buries
// the signal. With no box, nothing is relabelled: today's semantics stand,
// because the rows ARE the next launch.
//
// The mechanism is deliberately one substitution and one compare. statusInfo
// is resolved from the current config exactly as before; when a verified
// record exists, the exposure fields are REPLACED by the record's, and the
// config-derived values are kept aside for the diff. Every row, every escape,
// every wrapping decision then rides the funnel it already rode -- there is no
// second renderer to drift.

// launchEgressSource attributes an egress row that came off a launch record.
// The record holds the ONE resolved string the netns helper enforced, not the
// per-entry attribution -- so the row says where the entry came from (the
// record) rather than inventing the skill that once asked for it.
const launchEgressSource = "launch record"

// postureUnknown reports whether byre cannot say what the network posture is.
// Only true when the CURRENT skill set is what the row speaks for: a verified
// launch record carries the posture the box was launched with, so a skill set
// that stopped resolving since says nothing about the box on screen (it is a
// next-launch problem, and the Next launch section carries it).
func (s statusInfo) postureUnknown() bool { return s.Launch == nil && s.SkillErr != "" }

// launchDelta is one line of the Next launch section: the sign carries the
// direction, the text reuses the row vocabulary of the block it belongs to.
type launchDelta struct {
	Sign string // "-" gone at next launch, "+" arriving, "~" changed
	Text string
}

func (d launchDelta) String() string { return d.Sign + " " + d.Text }

// exposureView is the comparable half of a status page: the dimensions a
// launch record can speak for. Both sides of the diff are built into this one
// shape, so the compare is field-by-field and cannot accidentally compare a
// rendered string on one side with a struct on the other.
type exposureView struct {
	Binds    []config.Mount
	SelfEdit string
	Ports    []config.Port
	Volumes  []config.Volume
	Posture  string
	Egress   []string // host:port, deduped, sorted
	Closed   []string
	RunArgs  []string
	Skills   []string
	Base     string
}

// nextLaunchViewOf snapshots the config-derived exposure BEFORE a record
// replaces it. Base rides along because a changed base image is a real
// next-launch difference and nothing else on the page reports it.
func nextLaunchViewOf(s statusInfo, base string) exposureView {
	v := exposureView{
		Binds:    s.Binds,
		SelfEdit: s.SelfEdit,
		Ports:    s.Ports,
		Volumes:  s.Volumes,
		Posture:  s.NetPosture,
		Closed:   s.EgressClosed,
		RunArgs:  s.RunArgs,
		Skills:   s.Skills,
		Base:     base,
	}
	seen := map[string]bool{}
	for _, a := range s.Egress {
		hp := fmt.Sprintf("%s:%d", a.Host, a.Port)
		if !seen[hp] {
			seen[hp] = true
			v.Egress = append(v.Egress, hp)
		}
	}
	sort.Strings(v.Egress)
	return v
}

// recordedViewOf projects a launch record into the same shape.
//
// paths is needed for one exclusion: a worktree box carries two same-path git
// binds byre adds itself (ADR 0009), which the config-derived page has never
// listed -- the Worktree of row announces the arrangement instead. Dropping
// them here keeps the two sides comparable; carrying them would put a bind in
// the diff on every worktree launch that nothing in the config could ever
// match.
func recordedViewOf(rec *launchRecord, paths project.Paths) exposureView {
	v := exposureView{
		Posture: rec.Network.Posture,
		Closed:  rec.Network.EgressDeny,
		RunArgs: rec.RunArgs,
		Base:    rec.Image.Base,
	}
	for _, b := range rec.Binds {
		switch {
		case b.Target == "/workspace":
			continue // the Project row's subject, not a Host mounts row
		case b.Target == selfEditTarget:
			v.SelfEdit = b.Host
		case paths.IsWorktree && (b.Target == paths.CommonGitDir || b.Target == paths.WorkDir):
			continue // byre's own worktree git binds; never listed either side
		default:
			v.Binds = append(v.Binds, config.Mount{Host: b.Host, Target: b.Target, Mode: b.Mode})
		}
	}
	for _, p := range rec.Ports {
		v.Ports = append(v.Ports, config.Port{Interface: p.Interface, Host: p.Host, Container: p.Container})
	}
	for _, vol := range rec.Volumes {
		name := vol.Decl
		if name == "" {
			name = vol.Name // a record without the declared name still names something
		}
		v.Volumes = append(v.Volumes, config.Volume{Name: name, Role: vol.Role, Target: vol.Target, Scope: vol.Scope})
	}
	v.Egress = append(v.Egress, launchEgress(rec.Network.Egress)...)
	sort.Strings(v.Egress)
	for _, sk := range rec.Skills {
		v.Skills = append(v.Skills, sk.Name)
	}
	return v
}

// applyLaunchRecord makes the running box the page's subject: every exposure
// field the record can speak for is replaced by the record's, and the
// config-derived view is returned for the diff.
//
// What it deliberately does NOT replace: the wiring rows (MCP, Claude Skills,
// standing instructions), the build rows (template, raw build lines, config
// [env]), the host-env passthrough, and the skill-declared containment holes.
// The record is exposure-level by design -- serializing the resolved config
// into it was rejected precisely so it could not become a second copy of a
// moving schema -- and those rows describe configuration and construction
// rather than what the engine was told. The Container row says which is which.
func applyLaunchRecord(s *statusInfo, rec *launchRecord, paths project.Paths) (now, next exposureView) {
	next = nextLaunchViewOf(*s, s.Base)
	rv := recordedViewOf(rec, paths)

	s.Binds = rv.Binds
	s.SelfEdit = rv.SelfEdit
	s.Ports = rv.Ports
	s.Volumes = rv.Volumes
	s.NetPosture = rv.Posture
	s.NetPostureSkill = rec.Network.PostureSkill
	s.EgressClosed = rv.Closed
	s.RunArgs = rv.RunArgs
	s.Skills = rv.Skills
	s.LaunchSkills = rec.Skills
	s.Base = rv.Base
	s.Image = rec.Image
	// EnvKeys (config [env]) is deliberately LEFT config-derived: those keys
	// are baked into the image by the Dockerfile, never passed as `-e`, so
	// they are not in the record and blanking the row would lose them
	// entirely. It joins Template and the raw build lines in the set the
	// Container row calls "the current config".
	s.BoxEnvKeys = rec.EnvKeys
	// The claim-degradation inputs come off the record too, or the Network row
	// would hedge (or fail to hedge) on today's config over yesterday's box.
	s.ProjectRunArgs = rec.Network.ProjectRunArgs
	s.RecordedRawBuild = rec.Network.RawBuild
	s.SkillReservedEnv = launchReservedEnv(rec.Network.ReservedEnv)

	s.Egress = nil
	for _, hp := range rv.Egress {
		host, port, err := config.ParseEgress(hp)
		if err != nil {
			continue // a record byre wrote cannot hold an unparseable entry
		}
		s.Egress = append(s.Egress, skills.EgressAllow{Skill: launchEgressSource, Host: host, Port: port})
	}
	return rv, next
}

// diffLaunch is the whole of the Next launch section: a row-by-row compare of
// the record against the values status already computed. No new resolution,
// one compare -- and silence when nothing differs, because "unchanged" is a
// line nobody needs.
func diffLaunch(now, next exposureView) []launchDelta {
	var out []launchDelta
	add := func(sign, format string, args ...any) {
		out = append(out, launchDelta{Sign: sign, Text: fmt.Sprintf(format, args...)})
	}

	if now.Base != next.Base && next.Base != "" {
		add("~", "Base %s -> %s  (rebuild required)", orDefault(now.Base, "(default)"), next.Base)
	}
	diffSet(bindKeys(now.Binds), bindKeys(next.Binds), func(sign, text string) { add(sign, "Bind %s", text) })
	if now.SelfEdit != next.SelfEdit {
		switch {
		case next.SelfEdit == "":
			add("-", "Self-edit store mount %s -> %s  (rw)", now.SelfEdit, selfEditTarget)
		case now.SelfEdit == "":
			add("+", "Self-edit store mount %s -> %s  (rw)", next.SelfEdit, selfEditTarget)
		}
	}
	diffSet(portKeys(now.Ports), portKeys(next.Ports), func(sign, text string) { add(sign, "Port %s", text) })
	diffSet(volumeKeys(now.Volumes), volumeKeys(next.Volumes), func(sign, text string) { add(sign, "Volume %s", text) })
	if now.Posture != next.Posture {
		if now.Posture != "" {
			add("-", "Network %s", now.Posture)
		}
		if next.Posture != "" {
			add("+", "Network %s", next.Posture)
		}
		if next.Posture == "" {
			add("+", "Network open  (no posture declared)")
		}
	}
	diffSet(now.Egress, next.Egress, func(sign, text string) { add(sign, "Egress %s", text) })
	diffSet(now.Closed, next.Closed, func(sign, text string) { add(sign, "Closed %s", text) })
	diffSet(now.Skills, next.Skills, func(sign, text string) { add(sign, "Skill %s", text) })
	// run_args are ORDERED and byre does not introspect them, so a set diff
	// would lie about a reordering. One line either way, verbatim.
	if strings.Join(now.RunArgs, " ") != strings.Join(next.RunArgs, " ") {
		add("~", "Raw run args %s -> %s  (passed through; not introspected)",
			orDefault(strings.Join(now.RunArgs, " "), "(none)"),
			orDefault(strings.Join(next.RunArgs, " "), "(none)"))
	}
	return out
}

// diffSet emits a "-" for every entry present now and gone next, then a "+"
// for every arrival, preserving each side's own order. Duplicates on one side
// are collapsed: these are exposure SETS (a target mounted twice is refused
// upstream), and a count would report a config that cannot exist.
func diffSet(now, next []string, emit func(sign, text string)) {
	inNext := map[string]bool{}
	for _, s := range next {
		inNext[s] = true
	}
	inNow := map[string]bool{}
	for _, s := range now {
		inNow[s] = true
	}
	seen := map[string]bool{}
	for _, s := range now {
		if !inNext[s] && !seen[s] {
			seen[s] = true
			emit("-", s)
		}
	}
	seen = map[string]bool{}
	for _, s := range next {
		if !inNow[s] && !seen[s] {
			seen[s] = true
			emit("+", s)
		}
	}
}

// The key functions render one entry in the SAME words its status row uses, so
// a delta line and the row it refers to are recognisably the same thing.

func bindKeys(ms []config.Mount) []string {
	var out []string
	for _, m := range ms {
		if m.Disabled {
			continue // a disabled mount produces no bind; the record has none either
		}
		out = append(out, fmt.Sprintf("%s -> %s  (%s)", m.Host, m.Target, orDefault(m.Mode, "ro")))
	}
	return out
}

func portKeys(ps []config.Port) []string {
	var out []string
	for _, p := range ps {
		out = append(out, portStatusLine(p))
	}
	return out
}

func volumeKeys(vs []config.Volume) []string {
	var out []string
	for _, v := range vs {
		scope := v.Scope
		if scope == "" {
			scope = "project"
		}
		out = append(out, fmt.Sprintf("%s -> %s  (%s, %s)", v.Name, v.Target, orDefault(v.Role, "cache"), scope))
	}
	return out
}
