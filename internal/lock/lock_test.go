package lock

import (
	"path/filepath"
	"testing"
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

func TestReleaseNilSafe(t *testing.T) {
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("Release on nil lock: %v", err)
	}
}
