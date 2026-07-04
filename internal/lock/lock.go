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
)

// Lock is a held advisory file lock.
type Lock struct {
	f *os.File
}

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

func acquire(path string, nonblock bool) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
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
	return &Lock{f: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	ferr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if ferr != nil {
		return fmt.Errorf("unlock: %w", ferr)
	}
	return cerr
}
