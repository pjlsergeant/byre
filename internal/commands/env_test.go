package commands

import (
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
)

// addEnvFromHost precedence (ADR 0026): disabled keys and unknown schemes set
// nothing, and an explicit [env] KEY beats the passthrough.
func TestAddEnvFromHostPrecedence(t *testing.T) {
	cfg := config.Config{
		Env: map[string]string{"GIT_AUTHOR_NAME": "Handmade"},
		EnvFromHost: map[string]string{
			"GIT_AUTHOR_NAME": "git:user.name", // explicit env wins
			"DISABLED":        "",              // disabled: nothing
			"WEIRD":           "env:HOME",      // belt to validation's suspender
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
}

// The adoption summary flags only the host-env additions a proposal actually
// asks for — byre's own shipped git-identity defaults are every box's
// baseline, not the proposal's ask.
func TestExtraHostEnvSkipsCoreDefaults(t *testing.T) {
	m := config.CoreEnvFromHost()
	m["EDITOR_NAME"] = "git:user.name" // an addition
	m["GIT_AUTHOR_NAME"] = ""          // disabled: grants nothing
	got := extraHostEnv(m)
	if len(got) != 1 || got[0] != "EDITOR_NAME <- git:user.name" {
		t.Fatalf("extraHostEnv = %v", got)
	}
}
