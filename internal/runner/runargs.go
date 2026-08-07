package runner

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// BindMount is a host-path bind for `docker run -v host:target[:mode]`.
type BindMount struct {
	Host   string
	Target string
	Mode   string // ro|rw; empty defaults to ro
}

// PortPublish publishes a container port to the host: `docker run -p
// iface:host:container`. All three parts are required — byre normalizes every
// publication upstream (config.PortEffective), so this layer never decides a
// default.
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

// TmpfsMount is a per-session in-memory filesystem (`docker run --tmpfs`):
// nothing under it touches image layers, the writable layer, or a volume,
// and it empties when the container stops — the credential delivery
// surface. Rendered via the --tmpfs flag (not --mount type=tmpfs) because
// only the flag form passes uid=/gid= through to the kernel mount.
type TmpfsMount struct {
	Target   string
	Size     int64  // bytes; 0 omits the option (engine default)
	Mode     string // e.g. "0700"; "" omits
	UID, GID int    // ownership of the mount root (ADR 0032: the container identity)
	// NoCopyUp adds notmpcopyup — Podman copies the image directory's
	// contents up into a fresh tmpfs by default, which a byre-owned
	// delivery mount never wants. Docker has no such behavior (or option);
	// the CALLER sets this per engine, keeping argv assembly engine-blind.
	NoCopyUp bool
}

// spec renders the --tmpfs value: target:opt,opt,... — rw so the receiver
// can write, noexec/nosuid/nodev as ordinary mount hygiene (ADR 0057: not
// sold as a defense; the box's own user is handed the contents).
func (t TmpfsMount) spec() string {
	opts := []string{"rw", "noexec", "nosuid", "nodev"}
	if t.Mode != "" {
		opts = append(opts, "mode="+t.Mode)
	}
	opts = append(opts, fmt.Sprintf("uid=%d", t.UID), fmt.Sprintf("gid=%d", t.GID))
	if t.Size > 0 {
		opts = append(opts, fmt.Sprintf("size=%d", t.Size))
	}
	if t.NoCopyUp {
		opts = append(opts, "notmpcopyup")
	}
	return t.Target + ":" + strings.Join(opts, ",")
}

// RunParams is everything needed to assemble a `docker run` invocation.
type RunParams struct {
	Image           string
	Name            string   // container name; makes single-session atomic (engine rejects a dup)
	Labels          []string // every byre.* label on the container (identity, client, netns nonce, launch record); re-asserted last so run_args can't override any of them (ADR 0006)
	WorkspaceHost   string   // worktree dir bound rw at WorkspaceTarget
	WorkspaceTarget string
	Env             map[string]string
	Binds           []BindMount
	Volumes         []NamedVolume
	Tmpfs           []TmpfsMount  // per-session in-memory mounts (credential delivery)
	Ports           []PortPublish // -p publications (host-exposed container ports)
	Caps            []string      // --cap-add (from skills)
	GroupAdds       []int         // --group-add (numeric gids from sock_groups probe; no /etc/group entry needed)
	Userns          string        // --userns value (Identity.Userns; rootless Podman keep-id); empty = no flag
	RunArgs         []string      // raw passthrough, last-wins
	Command         []string      // agent command; empty uses the image entrypoint default
	TTY             bool          // allocate a pseudo-TTY (-t); set only when stdin is an actual terminal, so a piped/non-interactive invocation (CI, an agent driving byre) doesn't fail with "the input device is not a TTY"
}

// CreateArgs builds the argv (after the engine name) for `docker create` — the
// same invocation RunArgs assembles, but creating the container without
// starting it. develop creates the container under the setup lock (so the
// name claim and the freshly seeded volumes appear atomically to lifecycle
// commands) and starts/attaches after releasing it (Runner.StartAttach);
// `create` accepts the whole `run` flag surface, --rm included.
func CreateArgs(p RunParams) []string {
	args := RunArgs(p)
	args[0] = "create"
	return args
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

	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
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
	for _, t := range p.Tmpfs {
		args = append(args, "--tmpfs", t.spec())
	}
	for _, pub := range p.Ports {
		args = append(args, "-p", portSpec(pub))
	}
	for _, c := range p.Caps {
		args = append(args, "--cap-add", c)
	}
	// Numeric --group-add before raw run_args so a skill/project run_arg can
	// still override (last-wins), matching Caps. Gids are sorted for
	// deterministic argv (same class as env keys).
	for _, g := range sortedInts(p.GroupAdds) {
		args = append(args, "--group-add", strconv.Itoa(g))
	}
	// The userns mapping (rootless Podman keep-id) sits with byre's own flags:
	// raw run_args after it can still override (last-wins, ADR 0006 — an
	// identity-changing run_arg is an author-owned footgun, same as --user).
	args = appendUserns(args, p.Userns)

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

func sortedInts(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	out := append([]int{}, in...)
	sort.Ints(out)
	return out
}
