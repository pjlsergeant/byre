package commands

import (
	"errors"
	"fmt"
	"io"

	"byre/internal/lock"
)

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
