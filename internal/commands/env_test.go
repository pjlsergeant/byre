package commands

import (
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// resolveHostEnv precedence (ADR 0026), now carried as explicit outcomes:
// disabled and empty sources deliver nothing, an explicit [env] KEY beats
// the passthrough -- and every consumer reads THESE states, so the runtime
// application below can't diverge from what a status row would say.
func TestResolveHostEnvPrecedenceAndStates(t *testing.T) {
	t.Setenv("BYRE_TEST_HOSTVAL", "from-host")
	cfg := config.Config{
		Env: map[string]string{"GIT_AUTHOR_NAME": "Handmade"},
		EnvFromHost: map[string]string{
			"GIT_AUTHOR_NAME": "git:user.name",           // explicit env wins
			"DISABLED":        "",                        // disabled: nothing
			"WEIRD":           "future:scheme",           // validation refused it upstream; resolves empty
			"PASSED":          "env:BYRE_TEST_HOSTVAL",   // host var passes through
			"ABSENT":          "env:BYRE_TEST_NO_SUCH_V", // unset host var: nothing
		},
	}
	results := resolveHostEnv(cfg)
	states := map[string]hostEnvState{}
	for _, r := range results {
		states[r.Key] = r.State
	}
	want := map[string]hostEnvState{
		"GIT_AUTHOR_NAME": hostEnvOverridden,
		"DISABLED":        hostEnvDisabled,
		"WEIRD":           hostEnvEmpty,
		"PASSED":          hostEnvDelivered,
		"ABSENT":          hostEnvEmpty,
	}
	for k, w := range want {
		if states[k] != w {
			t.Errorf("state[%s] = %v, want %v", k, states[k], w)
		}
	}

	env := map[string]string{}
	addEnvFromHost(env, results)
	if len(env) != 1 || env["PASSED"] != "from-host" {
		t.Errorf("only the delivered result may set runtime env, got %v", env)
	}

	provided := providedEnv(cfg, results)
	if !provided["GIT_AUTHOR_NAME"] || !provided["PASSED"] {
		t.Errorf("provided must include [env] literals and delivered passthrough: %v", provided)
	}
	if provided["ABSENT"] || provided["WEIRD"] || provided["DISABLED"] {
		t.Errorf("an undelivered source must NOT annotate as provided: %v", provided)
	}
}

// The tz: source prefers the host TZ var and falls back to the /etc/localtime
// symlink's IANA name; the zone-name extraction handles both the Linux and
// macOS zoneinfo trees.
func TestHostTimezone(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	if got := hostSourceValue("tz:"); got != "America/New_York" {
		t.Fatalf("tz: must prefer the TZ env var, got %q", got)
	}

	cases := map[string]string{
		"/usr/share/zoneinfo/Europe/London":          "Europe/London",
		"/var/db/timezone/zoneinfo/Australia/Sydney": "Australia/Sydney",
		"/usr/share/zoneinfo/UTC":                    "UTC",
		"/not/a/zoneinfo-tree/path":                  "",
	}
	for target, want := range cases {
		if got := tzFromZoneinfoPath(target); got != want {
			t.Fatalf("tzFromZoneinfoPath(%q) = %q, want %q", target, got, want)
		}
	}
}

// The grant review flags only the host-env additions a preset actually
// asks for — byre's own shipped defaults are every box's baseline, not the
// preset's ask.
func TestExtraHostEnvSkipsCoreDefaults(t *testing.T) {
	m := config.CoreEnvFromHost()
	m["EDITOR_NAME"] = "git:user.name" // an addition
	m["GIT_AUTHOR_NAME"] = ""          // disabled: grants nothing
	got := extraHostEnv(m)
	if len(got) != 1 || got[0] != "EDITOR_NAME <- git:user.name" {
		t.Fatalf("extraHostEnv = %v", got)
	}
}
