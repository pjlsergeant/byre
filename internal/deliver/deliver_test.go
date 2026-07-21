package deliver

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
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
	failMkdir    bool                // interior mkdirScript execs fail
	hook         func(argv []string) // runs at each exec — race tests mutate the source here
	streams      []string            // "id name<-content" per delivered file
	execArgs     [][]string
	callerScoped bool // rootless engine: every visible session is the caller's

	// The simulated box filesystem grab reads (grab tests).
	boxfs    map[string]string // box file path -> content
	boxdirs  []string          // box directory paths
	boxOther []string          // box paths that are neither (symlinks, FIFOs)
	enumOut  string            // overrides enumerateScript output (hostile-output tests)
	enumErr  error             // enumerateScript exec error (partial-walk tests)
	catErr   error             // catScript exec error
}

func (f *fakeEngine) Name() string { return f.name }

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
	if f.hook != nil {
		f.hook(argv)
	}
	if f.execErr != nil {
		return "", f.execErr
	}
	if f.inbox == nil {
		f.inbox = map[string]bool{}
	}
	script := argv[2]
	args := argv[4:] // after the $0 tag
	switch {
	case strings.Contains(script, "pwd -P"): // classifyScript: abs path
		p := args[0]
		for _, d := range f.boxdirs {
			if d == p {
				return "d " + p, nil
			}
		}
		if _, ok := f.boxfs[p]; ok {
			return "f", nil
		}
		for _, o := range f.boxOther {
			if o == p {
				return "", fmt.Errorf("exit status 4: %s is not a regular file or directory in the box", p)
			}
		}
		return "", fmt.Errorf("exit status 4: no such path in the box: %s", p)
	}
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

// ExecOutput simulates the two streaming reads: enumerateScript (NUL-framed
// records) and catScript (a box file's content), each streamed to w.
func (f *fakeEngine) ExecOutput(id string, uid, gid int, w io.Writer, argv ...string) error {
	f.execArgs = append(f.execArgs, argv)
	if f.hook != nil {
		f.hook(argv)
	}
	script := argv[2]
	if strings.Contains(script, "-type d -exec") { // enumerateScript: phys dir
		out := f.enumOut
		if out == "" && f.enumErr == nil {
			out = f.buildEnum(argv[4]) // after the $0 tag
		}
		if out != "" {
			if _, err := io.WriteString(w, out); err != nil {
				return err
			}
		}
		return f.enumErr // may carry partial output already written above
	}
	if f.catErr != nil {
		return f.catErr
	}
	p := argv[4]
	content, ok := f.boxfs[p]
	if !ok {
		return fmt.Errorf("exit status 4: %s is not a file in the box", p)
	}
	_, err := io.WriteString(w, content)
	return err
}

// buildEnum renders the simulated box tree under root as enumerateScript would:
// directories first, then files (sorted), then others, each `tag\0path\0`.
func (f *fakeEngine) buildEnum(root string) string {
	var b strings.Builder
	under := func(p string) bool { return p == root || strings.HasPrefix(p, root+"/") }
	for _, d := range f.boxdirs {
		if under(d) {
			fmt.Fprintf(&b, "d\x00%s\x00", d)
		}
	}
	var fps []string
	for p := range f.boxfs {
		if under(p) {
			fps = append(fps, p)
		}
	}
	sort.Strings(fps)
	for _, p := range fps {
		fmt.Fprintf(&b, "f\x00%s\x00", p)
	}
	for _, o := range f.boxOther {
		if under(o) {
			fmt.Fprintf(&b, "o\x00%s\x00", o)
		}
	}
	return b.String()
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

func TestSymlinkToFifoInTreeSkipped(t *testing.T) {
	// Inside a delivered tree, a symlink to a FIFO skips with a note —
	// following it would block forever at open time. The rest delivers.
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	root := filepath.Join(dir, "bug")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "real.txt"), "R")
	fifo := filepath.Join(dir, "pipe")
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := os.Symlink(fifo, filepath.Join(root, "pipelink")); err != nil {
		t.Fatal(err)
	}
	landed, err := Run(cfg, Options{}, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 {
		t.Fatalf("landed = %v", landed)
	}
	if !strings.Contains(errw.String(), "skipping") || !strings.Contains(errw.String(), "pipelink") {
		t.Fatalf("no skip note: %q", errw.String())
	}
	if got := strings.Join(eng.streams, "|"); !strings.Contains(got, "real.txt") {
		t.Fatalf("the regular file should still deliver: %v", eng.streams)
	}
}

// Security: an interior symlink to a REGULAR file OUTSIDE the delivered tree
// (the agent-planted-symlink exfiltration vector) must be skipped, and the
// outside file's content must NEVER reach the box. The agent has /workspace
// rw, so it can drop such a link inside a dir the user delivers as a unit.
func TestDirectorySymlinkEscapeNotDelivered(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	// The "host secret" lives OUTSIDE the delivered tree.
	secret := filepath.Join(dir, "secret.txt")
	mustWrite(t, secret, "TOPSECRET")
	proj := filepath.Join(dir, "proj")
	mustMkdir(t, proj)
	mustWrite(t, filepath.Join(proj, "ok.txt"), "OK")
	if err := os.Symlink(secret, filepath.Join(proj, "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	landed, err := Run(cfg, Options{}, []string{proj})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 {
		t.Fatalf("landed = %v", landed)
	}
	joined := strings.Join(eng.streams, "|")
	if strings.Contains(joined, "TOPSECRET") || strings.Contains(joined, "innocent") {
		t.Fatalf("escaping symlink leaked outside content into the box: %v", eng.streams)
	}
	if !strings.Contains(joined, "ok.txt<-OK") {
		t.Fatalf("the real interior file should still deliver: %v", eng.streams)
	}
	if !strings.Contains(errw.String(), "skipping") || !strings.Contains(errw.String(), "innocent.txt") {
		t.Fatalf("expected a skip note for the escaping symlink: %q", errw.String())
	}
}

// Race regression (classify → root-open window): the source directory is
// swapped for a symlink to another tree while the in-box name is being
// claimed — after deliverPath's Lstat classified it a directory, before the
// root opens. The delivery must refuse: plain os.OpenRoot would follow the
// symlink and anchor the whole walk in a tree the user never named, which
// with an agent-writable source is a host-file exfiltration primitive
// (swap to a symlink at ~/.ssh, swap back to a decoy with matching names).
func TestDirectorySwappedToSymlinkMidDeliveryRefused(t *testing.T) {
	eng := box("docker", "aaa")
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets")
	mustMkdir(t, secrets)
	mustWrite(t, filepath.Join(secrets, "id_rsa"), "TOPSECRET")
	src := filepath.Join(dir, "proj")
	mustMkdir(t, src)
	mustWrite(t, filepath.Join(src, "a.txt"), "A")
	eng.hook = func(argv []string) {
		if strings.Contains(argv[2], `mkdir "/inbox/$n"`) { // dirScript: the claim moment
			if err := os.RemoveAll(src); err != nil {
				t.Error(err)
			}
			if err := os.Symlink(secrets, src); err != nil {
				t.Error(err)
			}
		}
	}
	cfg, _, errw := testConfig(eng)
	_, err := Run(cfg, Options{}, []string{src})
	if err == nil {
		t.Fatalf("a source swapped to a symlink mid-delivery must fail, stderr: %q", errw.String())
	}
	if joined := strings.Join(eng.streams, "|"); strings.Contains(joined, "TOPSECRET") || strings.Contains(joined, "id_rsa") {
		t.Fatalf("external tree exfiltrated through a swapped source: %v", eng.streams)
	}
	if !strings.Contains(errw.String(), "symlink") && !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected a symlink-refusal note, got err=%v stderr=%q", err, errw.String())
	}
}

// The enumeration side of the same race: once the host root is open, the walk
// must descend the OPENED root descriptor, never re-resolve the pathname. So a
// source swapped for an unrelated directory AFTER the root opens cannot change
// which files are enumerated — names and contents always come from the one
// selected tree. deliverDir opens the root before the walk and execs one
// fileScript per interior file; swapping at the first such exec proves later
// enumeration is unaffected by the pathname.
func TestDirectoryEnumerationRidesOpenedRoot(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, _ := testConfig(eng)
	dir := t.TempDir()
	src := filepath.Join(dir, "proj")
	mustMkdir(t, filepath.Join(src, "sub"))
	mustWrite(t, filepath.Join(src, "a.txt"), "A")
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "B")
	decoy := filepath.Join(dir, "decoy")
	mustMkdir(t, decoy)
	mustWrite(t, filepath.Join(decoy, "evil.txt"), "EVIL")
	swapped := false
	eng.hook = func(argv []string) {
		if !swapped && strings.Contains(argv[2], "cat >>") { // first interior file exec
			swapped = true
			if err := os.Rename(src, filepath.Join(dir, "proj-moved")); err != nil {
				t.Error(err)
				return
			}
			if err := os.Symlink(decoy, src); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := Run(cfg, Options{}, []string{src}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(eng.streams, "|")
	if strings.Contains(joined, "EVIL") || strings.Contains(joined, "evil.txt") {
		t.Fatalf("enumeration followed the swapped pathname into the decoy tree: %v", eng.streams)
	}
	if !strings.Contains(joined, "a.txt<-A") || !strings.Contains(joined, "sub/b.txt<-B") {
		t.Fatalf("the selected tree's files must all deliver from the opened root: %v", eng.streams)
	}
}

// A filename ending in a space must be reported at the path it ACTUALLY
// landed — the printed/copied path is the landed path (documented contract).
// Only the protocol newline is trimmed, never the name's own trailing space.
func TestTrailingSpaceFilenameLandsAtReportedPath(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, out, _ := testConfig(eng)
	dir := t.TempDir()
	src := filepath.Join(dir, "report ") // a trailing space is a legal filename
	mustWrite(t, src, "R")
	landed, err := Run(cfg, Options{}, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if len(landed) != 1 || landed[0] != "/inbox/report " {
		t.Fatalf("landed = %q, want \"/inbox/report \" (trailing space preserved)", landed)
	}
	if strings.TrimRight(out.String(), "\n") != "/inbox/report " {
		t.Fatalf("printed path dropped the trailing space: %q", out.String())
	}
}

// An interior symlink to a FIFO that is ITSELF inside the delivered tree is
// contained (os.Root would follow it), so only the nonblocking open stops it
// from hanging forever on a writer. Timeout-guarded so a regression (dropping
// O_NONBLOCK) fails loudly instead of wedging the suite.
func TestInteriorSymlinkToFifoDoesNotBlock(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	root := filepath.Join(dir, "bug")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "real.txt"), "R")
	if err := mkfifo(filepath.Join(root, "pipe")); err != nil { // FIFO inside the tree
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := os.Symlink("pipe", filepath.Join(root, "pipelink")); err != nil { // relative → contained
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Run(cfg, Options{}, []string{root}); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("delivery blocked on an interior symlink-to-FIFO — O_NONBLOCK regression")
	}
	if got := strings.Join(eng.streams, "|"); !strings.Contains(got, "real.txt") {
		t.Fatalf("the regular file should still deliver: %v", eng.streams)
	}
	if !strings.Contains(errw.String(), "pipelink") {
		t.Fatalf("expected a skip note for the FIFO symlink: %q", errw.String())
	}
}

// A top-level symlink the user names that points at a FIFO must be skipped
// (nonblocking open + fd stat), not hang. Timeout-guarded.
func TestTopLevelSymlinkToFifoDoesNotBlock(t *testing.T) {
	eng := box("docker", "aaa")
	cfg, _, errw := testConfig(eng)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	link := filepath.Join(dir, "namedlink")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Run(cfg, Options{}, []string{link}); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("top-level symlink-to-FIFO blocked — O_NONBLOCK regression")
	}
	if !strings.Contains(errw.String(), "skipping") {
		t.Fatalf("expected a skip note: %q", errw.String())
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

func mustSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatal(err)
	}
}

func mkfifo(p string) error { return syscall.Mkfifo(p, 0o600) }

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
