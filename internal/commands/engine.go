package commands

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/runner"
)

// lifecycleEngines returns a runner per INSTALLED engine (docker, then
// podman) for the recovery/lifecycle commands (reset, forget, rehome). They
// deliberately do NOT honor the configured engine: project state can live in
// an engine the config no longer names (an engine switch, a broken or missing
// config), and a "completely removed"/"migrated" claim that consulted only
// one engine would be false — forget could delete the authoritative store
// while the other engine still holds credentials. Commands that need a valid
// config anyway (develop, rebuild) detect fatally from it instead;
// informational commands (status, dockerrun) keep their own best-effort
// semantics.
// A DECLINED engine (any resolution failure that isn't absence: a binary
// hostexec refused out of a box-writable directory, a relative-PATH ErrDot
// refusal) fails the whole enumeration rather than dropping out of it.
// These commands speak in totals — "completely removed", "migrated" — and
// forget already refuses over an engine it could not fully query, on exactly
// this reasoning; an engine byre never even reached is the same uncertainty
// one step earlier. Silently skipping it would let forget delete the store
// while real docker volumes, images and credentials stayed behind, under the
// word "completely".
func lifecycleEngines(roots hostexec.Roots) ([]engineRunner, error) {
	var out []engineRunner
	for _, e := range []string{"docker", "podman"} {
		eng, exe, err := runner.Detect(e, hostexec.Looker(roots))
		if err != nil {
			if errors.As(err, new(*runner.NotInstalledError)) {
				continue // genuinely not here; nothing of this project can be on it
			}
			return nil, fmt.Errorf("this command speaks in totals and byre cannot account for %s: %w", e, declinedEngine{Engine: e, Err: err})
		}
		out = append(out, runner.New(eng, exe))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no container engine found on PATH (looked for docker, podman)")
	}
	return out, nil
}

// engineSuffix labels a resource line with its engine when more than one
// engine is being inspected — with a single installed engine the label is
// noise and stays off.
func engineSuffix(multi bool, r engineRunner) string {
	if !multi {
		return ""
	}
	return fmt.Sprintf(" [%s]", r.Engine())
}

// IgnoreEngines is the --ignore-docker / --ignore-podman choice the totals
// commands (backup, reset, forget, rehome) take: the user saying "that engine
// is installed but not running -- go ahead without it". byre never infers it,
// because a skip it chose for itself is how "completely removed" becomes a
// false claim over an engine that may still hold this project's volumes.
type IgnoreEngines struct {
	Docker bool
	Podman bool
}

// Has reports whether a flag named this engine.
func (ig IgnoreEngines) Has(eng runner.Engine) bool {
	switch eng {
	case runner.Docker:
		return ig.Docker
	case runner.Podman:
		return ig.Podman
	}
	return false
}

// Names lists the engines the flags named, docker first -- the order
// lifecycleEngines enumerates in, so every surface reads the same way.
func (ig IgnoreEngines) Names() []string {
	var out []string
	if ig.Docker {
		out = append(out, string(runner.Docker))
	}
	if ig.Podman {
		out = append(out, string(runner.Podman))
	}
	return out
}

// ignoreSplit is what one command's --ignore flags came to over the engines
// byre actually found: the set to query, the installed engines left out, and
// the flags that changed nothing because the engine is not here at all.
type ignoreSplit struct {
	query        []engineRunner
	ignored      []string
	notInstalled []string
}

// splitEngines is the ONE owner of the ignore filter. Every totals command
// applies it to the set lifecycleEngines handed back -- after that
// enumeration, never inside it, because a DECLINED binary (ADR 0047) is not
// what this flag is for and must keep failing the enumeration whether the
// engine was named or not.
func splitEngines(engines []engineRunner, ignore IgnoreEngines) ignoreSplit {
	var sp ignoreSplit
	for _, r := range engines {
		if ignore.Has(r.Engine()) {
			sp.ignored = append(sp.ignored, string(r.Engine()))
			continue
		}
		sp.query = append(sp.query, r)
	}
	for _, name := range ignore.Names() {
		if !slices.Contains(sp.ignored, name) {
			sp.notInstalled = append(sp.notInstalled, name)
		}
	}
	return sp
}

// note discloses what the flags cost, in the one shape every command uses
// (P4: an engine byre did not look at is never silent). Every command prints
// it on BOTH its surfaces, preview and summary, because off a terminal there
// is no preview and the summary is all the user reads. consequence is the
// verb's own -- what went unchecked, unremoved or unmigrated.
func (sp ignoreSplit) note(w io.Writer, consequence func(eng string) string) {
	for _, eng := range sp.ignored {
		dataf(w, "byre: %s ignored (--ignore-%s): %s\n", eng, eng, consequence(eng))
	}
	for _, eng := range sp.notInstalled {
		dataf(w, "byre: %s is not installed; --ignore-%s changed nothing.\n", eng, eng)
	}
}

// emptyScope is the qualifier an empty total carries: "" when every
// installed engine was queried, and " on <engines queried>" when a flag left
// one out -- unscoped, "none found" reads as covering the ignored engine
// that the note just said byre did not look at.
func (sp ignoreSplit) emptyScope() string {
	if len(sp.ignored) == 0 {
		return ""
	}
	queried := make([]string, len(sp.query))
	for i, r := range sp.query {
		queried[i] = string(r.Engine())
	}
	return " on " + strings.Join(queried, " or ")
}

// refuseIfEmpty is the refusal for a command with nothing left to ask: every
// installed engine was named by a flag, so there is no total to speak in, and
// reporting zero volumes would be the false success the flag exists to expose.
func (sp ignoreSplit) refuseIfEmpty(verb string) error {
	if len(sp.query) > 0 {
		return nil
	}
	return fmt.Errorf("every installed engine is ignored (%s), so byre %s has nothing to check — drop the flag", joinFlags(sp.ignored), verb)
}

// joinFlags renders a set of engine names as the flags that named them, which
// is how the user spelled them and what they can drop.
func joinFlags(engines []string) string {
	out := make([]string, 0, len(engines))
	for _, eng := range engines {
		out = append(out, "--ignore-"+eng)
	}
	return strings.Join(out, ", ")
}

// totalsVerb is the voice a totals command speaks in when an engine query
// fails: its own name, and -- backup's case -- the one engine that cannot be
// ignored because the command READS it.
type totalsVerb struct {
	name string
	// source is the engine the command's answer is made of (backup's source
	// engine). "" where every engine is equally ignorable: reset, forget and
	// rehome act on all of them.
	source runner.Engine
}

// queryErr is the ONE refusal a totals command gives for an engine query it
// could not make. A cleanly unreachable engine (deliver.IsUnreachable, the only
// classifier in play) names both ways forward -- start it, or say to skip it;
// the source engine has only the one, because it is what the file is made of.
// Any other failure keeps the caller's own wrapping, so a permission or TLS
// failure against a daemon that IS running reads as the engine problem it is.
func (v totalsVerb) queryErr(eng runner.Engine, what string, err error) error {
	if !deliver.IsUnreachable(err) {
		return fmt.Errorf("%s (%s): %w", what, eng, err)
	}
	if v.source != "" && eng == v.source {
		return fmt.Errorf("byre %s reads %s, and %s isn't reachable (%s): start %s and re-run",
			v.name, eng, eng, briefly(err.Error()), eng)
	}
	return fmt.Errorf("byre %s expects to check every installed engine for this project's state (%s). %s isn't reachable (%s): start %s, or run with --ignore-%s",
		v.name, what, eng, briefly(err.Error()), eng, eng)
}
