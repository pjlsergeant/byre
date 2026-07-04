package commands

import (
	"errors"

	"byre/internal/lock"
)

// withSetupLock runs fn while holding the per-project setup lock, surfacing both
// fn's error and any unlock error.
func withSetupLock(path string, fn func() error) error {
	lk, err := lock.Acquire(path)
	if err != nil {
		return err
	}
	ferr := fn()
	rerr := lk.Release()
	return errors.Join(ferr, rerr)
}

// withTwoSetupLocks holds two setup locks (acquired in a stable order to avoid
// deadlock) while running fn. Used by rehome, which mutates two projects' state.
func withTwoSetupLocks(a, b string, fn func() error) error {
	if a > b {
		a, b = b, a
	}
	la, err := lock.Acquire(a)
	if err != nil {
		return err
	}
	lb, err := lock.Acquire(b)
	if err != nil {
		return errors.Join(err, la.Release())
	}
	ferr := fn()
	return errors.Join(ferr, lb.Release(), la.Release())
}
