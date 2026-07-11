package lock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTryAcquireBlocksSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	l1, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}

	// A second holder must not be able to take it while l1 holds it.
	_, ok, err := TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second TryAcquire succeeded while first holds the lock")
	}

	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}

	// After release the lock is available again.
	l2, ok, err := TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("TryAcquire failed after the lock was released")
	}
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
}

// A waiter queued on a lock file that gets DELETED while it waits (forget/
// rehome remove the store, lock file included) must not return holding the
// orphaned inode — later arrivals would recreate the path and lock a fresh
// inode, splitting the mutex. The acquirer must requeue against the live file.
func TestAcquireNeverReturnsAnUnlinkedInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l1, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan *Lock, 1)
	errc := make(chan error, 1)
	go func() {
		l2, err := Acquire(path)
		if err != nil {
			errc <- err
			return
		}
		got <- l2
	}()

	// Let the waiter (usually) queue on the original inode, then unlink it
	// while it waits. Whichever side of the race it lands on, the invariant
	// below must hold.
	time.Sleep(50 * time.Millisecond)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errc:
		t.Fatalf("waiter failed: %v", err)
	case l2 := <-got:
		defer l2.Release()
		// The returned lock's inode must be the one the path names NOW — a
		// stale unlinked inode would be invisible to every later acquirer.
		lockedInfo, err := l2.f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		pathInfo, err := os.Stat(path)
		if err != nil {
			t.Fatalf("acquired lock's path does not exist (stale inode held): %v", err)
		}
		if !os.SameFile(lockedInfo, pathInfo) {
			t.Fatal("Acquire returned a lock on an unlinked inode — the mutex is split")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter never acquired")
	}
}

func TestReleaseNilSafe(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("Release on nil lock: %v", err)
	}
}
