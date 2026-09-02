package commands

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// processGroupCmd is CommandContext whose cancel kills the whole process
// group, not just the direct child. Stdout/stderr wiring, output caps, and
// the deadline itself stay with the caller -- the two callers have different
// policies on all three.
func processGroupCmd(ctx context.Context, waitDelay time.Duration, exe string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, args...)
	// Its own group: that is what the cancel below kills, and it also keeps
	// the child out of the foreground group, so a clipboard tool does not
	// make the terminal title flap (none of these tools read the tty).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The group exists, so cancel it as a GROUP. CommandContext's default kills
	// the direct child only, which leaves the descendants these tools spawn
	// (osascript's helpers, a git hook, a pager, a credential helper) alive
	// and holding the stdout pipe -- the caller's read then blocks past the
	// deadline that was supposed to end it. Negative pid is the whole process
	// group.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// And a second bound on the wait itself, for a descendant that outlives
	// even the group kill (one that changed its own group).
	cmd.WaitDelay = waitDelay
	return cmd
}
