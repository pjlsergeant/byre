package commands

import (
	"fmt"
	"io"
	"time"

	"byre/internal/skills"
)

// runNetnsInits applies the enabled skills' declared netns-init hooks (e.g.
// the firewall's rule script) to the box once its network namespace exists.
// It runs concurrently with the attached foreground `run`: poll until the
// container reports running, then run each hook — a run-to-completion helper
// container joining the box's netns as root with NET_ADMIN (the box itself
// has neither). The box's launcher waits at the launch gate meanwhile, so no
// skill or agent code executes before the hooks finish.
//
// done closes when the session ends; if the container never comes up (the
// run failed at once), the poll exits quietly — the main path reports that
// failure. A hook failure is printed, and nothing else is done here: the
// launch gate never opens, so the box times out and dies closed rather than
// running unprotected. Enforcement lives in the gate, not in this goroutine.
func runNetnsInits(r sessionRunner, warn io.Writer, container, image string, hooks []skills.NetnsHook, env map[string]string, done <-chan struct{}) {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		// Check first (not tick-first) so a container that is already up is
		// detected immediately; an inspect error means "not created yet" (or a
		// transient engine hiccup) and polling continues until done.
		if running, err := r.ContainerRunning(container); err == nil && running {
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
