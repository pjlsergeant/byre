// Package lock provides a per-project setup mutex via an advisory file lock.
//
// It serializes byre's setup mutations (generate/build/volume-create/seed) for a
// project. It is deliberately NOT held for the long-lived interactive container
// session — single-session safety for the running container is the engine label
// check, not this lock. Unix-only (byre targets Linux/macOS hosts).
package lock

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

// Lock is a held advisory file lock.
type Lock struct {
	f  *os.File
	fi os.FileInfo
}

// Held describes the INODE this lock is held on, as the descriptor itself
// reports it. flock is per-inode, so a caller that goes on to MUTATE the
// locked directory needs this: the name it locked can be renamed away and
// replaced underneath it, and acting on the name would then act on a store
// belonging to whoever created the replacement, with this process's lock
// protecting the old one. Compare before you delete.
func (l *Lock) Held() os.FileInfo { return l.fi }

// Acquire blocks until the lock at path is held.
func Acquire(path string) (*Lock, error) {
	return acquire(path, false)
}

// TryAcquire attempts to take the lock without blocking. ok is false if another
// holder currently has it.
func TryAcquire(path string) (l *Lock, ok bool, err error) {
	l, err = acquire(path, true)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return l, true, nil
}

// The requeue loop below is UNBOUNDED, deliberately. A process that keeps
// unlinking and recreating the lock file can keep a waiter cycling forever --
// but so can one that simply HOLDS the flock, which no timeout here would
// help with either, so a bound would close one path to a wedge and leave its
// twin open. It would also cost something real: legitimate waits are behind a
// live `byre develop` doing a cold build, and a timeout that fired there
// would abort a correct wait as if it were a fault. Waiters are not silent --
// acquireNoisy prints one "waiting for another byre setup..." line, so a wedge
// is visible and ctrl-C ends it. The only actor that can drive the loop is
// something already running on the host with write access to ~/.byre, which
// is the --self-edit trust boundary, disclosed where that grant is taken.
func acquire(path string, nonblock bool) (*Lock, error) {
	for {
		f, locked, err := hostopen.OpenLockFile(path)
		if err != nil {
			return nil, err
		}
		how := syscall.LOCK_EX
		if nonblock {
			how |= syscall.LOCK_NB
		}
		if err := syscall.Flock(int(f.Fd()), how); err != nil {
			f.Close()
			return nil, err
		}
		// The lock file can be legitimately deleted while we were queued on
		// it: forget/rehome clear a project store, lock file included. Flock
		// is per-INODE, so a waiter that then wins the unlinked inode holds a
		// lock no later arrival can see (they open — and recreate — the path,
		// locking a fresh inode): a split lock. Only return held if the path
		// still names the inode we locked; otherwise requeue against the live
		// file. If the whole store dir is gone the reopen fails ENOENT and
		// the caller hears it loudly — operating on a deleted store must not
		// proceed silently.
		same, serr := hostopen.SameFileAt(path, locked)
		if serr != nil {
			f.Close()
			return nil, serr
		}
		if same {
			return &Lock{f: f, fi: locked}, nil
		}
		f.Close()
	}
}

// Release drops the lock. A second Release is a no-op by contract: every
// acquisition path returns a held lock or an error, so the only repeat
// caller is a defer running after an explicit Release.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	ferr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f, l.fi = nil, nil // Held() describes a lock this process HOLDS
	if ferr != nil {
		return fmt.Errorf("unlock: %w", ferr)
	}
	return cerr
}
