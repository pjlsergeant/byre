package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/lock"
)

// TestRequireRecorded pins the forget/develop TOCTOU guard: a setup writer that
// waited for the lock while a concurrent forget cleared the store must abort,
// not build on the cleared store.
func TestRequireRecorded(t *testing.T) {
	paths, _ := testPaths(t) // bootstrapped: a valid path record exists

	if err := requireRecorded(paths); err != nil {
		t.Fatalf("a freshly bootstrapped project must be recorded: %v", err)
	}

	// A concurrent `byre forget` clears the store, path record included.
	if err := os.Remove(paths.PathRecord); err != nil {
		t.Fatal(err)
	}
	if err := requireRecorded(paths); err == nil || !strings.Contains(err.Error(), "cleared") {
		t.Fatalf("a cleared record must abort with a cleared-store error, got %v", err)
	}

	// A record naming a different canonical path (id collision) also aborts.
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireRecorded(paths); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("a colliding path record must abort on the id-collision rule, got: %v", err)
	}
}

// TestWithSetupLockNotesWhenWaiting pins the contended-lock UX: a second
// invocation says it's waiting instead of hanging silently; an uncontended one
// says nothing.
func TestWithSetupLockNotesWhenWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	var quiet bytes.Buffer
	if err := withSetupLock(&quiet, path, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if quiet.Len() != 0 {
		t.Fatalf("uncontended lock must be silent, got %q", quiet.String())
	}

	holder, err := lock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	var out safeBuffer
	var wg sync.WaitGroup
	var ran atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := withSetupLock(&out, path, func() error { ran.Store(true); return nil }); err != nil {
			t.Errorf("contended withSetupLock: %v", err)
		}
	}()
	// The waiting note appears while the holder still has the lock.
	for i := 0; i < 200 && !strings.Contains(out.String(), "waiting"); i++ {
		sleepMs(10)
	}
	if !strings.Contains(out.String(), "waiting for another byre setup") {
		holder.Release()
		wg.Wait()
		t.Fatalf("expected a waiting note while contended, got %q", out.String())
	}
	if ran.Load() {
		t.Fatal("fn must not run while the lock is held elsewhere")
	}
	holder.Release()
	wg.Wait()
	if !ran.Load() {
		t.Fatal("fn should run once the lock frees")
	}
}

// TestWithTwoSetupLocksSurvivesOppositeOrders pins the reason withTwoSetupLocks
// sorts its two paths: two concurrent rehomes naming the SAME pair of projects
// in opposite orders. Each caller passes the pair the way its own command reads
// it; the sort is what stops one holding the lock the other is waiting on, with
// neither ever making progress. The rounds are there because a deadlock needs
// the two acquisitions to interleave -- one pass could miss it by luck -- and
// the deadline is there because the failure mode is a hang: without it a
// regression would stall the suite until the whole run's timeout, reported as
// a package that never finished rather than as this contract broken.
func TestWithTwoSetupLocksSurvivesOppositeOrders(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "alpha.lock")
	b := filepath.Join(dir, "omega.lock")

	const rounds = 40
	var inside atomic.Int32 // callers inside fn at once
	var overlapped atomic.Bool
	var failed atomic.Value // first error from either caller

	var wg sync.WaitGroup
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		wg.Add(1)
		go func(first, second string) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				err := withTwoSetupLocks(io.Discard, first, second, func() error {
					if inside.Add(1) != 1 {
						overlapped.Store(true)
					}
					// Hold long enough that a rival reaches its own
					// acquisition while this one is inside: without a real
					// overlap window the exclusion check would pass on
					// serialized-by-luck runs.
					time.Sleep(time.Millisecond)
					inside.Add(-1)
					return nil
				})
				if err != nil {
					failed.CompareAndSwap(nil, err)
					return
				}
			}
		}(pair[0], pair[1])
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("withTwoSetupLocks deadlocked: two callers naming %s and %s in opposite orders never both finished (the acquisition order is not stable)", a, b)
	}
	if err, ok := failed.Load().(error); ok {
		t.Fatalf("contended withTwoSetupLocks: %v", err)
	}
	if overlapped.Load() {
		t.Fatal("two callers ran fn at the same time: the pair of locks is not mutually exclusive")
	}
}

// safeBuffer is a mutex-guarded bytes.Buffer (the waiting note is written from
// another goroutine).
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }
