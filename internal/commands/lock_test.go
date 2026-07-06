package commands

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/lock"
)

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
	ran := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := withSetupLock(&out, path, func() error { ran = true; return nil }); err != nil {
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
	if ran {
		t.Fatal("fn must not run while the lock is held elsewhere")
	}
	holder.Release()
	wg.Wait()
	if !ran {
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
