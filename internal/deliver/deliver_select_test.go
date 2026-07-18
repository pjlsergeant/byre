package deliver

import (
	"fmt"
	"strings"
	"testing"
)

// Session selection: which box a delivery lands in — auto-pick, --box
// prefixes, the UID filter, engine-pool degradation, and the picker.
// The delivery mechanics themselves live in deliver_test.go.

func TestSoleSessionAutoPick(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng)
	src := writeFile(t, "report.pdf", "content")
	landed, err := Run(cfg, Options{}, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/report.pdf" {
		t.Fatalf("landed = %v", landed)
	}
	if got := out.String(); got != "/inbox/report.pdf\n" {
		t.Fatalf("stdout = %q", got)
	}
	if !strings.Contains(errw.String(), "delivering to proj-aaa (docker, aaa)") {
		t.Fatalf("no target line in stderr: %q", errw.String())
	}
	if len(eng.streams) != 1 || eng.streams[0] != "aaa report.pdf<-content" {
		t.Fatalf("streams = %v", eng.streams)
	}
}

// A worktree box shares its project's id; the target line must name it by
// its own workdir id or main-tree and worktree deliveries are
// indistinguishable except by container id (QA pass-2 finding).
func TestDeliveryLineNamesWorktreeBox(t *testing.T) {
	eng := box("docker", "aaa")
	eng.labels["aaa"]["byre.workdir"] = "proj-wt1-aaa"
	cfg, _, errw := testConfig(eng)
	src := writeFile(t, "report.pdf", "content")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "delivering to proj-wt1-aaa (docker, aaa)") {
		t.Fatalf("worktree box not named by workdir id: %q", errw.String())
	}
}

func TestUIDFilterHidesForeign(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	eng.env["bbb"]["BYRE_UID"] = "777"
	cfg, _, _ := testConfig(eng)
	src := writeFile(t, "f", "x")
	// aaa is ours, bbb is foreign: sole-owned auto-pick should choose aaa.
	landed, err := Run(cfg, Options{}, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 {
		t.Fatalf("landed = %v", landed)
	}
}

// A caller-scoped engine (rootless Podman) never marks a session foreign:
// per-user storage means everything visible is the caller's, and a keep-id
// box's BYRE_UID is the generic in-container uid (1000 ≠ caller 501 here) —
// comparing it would hide the caller's own box (ADR 0032).
func TestCallerScopedEngineSkipsUIDFilter(t *testing.T) {
	eng := box("podman", "aaa")
	eng.callerScoped = true
	eng.env["aaa"]["BYRE_UID"] = "1000"
	eng.env["aaa"]["BYRE_GID"] = "1000"
	cfg, _, _ := testConfig(eng)
	src := writeFile(t, "f", "x")
	landed, err := Run(cfg, Options{}, []string{src})
	if err != nil {
		t.Fatalf("caller-scoped engine's keep-id box must be deliverable: %v", err)
	}
	if len(landed) != 1 {
		t.Fatalf("landed = %v", landed)
	}
	// The exec must run as the box's recorded in-container identity.
	if len(eng.execArgs) == 0 {
		t.Fatal("no exec recorded")
	}
}

func TestUIDFilterZeroOwnedNamesHiddenCount(t *testing.T) {
	eng := box("docker", "bbb")
	eng.env["bbb"]["BYRE_UID"] = "777"
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{}, []string{"whatever"})
	if err == nil || !strings.Contains(err.Error(), "1 hidden; --skip-uid-check") {
		t.Fatalf("err = %v", err)
	}
}

func TestSkipUIDCheckIncludesForeignAndSaysSo(t *testing.T) {
	eng := box("docker", "bbb")
	eng.env["bbb"]["BYRE_UID"] = "777"
	cfg, _, errw := testConfig(eng)
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{SkipUIDCheck: true}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "owned by uid 777, not you") {
		t.Fatalf("no foreign note: %q", errw.String())
	}
}

func TestAmbiguityListsSessions(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{}, []string{"whatever"})
	if err == nil || !strings.Contains(err.Error(), "2 boxes are running") ||
		!strings.Contains(err.Error(), "proj-aaa") || !strings.Contains(err.Error(), "proj-bbb") {
		t.Fatalf("err = %v", err)
	}
}

func TestBoxSelectsByPrefix(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	cfg, out, _ := testConfig(eng)
	src := writeFile(t, "f.txt", "x")
	if _, err := Run(cfg, Options{Box: "proj-b"}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/inbox/f.txt") {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(eng.streams) != 1 || !strings.HasPrefix(eng.streams[0], "bbb ") {
		t.Fatalf("streams = %v", eng.streams)
	}
}

func TestBoxAmbiguousPrefixErrors(t *testing.T) {
	eng := box("docker", "aaa", "abc")
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{Box: "proj-a"}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v", err)
	}
}

func TestBoxNoMatchErrors(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{Box: "nope"}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), `no running box matches --box "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestPartialPoolDisablesAutoPick(t *testing.T) {
	broken := &fakeEngine{name: "podman", idsErr: fmt.Errorf("permission denied on the socket")}
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng, broken)
	_, err := Run(cfg, Options{}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "engine query failed") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(errw.String(), "podman query failed") {
		t.Fatalf("no loud degrade: %q", errw.String())
	}
}

func TestPartialPoolBoxStillWorks(t *testing.T) {
	broken := &fakeEngine{name: "podman", idsErr: fmt.Errorf("permission denied on the socket")}
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng, broken)
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{Box: "proj-aaa"}, []string{src}); err != nil {
		t.Fatal(err)
	}
}

func TestEngineUnionAndAffinity(t *testing.T) {
	d := box("docker", "aaa")
	p := box("podman", "zzz")
	cfg, _, _ := testConfig(d, p)
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{Box: "proj-zzz"}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if len(p.streams) != 1 || len(d.streams) != 0 {
		t.Fatalf("exec went to the wrong engine: docker=%v podman=%v", d.streams, p.streams)
	}
}

func TestCwdAncestorWalkMatches(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	cfg, _, _ := testConfig(eng)
	cfg.Cwd = "/project/src/deep"
	cfg.WorkdirIDOf = func(dir string) (string, error) {
		if dir == "/project" {
			return "proj-bbb", nil
		}
		return "no-match-" + dir, nil
	}
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if len(eng.streams) != 1 || !strings.HasPrefix(eng.streams[0], "bbb ") {
		t.Fatalf("streams = %v", eng.streams)
	}
}

// A collided project id must ABORT selection, not skip the level: with the
// collided box as the machine's sole session, a skipped level falls through
// to the sole-session fallback and delivers into the OTHER project — the
// confused-deputy write the recorded-path check exists to prevent.
func TestCollisionAbortsSelectionBeforeFallbacks(t *testing.T) {
	eng := box("docker", "aaa") // the colliding project's box: the sole session
	cfg, _, _ := testConfig(eng)
	cfg.Cwd = "/project/b/sub"
	cfg.WorkdirIDOf = func(dir string) (string, error) {
		if dir == "/project/b" {
			return "", fmt.Errorf("project id proj-aaa collision: recorded path %q != current %q", "/project/a", "/project/b")
		}
		return "", fmt.Errorf("%w: not a project", ErrNoWorkdirID)
	}
	src := writeFile(t, "f", "x")
	_, err := Run(cfg, Options{}, []string{src})
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("err = %v, want the collision refusal", err)
	}
	if len(eng.streams) != 0 {
		t.Fatalf("ExecInput ran despite the collision: %v", eng.streams)
	}
}

// The sentinel keeps its meaning: ErrNoWorkdirID levels are skipped and the
// sole-session fallback still applies from a neutral directory.
func TestNoWorkdirIDKeepsWalkingToFallback(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	cfg.Cwd = "/somewhere/neutral"
	cfg.WorkdirIDOf = func(dir string) (string, error) {
		return "", fmt.Errorf("%w: not a project", ErrNoWorkdirID)
	}
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if len(eng.streams) != 1 {
		t.Fatalf("sole-session fallback should have delivered: %v", eng.streams)
	}
}

func TestZeroSessions(t *testing.T) {
	eng := box("docker")
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "no running byre boxes") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoValidIdentitySkipped(t *testing.T) {
	eng := box("docker", "aaa")
	eng.env["aaa"] = map[string]string{} // no BYRE_UID/GID
	cfg, _, errw := testConfig(eng)
	_, err := Run(cfg, Options{}, []string{"x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(errw.String(), "no valid BYRE_UID/BYRE_GID") {
		t.Fatalf("no fail-closed warning: %q", errw.String())
	}
}

func TestPickerResolvesAmbiguity(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	cfg, _, _ := testConfig(eng)
	cfg.Pick = func(sessions []Session) (Session, bool, error) {
		if len(sessions) != 2 {
			t.Fatalf("picker got %d sessions", len(sessions))
		}
		return sessions[1], true, nil
	}
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if len(eng.streams) != 1 || !strings.HasPrefix(eng.streams[0], "bbb ") {
		t.Fatalf("streams = %v", eng.streams)
	}
}

func TestPickerCancelIsClean(t *testing.T) {
	eng := box("docker", "aaa", "bbb")
	cfg, out, _ := testConfig(eng)
	cfg.Pick = func([]Session) (Session, bool, error) { return Session{}, false, nil }
	_, err := Run(cfg, Options{}, []string{"whatever"})
	if !IsCancelled(err) {
		t.Fatalf("err = %v, want the cancelled marker", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must stay empty on cancel: %q", out.String())
	}
}

func TestPickerNotConsultedWhenUnambiguous(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	cfg.Pick = func([]Session) (Session, bool, error) {
		t.Fatal("picker consulted for a sole session")
		return Session{}, false, nil
	}
	src := writeFile(t, "f", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadableIdentityNotBlamedOnUIDFilter(t *testing.T) {
	// Review finding: a session whose env can't be read must NOT be counted
	// as "hidden; --skip-uid-check to include" — that flag can't reveal it.
	eng := box("docker", "aaa")
	eng.envErr = fmt.Errorf("inspect broke")
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{}, []string{"x"})
	if err == nil || strings.Contains(err.Error(), "skip-uid-check") {
		t.Fatalf("err = %v (must not prescribe --skip-uid-check)", err)
	}
	if !strings.Contains(err.Error(), "readable dev identity") {
		t.Fatalf("err = %v", err)
	}
}

func TestBoxNoMatchNamesUnusableSessions(t *testing.T) {
	// Round-2 residual: --box aimed at a box whose identity is unreadable
	// must not claim nothing matched without mentioning the unusable session.
	eng := box("docker", "aaa")
	eng.envErr = fmt.Errorf("inspect broke")
	cfg, _, _ := testConfig(eng)
	_, err := Run(cfg, Options{Box: "proj-aaa"}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "readable dev identity") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnreachableEngineDoesNotPoisonAutoPick(t *testing.T) {
	// Field-found 2026-07-10: podman installed but its machine not started is
	// normal life — one quiet line, zero sessions, auto-pick stays alive.
	stale := &fakeEngine{name: "podman", idsErr: fmt.Errorf("exit status 125: Cannot connect to Podman. Please verify your connection ...")}
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng, stale)
	src := writeFile(t, "f.txt", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/inbox/f.txt") {
		t.Fatalf("delivery should proceed: %q", out.String())
	}
	msg := errw.String()
	if !strings.Contains(msg, "podman isn't reachable; skipping it") {
		t.Fatalf("no quiet skip note: %q", msg)
	}
	if strings.Contains(msg, "warning") || strings.Contains(msg, "libpod") {
		t.Fatalf("unreachable engine should be one quiet line: %q", msg)
	}
}

func TestPartialWarningIsOneLine(t *testing.T) {
	multi := &fakeEngine{name: "podman", idsErr: fmt.Errorf("broke badly\nwith a second line\nand a third")}
	eng := box("docker", "aaa", "bbb")
	cfg, _, errw := testConfig(eng, multi)
	_, _ = Run(cfg, Options{Box: "proj-aaa"}, []string{writeFile(t, "f", "x")})
	if strings.Contains(errw.String(), "second line") {
		t.Fatalf("engine essay leaked into the warning: %q", errw.String())
	}
}

func TestPermissionFailureStaysPartial(t *testing.T) {
	// A permission/TLS failure against a possibly-RUNNING daemon must keep
	// the loud partial-pool semantics, not classify as unreachable (codex
	// field-fix review: sessions exist and are merely invisible).
	broken := &fakeEngine{name: "docker", idsErr: fmt.Errorf("permission denied while trying to connect to the Docker daemon socket")}
	eng := box("podman", "aaa")
	cfg, _, errw := testConfig(broken, eng)
	_, err := Run(cfg, Options{}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "engine query failed") {
		t.Fatalf("err = %v (auto-pick must stay disabled)", err)
	}
	if !strings.Contains(errw.String(), "warning") {
		t.Fatalf("no loud warning: %q", errw.String())
	}
}

func TestPermissionDeniedWithDialPhrasingStaysPartial(t *testing.T) {
	// Codex round-2 on the field fixes: a real docker permission error
	// CONTAINS transport phrasing ("dial unix …: connect: permission
	// denied") — the cause must win, keeping the loud partial path.
	broken := &fakeEngine{name: "docker", idsErr: fmt.Errorf(
		"Got permission denied while trying to connect to the Docker daemon socket: Get \"http://...\": dial unix /var/run/docker.sock: connect: permission denied")}
	eng := box("podman", "aaa")
	cfg, _, errw := testConfig(broken, eng)
	_, err := Run(cfg, Options{}, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "engine query failed") {
		t.Fatalf("err = %v (permission failure must not be 'unreachable')", err)
	}
	if !strings.Contains(errw.String(), "warning") {
		t.Fatalf("no loud warning: %q", errw.String())
	}
}

func TestMissingSocketIsUnreachable(t *testing.T) {
	// Pete's actual field error shape: podman socket file absent.
	broken := &fakeEngine{name: "podman", idsErr: fmt.Errorf(
		"exit status 125: unable to connect to Podman socket: Get \"http://d/v5.0.2/libpod/_ping\": dial unix /var/.../podman.sock: connect: no such file or directory")}
	eng := box("docker", "aaa")
	cfg, out, _ := testConfig(eng, broken)
	src := writeFile(t, "f.txt", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/inbox/f.txt") {
		t.Fatalf("auto-pick should survive a missing socket: %q", out.String())
	}
}
