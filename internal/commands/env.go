package commands

import (
	"os/exec"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
)

// addEnvFromHost applies the resolved env_from_host passthrough (ADR 0026):
// each entry's host-side source value lands in env unless the source is
// disabled (""), the host has no value, or an explicit [env] KEY exists in
// the config — an explicit value in any layer beats the passthrough default.
func addEnvFromHost(env map[string]string, cfg config.Config) {
	for k, src := range cfg.EnvFromHost {
		if src == "" {
			continue
		}
		if _, explicit := cfg.Env[k]; explicit {
			continue
		}
		if v := hostSourceValue(src); v != "" {
			env[k] = v
		}
	}
}

// hostSourceValue reads one env_from_host source on the host. Unknown schemes
// read as empty — validation already refused them at config load; this is
// just the belt to that suspender.
func hostSourceValue(src string) string {
	if key, ok := strings.CutPrefix(src, "git:"); ok {
		return gitConfig(key)
	}
	return ""
}

func gitConfig(key string) string {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
