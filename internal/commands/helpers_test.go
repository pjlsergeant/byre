package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/runner"
)

// A helper whose `rm -f` fails because its own --rm already took it is gone,
// and gone is what the cleanup was for: it is reported removed, never as a
// leftover. A helper the `rm -f` failed on and the listing still shows IS a
// leftover, named with the line that removes it.
func TestRemoveRunHelpersNamesOnlyAHelperStillListed(t *testing.T) {
	f := &fakeRunner{
		allContainers: map[string][]string{helperRunLabel("run1"): {"stuck1", "vanished1"}},
		forceRmErr:    map[string]bool{"stuck1": true},
		forceRmGone:   map[string]bool{"vanished1": true},
	}
	var w bytes.Buffer
	err := removeRunHelpers(&w, f, "run1")
	if err == nil {
		t.Fatal("a helper still listed after a failed rm -f was not reported")
	}
	if !strings.Contains(err.Error(), "docker rm -f stuck1") {
		t.Errorf("the leftover is not named with its rm -f line: %v", err)
	}
	if strings.Contains(err.Error(), "vanished1") {
		t.Errorf("the helper that left the listing is named as a leftover: %v", err)
	}
	if !strings.Contains(w.String(), "removed helper container vanished1") {
		t.Errorf("the removal is not reported:\n%s", w.String())
	}
}

// The field-QA case on its own: the ONLY helper's `rm -f` fails because it had
// already vanished. Nothing is left, so nothing is reported as a leftover --
// a phantom one would make backup, reset and forget refuse.
func TestRemoveRunHelpersReportsNoLeftoverWhenTheOnlyFailedHelperVanished(t *testing.T) {
	f := &fakeRunner{
		allContainers: map[string][]string{helperRunLabel("run1"): {"vanished1"}},
		forceRmGone:   map[string]bool{"vanished1": true},
	}
	var w bytes.Buffer
	if err := removeRunHelpers(&w, f, "run1"); err != nil {
		t.Fatalf("a helper that left the listing was reported as a leftover: %v", err)
	}
	if !strings.Contains(w.String(), "removed helper container vanished1") {
		t.Errorf("the removal is not reported:\n%s", w.String())
	}
}

// stallCleaner is an engine whose `rm -f` always fails and whose every
// watching listing stalls for the whole deadline it is given and then still
// shows the helper: a daemon that stopped answering mid-cleanup.
type stallCleaner struct {
	mu     sync.Mutex
	limits []time.Duration
}

func (s *stallCleaner) Engine() runner.Engine { return runner.Docker }

func (s *stallCleaner) ContainersByLabelBounded(string) ([]string, error) {
	return []string{"stuck1"}, nil
}

func (s *stallCleaner) ContainersByLabelWithin(d time.Duration, _ string) ([]string, error) {
	s.mu.Lock()
	s.limits = append(s.limits, d)
	s.mu.Unlock()
	time.Sleep(d)
	return []string{"stuck1"}, nil
}

func (s *stallCleaner) ContainerForceRemove(string) error        { return errors.New("engine gone") }
func (s *stallCleaner) VolumeExistsBounded(string) (bool, error) { return false, nil }
func (s *stallCleaner) VolumeRemoveBounded(string) error         { return nil }

// Watching a failed removal stays inside helperGoneWait on a stalled engine:
// each listing is capped at what is left of the window, never the engine
// cleanup deadline, and none starts once the window has run out.
func TestRemoveRunHelpersWatchStaysInsideItsWindowOnAStalledEngine(t *testing.T) {
	s := &stallCleaner{}
	start := time.Now()
	if err := removeRunHelpers(io.Discard, s, "run1"); err == nil {
		t.Fatal("a helper the stalled engine still lists was not reported")
	}
	if took := time.Since(start); took > helperGoneWait+time.Second {
		t.Errorf("the watch took %s, past its %s window", took, helperGoneWait)
	}
	if len(s.limits) != 1 {
		t.Errorf("listings started = %d, want 1 (none once the window ran out): %v", len(s.limits), s.limits)
	}
	for _, d := range s.limits {
		if d > helperGoneWait {
			t.Errorf("a listing was given %s, more than the %s window", d, helperGoneWait)
		}
	}
}

// The first interrupt releases the signal BEFORE it waits for an exclusive step
// to finish, so a second Ctrl-C kills byre outright even while enrolment is
// stalled; the first one's own action still waits for the step.
func TestRunInterruptsReleaseTheSignalBeforeWaitingOnAnExclusiveStep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	released := make(chan struct{})
	ri := &runInterrupts{ch: make(chan os.Signal, 1), done: make(chan struct{}), cancel: cancel,
		unnotify: func() { close(released) }}
	ri.exclusive(func() {
		go ri.fire()
		select {
		case <-released:
		case <-time.After(5 * time.Second):
			t.Fatal("the interrupt did not release the signal while the exclusive step held the handler off")
		}
		if ctx.Err() != nil {
			t.Error("the interrupt acted inside the exclusive step")
		}
	})
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupt never acted after the exclusive step finished")
	}
}
