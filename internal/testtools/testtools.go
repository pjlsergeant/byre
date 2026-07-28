// Package testtools decides what a missing test dependency MEANS.
//
// Plenty of byre's unit tests need something of the machine: git, a POSIX
// shell, a filesystem that can make symlinks or FIFOs. Skipping when it
// isn't there is right on a developer's machine -- a minimal container
// shouldn't fail a suite over a tool nobody promised. It is wrong in CI,
// where the environment is chosen deliberately: there a skip is how a
// shrunken image silently deletes coverage, and the tests go on reporting
// green for rules nothing ran.
//
// So CI sets BYRE_REQUIRE_TEST_TOOLS=1 and every skip routed through here
// becomes a failure naming the missing dependency. It is the rule the tmux
// tier already applies to its own gate (ADR 0038: gate set without the tool
// FAILS), pointed at the tools the unit suite needs.
//
// What does NOT belong here: skips that turn on the operating system's own
// semantics -- a behaviour root doesn't have, a backend only Linux has.
// Those aren't dependencies a CI image could supply, and demanding them
// would just make the suite unrunnable somewhere it legitimately differs.
package testtools

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// RequireEnv is the environment variable that turns these skips into
// failures.
const RequireEnv = "BYRE_REQUIRE_TEST_TOOLS"

// NeedTool skips unless every named program resolves on PATH.
func NeedTool(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			report(t, fmt.Sprintf("%s is not on PATH", name))
		}
	}
}

// Unavailable reports an OS feature the test needs and the machine refused
// -- err is the probe's own error (a failed os.Symlink, a failed mkfifo).
// Call it on the failed probe, in place of the skip.
func Unavailable(t *testing.T, feature string, err error) {
	t.Helper()
	report(t, fmt.Sprintf("%s unavailable: %v", feature, err))
}

func report(t *testing.T, what string) {
	t.Helper()
	if os.Getenv(RequireEnv) == "1" {
		t.Fatalf("%s=1 but %s — the environment is missing a test dependency, which is a configuration error, not a skip", RequireEnv, what)
	}
	t.Skipf("%s (set %s=1 to make this a failure)", what, RequireEnv)
}
