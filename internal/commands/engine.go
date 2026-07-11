package commands

import (
	"fmt"

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
func lifecycleEngines() ([]engineRunner, error) {
	var out []engineRunner
	for _, e := range []string{"docker", "podman"} {
		eng, err := runner.Detect(e, nil)
		if err != nil {
			continue // engine not installed
		}
		out = append(out, runner.New(eng))
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
