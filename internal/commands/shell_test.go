package commands

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// callerUID used across shell tests: the container's BYRE_UID matches it, so the
// accident guard treats the box as the caller's (the normal single-user case).
const testCallerUID = 1000

func TestShellNoSessionAnywhere(t *testing.T) {
	_, proj := testPaths(t)
	err := shell(discardStreams(), proj, []sessionRunner{&fakeRunner{}, &fakeRunner{}}, testCallerUID, false)
	if err == nil || !strings.Contains(err.Error(), "byre develop") {
		t.Fatalf("expected 'no session' error pointing at develop, got %v", err)
	}
}

func TestShellNoEnginesInstalled(t *testing.T) {
	_, proj := testPaths(t)
	if err := shell(discardStreams(), proj, nil, testCallerUID, false); err == nil {
		t.Fatal("expected an error with no engines installed")
	}
}

func TestShellQueryErrorNotMaskedAsNothingRunning(t *testing.T) {
	_, proj := testPaths(t)
	broken := &fakeRunner{liveErr: errors.New("daemon down")}
	err := shell(discardStreams(), proj, []sessionRunner{broken}, testCallerUID, false)
	if err == nil || !strings.Contains(err.Error(), "daemon down") {
		t.Fatalf("a broken engine must surface, not read as 'nothing running': %v", err)
	}
}

func TestShellExecsAsContainerDevUser(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456"),
		env:  map[string]string{"BYRE_UID": "1234", "BYRE_GID": "5678"},
	}
	// The session lives in the SECOND engine; the first is installed but idle —
	// shell must keep probing rather than stop at the first engine. The caller's
	// uid matches the box's BYRE_UID, so it's not foreign.
	if err := shell(discardStreams(), proj, []sessionRunner{&fakeRunner{}, holder}, 1234, false); err != nil {
		t.Fatal(err)
	}
	want := "abc123def456 1234:5678 /workspace bash -l"
	if len(holder.execs) != 1 || holder.execs[0] != want {
		t.Fatalf("exec = %v, want %q", holder.execs, want)
	}
}

// The accident guard: on a rootful daemon a box owned by ANOTHER Unix user
// (BYRE_UID != caller) is hidden, and shell refuses rather than enter it as that
// user. --skip-uid-check enters it anyway. Rootless podman is caller-scoped, so
// the filter doesn't run there.
func TestShellHidesForeignUIDSession(t *testing.T) {
	p, proj := testPaths(t)
	foreign := func() *fakeRunner {
		return &fakeRunner{
			live: liveWorkdir(p, "foreignbox01"),
			env:  map[string]string{"BYRE_UID": "2222", "BYRE_GID": "2222"},
		}
	}

	t.Run("rootful foreign box is refused, not entered", func(t *testing.T) {
		h := foreign()
		err := shell(discardStreams(), proj, []sessionRunner{h}, testCallerUID, false)
		if err == nil || !strings.Contains(err.Error(), "another user") {
			t.Fatalf("expected a foreign-session refusal, got %v", err)
		}
		if len(h.execs) != 0 {
			t.Fatal("must not exec into a foreign box")
		}
	})

	t.Run("--skip-uid-check enters the foreign box", func(t *testing.T) {
		h := foreign()
		if err := shell(discardStreams(), proj, []sessionRunner{h}, testCallerUID, true); err != nil {
			t.Fatalf("skip-uid-check should enter the foreign box: %v", err)
		}
		if len(h.execs) != 1 {
			t.Fatalf("expected the box entered under skip-uid-check, got %v", h.execs)
		}
	})

	t.Run("rootless podman is caller-scoped, filter does not run", func(t *testing.T) {
		h := foreign()
		h.rootless = true
		if err := shell(discardStreams(), proj, []sessionRunner{h}, testCallerUID, false); err != nil {
			t.Fatalf("rootless podman box must be enterable without the uid filter: %v", err)
		}
		if len(h.execs) != 1 {
			t.Fatalf("expected the rootless box entered, got %v", h.execs)
		}
	})

	t.Run("undetermined rootless mode hides fail-closed but says so", func(t *testing.T) {
		// The probe failing must not silently assert "another user's identity":
		// on rootless podman the filter can only false-hide the caller's own
		// keep-id box, so the refusal names the undetermined mode and the
		// probe's error alongside the --skip-uid-check remedy.
		h := foreign()
		h.rootlessErr = errors.New("info query boom")
		err := shell(discardStreams(), proj, []sessionRunner{h}, testCallerUID, false)
		if err == nil || !strings.Contains(err.Error(), "rootless podman") ||
			!strings.Contains(err.Error(), "info query boom") ||
			!strings.Contains(err.Error(), "--skip-uid-check") {
			t.Fatalf("expected an undetermined-mode refusal naming the probe error and remedy, got %v", err)
		}
		if len(h.execs) != 0 {
			t.Fatal("must not exec into the box while the mode is undetermined")
		}
	})

	t.Run("caller's own box beats a foreign box on an earlier engine", func(t *testing.T) {
		mine := &fakeRunner{
			live: liveWorkdir(p, "mybox000000"),
			env:  map[string]string{"BYRE_UID": strconv.Itoa(testCallerUID), "BYRE_GID": "1000"},
		}
		// Foreign engine first, own engine second — must keep scanning past the
		// foreign box and land on the caller's own.
		if err := shell(discardStreams(), proj, []sessionRunner{foreign(), mine}, testCallerUID, false); err != nil {
			t.Fatal(err)
		}
		if len(mine.execs) != 1 {
			t.Fatalf("expected the caller's own box entered, got %v", mine.execs)
		}
	})
}

// The "N containers match" disclosure counts only ENTERABLE boxes: a foreign
// box the identity check just filtered out must not inflate the count — one
// enterable candidate is not ambiguous, however many were hidden.
func TestShellMultiMatchCountExcludesFiltered(t *testing.T) {
	p, proj := testPaths(t)
	h := &fakeRunner{
		live: liveWorkdir(p, "foreignbox01", "minebox00001"),
		envByID: map[string]map[string]string{
			"foreignbox01": {"BYRE_UID": "2222", "BYRE_GID": "2222"},
			"minebox00001": {"BYRE_UID": strconv.Itoa(testCallerUID), "BYRE_GID": "1000"},
		},
	}
	s, _, stderr := testStreams("", false)
	if err := shell(s, proj, []sessionRunner{h}, testCallerUID, false); err != nil {
		t.Fatal(err)
	}
	if len(h.execs) != 1 || !strings.Contains(h.execs[0], "minebox00001") {
		t.Fatalf("expected the caller's box entered: %v", h.execs)
	}
	if strings.Contains(stderr.String(), "containers match") {
		t.Errorf("one enterable box is not ambiguous; no multi-match line expected:\n%s", stderr.String())
	}

	// Two enterable boxes ARE ambiguous: disclose with the enterable count,
	// still excluding the hidden one.
	h2 := &fakeRunner{
		live: liveWorkdir(p, "mine00000001", "mine00000002", "foreignbox01"),
		envByID: map[string]map[string]string{
			"foreignbox01": {"BYRE_UID": "2222", "BYRE_GID": "2222"},
		},
		env: map[string]string{"BYRE_UID": strconv.Itoa(testCallerUID), "BYRE_GID": "1000"},
	}
	s2, _, stderr2 := testStreams("", false)
	if err := shell(s2, proj, []sessionRunner{h2}, testCallerUID, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr2.String(), "2 containers match") {
		t.Errorf("expected the enterable count (2) disclosed:\n%s", stderr2.String())
	}
}

// A box with a valid UID but an unreadable GID is "unreadable" too — it must be
// skipped (not selected-then-failed), so it can't shadow the caller's valid box
// on another engine.
func TestShellBadGIDIsUnreadable(t *testing.T) {
	p, proj := testPaths(t)
	badGID := &fakeRunner{
		live: liveWorkdir(p, "badgidbox000"),
		env:  map[string]string{"BYRE_UID": strconv.Itoa(testCallerUID)}, // BYRE_GID missing
	}
	good := &fakeRunner{
		live: liveWorkdir(p, "goodbox00000"),
		env:  map[string]string{"BYRE_UID": strconv.Itoa(testCallerUID), "BYRE_GID": "1000"},
	}
	// Bad-GID box first; it must not shadow the valid one on the second engine.
	if err := shell(discardStreams(), proj, []sessionRunner{badGID, good}, testCallerUID, false); err != nil {
		t.Fatal(err)
	}
	if len(good.execs) != 1 || len(badGID.execs) != 0 {
		t.Fatalf("a bad-GID box must be skipped for the valid one: badGID=%v good=%v", badGID.execs, good.execs)
	}
}

// The shell must pass the container's BYRE_* plumbing through the exec so the
// /etc/profile.d shim's env.d hooks have their inputs (docker-host's
// COMPOSE_PROJECT_NAME reads BYRE_WORKTREE). Non-BYRE_ container env stays out;
// HOME is set to the baked dev home, not inherited.
func TestShellPassesByreEnvThrough(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456"),
		env: map[string]string{
			"BYRE_UID": "1000", "BYRE_GID": "1000",
			"BYRE_WORKTREE": "wt-xyz", "BYRE_PROJECT": "proj",
			"PATH": "/should/not/propagate",
		},
	}
	if err := shell(discardStreams(), proj, []sessionRunner{holder}, testCallerUID, false); err != nil {
		t.Fatal(err)
	}
	if holder.execEnv["BYRE_WORKTREE"] != "wt-xyz" || holder.execEnv["BYRE_PROJECT"] != "proj" {
		t.Fatalf("BYRE_* not passed through: %v", holder.execEnv)
	}
	if holder.execEnv["HOME"] == "" {
		t.Fatalf("HOME must be set to the baked dev home: %v", holder.execEnv)
	}
	if _, ok := holder.execEnv["PATH"]; ok {
		t.Fatalf("non-BYRE_ container env must not propagate: %v", holder.execEnv)
	}
}

func TestShellFailsClosedWithoutContainerUID(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456"),
		env:  map[string]string{}, // no BYRE_UID/BYRE_GID in the container env
	}
	err := shell(discardStreams(), proj, []sessionRunner{holder}, testCallerUID, false)
	if err == nil || !strings.Contains(err.Error(), "BYRE_UID") {
		t.Fatalf("expected fail-closed on missing container identity, got %v", err)
	}
	if len(holder.execs) != 0 {
		t.Fatal("must not exec without a valid dev identity")
	}
}

func TestShellNotesMultipleMatches(t *testing.T) {
	p, proj := testPaths(t)
	holder := &fakeRunner{
		live: liveWorkdir(p, "abc123def456", "0123456789ab"),
		env:  map[string]string{"BYRE_UID": "1000", "BYRE_GID": "1000"},
	}
	s, _, errBuf := testStreams("", false)
	if err := shell(s, proj, []sessionRunner{holder}, testCallerUID, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "2 containers match") {
		t.Errorf("expected a multiple-match note on stderr, got %q", errBuf.String())
	}
}
