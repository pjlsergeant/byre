package commands

import (
	"os"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
)

// hostEnvState is the outcome of resolving one env_from_host entry. Four
// states, not a boolean: a caller holding only "present" is invited to
// reconstruct the precedence rules itself and get them wrong -- the exact
// class of divergence resolveHostEnv exists to end.
type hostEnvState int

const (
	hostEnvDisabled   hostEnvState = iota // source is "" -- a layer switched the key off
	hostEnvOverridden                     // an explicit [env] KEY beats the passthrough (ADR 0026)
	hostEnvEmpty                          // the host source resolved to nothing -- NOT passed
	hostEnvDelivered                      // the value below reaches the box
	// hostEnvEncrypted: a credential row. It reaches the box on the tmpfs
	// channel after the launch unlock, never on the engine argv, so it must
	// be excluded from the `-e` export -- and it is NOT "not passed", which
	// is what an unrecognized source would otherwise read as.
	hostEnvEncrypted
)

// hostEnvResult is one entry's resolution: source and outcome together.
type hostEnvResult struct {
	Key    string
	Source string
	Value  string // set only when State == hostEnvDelivered
	State  hostEnvState
}

// resolveHostEnv resolves every env_from_host entry ONCE, deterministically
// ordered. Every consumer -- runtime env assembly, the status row, the
// provided-env annotations, the exposure tally -- reads THIS result, so
// "delivered" can only ever mean one thing: status renders the effect the
// runner applies, never its own re-derivation of the intent (the
// render-from-effect rule; the empty-git-identity lie was the 2026-07
// review's headline finding).
func resolveHostEnv(cfg config.Config, gitExe string) []hostEnvResult {
	keys := make([]string, 0, len(cfg.EnvFromHost))
	for k := range cfg.EnvFromHost {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]hostEnvResult, 0, len(keys))
	for _, k := range keys {
		r := hostEnvResult{Key: k, Source: cfg.EnvFromHost[k]}
		_, isCred, _ := config.ParseEncryptedRow(k, r.Source)
		if _, explicit := cfg.Env[k]; r.Source == "" {
			r.State = hostEnvDisabled
		} else if explicit {
			r.State = hostEnvOverridden
		} else if isCred {
			// A row whose payload is damaged is still a credential row, and
			// still not an argv value: the launch refuses it by name.
			r.State = hostEnvEncrypted
		} else if v := hostSourceValue(r.Source, gitExe); v != "" {
			r.Value, r.State = v, hostEnvDelivered
		} else {
			r.State = hostEnvEmpty
		}
		out = append(out, r)
	}
	return out
}

// addEnvFromHost applies the passthrough (ADR 0026): only a delivered
// result sets anything.
func addEnvFromHost(env map[string]string, hostEnv []hostEnvResult) {
	for _, r := range hostEnv {
		if r.State == hostEnvDelivered {
			env[r.Key] = r.Value
		}
	}
}

// providedEnv is the one builder of the "env keys this box actually
// supplies" set (MCP consumes-X annotations): config literals, plus
// DELIVERED host passthrough -- a configured source that resolved empty
// supplies nothing and must not annotate as if it did.
func providedEnv(cfg config.Config, hostEnv []hostEnvResult) map[string]bool {
	provided := map[string]bool{}
	for k := range cfg.Env {
		provided[k] = true
	}
	for _, r := range hostEnv {
		if r.State == hostEnvDelivered {
			provided[r.Key] = true
		}
	}
	return provided
}

// hostSourceValue reads one env_from_host source on the host. Credential
// schemes are not read here at all (they resolve at the launch unlock, on
// their own channel), and an unknown scheme reads as empty — validation
// already refused it at config load; this is a total function so a scheme
// reaching this point through a path that skipped validation sets nothing
// rather than panicking.
func hostSourceValue(src, gitExe string) string {
	if key, ok := strings.CutPrefix(src, "git:"); ok {
		return gitConfig(gitExe, key)
	}
	if name, ok := strings.CutPrefix(src, "env:"); ok {
		return os.Getenv(name)
	}
	if src == "tz:" {
		return hostTimezone()
	}
	return ""
}

func gitConfig(gitExe, key string) string {
	// Unsolicited (develop/status env resolution) against agent-shaped git
	// state — gitProbe's bounds apply; any refusal degrades to "" like an
	// unset key.
	out, err := gitProbe(gitExe, "config", "--get", key)
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
	target, err := hostopen.PlainReadlink("/etc/localtime", hostopen.HostUserOwned)
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
