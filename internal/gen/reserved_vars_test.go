package gen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The config-side rejection of BYRE_* keys in [env] (config.validateScalarsCommon)
// reserves a PREFIX, so it covers the chassis scripts' control knobs only if
// every knob actually carries the prefix. This test closes that scripts<->Go
// coupling: any all-caps variable a chassis script reads without assigning
// must be BYRE_-prefixed (byre's reserved vocabulary) or a known ambient var.
// A future `${GATE_FILE:-...}` knob added without the prefix fails here,
// because [env] could then reach it around the reservation.

var ambientVars = map[string]bool{
	"HOME": true, "PATH": true, "TERM": true, "TZ": true,
	"USER": true, "SHELL": true, "PWD": true,
}

func TestChassisScriptKnobsRideReservedPrefix(t *testing.T) {
	scripts, err := filepath.Glob("*.sh")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("globbing chassis scripts: %v (%d found)", err, len(scripts))
	}

	assignRe := regexp.MustCompile(`(?m)^\s*(?:export\s+|local\s+)?([A-Z][A-Z0-9_]*)=`)
	forRe := regexp.MustCompile(`(?m)\bfor\s+([A-Z][A-Z0-9_]*)\s+in\b`)
	readRe := regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*)`)

	for _, script := range scripts {
		b, err := os.ReadFile(script)
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		src := string(b)

		assigned := map[string]bool{}
		for _, m := range assignRe.FindAllStringSubmatch(src, -1) {
			assigned[m[1]] = true
		}
		for _, m := range forRe.FindAllStringSubmatch(src, -1) {
			assigned[m[1]] = true
		}

		seen := map[string]bool{}
		for _, m := range readRe.FindAllStringSubmatch(src, -1) {
			name := m[1]
			if seen[name] || assigned[name] || ambientVars[name] {
				continue
			}
			seen[name] = true
			if !strings.HasPrefix(name, "BYRE_") {
				t.Errorf("%s reads env var %s: chassis control knobs must carry the BYRE_ prefix (the [env] reservation covers only that namespace) or be added to ambientVars with a reason", script, name)
			}
		}
	}
}
