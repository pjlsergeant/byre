package deliver

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func syscallMkfifo(p string) error { return syscall.Mkfifo(p, 0o600) }

// fakeSSH scripts one response per remote invocation, records what was
// invoked, and captures each invocation's stdin (the tar stream).
type fakeSSH struct {
	responses []sshResponse
	calls     []sshCall
}

type sshResponse struct {
	stdout string
	stderr string
	err    error
	// drainStdin controls whether the fake consumes the tar stream (the
	// real ssh always drains; a dead connection doesn't).
	dropStdin bool
}

type sshCall struct {
	target SSHTarget
	argv   []string
	stdin  []byte
}

func (f *fakeSSH) exec(t SSHTarget, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	i := len(f.calls)
	if i >= len(f.responses) {
		return fmt.Errorf("unexpected ssh call #%d: %v", i, argv)
	}
	r := f.responses[i]
	var in []byte
	if !r.dropStdin {
		in, _ = io.ReadAll(stdin)
	}
	f.calls = append(f.calls, sshCall{target: t, argv: argv, stdin: in})
	io.WriteString(stdout, r.stdout)
	io.WriteString(stderr, r.stderr)
	return r.err
}

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func remoteConfig() (Config, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	return Config{Out: &out, Err: &errw}, &out, &errw
}

func TestParseSSHTarget(t *testing.T) {
	cases := []struct {
		in    string
		want  SSHTarget
		isSSH bool
		bad   bool
	}{
		{"ssh://far", SSHTarget{Host: "far"}, true, false},
		{"ssh://dev@far", SSHTarget{User: "dev", Host: "far"}, true, false},
		{"ssh://dev@far:2222", SSHTarget{User: "dev", Host: "far", Port: "2222"}, true, false},
		{"ssh://far/", SSHTarget{Host: "far"}, true, false},
		{"ssh://[2001:db8::1]", SSHTarget{Host: "2001:db8::1"}, true, false},
		{"ssh://dev@[2001:db8::1]:2222", SSHTarget{User: "dev", Host: "2001:db8::1", Port: "2222"}, true, false},
		{"shot.png", SSHTarget{}, false, false},
		{"./ssh://odd", SSHTarget{}, false, false},
		{"ssh://", SSHTarget{}, true, true},
		{"ssh://far/inbox", SSHTarget{}, true, true}, // no paths
		{"ssh://u:pw@far", SSHTarget{}, true, true},  // no passwords
		{"ssh://far?opt=1", SSHTarget{}, true, true}, // no query
	}
	for _, tc := range cases {
		got, isSSH, err := ParseSSHTarget(tc.in)
		if isSSH != tc.isSSH || (err != nil) != tc.bad {
			t.Errorf("%q: isSSH=%v err=%v", tc.in, isSSH, err)
			continue
		}
		if !tc.bad && got != tc.want {
			t.Errorf("%q = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestSSHTargetStringRebracketsIPv6(t *testing.T) {
	cases := []struct {
		t    SSHTarget
		want string
	}{
		{SSHTarget{Host: "far"}, "far"},
		{SSHTarget{User: "dev", Host: "far"}, "dev@far"},
		{SSHTarget{Host: "2001:db8::1"}, "[2001:db8::1]"},
		{SSHTarget{User: "dev", Host: "2001:db8::1"}, "dev@[2001:db8::1]"},
	}
	for _, tc := range cases {
		if got := tc.t.String(); got != tc.want {
			t.Errorf("%+v.String() = %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestRemoteAutoPickSoleBox(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "abc123\tdocker\tproj\tproj\t\n"},
		{stdout: "/inbox/report.pdf\n", stderr: "byre: delivering to proj (docker, abc123)\n"},
	}}
	cfg, out, errw := remoteConfig()
	src := writeTestFile(t, "report.pdf", "content")
	landed, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/report.pdf" {
		t.Fatalf("landed = %v", landed)
	}
	if out.String() != "/inbox/report.pdf\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	// Both legs, in protocol shape.
	if len(ssh.calls) != 2 {
		t.Fatalf("calls = %d", len(ssh.calls))
	}
	wantList := "byre deliver --boxes --proto 1"
	if got := strings.Join(ssh.calls[0].argv, " "); got != wantList {
		t.Fatalf("enumerate argv = %q, want %q", got, wantList)
	}
	wantDeliver := "byre deliver --proto 1 --box abc123 --no-clip --tar -"
	if got := strings.Join(ssh.calls[1].argv, " "); got != wantDeliver {
		t.Fatalf("deliver argv = %q, want %q", got, wantDeliver)
	}
	// The tar stream really carries the file: unpack it against the fake
	// engine and confirm the content round-trips.
	eng := box("docker", "aaa")
	ucfg, _, _ := testConfig(eng)
	if _, err := RunTar(ucfg, Options{}, bytes.NewReader(ssh.calls[1].stdin)); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(eng.streams) != 1 || eng.streams[0] != "aaa report.pdf<-content" {
		t.Fatalf("round trip streams = %v", eng.streams)
	}
	// The remote's notes passed through.
	if !strings.Contains(errw.String(), "delivering to proj") {
		t.Fatalf("remote stderr not forwarded: %q", errw.String())
	}
}

func TestRemoteExplicitBoxSkipsEnumeration(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "/inbox/f.txt\n"},
	}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f.txt", "x")
	_, err := RunRemote(cfg, Options{Box: "abc"}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ssh.calls) != 1 {
		t.Fatalf("one connection expected with --box, got %d", len(ssh.calls))
	}
	if got := strings.Join(ssh.calls[0].argv, " "); !strings.Contains(got, "--box abc") {
		t.Fatalf("argv = %q", got)
	}
}

func TestRemoteSkipUIDCheckRidesBothLegs(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "abc\tdocker\tproj\tproj\tforeign\n"},
		{stdout: "/inbox/f\n"},
	}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	if _, err := RunRemote(cfg, Options{SkipUIDCheck: true}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false); err != nil {
		t.Fatal(err)
	}
	for i, c := range ssh.calls {
		if !strings.Contains(strings.Join(c.argv, " "), "--skip-uid-check") {
			t.Fatalf("leg %d lacks --skip-uid-check: %v", i, c.argv)
		}
	}
}

func TestRemoteZeroBoxes(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{{stdout: ""}}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "no running byre boxes on far") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteManyBoxesUsesPicker(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "aaa\tdocker\tp1\tp1\t\nbbb\tdocker\tp2\tp2\t\n"},
		{stdout: "/inbox/f\n"},
	}}
	cfg, _, _ := remoteConfig()
	var offered []Session
	cfg.Pick = func(sessions []Session) (Session, bool, error) {
		offered = sessions
		return sessions[1], true, nil
	}
	src := writeTestFile(t, "f", "x")
	if _, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false); err != nil {
		t.Fatal(err)
	}
	if len(offered) != 2 || offered[0].ProjectID != "p1" || offered[1].EngineName != "docker" {
		t.Fatalf("picker offered %+v", offered)
	}
	if !strings.Contains(strings.Join(ssh.calls[1].argv, " "), "--box bbb") {
		t.Fatalf("picked box not targeted: %v", ssh.calls[1].argv)
	}
}

func TestRemotePickerCancelIsClean(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "aaa\tdocker\tp1\tp1\t\nbbb\tdocker\tp2\tp2\t\n"},
	}}
	cfg, _, _ := remoteConfig()
	cfg.Pick = func(sessions []Session) (Session, bool, error) { return Session{}, false, nil }
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if !IsCancelled(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteManyBoxesNoPickerListsThem(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "aaa\tdocker\tp1\tp1\t\nbbb\tdocker\tp2\tp2\t\n"},
	}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "2 boxes are running on far") || !strings.Contains(err.Error(), "p2") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemotePartialPoolNeverAutoPicks(t *testing.T) {
	// One box listed, but the remote exited ExitPartialPool: the sole box
	// must NOT be auto-picked; with no picker this is an explicit-pick error.
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "aaa\tdocker\tp1\tp1\t\n", err: &SSHExitError{Code: ExitPartialPool}},
	}}
	cfg, _, errw := remoteConfig()
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "1 box is running on far") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(errw.String(), "engine query failed on far") {
		t.Fatalf("no partial note: %q", errw.String())
	}
}

func TestRemoteOldByre(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{{err: &SSHExitError{Code: 2}}}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteNoByreOnPath(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{{err: &SSHExitError{Code: 127}}}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "--remote-byre") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoteSSHFailure(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{{err: &SSHExitError{Code: 255}, stderr: "ssh: connect refused\n"}}}
	cfg, _, errw := remoteConfig()
	src := writeTestFile(t, "f", "x")
	_, err := RunRemote(cfg, Options{}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "ssh to far failed") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(errw.String(), "connect refused") {
		t.Fatalf("ssh's own message lost: %q", errw.String())
	}
}

func TestRemoteByreOverride(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "aaa\tdocker\tp\tp\t\n"},
		{stdout: "/inbox/f\n"},
	}}
	cfg, _, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	if _, err := RunRemote(cfg, Options{RemoteByre: "/opt/byre"}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false); err != nil {
		t.Fatal(err)
	}
	for i, c := range ssh.calls {
		if c.argv[0] != "/opt/byre" {
			t.Fatalf("leg %d argv[0] = %q", i, c.argv[0])
		}
	}
}

func TestRemotePartialDeliveryKeepsLandedPaths(t *testing.T) {
	// The remote deliver failed midway, but two paths had already landed and
	// printed: they must still print locally; the error still reports.
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "/inbox/a.txt\n/inbox/b.txt\n", err: &SSHExitError{Code: 1}},
	}}
	cfg, out, _ := remoteConfig()
	src := writeTestFile(t, "f", "x")
	landed, err := RunRemote(cfg, Options{Box: "abc"}, SSHTarget{Host: "far"}, PathSources([]string{src}), ssh.exec, false)
	if err == nil {
		t.Fatal("no error for a failed delivery")
	}
	if len(landed) != 2 || out.String() != "/inbox/a.txt\n/inbox/b.txt\n" {
		t.Fatalf("landed = %v, stdout = %q", landed, out.String())
	}
}

func TestPackUnpackRoundTripDirectory(t *testing.T) {
	// The definitive both-ends test: pack a real directory tree locally,
	// unpack it through the fake engine, compare structure and content.
	dir := t.TempDir()
	sub := filepath.Join(dir, "bug", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "bug", "notes.txt"), []byte("n"), 0o644)
	os.WriteFile(filepath.Join(sub, "deep.txt"), []byte("d"), 0o644)
	os.Symlink(filepath.Join(dir, "bug", "notes.txt"), filepath.Join(dir, "bug", "link.txt"))

	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "/inbox/bug\n"},
	}}
	cfg, _, _ := remoteConfig()
	if _, err := RunRemote(cfg, Options{Box: "abc"}, SSHTarget{Host: "far"}, PathSources([]string{filepath.Join(dir, "bug")}), ssh.exec, false); err != nil {
		t.Fatal(err)
	}

	eng := box("docker", "aaa")
	ucfg, _, _ := testConfig(eng)
	landed, err := RunTar(ucfg, Options{}, bytes.NewReader(ssh.calls[0].stdin))
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/bug" {
		t.Fatalf("landed = %v", landed)
	}
	got := strings.Join(eng.streams, "|")
	for _, want := range []string{"bug/link.txt<-n", "bug/notes.txt<-n", "bug/sub/deep.txt<-d"} {
		if !strings.Contains(got, want) {
			t.Fatalf("streams = %v, missing %q", eng.streams, want)
		}
	}
}

func TestPackStdinAndClipboardSources(t *testing.T) {
	ssh := &fakeSSH{responses: []sshResponse{
		{stdout: "/inbox/shot.png\n/inbox/note.txt\n"},
	}}
	cfg, _, _ := remoteConfig()
	sources := []Source{
		{Reader: strings.NewReader("png-bytes"), Name: "shot.png", Kind: "stdin"},
		{Data: []byte("clip"), Name: "note.txt", Kind: "clipboard text"},
	}
	if _, err := RunRemote(cfg, Options{Box: "abc"}, SSHTarget{Host: "far"}, sources, ssh.exec, false); err != nil {
		t.Fatal(err)
	}
	eng := box("docker", "aaa")
	ucfg, _, _ := testConfig(eng)
	if _, err := RunTar(ucfg, Options{}, bytes.NewReader(ssh.calls[0].stdin)); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(eng.streams, "|")
	for _, want := range []string{"shot.png<-png-bytes", "note.txt<-clip"} {
		if !strings.Contains(got, want) {
			t.Fatalf("streams = %v, missing %q", eng.streams, want)
		}
	}
}

func TestPackNothingDeliverable(t *testing.T) {
	// A FIFO-only delivery: everything skips, nothing to send, no ssh
	// deliver leg at all.
	ssh := &fakeSSH{responses: []sshResponse{}}
	cfg, _, errw := remoteConfig()
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscallMkfifo(fifo); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	_, err := RunRemote(cfg, Options{Box: "abc"}, SSHTarget{Host: "far"}, PathSources([]string{fifo}), ssh.exec, false)
	if err == nil || !strings.Contains(err.Error(), "nothing deliverable") {
		t.Fatalf("err = %v", err)
	}
	if len(ssh.calls) != 0 {
		t.Fatalf("ssh ran for an empty pack: %v", ssh.calls)
	}
	if !strings.Contains(errw.String(), "skipping") {
		t.Fatalf("no skip note: %q", errw.String())
	}
}

func TestPackTopLevelNameCollisionUniquifiesLocally(t *testing.T) {
	// Two sources landing as the same top-level name: the pack renames the
	// second before it ships, so the remote never silently merges them.
	a := writeTestFile(t, "report.pdf", "one")
	bdir := t.TempDir()
	b := filepath.Join(bdir, "report.pdf")
	os.WriteFile(b, []byte("two"), 0o644)

	ssh := &fakeSSH{responses: []sshResponse{{stdout: "/inbox/report.pdf\n/inbox/report-2.pdf\n"}}}
	cfg, _, _ := remoteConfig()
	if _, err := RunRemote(cfg, Options{Box: "abc"}, SSHTarget{Host: "far"}, PathSources([]string{a, b}), ssh.exec, false); err != nil {
		t.Fatal(err)
	}
	eng := box("docker", "aaa")
	ucfg, _, _ := testConfig(eng)
	if _, err := RunTar(ucfg, Options{}, bytes.NewReader(ssh.calls[0].stdin)); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(eng.streams, "|")
	if !strings.Contains(got, "report.pdf<-one") || !strings.Contains(got, "report-2.pdf<-two") {
		t.Fatalf("streams = %v", eng.streams)
	}
}

func TestSendMeterHonestUnderInterruption(t *testing.T) {
	var errw bytes.Buffer
	m := &sendMeter{err: &errw, total: 4 * meterStep, enabled: true}
	g := meterGuard{m: m, w: &errw}
	m.Write(make([]byte, meterStep))
	g.Write([]byte("byre: a remote note\n"))
	m.Write(make([]byte, 3*meterStep))
	m.finish(true)
	s := errw.String()
	if !strings.Contains(s, "sending") || !strings.Contains(s, "a remote note") || !strings.Contains(s, "sent 1.0 MB") {
		t.Fatalf("meter transcript = %q", s)
	}
	// The note was preceded by a line clear, never spliced mid-line.
	if !strings.Contains(s, "\x1b[K"+"byre: a remote note") {
		t.Fatalf("note not on a cleared line: %q", s)
	}
}

func TestSendMeterSilentWhenDisabledOrSmall(t *testing.T) {
	var errw bytes.Buffer
	m := &sendMeter{err: &errw, total: 10, enabled: true}
	m.Write(make([]byte, 10))
	m.finish(true)
	if errw.Len() != 0 {
		t.Fatalf("small delivery drew a meter: %q", errw.String())
	}
	var errw2 bytes.Buffer
	m2 := &sendMeter{err: &errw2, total: 10 * meterStep, enabled: false}
	m2.Write(make([]byte, 10*meterStep))
	m2.finish(true)
	if errw2.Len() != 0 {
		t.Fatalf("disabled meter drew: %q", errw2.String())
	}
}
