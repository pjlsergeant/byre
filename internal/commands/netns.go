package commands

import (
	"fmt"
	"io"
	"time"

	"byre/internal/skills"
)

// runNetnsInits applies the enabled skills' declared netns-init hooks (e.g.
// the firewall's rule script) to the box once its network namespace exists.
// It runs concurrently with the attached foreground `run`: poll until OUR
// container appears, then run each hook — a run-to-completion helper container
// joining the box's netns as root with NET_ADMIN (the box itself has
// neither). The box's launcher waits at the launch gate meanwhile, so no
// skill or agent code executes before the hooks finish.
//
// It polls by the worktree LABEL (not the container name) and targets the
// resolved container ID: the name byre-<worktreeID> is deterministic, so an
// unrelated container squatting it would otherwise get the root+NET_ADMIN
// helper run in its netns. The label is byre's own, asserted last in the run
// argv (unspoofable by run_args), so a match is definitely our box. develop's
// fast path already refused to start if a session held this label, so at most
// one container carries it — ours.
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
	for _, h := range hooks {
		if err := r.NetnsInit(image, container, h.Path, env); err != nil {
			fmt.Fprintf(warn, "byre: netns init (skill %q, %s) failed: %v\n", h.Skill, h.Path, err)
			fmt.Fprintln(warn, "byre: the launch gate will not open — the box will time out and exit rather than run unprotected (failing closed).")
			return
		}
	}
}
