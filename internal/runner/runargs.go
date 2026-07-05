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

// PortPublish publishes a container port to the host: `docker run -p
// iface:host:container`. All three parts are required — byre normalizes every
// publication upstream (blank interface -> 127.0.0.1, blank host -> the
// container port), so this layer never decides a default.
type PortPublish struct {
	Interface string
	Host      int
	Container int
}

// NamedVolume is a resolved named volume for `docker run -v name:target`.
type NamedVolume struct {
	Name   string // docker volume name (already byre-<id>-<name>)
	Target string
}

// RunParams is everything needed to assemble a `docker run` invocation.
type RunParams struct {
	Image           string
	Name            string   // container name; makes single-session atomic (engine rejects a dup)
	Labels          []string // identity labels (byre.project=<id>, byre.workdir=<wt-id>); re-asserted last so run_args can't override them
	WorkspaceHost   string   // worktree dir bound rw at WorkspaceTarget
	WorkspaceTarget string
	Env             map[string]string
	Binds           []BindMount
	Volumes         []NamedVolume
	Ports           []PortPublish // -p publications (host-exposed container ports)
	Caps            []string      // --cap-add (from skills)
	RunArgs         []string      // raw passthrough, last-wins
	Command         []string      // agent command; empty uses the image entrypoint default
	TTY             bool          // allocate a pseudo-TTY (-t); set only when stdin is an actual terminal, so a piped/non-interactive invocation (CI, an agent driving byre) doesn't fail with "the input device is not a TTY"
}

// RunArgs builds the argv (after the engine name) for `docker run`.
//
// Ordering encodes the ADR 0006 contract: byre's own flags first, then the raw
// run_args (so they can override byre's, e.g. --user/--network), then the
// identity --label re-asserted last so it always wins and lifecycle/status can
// find the container. The image and command come last.
//
// -i is always passed (stdin stays open for the agent); -t (pseudo-TTY) is
// added only when TTY is set, since docker refuses -t under a non-TTY stdin
// ("the input device is not a TTY") — the case under CI or when another
// process drives byre non-interactively.
func RunArgs(p RunParams) []string {
	args := []string{"run", "--rm", "-i"}
	if p.TTY {
		args = append(args, "-t")
	}
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
	for _, pub := range p.Ports {
		args = append(args, "-p", portSpec(pub))
	}
	for _, c := range p.Caps {
		args = append(args, "--cap-add", c)
	}

	// Raw passthrough — last-wins over byre's flags.
	args = append(args, p.RunArgs...)

	// Identity labels re-asserted after run_args so they can't be overridden.
	for _, l := range p.Labels {
		if l != "" {
			args = append(args, "--label", l)
		}
	}

	args = append(args, p.Image)
	args = append(args, p.Command...)
	return args
}

// portSpec renders a docker -p value from a normalized publication (see
// PortPublish: interface and host are always set upstream). The old
// ephemeral/all-interfaces fallbacks were unreachable from byre and only
// documented behavior nothing produced.
func portSpec(p PortPublish) string {
	return fmt.Sprintf("%s:%d:%d", p.Interface, p.Host, p.Container)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
