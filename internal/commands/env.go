package commands

import (
	"os"
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
	if name, ok := strings.CutPrefix(src, "env:"); ok {
		return os.Getenv(name)
	}
	if src == "tz:" {
		return hostTimezone()
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

// hostTimezone resolves the "tz:" source: the host's TZ env var when set,
// else the IANA name read from the /etc/localtime symlink (Linux and macOS
// both point it into a zoneinfo tree). Underivable — no TZ var and no
// symlink — reads as empty, and the entry sets nothing, like an unset git
// config key.
func hostTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	return tzFromZoneinfoPath(target)
}

// tzFromZoneinfoPath extracts the IANA zone name from a localtime symlink
// target: everything after the last "zoneinfo/" path element.
func tzFromZoneinfoPath(target string) string {
	const marker = "zoneinfo/"
	i := strings.LastIndex(target, marker)
	if i < 0 {
		return ""
	}
	return target[i+len(marker):]
}
