package commands

import (
	"bytes"
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
	if err := requireRecorded(paths); err == nil {
		t.Fatal("a colliding path record must abort")
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
