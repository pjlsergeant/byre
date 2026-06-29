package runner

import (
	"fmt"
	"sort"
)

// BindMount is a host-path bind for `docker run -v host:target[:mode]`.
type BindMount struct {
	Host   string
	Target string
	Mode   string // ro|rw; empty defaults to ro
}

// NamedVolume is a resolved named volume for `docker run -v name:target`.
type NamedVolume struct {
	Name   string // docker volume name (already byre-<id>-<name>)
	Target string
}

// RunParams is everything needed to assemble a `docker run` invocation.
type RunParams struct {
	Image           string
	Name            string // container name; makes single-session atomic (engine rejects a dup)
	Label           string // byre.project=<id>
	WorkspaceHost   string // canonical project dir (bound rw at WorkspaceTarget)
	WorkspaceTarget string
	Env             map[string]string
	Binds           []BindMount
	Volumes         []NamedVolume
	Caps            []string // --cap-add (from skills)
	RunArgs         []string // raw passthrough, last-wins
	Command         []string // agent command; empty uses the image entrypoint default
}

// RunArgs builds the argv (after the engine name) for `docker run`.
//
// Ordering encodes the spec contract: byre's own flags first, then the raw
// run_args (so they can override byre's, e.g. --user/--network), then the
// identity --label re-asserted last so it always wins and lifecycle/status can
// find the container. The image and command come last.
func RunArgs(p RunParams) []string {
	args := []string{"run", "--rm", "-it"}
	if p.Name != "" {
		args = append(args, "--name", p.Name)
	}

	for _, k := range sortedKeys(p.Env) {
		args = append(args, "-e", k+"="+p.Env[k])
	}

	// --mount (not -v) so host paths containing ':' aren't misparsed and a
	// missing bind source is a clear error rather than a surprise named volume.
	if p.WorkspaceHost != "" {
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=%s", p.WorkspaceHost, p.WorkspaceTarget))
	}
	for _, b := range p.Binds {
		m := fmt.Sprintf("type=bind,source=%s,target=%s", b.Host, b.Target)
		if b.Mode != "rw" { // default (and "ro") => read-only
			m += ",readonly"
		}
		args = append(args, "--mount", m)
	}
	for _, v := range p.Volumes {
		args = append(args, "--mount", fmt.Sprintf("type=volume,source=%s,target=%s", v.Name, v.Target))
	}
	for _, c := range p.Caps {
		args = append(args, "--cap-add", c)
	}

	// Raw passthrough — last-wins over byre's flags.
	args = append(args, p.RunArgs...)

	// Identity label re-asserted after run_args so it can't be overridden.
	if p.Label != "" {
		args = append(args, "--label", p.Label)
	}

	args = append(args, p.Image)
	args = append(args, p.Command...)
	return args
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
