package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/skills"
)

// sharedNetMode reports whether a NetworkMode value means the container was
// joined to a namespace that is not its own: the host's ("host"), another
// container's ("container:<id>"), or an arbitrary namespace path (podman's
// "ns:/path"). Everything else (bridge/none/slirp4netns/pasta/a network name)
// is a private namespace.
func sharedNetMode(mode string) bool {
	return mode == "host" || strings.HasPrefix(mode, "container:") || strings.HasPrefix(mode, "ns:")
}

// describeNetMode names a shared network mode for the skip message: "host"
// reads as the host's namespace, everything else quotes the mode.
func describeNetMode(mode string) string {
	if mode == "host" {
		return "HOST"
	}
	return mode
}

// stopClosed ends the session because netns hooks were refused: without them
// the launch gate is the only barrier, and it can't be trusted here (in a
// shared namespace any listener on the gate port would open it). Stopping the
// box is byre failing ITS claim closed, not gating the user's config.
func stopClosed(r sessionRunner, warn io.Writer, container string) {
	fmt.Fprintln(warn, "byre: stopping the box — the launch gate can't be trusted without the hooks, so byre won't let the session run unprotected (failing closed).")
	if err := r.Stop(container); err != nil {
		fmt.Fprintf(warn, "byre: stopping the box failed: %v — if the session launches anyway, it is running WITHOUT the declared network setup.\n", err)
	}
}

// runNonce returns the random value for this invocation's byre.run label (see
// naming.go: the netns target's ownership proof). A package var so tests can
// pin it; production always uses fresh crypto randomness.
var runNonce = func() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// No randomness means no ownership proof; the caller treats an empty
		// nonce as "don't run hooks" and the launch gate then fails closed.
		return ""
	}
	return hex.EncodeToString(b)
}

// runNetnsInits applies the enabled skills' declared netns-init hooks (e.g.
// the firewall's rule script) to the box once its network namespace exists.
// It runs concurrently with the attached foreground `run`: poll until OUR
// container appears, then run each hook — a run-to-completion helper container
// joining the box's netns as root with NET_ADMIN (the box itself has
// neither). The box's launcher waits at the launch gate meanwhile, so no
// skill or agent code executes before the hooks finish.
//
// It polls by the per-invocation byre.run NONCE label and targets the
// resolved container ID. The container name and the project/workdir labels are
// all derivable from the project path, so a container planted with any of
// them could capture the root+NET_ADMIN helper; the nonce is fresh randomness
// that exists only in this invocation's run argv (asserted last, unspoofable
// by run_args), so a match is OUR box, full stop.
//
// done closes when the session ends; if our container never appears (the run
// failed at once, or lost the name race to a peer that we don't own), the
// poll exits quietly — the main path reports that failure. A hook failure is
// printed and nothing else is done: the launch gate never opens, so the box
// times out and dies closed rather than running unprotected. Enforcement
// lives in the gate, not in this goroutine.
func runNetnsInits(r sessionRunner, warn io.Writer, label, image string, hooks []skills.NetnsHook, env map[string]string, done <-chan struct{}) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var container string
	for container == "" {
		// Check first (not tick-first) so an already-running box is detected
		// immediately; a query error means "not created yet" (or a transient
		// engine hiccup) and polling continues until done.
		if ids, err := r.RunningContainersByLabel(label); err == nil && len(ids) > 0 {
			container = ids[0]
			break
		}
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
	// The hooks join the box's network namespace as root with NET_ADMIN
	// (--net container:<id>), which is only byre's to mutate when that
	// namespace is the box's OWN. Under --network host, container:<other>, or
	// ns:/path (all reachable via run_args) the "box's namespace" is really
	// the host's, another container's, or an arbitrary one, and a hook like
	// the firewall's default-DROP rules would rewrite network state outside
	// the box. Refuse byre's own root action there — and STOP the box rather
	// than trusting the launch gate: the gate is "a listener on a loopback
	// port", and in a shared namespace a stranger's listener on that port
	// would open it, launching the agent with no rules applied. An inspect
	// failure gets the same treatment: no proof of a private namespace, no
	// hooks, no launch.
	mode, err := r.NetworkMode(container)
	if err != nil {
		fmt.Fprintf(warn, "byre: netns init: could not determine the box's network mode: %v\n", err)
		stopClosed(r, warn, container)
		return
	}
	if sharedNetMode(mode) {
		fmt.Fprintf(warn, "byre: netns init skipped: the box shares the %s network namespace (run_args?) — that namespace is not byre's to modify, so the netns hooks (e.g. firewall rules) will not be applied there. Drop the network override or disable the skill that declares the hook.\n", describeNetMode(mode))
		stopClosed(r, warn, container)
		return
	}
	for _, h := range hooks {
		if err := r.NetnsInit(image, container, h.Path, env); err != nil {
			fmt.Fprintf(warn, "byre: netns init (skill %q, %s) failed: %v\n", h.Skill, h.Path, err)
			fmt.Fprintln(warn, "byre: the launch gate will not open — the box will time out and exit rather than run unprotected (failing closed).")
			return
		}
	}
}
