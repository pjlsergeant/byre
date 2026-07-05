package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"byre/internal/skills"
)

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
	for _, h := range hooks {
		if err := r.NetnsInit(image, container, h.Path, env); err != nil {
			fmt.Fprintf(warn, "byre: netns init (skill %q, %s) failed: %v\n", h.Skill, h.Path, err)
			fmt.Fprintln(warn, "byre: the launch gate will not open — the box will time out and exit rather than run unprotected (failing closed).")
			return
		}
	}
}
