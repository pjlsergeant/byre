package commands

import (
	"errors"
	"fmt"
	"io"

	"github.com/pjlsergeant/byre/internal/lock"
	"github.com/pjlsergeant/byre/internal/project"
)

// requireRecorded fails if the project is no longer enrolled -- its
// collision-fence `path` record is gone (a concurrent `byre forget` cleared the
// store while this command waited for the setup lock) or names a different
// canonical path. Setup writers call it as the FIRST action inside the lock, so
// they never build images/volumes/containers on a store another command already
// tore down (which would resurrect a forgotten project). It deliberately does
// NOT re-Bootstrap: recreating the record would convert forget's cancellation
// into resurrection and could race a colliding claimant.
//
// Applied to the two image BUILDERS (develop, rebuild). The other setup-lock
// writers are consciously deferred: forget/reset are the teardown side (they do
// the clearing), and rehome/worktree/preset/config have create/retire semantics
// where a plain "must already be recorded" check is not obviously correct --
// they want the per-command analysis the tombstone design below implies.
//
// Residual (consciously deferred): re-checking Recorded() does not preserve the
// collision fence across an id-hash-collision window -- if `forget` left `path`
// in place a colliding claimant's own check would pass. Closing that needs a
// retiring-tombstone/generation design across every setup writer; not built.
func requireRecorded(paths project.Paths) error {
	recorded, err := paths.Recorded()
	if err != nil {
		return err // id collision or an unreadable record -- fail loudly
	}
	if !recorded {
		return fmt.Errorf("the project store was cleared while waiting for the setup lock (a concurrent `byre forget`?) — re-run the command")
	}
	return nil
}

// acquireNoisy takes the setup lock, telling the user (on w — stderr) when it
// has to wait. Without the message, a second byre invocation just hangs
// silently while another setup's build/seed finishes.
func acquireNoisy(w io.Writer, path string) (*lock.Lock, error) {
	if l, ok, err := lock.TryAcquire(path); err != nil {
		return nil, err
	} else if ok {
		return l, nil
	}
	fmt.Fprintln(w, "byre: waiting for another byre setup on this project to finish…")
	return lock.Acquire(path)
}

// withSetupLock runs fn while holding the per-project setup lock, surfacing both
// fn's error and any unlock error. w gets the waiting note if the lock is held.
func withSetupLock(w io.Writer, path string, fn func() error) error {
	lk, err := acquireNoisy(w, path)
	if err != nil {
		return err
	}
	ferr := fn()
	rerr := lk.Release()
	return errors.Join(ferr, rerr)
}

// withTwoSetupLocks holds two setup locks (acquired in a stable order to avoid
// deadlock) while running fn. Used by rehome, which mutates two projects' state.
func withTwoSetupLocks(w io.Writer, a, b string, fn func() error) error {
	if a > b {
		a, b = b, a
	}
	la, err := acquireNoisy(w, a)
	if err != nil {
		return err
	}
	lb, err := acquireNoisy(w, b)
	if err != nil {
		return errors.Join(err, la.Release())
	}
	ferr := fn()
	return errors.Join(ferr, lb.Release(), la.Release())
}
