package deliver

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// fakeEngine is a configurable Engine. Its ExecInput understands the three
// transport scripts by their argv shape (script text is argv[2], tag argv[3])
// and simulates an in-box /inbox: existing names collide, claims uniquify.
type fakeEngine struct {
	name     string
	ids      []string
	idsErr   error
	env      map[string]map[string]string // id -> env
	labels   map[string]map[string]string // id -> labels
	envErr   error
	labelErr error

	inbox        map[string]bool // existing in-box names, relative to /inbox
	execErr      error
	failMkdir    bool     // interior mkdirScript execs fail
	streams      []string // "id name<-content" per delivered file
	execArgs     [][]string
	callerScoped bool // rootless engine: every visible session is the caller's
}

func (f *fakeEngine) Name() string       { return f.name }
func (f *fakeEngine) CallerScoped() bool { return f.callerScoped }
func (f *fakeEngine) Sessions(label string) ([]string, error) {
	return f.ids, f.idsErr
}
func (f *fakeEngine) Env(id string) (map[string]string, error) {
	if f.envErr != nil {
		return nil, f.envErr
	}
	return f.env[id], nil
}
func (f *fakeEngine) Labels(id string) (map[string]string, error) {
	if f.labelErr != nil {
		return nil, f.labelErr
	}
	return f.labels[id], nil
}

func (f *fakeEngine) ExecInput(id string, uid, gid int, stdin io.Reader, argv ...string) (string, error) {
	f.execArgs = append(f.execArgs, argv)
	if f.execErr != nil {
		return "", f.execErr
	}
	if f.inbox == nil {
		f.inbox = map[string]bool{}
	}
	script := argv[2]
	args := argv[4:] // after the $0 tag
	claim := func(prefix, stem, ext string) string {
		n := stem + ext
		for k := 2; f.inbox[prefix+n]; k++ {
			n = fmt.Sprintf("%s-%d%s", stem, k, ext)
		}
		f.inbox[prefix+n] = true
		return n
	}
	switch {
	case strings.Contains(script, "cat >>"): // fileScript: dir stem ext [mk]
		dir, stem, ext := args[0], args[1], args[2]
		rel := strings.TrimPrefix(dir, "/inbox")
		rel = strings.TrimPrefix(rel, "/")
		if rel != "" {
			rel += "/"
		}
		n := claim(rel, stem, ext)
		b, _ := io.ReadAll(stdin)
		f.streams = append(f.streams, id+" "+rel+n+"<-"+string(b))
		return dir + "/" + n + "\n", nil
	case strings.Contains(script, "mkdir \"/inbox/$n\""): // dirScript: stem ext
		n := claim("", args[0], args[1])
		return "/inbox/" + n + "\n", nil
	default: // mkdirScript: dir
		if f.failMkdir {
			return "", fmt.Errorf("mkdir refused")
		}
		return "", nil
	}
}

// session helpers

func box(name string, ids ...string) *fakeEngine {
	f := &fakeEngine{name: name, ids: ids,
		env:    map[string]map[string]string{},
		labels: map[string]map[string]string{}}
	for _, id := range ids {
		f.env[id] = map[string]string{"BYRE_UID": "501", "BYRE_GID": "20"}
		f.labels[id] = map[string]string{"byre.project": "proj-" + id, "byre.workdir": "proj-" + id}
	}
	return f
}

func testConfig(engines ...Engine) (Config, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	return Config{
		Engines:      engines,
		ProjectLabel: "byre.project",
		WorkdirLabel: "byre.workdir",
		CallerUID:    501,
		Cwd:          "/nowhere",
		Out:          &out,
		Err:          &errw,
	}, &out, &errw
}

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

// TestRunSurfacesUniquifiedName pins that Run reports the box-claimed name
// verbatim (the -2 here comes from the fake's claim loop; the REAL loop is
// pinned by TestFileClaimLoop below).
func TestRunSurfacesUniquifiedName(t *testing.T) {
	eng := box("docker", "aaa")
	eng.inbox = map[string]bool{"report.pdf": true}
	cfg, out, _ := testConfig(eng)
	src := writeFile(t, "report.pdf", "x")
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "/inbox/report-2.pdf\n" {
		t.Fatalf("stdout = %q", got)
	}
}

// TestFileClaimLoop runs the PRODUCTION claim script under a real sh: the
// ln-EEXIST uniquify picks the next free -k name, the content lands there,
// and the noclobber temp is cleaned up.
func TestFileClaimLoop(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	d := t.TempDir()
	for _, existing := range []string{"report.pdf", "report-2.pdf"} {
		if err := os.WriteFile(filepath.Join(d, existing), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", "set -eu\n"+fileClaim, "sh", d, "report", ".pdf")
	cmd.Stdin = strings.NewReader("new content")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("claim script failed: %v", err)
	}
	want := filepath.Join(d, "report-3.pdf") + "\n"
	if string(out) != want {
		t.Fatalf("claimed name = %q, want %q", out, want)
	}
	if b, err := os.ReadFile(filepath.Join(d, "report-3.pdf")); err != nil || string(b) != "new content" {
		t.Fatalf("landed content = %q, %v", b, err)
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".byre-tmp-") {
			t.Errorf("temp file not cleaned up: %s", e.Name())
		}
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

func TestMultiFilePartialFailureKeepsSuccesses(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, _ := testConfig(eng)
	good := writeFile(t, "good.txt", "x")
	landed, err := Run(cfg, Options{}, []string{good, "/does/not/exist"})
	if err == nil || !strings.Contains(err.Error(), "1 of 2 deliveries failed") {
		t.Fatalf("err = %v", err)
	}
	if len(landed) != 1 || !strings.Contains(out.String(), "/inbox/good.txt") {
		t.Fatalf("landed = %v out = %q", landed, out.String())
	}
}

func TestDirectoryDeliveryPreservesStructure(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng)
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	mustMkdir(t, filepath.Join(proj, "sub"))
	mustWrite(t, filepath.Join(proj, "a.txt"), "A")
	mustWrite(t, filepath.Join(proj, "sub", "b.txt"), "B")
	landed, err := Run(cfg, Options{}, []string{proj})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/proj" {
		t.Fatalf("landed = %v", landed)
	}
	if got := out.String(); got != "/inbox/proj\n" {
		t.Fatalf("stdout = %q (one path per directory)", got)
	}
	joined := strings.Join(eng.streams, "\n")
	if !strings.Contains(joined, "proj/a.txt<-A") || !strings.Contains(joined, "proj/sub/b.txt<-B") {
		t.Fatalf("structure not preserved: %v", eng.streams)
	}
	if !strings.Contains(errw.String(), "delivered /inbox/proj — 2 files") {
		t.Fatalf("no summary: %q", errw.String())
	}
}

func TestDirectoryPartialStillPrintsPath(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng)
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	mustMkdir(t, proj)
	mustWrite(t, filepath.Join(proj, "a.txt"), "A")
	unreadable := filepath.Join(proj, "secret.txt")
	mustWrite(t, unreadable, "S")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
	if os.Getuid() == 0 {
		t.Skip("root reads anything; the unreadable-file case needs a plain user")
	}
	landed, err := Run(cfg, Options{}, []string{proj})
	if err == nil {
		t.Fatal("expected the partial-delivery error")
	}
	if len(landed) != 1 || !strings.Contains(out.String(), "/inbox/proj") {
		t.Fatalf("partial dir must still print its path: %v %q", landed, out.String())
	}
	if !strings.Contains(errw.String(), "1 of 2 files") {
		t.Fatalf("no honest count: %q", errw.String())
	}
}

func TestSymlinkFileFollowedDirSkipped(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, errw := testConfig(eng)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "real.txt"), "R")
	mustMkdir(t, filepath.Join(dir, "realdir"))
	fl := filepath.Join(dir, "link.txt")
	dl := filepath.Join(dir, "dirlink")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), fl); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "realdir"), dl); err != nil {
		t.Fatal(err)
	}
	landed, err := Run(cfg, Options{}, []string{fl, dl})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || !strings.Contains(out.String(), "/inbox/link.txt") {
		t.Fatalf("file symlink should deliver: %v %q", landed, out.String())
	}
	if !strings.Contains(errw.String(), "skipping "+dl) {
		t.Fatalf("dir symlink should be skipped with a note: %q", errw.String())
	}
}

func TestFifoSkippedWithNote(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	landed, err := Run(cfg, Options{}, []string{fifo})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 0 {
		t.Fatalf("landed = %v", landed)
	}
	if !strings.Contains(errw.String(), "not a regular file or directory") {
		t.Fatalf("no skip note: %q", errw.String())
	}
}

func TestControlCharBasenameSanitized(t *testing.T) {
	stem, ext, sanitized := splitName("bad\nname.txt")
	if !sanitized || stem != "bad_name" || ext != ".txt" {
		t.Fatalf("splitName = %q %q %v", stem, ext, sanitized)
	}
}

func TestSplitNameDotfileAndMultiExt(t *testing.T) {
	if s, e, _ := splitName(".bashrc"); s != ".bashrc" || e != "" {
		t.Fatalf("dotfile: %q %q", s, e)
	}
	if s, e, _ := splitName("archive.tar.gz"); s != "archive.tar" || e != ".gz" {
		t.Fatalf("multi-ext: %q %q", s, e)
	}
	if s, e, _ := splitName("noext"); s != "noext" || e != "" {
		t.Fatalf("noext: %q %q", s, e)
	}
}

func TestInboxMissingErrorSurfaces(t *testing.T) {
	eng := box("docker", "aaa")
	eng.execErr = fmt.Errorf("exit status 3: this box has no /inbox (image predates it); rebuild with 'byre develop'")
	cfg, _, errw := testConfig(eng)
	src := writeFile(t, "f", "x")
	_, err := Run(cfg, Options{}, []string{src})
	if err == nil {
		t.Fatal("expected error")
	}
	// The box's guidance must SURFACE — Run prints per-file errors to stderr
	// and returns only the failure tally, so stderr is where it must land.
	if !strings.Contains(errw.String(), "no /inbox") {
		t.Fatalf("engine guidance not on stderr: %q", errw.String())
	}
}

// helpers

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	mustWrite(t, p, content)
	return p
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mkfifo(p string) error { return syscall.Mkfifo(p, 0o600) }

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

func TestDirectoryRenameIsNoted(t *testing.T) {
	// grok review finding: a control-char DIRECTORY name was sanitized
	// silently while files printed a note.
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	weird := filepath.Join(dir, "pro\nj")
	mustMkdir(t, weird)
	mustWrite(t, filepath.Join(weird, "a.txt"), "A")
	landed, err := Run(cfg, Options{}, []string{weird})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/pro_j" {
		t.Fatalf("landed = %v", landed)
	}
	if !strings.Contains(errw.String(), "renamed") {
		t.Fatalf("silent dir rename: %q", errw.String())
	}
}

func TestDirSummaryCountsFailedDirEntries(t *testing.T) {
	// grok review finding: with only an interior mkdir failing, the summary
	// claimed "N of N files" — the failed-entries count must be visible.
	eng := box("docker", "aaa")
	eng.failMkdir = true
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	proj := filepath.Join(dir, "proj")
	mustMkdir(t, filepath.Join(proj, "sub"))
	mustWrite(t, filepath.Join(proj, "a.txt"), "A")
	_, err := Run(cfg, Options{}, []string{proj})
	if err == nil {
		t.Fatal("expected the partial-delivery error")
	}
	if !strings.Contains(errw.String(), "1 entry failed") {
		t.Fatalf("summary hides the failed dir entry: %q", errw.String())
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
