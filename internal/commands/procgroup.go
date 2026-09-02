package commands

import (
	"context"
	"io"
	"os/exec"
	"sync"
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
	// Three bounds, one each: Cancel's group SIGKILL for descendants still
	// in the group; boundPipe's delayed close of the caller's stdout pipe
	// for a descendant that left the group and holds stdout; WaitDelay for
	// the post-exit unclosed-pipe case inside Wait. WaitDelay alone does
	// not bound a ReadAll that runs before Wait.
	cmd.WaitDelay = waitDelay
	return cmd
}

// boundPipe closes pipe `delay` after ctx ends, unblocking a ReadAll that a
// descendant holding stdout would otherwise keep blocked past the deadline.
// Closing the read end gives the writer EPIPE. The returned stop cancels the
// delayed close so a command that completed normally does not leak the
// goroutine for the full delay. Closing an already-closed pipe is ignored.
func boundPipe(ctx context.Context, delay time.Duration, pipe io.Closer) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	stop = func() { once.Do(func() { close(done) }) }
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			_ = pipe.Close()
		case <-done:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}()
	return stop
}
