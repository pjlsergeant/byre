package commands

// Gated integration coverage for `byre deliver` against a live engine:
// the exec-stream and SSH transports, the TUI picker, and the remote
// loop. Run with BYRE_DOCKER_TESTS=1 (see integration_test.go).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/tuitest"
)

// TestIntegrationDeliverTransport lands real files in a LIVE box's /inbox —
// the transport scripts (inboxCheck, fileClaim) executing through a real
// engine exec, the seam no unit fake can vouch for (deliver_test's fake
// reimplements the claim loop). Pins ADR 0021's promises end to end: the
// box is discovered by label from cwd, the landed path comes back on
// stdout, the bytes arrive exactly, and re-delivering the same name claims
// -2 instead of clobbering.
func TestIntegrationDeliverTransport(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}

	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"sleep", "120"}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	args := runner.RunArgs(params)
	args = append([]string{args[0], "-d"}, args[1:]...)
	if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
		t.Fatalf("box failed to start: %v\n%s", err, out)
	}

	src := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(src, []byte("hello from the host\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Through deliverWith itself — the production wiring (engine adapters,
	// callerScoped probe, uid guard) is part of what this test vouches for.
	// No clipboard, no picker: a single owned session resolves without both.
	deliverOnce := func() string {
		var out, errw strings.Builder
		s := Streams{Out: &out, Err: &errw, In: strings.NewReader("")}
		if _, err := deliverWith(s, proj, deliver.Options{}, deliver.PathSources([]string{src}), []sessionRunner{r}, os.Getuid(), nil, nil); err != nil {
			t.Fatalf("deliver failed: %v\nstderr: %s", err, errw.String())
		}
		return out.String()
	}

	if got := deliverOnce(); got != "/inbox/hello.txt\n" {
		t.Fatalf("landed path = %q, want /inbox/hello.txt", got)
	}
	ids, err := r.RunningContainersByLabel(labelKey + "=" + p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("session discovery = (%v, %v), want exactly one box", ids, err)
	}
	content, err := r.ExecInput(ids[0], ident.UID, ident.GID, nil, "cat", "/inbox/hello.txt")
	if err != nil {
		t.Fatalf("reading the landed file: %v", err)
	}
	if content != "hello from the host\n" {
		t.Fatalf("landed content = %q", content)
	}

	// Same name again: the ln-EEXIST claim loop must uniquify, not clobber.
	if got := deliverOnce(); got != "/inbox/hello-2.txt\n" {
		t.Fatalf("second landed path = %q, want /inbox/hello-2.txt", got)
	}
}

// TestIntegrationDeliverLoopbackSSH is the no-fakes version of the remote
// loop: REAL ssh to this machine's own sshd, the REAL sshExec seam, and a
// byre binary built from this tree answering on the far side — quoting
// through an actual remote shell (the binary's path carries a space on
// purpose), exit codes propagating through an actual sshd, the tar riding an
// actual no-pty channel. What stays untestable here is only the
// other-machine-ness (macOS sshd's sparse PATH, network latency).
//
// It provisions loopback ssh by MUTATING ~/.ssh (an ephemeral key appended
// to authorized_keys, a Host alias appended to config; both restored on
// cleanup), so it gates on BYRE_SSH_LOOP_TESTS=1 on top of the docker gate —
// set by byre-inttest for the sacrificial VM, never by a developer's default
// run on a real machine.
// provisionLoopbackSSH makes `ssh byre-test-loopback` reach this machine's
// own sshd with an ephemeral key: skips without BYRE_SSH_LOOP_TESTS=1 (it
// edits ~/.ssh — sacrificial machines only; every mutation restores the
// exact prior bytes on cleanup), then probes. A second alias,
// byre-test-loopback-down, points at a refused port for 255-path tests.
func provisionLoopbackSSH(t *testing.T) {
	t.Helper()
	if os.Getenv("BYRE_SSH_LOOP_TESTS") != "1" {
		t.Skip("set BYRE_SSH_LOOP_TESTS=1 to run loopback-ssh tests (they edit ~/.ssh — sacrificial machines only)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	key := filepath.Join(tmp, "loop-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	appendRestoring(t, filepath.Join(sshDir, "authorized_keys"), string(pub))
	appendRestoring(t, filepath.Join(sshDir, "config"), fmt.Sprintf(`
Host byre-test-loopback byre-test-loopback-down
  HostName 127.0.0.1
  IdentityFile %s
  IdentitiesOnly yes
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  BatchMode yes
  ConnectTimeout 5
Host byre-test-loopback-down
  Port 9
`, key))
	if out, err := exec.Command("ssh", "byre-test-loopback", "true").CombinedOutput(); err != nil {
		t.Fatalf("loopback ssh probe failed (is sshd running here?): %v\n%s", err, out)
	}
}

// startTestBox builds the project's image and starts a sleep box, with all
// cleanups registered; it returns the running container id.
func startTestBox(t *testing.T, r *runner.Runner, p project.Paths, proj string, ident runner.Identity) string {
	t.Helper()
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}
	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"sleep", "120"}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	args := runner.RunArgs(params)
	args = append([]string{args[0], "-d"}, args[1:]...)
	if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
		t.Fatalf("box failed to start: %v\n%s", err, out)
	}
	ids, err := r.RunningContainersByLabel(labelKey + "=" + p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("session discovery for %s = (%v, %v)", p.ID, ids, err)
	}
	return ids[0]
}

func TestIntegrationDeliverLoopbackSSH(t *testing.T) {
	r := requireEngineRunner(t)
	provisionLoopbackSSH(t)
	tmp := t.TempDir()

	// The remote byre is BUILT from this tree — both ends of the wire run
	// the code under test — and lands in a directory with a space, so the
	// argv the remote shell evaluates actually exercises the quoting.
	binDir := filepath.Join(tmp, "remote bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "byre")
	build := exec.Command("go", "build", "-o", bin, "./cmd/byre")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building byre: %v\n%s", err, out)
	}

	// A live box, exactly as the fake-ssh loop test runs one.
	p, proj := testPaths(t)
	ident := testIdentity(t, r)
	ids := []string{startTestBox(t, r, p, proj, ident)}

	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "bug", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(srcDir, "bug", "notes.txt"), []byte("notes\n"), 0o644)
	mustWriteFile(t, filepath.Join(srcDir, "bug", "sub", "deep.txt"), []byte("deep\n"), 0o644)
	top := filepath.Join(srcDir, "top.txt")
	mustWriteFile(t, top, []byte("top\n"), 0o644)
	sources := deliver.PathSources([]string{filepath.Join(srcDir, "bug"), top})

	target, isSSH, err := deliver.ParseSSHTarget("ssh://byre-test-loopback")
	if err != nil || !isSSH {
		t.Fatalf("target = (%v, %v)", isSSH, err)
	}

	// The full two-leg flow over real ssh: enumeration first (the picker
	// pins OUR box, so a leftover box on the machine can't misroute the
	// test), then the tar leg into it.
	var out, errw strings.Builder
	cfg := deliver.Config{Out: &out, Err: &errw, Pick: func(ss []deliver.Session) (deliver.Session, bool, error) {
		for _, s := range ss {
			if s.ID == ids[0] {
				return s, true, nil
			}
		}
		return deliver.Session{}, false, fmt.Errorf("our box %s missing from the remote list: %+v", ids[0], ss)
	}}
	landed, err := deliver.RunRemote(cfg, deliver.Options{RemoteByre: bin, NoClip: true}, target, sources, sshExec, false)
	if err != nil {
		t.Fatalf("loopback delivery failed: %v\nstderr: %s", err, errw.String())
	}
	if strings.Join(landed, " ") != "/inbox/bug /inbox/top.txt" {
		t.Fatalf("landed = %v\nstderr: %s", landed, errw.String())
	}
	for path, want := range map[string]string{
		"/inbox/bug/notes.txt":    "notes\n",
		"/inbox/bug/sub/deep.txt": "deep\n",
		"/inbox/top.txt":          "top\n",
	} {
		got, err := r.ExecInput(ids[0], ident.UID, ident.GID, nil, "cat", path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	// Exit-code translation through a REAL sshd: a missing remote binary is
	// the shell's 127, and byre must say "PATH", not "exit status".
	_, err = deliver.RunRemote(cfg, deliver.Options{RemoteByre: "/nonexistent/byre", NoClip: true}, target, sources, sshExec, false)
	if err == nil || !strings.Contains(err.Error(), "ssh PATH") {
		t.Fatalf("127 translation: err = %v", err)
	}

	// And ssh's own 255 (a refused port) must blame the transport.
	down, _, err := deliver.ParseSSHTarget("ssh://byre-test-loopback-down")
	if err != nil {
		t.Fatal(err)
	}
	_, err = deliver.RunRemote(cfg, deliver.Options{RemoteByre: bin, NoClip: true}, down, sources, sshExec, false)
	if err == nil || !strings.Contains(err.Error(), "ssh to") {
		t.Fatalf("255 translation: err = %v", err)
	}
}

// TestIntegrationTUIPickerDeliver drives the real binary's TTY picker in a
// tmux pane against two live boxes: `byre deliver <file>` from a neutral
// cwd finds both, the picker renders, the test steers the cursor to the
// second project's row, and the delivery lands where the pick said. (TUI
// tier of the harness design; lives HERE because everything sharing docker
// or loopback-ssh state stays in this one serial test binary — the
// serialization rule the design records.)
func TestIntegrationTUIPickerDeliver(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	ident := testIdentity(t, r)
	p1, proj1 := testPaths(t)
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	id1 := startTestBox(t, r, p1, proj1, ident)
	id2 := startTestBox(t, r, p2, proj2, ident)

	src := filepath.Join(t.TempDir(), "picked.txt")
	if err := os.WriteFile(src, []byte("picked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := tuitest.Start(t, tuitest.Opts{}, tuitest.Binary(t), "deliver", src)
	s.WaitFor("deliver to which box?")
	steerPickTo(t, s, p2.ID)
	s.Keys("Enter")
	s.WaitFor("/inbox/picked.txt")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}

	got, err := r.ExecInput(id2, ident.UID, ident.GID, nil, "cat", "/inbox/picked.txt")
	if err != nil || got != "picked\n" {
		t.Fatalf("picked box content = (%q, %v)", got, err)
	}
	assertAbsentInBox(t, r, ident, id1, "/inbox/picked.txt")
}

// assertAbsentInBox proves a path is NOT in a box — and that the inspection
// itself succeeded: an engine error must never masquerade as absence.
func assertAbsentInBox(t *testing.T, r *runner.Runner, ident runner.Identity, id, path string) {
	t.Helper()
	if _, err := r.ExecInput(id, ident.UID, ident.GID, nil, "test", "!", "-e", path); err != nil {
		t.Fatalf("%s should not exist in %s (or the inspection failed): %v", path, id, err)
	}
}

// steerPickTo moves the picker's highlight onto the row containing target.
// After each Down it waits for the highlight to actually MOVE before reading
// it again — sampling a stale frame would double-step past the target. Row
// order isn't promised, and a leftover box on the machine must not break
// the pick.
func steerPickTo(t *testing.T, s *tuitest.Session, target string) {
	t.Helper()
	highlighted := func() string {
		for _, l := range strings.Split(s.CaptureNow(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "> ") {
				return l
			}
		}
		return ""
	}
	row := highlighted()
	for i := 0; i < 10 && !strings.Contains(row, target); i++ {
		s.Keys("Down")
		moved := row
		for j := 0; j < 40 && moved == row; j++ {
			time.Sleep(50 * time.Millisecond)
			moved = highlighted()
		}
		if moved == row {
			break // bottom of the list: the cursor stops moving
		}
		row = moved
	}
	if !strings.Contains(row, target) {
		t.Fatalf("never reached %s's row:\n%s", target, s.CaptureNow())
	}
}

// TestIntegrationTUIPickerOnDevTTY pins ssh's contract, adopted for byre's
// prompts (ADR 0038's resolved question): stdin carries the payload (a
// pipe), and the picker still appears — read from /dev/tty, rendered to
// stderr — while the piped bytes become the delivery.
func TestIntegrationTUIPickerOnDevTTY(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	ident := testIdentity(t, r)
	p1, proj1 := testPaths(t)
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	id1 := startTestBox(t, r, p1, proj1, ident)
	id2 := startTestBox(t, r, p2, proj2, ident)

	bin := tuitest.Binary(t)
	s := tuitest.Start(t, tuitest.Opts{}, "sh", "-c",
		fmt.Sprintf("printf 'hello from a pipe' | '%s' deliver - --name piped.txt", bin))
	s.WaitFor("deliver to which box?")
	steerPickTo(t, s, p2.ID)
	s.Keys("Enter")
	s.WaitFor("/inbox/piped.txt")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	got, err := r.ExecInput(id2, ident.UID, ident.GID, nil, "cat", "/inbox/piped.txt")
	if err != nil || got != "hello from a pipe" {
		t.Fatalf("picked box content = (%q, %v)", got, err)
	}
	assertAbsentInBox(t, r, ident, id1, "/inbox/piped.txt")
}

// TestIntegrationTUIPickerCancel pins the picker's other exit: q abandons
// the delivery cleanly — the cancel notice, exit 0, and nothing lands
// anywhere.
func TestIntegrationTUIPickerCancel(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	ident := testIdentity(t, r)
	p1, proj1 := testPaths(t)
	proj2 := t.TempDir()
	p2, err := project.Resolve(proj2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	id1 := startTestBox(t, r, p1, proj1, ident)
	id2 := startTestBox(t, r, p2, proj2, ident)

	src := filepath.Join(t.TempDir(), "unwanted.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := tuitest.Start(t, tuitest.Opts{}, tuitest.Binary(t), "deliver", src)
	s.WaitFor("deliver to which box?")
	s.Keys("q")
	s.WaitFor("cancelled — nothing delivered")
	// Cancel exits 1 (ruling 2026-07-17, field-QA finding 3): every
	// nothing-was-delivered outcome is nonzero — a script wrapping deliver
	// must be able to trust rc=0 to mean bytes landed. The friendly stderr
	// line above stays the human disambiguation.
	if st := s.WaitForExit(); st != 1 {
		t.Fatalf("cancel should exit 1 (nothing delivered), got %d\n%s", st, s.CaptureNow())
	}
	for _, id := range []string{id1, id2} {
		assertAbsentInBox(t, r, ident, id, "/inbox/unwanted.txt")
	}
}

// TestIntegrationTUIMeterFinalState delivers a >256 KiB payload over real
// loopback ssh with a TTY, and asserts the FINAL terminal state only: the
// meter resolved to a sent-total, the remote's notes sit on their own
// lines, the landed path printed, exit 0. Mid-transfer meter observation is
// deliberately not claimed (design: it races an unthrottled loopback
// transfer; the guard's byte ordering stays pinned by the unit tests).
func TestIntegrationTUIMeterFinalState(t *testing.T) {
	r := requireEngineRunner(t)
	tuitest.Require(t)
	provisionLoopbackSSH(t)
	ident := testIdentity(t, r)
	p, proj := testPaths(t)
	id := startTestBox(t, r, p, proj, ident)

	big := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := tuitest.Binary(t)
	// --box pins the target (enumeration is tier 1's claim; a leftover box
	// must not turn the sole-box auto-pick into a picker here).
	s := tuitest.Start(t, tuitest.Opts{}, bin,
		"deliver", "ssh://byre-test-loopback", big, "--remote-byre", bin, "--box", id)
	s.WaitFor("/inbox/big.bin")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	final := s.CaptureNow()
	if !strings.Contains(final, "byre: sent ") {
		t.Fatalf("meter never resolved to a sent-total:\n%s", final)
	}
	if !strings.Contains(final, "byre: delivered 1 file") {
		t.Fatalf("remote note missing from the final terminal:\n%s", final)
	}

	got, err := r.ExecInput(id, ident.UID, ident.GID, nil, "sh", "-c", "wc -c < /inbox/big.bin")
	if err != nil || strings.TrimSpace(got) != "1048576" {
		t.Fatalf("delivered size = (%q, %v)", got, err)
	}
}

// appendRestoring appends text to path (creating it 0600 if absent) and
// restores the exact prior state — original bytes, or absence — on cleanup.
func appendRestoring(t *testing.T, path, text string) {
	t.Helper()
	orig, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	next := append([]byte{}, orig...)
	// A last line without its newline (legal in authorized_keys) must not
	// concatenate with the appended text into one invalid entry.
	if len(next) > 0 && next[len(next)-1] != '\n' {
		next = append(next, '\n')
	}
	next = append(next, []byte(text)...)
	if err := os.WriteFile(path, next, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// This restores SHARED credential state (~/.ssh): a failed restore
		// is a loud test failure, never a silent leftover authorization.
		if existed {
			if err := os.WriteFile(path, orig, 0o600); err != nil {
				t.Errorf("restoring %s: %v", path, err)
			}
		} else if err := os.Remove(path); err != nil {
			t.Errorf("removing %s: %v", path, err)
		}
	})
}

// TestIntegrationDeliverRemoteLoop runs remote delivery end to end with the
// ssh binary as the ONLY fake: deliver.RunRemote packs real files through
// the production planner, the "ssh" hop hands the stream straight to
// commands.Deliver in tar mode (dispatch, --proto handshake, deliverConfig
// wiring), and the archive unpacks through the REAL transport scripts into
// a live box — claims, interior mkdirs, uniquify and all (ADR 0037).
func TestIntegrationDeliverRemoteLoop(t *testing.T) {
	r := requireEngineRunner(t)
	p, proj := testPaths(t)
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	ident := testIdentity(t, r)
	image := imageTag(p.ID, ident.UID, ident.GID)
	t.Cleanup(func() { _ = r.ImageRemove(image) })
	if err := buildImage(r, p, rv.cfg, rv.skills, image, false, ident); err != nil {
		t.Fatalf("image failed to build: %v", err)
	}
	params, err := runParams(p, rv, image, false, false, ident)
	if err != nil {
		t.Fatal(err)
	}
	params.Command = []string{"sleep", "120"}
	t.Cleanup(func() {
		_ = exec.Command(string(r.Engine()), "rm", "-f", params.Name).Run()
		for _, v := range params.Volumes {
			_ = r.VolumeRemove(v.Name)
		}
	})
	args := runner.RunArgs(params)
	args = append([]string{args[0], "-d"}, args[1:]...)
	if out, err := exec.Command(string(r.Engine()), args...).CombinedOutput(); err != nil {
		t.Fatalf("box failed to start: %v\n%s", err, out)
	}

	// The enumeration leg against the live engine: one row, protocol-shaped,
	// naming this box.
	var listOut, listErr strings.Builder
	ls := Streams{Out: &listOut, Err: &listErr, In: strings.NewReader("")}
	lcfg, err := deliverConfig(ls, proj, []sessionRunner{r}, os.Getuid(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := deliver.Boxes(lcfg, deliver.Options{})
	if err != nil || partial {
		t.Fatalf("Boxes = partial %v, err %v\nstderr: %s", partial, err, listErr.String())
	}
	rows, err := deliver.ParseBoxes(listOut.String())
	if err != nil {
		t.Fatalf("the live listing broke its own grammar: %v\n%s", err, listOut.String())
	}
	// The claim is scoped to THIS project's row: the inttest VM is shared, so
	// a concurrent suite's boxes may appear in the machine-wide listing and a
	// "rows == exactly one" assertion races them (seen live 2026-07-18).
	var mine []deliver.RemoteBox
	for _, row := range rows {
		if row.Project == p.ID {
			mine = append(mine, row)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("rows for project %s = %+v (full listing %+v), want exactly one", p.ID, mine, rows)
	}
	box := mine[0]

	// Sources: a tree and a top-level file.
	srcDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "bug", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(srcDir, "bug", "notes.txt"), []byte("notes\n"), 0o644)
	mustWriteFile(t, filepath.Join(srcDir, "bug", "sub", "deep.txt"), []byte("deep\n"), 0o644)
	top := filepath.Join(srcDir, "top.txt")
	mustWriteFile(t, top, []byte("top\n"), 0o644)

	// The ssh hop: assert the frozen argv shape, then run the remote side
	// for real — commands.Deliver dispatching tar mode into the live box.
	sshLoop := func(tgt deliver.SSHTarget, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
		want := []string{"byre", "deliver", "--proto", "1", "--box", box.ID, "--no-clip", "--tar", "-"}
		if strings.Join(argv, " ") != strings.Join(want, " ") {
			t.Fatalf("remote argv = %v, want %v", argv, want)
		}
		s2 := Streams{Out: stdout, Err: stderr, In: stdin}
		return Deliver(s2, proj, deliver.Options{Tar: true, Proto: 1, Box: box.ID, NoClip: true}, []string{"-"})
	}
	deliverRemoteOnce := func() []string {
		var out, errw strings.Builder
		cfg := deliver.Config{Out: &out, Err: &errw}
		landed, err := deliver.RunRemote(cfg, deliver.Options{Box: box.ID, NoClip: true},
			deliver.SSHTarget{Host: "loop"}, deliver.PathSources([]string{filepath.Join(srcDir, "bug"), top}), sshLoop, false)
		if err != nil {
			t.Fatalf("remote delivery failed: %v\nstderr: %s", err, errw.String())
		}
		return landed
	}

	landed := deliverRemoteOnce()
	if strings.Join(landed, " ") != "/inbox/bug /inbox/top.txt" {
		t.Fatalf("landed = %v", landed)
	}
	ids, err := r.RunningContainersByLabel(labelKey + "=" + p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("session discovery = (%v, %v)", ids, err)
	}
	for path, want := range map[string]string{
		"/inbox/bug/notes.txt":    "notes\n",
		"/inbox/bug/sub/deep.txt": "deep\n",
		"/inbox/top.txt":          "top\n",
	} {
		got, err := r.ExecInput(ids[0], ident.UID, ident.GID, nil, "cat", path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	// Again: every top-level name claims -2, nothing clobbers.
	if again := deliverRemoteOnce(); strings.Join(again, " ") != "/inbox/bug-2 /inbox/top-2.txt" {
		t.Fatalf("second landed = %v", again)
	}
}
