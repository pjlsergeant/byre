// exposure.go owns the terse total-exposure summary two surfaces render: the
// config UI's one-line tally and the `byre:` lines develop prints at launch.
// Each surface counts from its own resolved view (the UI from effective rows,
// launch from the resolved config+skills); this type owns the words, so the
// surfaces can't drift into telling different stories. `byre status` stays
// the detailed, attributed view and deliberately keeps its own rendering —
// its output is pinned by README examples (status/marketing lockstep).
package config

import (
	"fmt"
	"strings"
)

// Exposure is the tally behind the summary line. Vocabulary (GLOSSARY.md
// "Grant"): a config-literal env var reaches the box but is not a grant, so
// the rendered line is labeled "exposure", never "grants".
type Exposure struct {
	Workspace      bool   // include the implicit project mount — set by launch; the UI summarizes only config
	Mounts         int    // host mounts that will actually bind
	DisabledMounts int    // switched off: no bind, but staying visible while off is the switch's point
	Ports          int    // published ports
	Env            int    // env vars the box gets (config-literal + skill runtime)
	Posture        string // declared network posture; "" = open (the default world, not a grant)
	Egress         int    // resolved allowlist size; meaningful only under a posture
	// The project's own raw escape hatches degrade the posture claim — the
	// same honesty rule as status's networkLine: byre can't audit arbitrary
	// argv or Dockerfile text, so a declared posture is not guaranteed.
	RawRunArgs bool
	RawBuild   bool
}

// GrantsLine renders the count segments — "/workspace rw · 2 host mounts
// (+1 disabled) · 1 port · 4 env vars". Zero counts are skipped; "" when
// nothing applies.
func (e Exposure) GrantsLine() string {
	var parts []string
	if e.Workspace {
		parts = append(parts, "/workspace rw")
	}
	if e.Mounts > 0 || e.DisabledMounts > 0 {
		s := fmt.Sprintf("%d host %s", e.Mounts, plural("mount", e.Mounts))
		if e.DisabledMounts > 0 {
			s += fmt.Sprintf(" (+%d disabled)", e.DisabledMounts)
		}
		parts = append(parts, s)
	}
	if e.Ports > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", e.Ports, plural("port", e.Ports)))
	}
	if e.Env > 0 {
		parts = append(parts, fmt.Sprintf("%d env %s", e.Env, plural("var", e.Env)))
	}
	return strings.Join(parts, " · ")
}

// NetworkLine renders the network stance: "network open", or the declared
// posture with its allowlist size — qualified, not asserted, when raw config
// is present (the same degrade rule status applies).
func (e Exposure) NetworkLine() string {
	if e.Posture == "" {
		return "network open"
	}
	s := "network " + e.Posture
	if e.Egress == 0 {
		s += " · egress none"
	} else {
		s += fmt.Sprintf(" · egress %d %s", e.Egress, plural("host", e.Egress))
	}
	var raw []string
	if e.RawRunArgs {
		raw = append(raw, "raw run_args")
	}
	if e.RawBuild {
		raw = append(raw, "raw build lines")
	}
	if len(raw) > 0 {
		s += "  (declared; " + strings.Join(raw, " + ") + " present — not guaranteed)"
	}
	return s
}

// Line joins both halves for a single-line surface (the config UI).
func (e Exposure) Line() string {
	g := e.GrantsLine()
	if g == "" {
		return e.NetworkLine()
	}
	return g + " · " + e.NetworkLine()
}

func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
