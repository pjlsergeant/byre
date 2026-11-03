package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Every BYRE_* variable a gen chassis script reads is a chassis knob, so
// reservedEnvKnobs must know it: a knob missing from the map is announced as
// "not a control this byre recognizes" while byre itself reads it, and
// gets the conservative claims instead of the ones it really skews. gen's
// TestChassisScriptKnobsRideReservedPrefix pins only that the knobs carry the
// prefix; this pins them against the map.
//
// The regexes are that test's, with one difference: the read must end at a
// word boundary. The launcher's own locals (BYRE_cred_key, BYRE_hook) are
// BYRE_-prefixed and mixed-case -- set by `read` and `for`, not always by an
// assignment the scan can see -- and without the boundary `$BYRE_cred_key`
// matches as the bare prefix "BYRE_". Knobs are all-caps by convention; a
// mixed-case name is a script's local, not an environment input.
func TestReservedEnvKnobsCoverChassisReads(t *testing.T) {
	scripts, err := filepath.Glob("../gen/*.sh")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("globbing chassis scripts: %v (%d found)", err, len(scripts))
	}

	assignRe := regexp.MustCompile(`(?m)^\s*(?:export\s+|local\s+)?([A-Z][A-Z0-9_]*)=`)
	forRe := regexp.MustCompile(`(?m)\bfor\s+([A-Z][A-Z0-9_]*)\s+in\b`)
	readRe := regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*)\b`)

	var checked []string
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

		for _, m := range readRe.FindAllStringSubmatch(src, -1) {
			name := m[1]
			if !strings.HasPrefix(name, reservedPrefix) || assigned[name] || slices.Contains(checked, script+":"+name) {
				continue
			}
			checked = append(checked, script+":"+name)
			if !ReservedEnvKnown(name) {
				t.Errorf("%s reads %s, which reservedEnvKnobs does not list: add it with the claims it can skew", filepath.Base(script), name)
			}
		}
	}
	// The check must have seen the knobs it exists for, or a regex that
	// matches nothing would keep it green.
	for _, want := range []string{"BYRE_LAUNCH_GATE_FILE", "BYRE_FETCH_RUN_RETRIES", "BYRE_CRED_DIR"} {
		found := false
		for _, c := range checked {
			if strings.HasSuffix(c, ":"+want) {
				found = true
			}
		}
		if !found {
			t.Errorf("the scan never saw %s read by a chassis script", want)
		}
	}
}
