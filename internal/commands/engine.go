package commands

import (
	"errors"
	"fmt"

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
// A DECLINED engine (hostexec refused a binary resolved out of a directory
// the box writes) fails the whole enumeration rather than dropping out of it.
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
