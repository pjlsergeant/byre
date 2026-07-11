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

	inbox    map[string]bool // existing in-box names, relative to /inbox
	execErr  error
	streams  []string // "id name<-content" per delivered file
	execArgs [][]string
}

func (f *fakeEngine) Name() string { return f.name }
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

func TestUniquifyOnCollision(t *testing.T) {
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
	broken := &fakeEngine{name: "podman", idsErr: fmt.Errorf("cannot connect")}
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
	broken := &fakeEngine{name: "podman", idsErr: fmt.Errorf("cannot connect")}
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
	cfg, _, _ := testConfig(eng)
	src := writeFile(t, "f", "x")
	_, err := Run(cfg, Options{}, []string{src})
	if err == nil {
		t.Fatal("expected error")
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
