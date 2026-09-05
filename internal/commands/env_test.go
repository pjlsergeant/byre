package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/credentials"
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
			"SECRET":          "encrypted:AAAA",          // credential: its own channel
		},
	}
	// "" for the host git: this case turns on the `git:` source LOSING to an
	// explicit [env] key, which it must do before any probe runs.
	results := resolveHostEnv(cfg, "")
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
		"SECRET":          hostEnvEncrypted,
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
	// A credential row provides its key: the launch is fail-closed on the
	// delivery, so a box that runs at all carries it.
	if !provided["SECRET"] {
		t.Errorf("a credential row must annotate as provided: %v", provided)
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
	if got := hostSourceValue("tz:", ""); got != "America/New_York" {
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

// A credential row is env_from_host's fifth outcome: it reaches the box on
// the tmpfs channel after the launch unlock, so it must never join the `-e`
// export — and it is not "NOT passed" either, which is what an unrecognized
// source would otherwise read as.
func TestResolveHostEnvExcludesCredentialRows(t *testing.T) {
	credentials.SetWorkFactorForTesting(10)
	_, recipient, err := credentials.NewIdentity("pw")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := credentials.EncryptValue(recipient, "STRIPE_KEY", credentials.KindEnv, []byte("sk"))
	if err != nil {
		t.Fatal(err)
	}
	row, err := config.FormatEncryptedRow(credentials.KindEnv, blob)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{EnvFromHost: map[string]string{"STRIPE_KEY": row, "TERM": "env:TERM"}}
	t.Setenv("TERM", "xterm")
	got := resolveHostEnv(cfg, "")
	states := map[string]hostEnvState{}
	for _, r := range got {
		states[r.Key] = r.State
	}
	if states["STRIPE_KEY"] != hostEnvEncrypted {
		t.Fatalf("credential row state = %v, want encrypted", states["STRIPE_KEY"])
	}
	env := map[string]string{}
	addEnvFromHost(env, got)
	if _, leaked := env["STRIPE_KEY"]; leaked {
		t.Fatal("a credential row must never reach the engine's -e export")
	}
	if env["TERM"] != "xterm" {
		t.Fatalf("ordinary sources still deliver: %v", env)
	}
	// A damaged payload is still a credential row, not an argv value: the
	// launch refuses it by name, and nothing here quietly passes it through.
	cfg.EnvFromHost["STRIPE_KEY"] = "encrypted:AAAA"
	for _, r := range resolveHostEnv(cfg, "") {
		if r.Key == "STRIPE_KEY" && r.State != hostEnvEncrypted {
			t.Fatalf("damaged credential row state = %v", r.State)
		}
	}
}

// An env_from_host row that resolved empty is said at develop, grouped by
// source, so a missing host git identity is a ten-second fix instead of a
// failed commit mid-session; a delivered, disabled or credential row is not
// news, and nothing empty means nothing printed.
func TestWarnHostEnvEmptyGroupsBySourceAndNamesTheGitRemedy(t *testing.T) {
	var b bytes.Buffer
	warnHostEnvEmpty(&b, []hostEnvResult{
		{Key: "GIT_AUTHOR_NAME", Source: "git:user.name", State: hostEnvEmpty},
		{Key: "GIT_COMMITTER_NAME", Source: "git:user.name", State: hostEnvEmpty},
		{Key: "OFF", Source: "", State: hostEnvDisabled},
		{Key: "PASSED", Source: "env:BYRE_TEST_HOSTVAL", Value: "v", State: hostEnvDelivered},
		{Key: "SECRET", Source: "encrypted:AAAA", State: hostEnvEncrypted},
		{Key: "TERM", Source: "env:TERM", State: hostEnvEmpty},
	})
	out := b.String()
	if n := strings.Count(out, "\n"); n != 2 {
		t.Fatalf("one line per empty source, got %d:\n%s", n, out)
	}
	for _, w := range []string{
		"GIT_AUTHOR_NAME, GIT_COMMITTER_NAME <- git:user.name resolved empty",
		"git config --global user.name",
		"TERM <- env:TERM resolved empty",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
	for _, no := range []string{"OFF", "PASSED", "SECRET"} {
		if strings.Contains(out, no) {
			t.Errorf("%q is not news:\n%s", no, out)
		}
	}

	b.Reset()
	warnHostEnvEmpty(&b, []hostEnvResult{{Key: "PASSED", Source: "env:X", Value: "v", State: hostEnvDelivered}})
	if b.Len() != 0 {
		t.Errorf("nothing empty must print nothing, got %q", b.String())
	}
}
