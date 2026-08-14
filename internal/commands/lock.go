package commands

import (
	"errors"
	"fmt"
	"io"
	"os"

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
// Applied to setup writers that Bootstrap before taking the lock: develop,
// rebuild, worktree image preparation, preset apply, and rehome's NEW-id side.
// Forget/reset are the teardown side and config writes have their own
// compare-and-swap semantics.
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
		return projectClearedError{}
	}
	return nil
}

type projectClearedError struct{}

func (projectClearedError) Error() string {
	return "the project store was cleared while waiting for the setup lock (a concurrent `byre forget`?) — re-run the command"
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

// setupLocked runs fn while holding the per-project setup lock and hands its
// result across the lock boundary as a real return value — the boundary's
// contract lives in the signature instead of in variables captured around a
// closure. Both fn's error and any unlock error surface (joined), and either
// one discards the value: a caller never sees a partially prepared T. That
// discard preserves the existing abandon-on-Release-failure semantics — fn's
// side effects (a created container, say) stand, but the launch that would
// have consumed the value does not run. w gets the waiting note if the lock
// is held.
func setupLocked[T any](w io.Writer, path string, fn func() (T, error)) (T, error) {
	var zero T
	lk, err := acquireNoisy(w, path)
	if err != nil {
		return zero, err
	}
	v, ferr := fn()
	if err := errors.Join(ferr, lk.Release()); err != nil {
		return zero, err
	}
	return v, nil
}

// setupLockedProject is the project-recorded sibling used after pre-lock human
// work. If forget removed the whole store, acquiring its lock fails before
// requireRecorded can produce the typed cancellation; normalize that one
// acquisition failure without relabeling ENOENT returned by fn itself.
func setupLockedProject[T any](w io.Writer, paths project.Paths, fn func() (T, error)) (T, error) {
	var zero T
	lk, err := acquireNoisy(w, paths.LockFile)
	if os.IsNotExist(err) {
		return zero, projectClearedError{}
	}
	if err != nil {
		return zero, err
	}
	v, ferr := fn()
	if err := errors.Join(ferr, lk.Release()); err != nil {
		return zero, err
	}
	return v, nil
}

// withSetupLock runs fn while holding the per-project setup lock, surfacing both
// fn's error and any unlock error. w gets the waiting note if the lock is held.
func withSetupLock(w io.Writer, path string, fn func() error) error {
	_, err := setupLocked(w, path, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

// withDestructiveSetupLock is reset/forget's fail-fast setup lock. Destructive
// work must never queue behind an active setup and then erase what that command
// just created; the user can safely retry after the named operation finishes.
func withDestructiveSetupLock(path, verb string, fn func() error) error {
	return withPreparedDestructiveSetupLock(path, "run `byre "+verb+"` again", nil, fn)
}

// withPreparedDestructiveSetupLock is the editor-volume sibling. An editor on
// a never-enrolled project has no lock path yet, so its deliberate first
// mutation may Bootstrap under this helper; an existing store is always tried
// first, ensuring prepare cannot recreate enrollment before a contention
// refusal.
func withPreparedDestructiveSetupLock(path, retry string, prepare func() error, fn func() error) error {
	lk, ok, err := lock.TryAcquire(path)
	if os.IsNotExist(err) && prepare != nil {
		if err := prepare(); err != nil {
			return err
		}
		lk, ok, err = lock.TryAcquire(path)
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("another byre setup operation is in progress for this project — wait for it to finish, then %s", retry)
	}
	return errors.Join(fn(), lk.Release())
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
