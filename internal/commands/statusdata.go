package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
)

// statusdata.go is `byre status --data`: everything `--full` renders, as
// JSON, so a reader can answer "what can this box touch?" without parsing a
// page laid out for eyes.
//
// VERSIONED, NOT FROZEN. `version` moves when the shape changes and the
// change is written up in CHANGES.md; nothing consumes this yet, and byre
// does not advertise it as a scripting interface. When it gains an external
// consumer it becomes frozen the way deliver's wire protocol is
// (internal/deliver/proto.go: a FROZEN comment and a version constant that
// only ever grows) -- and it becomes frozen the moment byre documents it as
// scriptable, which is the same event said two ways.
//
// Env VALUES never appear here, only KEYS -- the exit report's rule, and it
// holds on every surface. `run_args` is verbatim because status already
// prints it verbatim and the configuration reference already says that is
// not the place for a secret.

// StatusDataVersion is the shape of the --data document. Bump it when a
// consumer would have to change, and note the change in CHANGES.md.
//
// 2 (2026-07-28): the Running / Next-launch split. `subject` says whose box
// the exposure fields describe, `launch` carries the record, and
// `changes_on_next_launch` carries the diff -- the same content --full
// renders, which is the rule this document is held to.
// 2 (2026-07-29, same unreleased batch): each volume carries `sharing`,
// beside the `(exclusive)` mark the page prints. Folded into 2 rather than
// bumped: no release has published 2, so there is no consumer of the shape
// without the key to migrate.
const StatusDataVersion = 2

type statusData struct {
	Version int `json:"version"`

	// Subject is what the exposure fields below describe:
	// "running_box" when a verified launch record was read (the box IS the
	// subject whenever one exists), else "next_launch". A reader that ignores
	// this field will read next-launch config as though it were live state --
	// the exact confusion the record exists to end.
	Subject string `json:"subject"`

	ProjectID  string   `json:"project_id,omitempty"`
	Workdir    string   `json:"workdir"`
	WorktreeOf string   `json:"worktree_of,omitempty"`
	Agent      string   `json:"agent"`
	Template   string   `json:"template,omitempty"`
	Extends    []string `json:"extends,omitempty"`
	PresetNote string   `json:"preset_note,omitempty"`
	SelfEdit   string   `json:"self_edit,omitempty"`

	Engine  statusDataEngine  `json:"engine"`
	Network statusDataNetwork `json:"network"`

	Ports   []statusDataPort   `json:"ports"`
	Binds   []statusDataBind   `json:"binds"`
	Volumes []statusDataVolume `json:"volumes"`

	Skills      []statusDataSkill    `json:"skills"`
	SkillError  string               `json:"skill_error,omitempty"`
	SkillGrants []statusDataGrant    `json:"skill_grants,omitempty"`
	ReservedEnv []statusDataReserved `json:"reserved_env,omitempty"`

	MCPServers  []statusDataMCP `json:"mcp_servers"`
	MCPClosed   []string        `json:"mcp_closed,omitempty"`
	MCPDelivery string          `json:"mcp_delivery,omitempty"`

	ClaudeSkills         []statusDataClaudeSkill `json:"claude_skills"`
	ClaudeSkillsClosed   []string                `json:"claude_skills_closed,omitempty"`
	ClaudeSkillsDelivery string                  `json:"claude_skills_delivery,omitempty"`

	Instructions         []statusDataContext `json:"instructions"`
	InstructionsDelivery string              `json:"instructions_delivery,omitempty"`

	// Credentials is the declared set with value-state; credential_unlock is
	// the running box's LAUNCH-TIME unlock outcome from its record. Neither
	// is a live-state claim — byre does not probe the box. Values never
	// appear anywhere in this document.
	Credentials      []statusDataCredential `json:"credentials,omitempty"`
	CredentialUnlock string                 `json:"credential_unlock,omitempty"`

	HostEnv []statusDataHostEnv `json:"host_env,omitempty"`
	EnvKeys []string            `json:"env_keys,omitempty"`

	RunArgs  []string `json:"run_args,omitempty"`
	BuildRaw []string `json:"build_raw,omitempty"`

	Containments   []statusDataContainment `json:"containments,omitempty"`
	ManagedShadows []statusDataShadow      `json:"managed_path_shadows,omitempty"`

	Container statusDataContainer `json:"container"`

	// Launch is the running box's record when byre read and VERIFIED one; it
	// is present with a state and a note and no record when byre could not,
	// so a reader learns why rather than inferring absence.
	Launch *statusDataLaunch `json:"launch,omitempty"`
	// Changes is what differs in the current config, in the page's own words.
	// Absent when nothing differs.
	Changes []string `json:"changes_on_next_launch,omitempty"`
}

type statusDataLaunch struct {
	// State is verified / pre_record / missing / tampered / unreadable /
	// newer_schema. Only "verified" means the fields above describe the box.
	State string `json:"state"`
	// Note is the human sentence the page prints for a non-verified state.
	Note string `json:"note,omitempty"`
	// Record is the content address the container carries, full length.
	Record  string `json:"record,omitempty"`
	Schema  int    `json:"schema,omitempty"`
	Byre    string `json:"byre,omitempty"`
	Created string `json:"created,omitempty"`
	Engine  string `json:"engine,omitempty"`
	// Image is what the box RAN: the tag it was created from and the engine's
	// id for it, which a later `byre rebuild` moves the tag away from.
	Image *statusDataImage `json:"image,omitempty"`
	// BoxEnvKeys are the env keys the box received. KEYS only, here as
	// everywhere.
	BoxEnvKeys []string `json:"box_env_keys,omitempty"`
}

type statusDataImage struct {
	Tag         string `json:"tag,omitempty"`
	Digest      string `json:"digest,omitempty"`
	DigestError string `json:"digest_error,omitempty"`
	Base        string `json:"base,omitempty"`
}

type statusDataEngine struct {
	Name string `json:"name"`
	// Error is why byre cannot speak for the engine: not installed, or a
	// resolution refusal. Everything below it is then unknown, not absent.
	Error string `json:"error,omitempty"`
	// Rootless/KeepID describe the identity a box is built and run with.
	// RootlessError means the probe could not answer, which is not "no".
	Rootless      bool   `json:"rootless"`
	KeepID        bool   `json:"keep_id"`
	RootlessError string `json:"rootless_error,omitempty"`
}

type statusDataNetwork struct {
	// Posture is the skill-declared posture, empty for the open default.
	// Warranted is whether byre stands behind the Network ROW AS PRINTED
	// (networkWarranted): a raw escape hatch or a skill holding byre's own
	// network knobs takes that away from a declared posture, unresolved
	// skills make it unknowable, and the open default has no posture to
	// withdraw.
	Posture      string             `json:"posture"`
	PostureSkill string             `json:"posture_skill,omitempty"`
	Warranted    bool               `json:"warranted"`
	Egress       []statusDataEgress `json:"egress,omitempty"`
	Closed       []string           `json:"closed,omitempty"`
}

type statusDataEgress struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Source is the skill that asked for the hole, or "config".
	Source string `json:"source"`
	// Enforced is false when the posture does not enforce an allowlist: the
	// entry is declared and inert (ADR 0019 -- config must not carry
	// invisible teeth).
	Enforced bool `json:"enforced"`
	// ClosedBy is the config `!host[:port]` closure subtracting this entry.
	ClosedBy string `json:"closed_by,omitempty"`
}

type statusDataPort struct {
	Interface string `json:"interface"`
	Host      int    `json:"host"`
	Container int    `json:"container"`
}

type statusDataBind struct {
	Host     string `json:"host"`
	Target   string `json:"target"`
	Mode     string `json:"mode"`
	Disabled bool   `json:"disabled,omitempty"`
}

type statusDataVolume struct {
	Name   string `json:"name"`
	Role   string `json:"role,omitempty"`
	Target string `json:"target,omitempty"`
	Scope  string `json:"scope"`
	// Sharing is always written, like Scope: the text page marks an
	// exclusive volume in its row, so a document that omitted the key would
	// be the one place the two tiers described different boxes.
	Sharing string `json:"sharing"`
}

type statusDataSkill struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	Provenance string `json:"provenance,omitempty"`
}

type statusDataGrant struct {
	Skill      string           `json:"skill"`
	Mounts     []statusDataBind `json:"mounts,omitempty"`
	Caps       []string         `json:"caps,omitempty"`
	RunArgs    []string         `json:"run_args,omitempty"`
	NetnsInit  string           `json:"netns_init,omitempty"`
	SockGroups []string         `json:"sock_groups,omitempty"`
}

type statusDataReserved struct {
	Skill string `json:"skill"`
	Key   string `json:"key"`
	// Known is whether this is a chassis knob byre reads. Claims degrade
	// either way; only what byre may say about the key differs.
	Known  bool     `json:"known"`
	Claims []string `json:"claims"`
	Note   string   `json:"note"`
}

type statusDataMCP struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	URL         string   `json:"url,omitempty"`
	Command     []string `json:"command,omitempty"`
	Egress      []string `json:"egress,omitempty"`
	HeaderNames []string `json:"header_names,omitempty"`
	// ConsumedEnv are the env names the declaration reads; Provided is the
	// subset this box actually supplies.
	ConsumedEnv []string `json:"consumed_env,omitempty"`
	Provided    []string `json:"provided_env,omitempty"`
}

type statusDataClaudeSkill struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	From   string `json:"from,omitempty"`
}

type statusDataContext struct {
	Name string `json:"name"`
	File string `json:"file,omitempty"`
	// Lines counts the inline text's lines. The TEXT is the config editor's
	// screen, not an exposure fact.
	Lines int `json:"lines,omitempty"`
}

type statusDataCredential struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	// Set is the vault value-state (an entries-dir fact, never a decrypt).
	Set bool `json:"set"`
}

type statusDataHostEnv struct {
	Key    string `json:"key"`
	Source string `json:"source"`
	// State is the outcome, not the intent: delivered / empty (configured,
	// resolved to nothing, NOT passed) / overridden (an explicit [env] key
	// beats the passthrough). An entry a layer switched off is absent, the
	// same way it is absent from the page.
	State string `json:"state"`
}

type statusDataContainment struct {
	Skill string `json:"skill"`
	Text  string `json:"text"`
}

type statusDataShadow struct {
	Target string `json:"target"`
	Source string `json:"source"`
}

type statusDataContainer struct {
	// State is running / stopped / unknown. "unknown" is never collapsed to
	// "stopped": a found engine that will not answer leaves the box's state
	// genuinely unknown, which the lifecycle commands already refuse on.
	State    string   `json:"state"`
	ID       string   `json:"id,omitempty"`
	Orphaned bool     `json:"orphaned,omitempty"`
	Error    string   `json:"error,omitempty"`
	Siblings []string `json:"siblings,omitempty"`
	// SiblingsError means the sibling query failed while the own-session one
	// worked: other live sessions are unknown, not absent.
	SiblingsError string `json:"siblings_error,omitempty"`
}

// writeStatusData writes the --data document. Control characters in values
// byre did not author cannot reach the terminal raw: the JSON encoder writes
// them as \uXXXX escapes, which is the same guarantee the row funnel gives
// the rendered page by a different mechanism.
func writeStatusData(w io.Writer, s statusInfo) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(statusDataOf(s)); err != nil {
		return fmt.Errorf("writing status data: %w", err)
	}
	return nil
}

// statusDataOf projects the resolved view into the wire shape. It reads the
// SAME statusInfo the page renders from, so the two tiers cannot describe
// different boxes.
func statusDataOf(s statusInfo) statusData {
	d := statusData{
		Version:    StatusDataVersion,
		Subject:    statusSubject(s),
		ProjectID:  s.ID,
		Workdir:    s.Canonical,
		WorktreeOf: s.WorktreeOf,
		Agent:      s.Agent,
		Template:   s.Template,
		Extends:    s.Chain,
		PresetNote: s.PresetNote,
		SelfEdit:   s.SelfEdit,
		Engine: statusDataEngine{
			Name:          s.Engine,
			Error:         s.EngineErr,
			Rootless:      s.Rootless,
			KeepID:        s.KeepID,
			RootlessError: s.RootlessErr,
		},
		SkillError: s.SkillErr,
		MCPClosed:  s.MCPClosed,
		EnvKeys:    s.EnvKeys,
		RunArgs:    s.RunArgs,
		BuildRaw:   s.BuildRaw,
	}
	d.ClaudeSkillsClosed = s.ClaudeSkillsClosed
	d.Network = statusDataNetworkOf(s)

	d.Ports = make([]statusDataPort, 0, len(s.Ports))
	for _, p := range s.Ports {
		iface, host := config.PortEffective(p)
		d.Ports = append(d.Ports, statusDataPort{Interface: iface, Host: host, Container: p.Container})
	}
	d.Binds = make([]statusDataBind, 0, len(s.Binds))
	for _, m := range s.Binds {
		d.Binds = append(d.Binds, bindData(m))
	}
	if s.SelfEdit != "" {
		d.Binds = append(d.Binds, statusDataBind{Host: s.SelfEdit, Target: selfEditTarget, Mode: "rw"})
	}
	d.Volumes = make([]statusDataVolume, 0, len(s.Volumes))
	for _, v := range s.Volumes {
		scope := v.Scope
		if scope == "" {
			scope = "project"
		}
		d.Volumes = append(d.Volumes, statusDataVolume{Name: v.Name, Role: v.Role, Target: v.Target, Scope: scope, Sharing: orDefault(v.Sharing, "shared")})
	}
	d.Skills = make([]statusDataSkill, 0, len(s.Skills))
	for _, name := range s.Skills {
		id, prov := pkgParts(s.Cat, name, tierFull)
		d.Skills = append(d.Skills, statusDataSkill{Name: name, ID: id, Provenance: prov})
	}
	for _, g := range s.Grants {
		gd := statusDataGrant{Skill: g.Skill, Caps: g.Caps, RunArgs: g.RunArgs, NetnsInit: g.NetnsInit, SockGroups: g.SockGroups}
		for _, m := range g.Mounts {
			gd.Mounts = append(gd.Mounts, bindData(m))
		}
		d.SkillGrants = append(d.SkillGrants, gd)
	}
	for _, e := range s.SkillReservedEnv {
		d.ReservedEnv = append(d.ReservedEnv, statusDataReserved{
			Skill:  e.Skill,
			Key:    e.Key,
			Known:  skills.ReservedEnvKnown(e.Key),
			Claims: skills.ReservedEnvClaims(e.Key),
			Note:   skills.ReservedEnvNote(e),
		})
	}

	d.MCPServers = make([]statusDataMCP, 0, len(s.MCPs))
	for _, decl := range s.MCPs {
		m := decl.MCP
		md := statusDataMCP{
			Name:        m.Name,
			Source:      declSource(decl.Skill, skills.MCPFromConfig),
			URL:         m.URL,
			Command:     m.Command,
			Egress:      m.Egress,
			HeaderNames: m.HeaderNames(),
			ConsumedEnv: m.ConsumedEnv(),
		}
		for _, k := range md.ConsumedEnv {
			if s.EnvProvided[k] {
				md.Provided = append(md.Provided, k)
			}
		}
		d.MCPServers = append(d.MCPServers, md)
	}
	if len(s.MCPs) > 0 {
		d.MCPDelivery = mcpDeliveryLine(s).Full
	}

	d.ClaudeSkills = make([]statusDataClaudeSkill, 0, len(s.ClaudeSkills))
	for _, decl := range s.ClaudeSkills {
		from := decl.CS.Path
		if decl.Skill != skills.ClaudeSkillsFromConfig {
			from = decl.CS.From
		}
		d.ClaudeSkills = append(d.ClaudeSkills, statusDataClaudeSkill{
			Name:   decl.CS.Name,
			Source: declSource(decl.Skill, skills.ClaudeSkillsFromConfig),
			From:   from,
		})
	}
	if len(s.ClaudeSkills) > 0 {
		d.ClaudeSkillsDelivery = claudeSkillsDeliveryLine(s).Full
	}

	d.Instructions = make([]statusDataContext, 0, len(s.Contexts))
	for _, cd := range s.Contexts {
		d.Instructions = append(d.Instructions, statusDataContext{
			Name: cd.Name, File: cd.File, Lines: contextLines(cd),
		})
	}
	if len(s.Contexts) > 0 {
		d.InstructionsDelivery = contextDeliveryLine(s).Full
	}

	for _, cd := range s.Credentials {
		d.Credentials = append(d.Credentials, statusDataCredential{
			Name: cd.Name, Kind: cd.Kind, Target: cd.Target, Set: s.CredentialStates[cd.Name],
		})
	}
	d.CredentialUnlock = s.CredentialUnlock

	for _, r := range s.HostEnv {
		// A DISABLED entry (`KEY = ""` in some layer) is not a channel this
		// box has -- the page omits it for that reason, and emitting it here
		// would make --data a superset of --full rather than the same
		// content, inviting a reader to count a passthrough that was
		// switched off as one that exists.
		if r.State == hostEnvDisabled {
			continue
		}
		d.HostEnv = append(d.HostEnv, statusDataHostEnv{
			Key: r.Key, Source: r.Source, State: hostEnvStateName(r.State),
		})
	}
	for _, c := range s.Containments {
		d.Containments = append(d.Containments, statusDataContainment{Skill: c.Skill, Text: c.Text})
	}
	for _, sh := range s.ManagedShadows {
		d.ManagedShadows = append(d.ManagedShadows, statusDataShadow{Target: sh.Target, Source: sh.Source})
	}
	d.Container = statusDataContainerOf(s)
	d.Launch = statusDataLaunchOf(s)
	for _, c := range launchChanges(s) {
		d.Changes = append(d.Changes, c.String())
	}
	return d
}

// statusSubject names whose box the exposure fields describe. The running box
// is the subject whenever byre could read a record for one.
func statusSubject(s statusInfo) string {
	if s.Launch != nil {
		return "running_box"
	}
	return "next_launch"
}

// statusDataLaunchOf projects the record (or the reason there isn't one) into
// the wire shape. It is emitted for any running box, including one byre could
// not read a record for: `--data` carries the same qualifiers the page does,
// and "no launch key" would leave a reader to invent an explanation.
func statusDataLaunchOf(s statusInfo) *statusDataLaunch {
	if s.Container == "" {
		return nil
	}
	l := &statusDataLaunch{State: launchStateName(s.LaunchState), Note: launchDegradeNote(s.LaunchState)}
	if s.Launch == nil {
		return l
	}
	l.Record = s.LaunchHash
	l.Schema = s.Launch.Record
	l.Byre = s.Launch.Byre
	l.Created = s.Launch.Created.UTC().Format(time.RFC3339)
	l.Engine = s.Launch.Engine
	l.BoxEnvKeys = s.BoxEnvKeys
	l.Image = &statusDataImage{
		Tag: s.Image.Tag, Digest: s.Image.Digest, DigestError: s.Image.DigestError, Base: s.Image.Base,
	}
	return l
}

func launchStateName(st launchState) string {
	switch st {
	case launchRecordOK:
		return "verified"
	case launchPreRecord:
		return "pre_record"
	case launchMissing:
		return "missing"
	case launchTampered:
		return "tampered"
	case launchNewer:
		return "newer_schema"
	case launchUnreadable:
		return "unreadable"
	default:
		return "none"
	}
}

// statusDataNetworkOf reports what byre WARRANTS, not merely what a skill
// declared -- through networkWarranted, the same predicate the Network row
// itself branches on, so the page and this document cannot disagree about
// whether the wall is byre's.
func statusDataNetworkOf(s statusInfo) statusDataNetwork {
	n := statusDataNetwork{
		Posture:      s.NetPosture,
		PostureSkill: s.NetPostureSkill,
		Closed:       s.EgressClosed,
		Warranted:    networkWarranted(s),
	}
	enforced := config.PostureEnforcesAllowlist(s.NetPosture)
	seen := map[string]bool{}
	for _, a := range s.Egress {
		hp := fmt.Sprintf("%s:%d", a.Host, a.Port)
		if seen[hp] {
			continue
		}
		seen[hp] = true
		e := statusDataEgress{Host: a.Host, Port: a.Port, Source: a.Skill, Enforced: enforced}
		if c, closed := closedBy(s.EgressClosed, a.Host, a.Port); closed && enforced {
			e.ClosedBy = c
		}
		n.Egress = append(n.Egress, e)
	}
	return n
}

func statusDataContainerOf(s statusInfo) statusDataContainer {
	switch {
	case s.EngineErr != "":
		return statusDataContainer{State: "unknown", Error: s.EngineErr}
	case s.ContainerQueryErr != "":
		return statusDataContainer{State: "unknown", Error: s.ContainerQueryErr}
	case s.Container != "":
		return statusDataContainer{
			State: "running", ID: s.Container, Orphaned: s.Orphaned,
			Siblings: s.SiblingSessions, SiblingsError: s.SiblingQueryErr,
		}
	default:
		return statusDataContainer{
			State: "stopped", Siblings: s.SiblingSessions, SiblingsError: s.SiblingQueryErr,
		}
	}
}

func bindData(m config.Mount) statusDataBind {
	return statusDataBind{Host: m.Host, Target: m.Target, Mode: orDefault(m.Mode, "ro"), Disabled: m.Disabled}
}

// declSource names who declared an MCP / Claude Skill: "config", or the
// skill's canonical id.
func declSource(skill, fromConfig string) string {
	if skill == fromConfig {
		return "config"
	}
	return skill
}

// contextLines counts an inline instruction block's lines; 0 for a file
// declaration, whose content is read at bake time.
func contextLines(cd config.ContextDecl) int {
	if cd.File != "" || cd.Text == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(cd.Text, "\n"), "\n") + 1
}

func hostEnvStateName(st hostEnvState) string {
	switch st {
	case hostEnvDelivered:
		return "delivered"
	case hostEnvEmpty:
		return "empty"
	case hostEnvOverridden:
		return "overridden"
	default:
		return "disabled"
	}
}
