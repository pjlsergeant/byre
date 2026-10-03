package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/pjlsergeant/byre/internal/runner"
)

// helperQuerier is the slice of the engine surface the leftover-helper check
// needs: any-state container listing plus the engine's name for the remedy.
type helperQuerier interface {
	Engine() runner.Engine
	ContainersByLabel(label string) ([]string, error)
}

// refuseLeftoverHelpers refuses while a helper container of project id is
// alive on r, in any state, under EITHER key a helper can name this project by
// (its own, and rehome's copy source). A helper is a one-shot that fills or
// reads a project volume (a seed, rehome's copy, backup's capture, restore's pour);
// one that outlived the byre that started it -- an engine outage mid-pour, a
// byre killed mid-seed -- is still holding a volume the caller is about to
// mount, read, delete or fill, and the launch-record scan (ADR 0054) cannot
// see it because a helper writes no record. Every path that mounts or
// mutates a project volume runs this and names the leftover with its `rm -f`
// line; nothing is removed on the user's behalf. A query failure refuses
// too: an engine that cannot be asked cannot be declared helper-free.
func refuseLeftoverHelpers(r helperQuerier, id string) error {
	for _, label := range helperProjectLabels(id) {
		ids, err := r.ContainersByLabel(label)
		if err != nil {
			return fmt.Errorf("checking for leftover helper containers (%s): %w", r.Engine(), err)
		}
		if len(ids) > 0 {
			return errors.New(leftoverHelperText(r.Engine(), ids))
		}
	}
	return nil
}

// leftoverHelperText is the refusal every sweep prints for a surviving
// helper: what it is, and the one command that clears it.
func leftoverHelperText(eng runner.Engine, ids []string) string {
	short := make([]string, len(ids))
	for i, id := range ids {
		short[i] = shortID(id)
	}
	return fmt.Sprintf("a byre helper container from an earlier backup, restore, seed or rehome is still present on %s (%s); it may still hold this project's volumes — remove it, then re-run: %s rm -f %s",
		eng, strings.Join(short, ", "), eng, strings.Join(short, " "))
}

// helperCleaner is the slice a verb's own cleanup needs: the engine's name for
// the remedies it prints, and the bounded calls it makes -- its own helpers,
// and the volume a rollback has to take back. Bounded throughout, because this
// slice is only ever used on a path that has already failed and still holds the
// setup lock.
type helperCleaner interface {
	Engine() runner.Engine
	ContainersByLabelBounded(label string) ([]string, error)
	ContainerForceRemove(container string) error
	VolumeExistsBounded(name string) (bool, error)
	VolumeRemoveBounded(name string) error
}

// removeRunHelpers force-removes every helper carrying THIS invocation's run
// id (never another invocation's), on a path that has already failed or been
// cancelled. Both calls are bounded (runner.CleanupTimeout): a daemon that
// never answers makes the cleanup stop, and the summary then says what may
// remain rather than hanging. Returns nil when nothing is left; otherwise the
// text the summary prints -- the container, and the `rm -f` line the user
// runs by hand -- as the error.
func removeRunHelpers(w io.Writer, r helperCleaner, runID string) error {
	ids, err := r.ContainersByLabelBounded(helperRunLabel(runID))
	if err != nil {
		return fmt.Errorf("could not list this run's helper containers on %s (%v); if one is still running it holds the volume it was filling — find it with: %s ps -a --filter label=%s, and remove it with: %s rm -f <id>",
			r.Engine(), err, r.Engine(), helperRunLabel(runID), r.Engine())
	}
	var left []string
	for _, id := range ids {
		if rerr := r.ContainerForceRemove(id); rerr != nil {
			left = append(left, shortID(id))
			continue
		}
		fmt.Fprintf(w, "byre: removed helper container %s\n", shortID(id))
	}
	if len(left) == 0 {
		return nil
	}
	return fmt.Errorf("helper container %s could not be removed on %s; it may still hold the volume it was filling — remove it by hand: %s rm -f %s (backup, reset and forget refuse until it is gone)",
		strings.Join(left, ", "), r.Engine(), r.Engine(), strings.Join(left, " "))
}

// endCancelledHelper ends the synchronous helper a cancelled verb left in
// flight: force-removing this run's helper containers is WHAT makes RunHelper
// return, and the wait for it is bounded by the same deadline the removal
// itself gets (runner.CleanupTimeout, which says why). done is the channel the
// in-flight call reports on; the return says whether it reported in time, so
// the caller can name what may still be running before it rolls back. wait is
// the deadline, 0 meaning CleanupTimeout -- a verb carries it as a field so a
// unit test can reach the expiry without spending the real thirty seconds.
func endCancelledHelper[T any](w io.Writer, r helperCleaner, runID string, done <-chan T, wait time.Duration) bool {
	if err := removeRunHelpers(w, r, runID); err != nil {
		dataf(w, "byre: %v\n", err)
	}
	return waitBounded(done, wait)
}

// waitBounded waits for one in-flight engine call to report on done, under the
// cleanup deadline (wait, 0 meaning CleanupTimeout). false means it had not
// reported by then, and the caller says what may still be running. Used on its
// own where there is nothing byre can do to MAKE the call return -- a stalled
// `volume create` has no container to take away -- and by endCancelledHelper
// after the removal that does.
func waitBounded[T any](done <-chan T, wait time.Duration) bool {
	t := time.NewTimer(orCleanupTimeout(wait))
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// orCleanupTimeout resolves a verb's cancellation deadline: the engine cleanup
// timeout unless the caller pinned a shorter one.
func orCleanupTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return runner.CleanupTimeout
	}
	return d
}

// runInterrupts is ONE SIGINT handler for a whole backup or restore, with the
// two behaviours the run's two phases need. It goes on the moment the run makes
// its first thing of its own -- the staging directory, which holds the volume
// bytes in plaintext -- and comes off at the commit point.
//
// Before that commit point nothing the user owns has been touched, and what
// byre has made is a staging directory, a project directory restore created,
// and perhaps a preflight helper. An interrupt there is handled where it lands:
// before clears those and the process ends, 1. It cannot merely cancel a
// context, because the verb is sitting in an install offer, a review prompt or
// a lock wait and none of them watches one -- Go holds SIGINT off the process
// while a handler is installed, so a cancel-only handler would swallow the
// Ctrl-C of a verb that is only WAITING and leave byre unkillable.
//
// From the critical section on the SAME handler cancels the context the
// mutating stretch watches: there a created volume, a written config or a
// helper mid-extraction has to be UNDONE, and only the code doing it knows how.
// After the commit point stop takes it off, so a Ctrl-C during the summary
// kills byre the ordinary way -- and so does a second one at any time, because
// firing restores the default disposition before it acts.
type runInterrupts struct {
	ch   chan os.Signal
	done chan struct{}
	once sync.Once

	// mu makes the phase switch and the handler's own action exclusive: an
	// interrupt that arrives first finishes clearing (and exits) before the verb
	// can write anything, and one that arrives later finds the cancellation
	// instead of the clearing.
	mu     sync.Mutex
	before func()
	cancel context.CancelFunc
}

// armRunInterrupts installs the run's handler. existing is the caller's own
// cancellation when a test pinned one: there nothing is installed (nil, which
// every method below accepts) -- a test drives both phases itself, and a real
// handler in a test process would divert the interrupt of whoever ran it.
func armRunInterrupts(existing context.Context, before func()) *runInterrupts {
	if existing != nil {
		return nil
	}
	ri := &runInterrupts{ch: make(chan os.Signal, 1), done: make(chan struct{}), before: before}
	signal.Notify(ri.ch, os.Interrupt)
	go func() {
		select {
		case <-ri.done:
		case <-ri.ch:
			ri.fire()
		}
	}()
	return ri
}

// fire is the handler's one action, in whichever phase the interrupt landed.
func (ri *runInterrupts) fire() {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	// Off first: a SECOND Ctrl-C must kill byre the ordinary way rather than
	// queue behind the clearing this one is about to do.
	signal.Stop(ri.ch)
	if ri.cancel != nil {
		ri.cancel()
		return
	}
	ri.before()
	// Exiting straight from the handler goroutine, deliberately: the main
	// goroutine is blocked in a terminal read or a lock wait, neither of which
	// watches anything, so there is nobody to hand an error to; the clearing
	// above is the whole of what this phase made, and the setup lock is an flock
	// the kernel drops with the process. Re-raising SIGINT instead would exit
	// 130, where every other byre cancellation exits 1.
	os.Exit(1)
}

// enterCritical switches the handler to cancelling the context the mutating
// stretch watches, and returns that context with the function that takes the
// handler off again (which cancels too, as signal.NotifyContext's own stop
// does). existing is the caller's own context when a test pinned one: it is
// used as it stands, and a nil receiver IS that case.
func (ri *runInterrupts) enterCritical(existing context.Context) (context.Context, func()) {
	if existing != nil {
		return existing, func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ri.mu.Lock()
	ri.cancel = cancel
	ri.mu.Unlock()
	return ctx, func() { cancel(); ri.stop() }
}

// stop takes the handler off for good. Safe to call more than once, and never
// under mu: before runs holding it and reaches this through the verb's own
// cleanup.
func (ri *runInterrupts) stop() {
	if ri == nil {
		return
	}
	signal.Stop(ri.ch)
	ri.once.Do(func() { close(ri.done) })
}

// noteHelperMayStillRun is what a cancelled verb says when its helper did not
// end within that deadline: the container may still be holding the volume it
// was reading or filling, and ending it is the user's to do -- byre has stopped
// waiting, not stopped caring.
func noteHelperMayStillRun(w io.Writer, r helperCleaner, runID, volume string, wait time.Duration) {
	dataf(w, "byre: the helper byre ran over volume %s has not ended within %s and may still be running — find it with: %s ps -a --filter label=%s, and remove it with: %s rm -f <id>\n",
		volume, orCleanupTimeout(wait), r.Engine(), helperRunLabel(runID), r.Engine())
}

// noteVolumeMayStillBeCreated is the same admission for the other call byre
// cannot make return: a `volume create` the engine never answered. The
// rollback found nothing at the name to remove, so the create may land after
// byre has gone -- and a volume that IS at that name is one the next restore
// reports as already here and KEEPS, dropping the backed-up copy of it. That
// outcome is silent, so the removal is named here, to run before re-running.
func noteVolumeMayStillBeCreated(w io.Writer, r helperCleaner, phys string) {
	dataf(w, "byre: %s may still create volume %s after byre has gone, and a re-run would then keep whatever occupies that name and drop the backed-up copy of it — remove it before re-running: %s volume rm %s. %s\n",
		r.Engine(), phys, r.Engine(), phys, escaped(forgetFallback))
}
