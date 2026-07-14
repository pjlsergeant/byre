package commands

import (
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// addEnvFromHost precedence (ADR 0026): disabled keys and unknown schemes set
// nothing, and an explicit [env] KEY beats the passthrough.
func TestAddEnvFromHostPrecedence(t *testing.T) {
	t.Setenv("BYRE_TEST_HOSTVAL", "from-host")
	cfg := config.Config{
		Env: map[string]string{"GIT_AUTHOR_NAME": "Handmade"},
		EnvFromHost: map[string]string{
			"GIT_AUTHOR_NAME": "git:user.name",           // explicit env wins
			"DISABLED":        "",                        // disabled: nothing
			"WEIRD":           "future:scheme",           // belt to validation's suspender
			"PASSED":          "env:BYRE_TEST_HOSTVAL",   // host var passes through
			"ABSENT":          "env:BYRE_TEST_NO_SUCH_V", // unset host var: nothing
		},
	}
	env := map[string]string{}
	addEnvFromHost(env, cfg)
	if _, ok := env["GIT_AUTHOR_NAME"]; ok {
		t.Fatalf("explicit [env] key must beat the passthrough: %v", env)
	}
	if _, ok := env["DISABLED"]; ok {
		t.Fatalf("disabled source must set nothing: %v", env)
	}
	if _, ok := env["WEIRD"]; ok {
		t.Fatalf("unknown scheme must set nothing: %v", env)
	}
	if env["PASSED"] != "from-host" {
		t.Fatalf("env: source must pass the host value through: %v", env)
	}
	if _, ok := env["ABSENT"]; ok {
		t.Fatalf("unset host var must set nothing: %v", env)
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

// The adoption summary flags only the host-env additions a proposal actually
// asks for — byre's own shipped defaults are every box's baseline, not the
// proposal's ask.
func TestExtraHostEnvSkipsCoreDefaults(t *testing.T) {
	m := config.CoreEnvFromHost()
	m["EDITOR_NAME"] = "git:user.name" // an addition
	m["GIT_AUTHOR_NAME"] = ""          // disabled: grants nothing
	got := extraHostEnv(m)
	if len(got) != 1 || got[0] != "EDITOR_NAME <- git:user.name" {
		t.Fatalf("extraHostEnv = %v", got)
	}
}
