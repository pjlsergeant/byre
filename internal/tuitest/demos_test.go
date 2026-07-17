package tuitest

// The publish-time site demos (BYRE_DEMO_REC=1): each test drives a real
// scenario under the recording harness (demo.go) and installs the cast where
// the site build embeds it (site/static/casts/<slug>.cast). The WaitFors are
// the point — a layout change fails the recording, which fails the publish;
// the slugs match the site's demo shortcodes (placement:
// docs/marketing/positioning.md "Site plan").
//
// Determinism: every scenario runs on a curated PATH (a symlink farm of
// plain tools plus, per scenario, stub engines/clipboards), so the frames
// recorded in CI — where a real docker exists — are the frames recorded
// anywhere. The engine is never real here: the deliver demo's docker is a
// stub answering deliver's exact discovery/exec calls (the clipbeat fakes'
// stance extended to the demo tier: the product binary is untouched, the
// HOST capability is played), and the quickstart's develop runs to the
// engine boundary and is trimmed there.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// demoTools is the symlink farm's inventory: everything a scenario's
// children may exec. Missing tools are skipped (platform variance), the
// load-bearing ones fail loudly at use.
var demoTools = []string{
	"bash", "sh", "env", "git", "ls", "cat", "id", "stty", "tput", "clear",
	"sed", "grep", "awk", "sort", "uniq", "head", "tail", "cut", "tr", "wc",
	"sleep", "printf", "echo", "mkdir", "rm", "true", "dircolors", "uname",
	"readlink", "dirname", "basename", "find",
}

// demoPath builds the curated PATH: a farm of symlinks to real tools, the
// byre binary under its own name, plus any stub dirs prepended (stubs win).
func demoPath(t *testing.T, stubDirs ...string) string {
	t.Helper()
	farm := t.TempDir()
	for _, tool := range demoTools {
		p, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if err := os.Symlink(p, filepath.Join(farm, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(Binary(t), filepath.Join(farm, "byre")); err != nil {
		t.Fatal(err)
	}
	return strings.Join(append(append([]string{}, stubDirs...), farm), ":")
}

// demoHome is the recorded $HOME. Paths paint into the cast, so CI creates a
// real-looking one (BYRE_DEMO_HOME=/home/pete, see site.yml) — locally the
// fallback is a tempdir and the frames just carry its path.
func demoHome(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("BYRE_DEMO_HOME"); h != "" {
		// A prior run's state would leak into the frames (an already-seeded
		// store skips the picker); each test starts it fresh.
		for _, e := range []string{".byre", "code"} {
			if err := os.RemoveAll(filepath.Join(h, e)); err != nil {
				t.Fatal(err)
			}
		}
		return h
	}
	return t.TempDir()
}

// demoProject creates <home>/code/my-app — a plausible project dir.
func demoProject(t *testing.T, home string) string {
	t.Helper()
	proj := filepath.Join(home, "code", "my-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	runDemoCmd(t, proj, home, "", "git", "init", "-q", ".")
	return proj
}

// runDemoCmd runs one setup command in the demo env (unrecorded).
func runDemoCmd(t *testing.T, dir, home, path string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home)
	if path != "" {
		cmd.Env = append(cmd.Env, "PATH="+path)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(argv, " "), err, out)
	}
	return string(out)
}

// seedStore runs one un-enrolling byre command so the store preamble (the
// AGENTS.md write, the bundled-mirror refresh) is spent before recording —
// a first byre command paints those lines, and they aren't the demo's story.
func seedStore(t *testing.T, home, path string) {
	t.Helper()
	// exec.Command resolves argv[0] against the TEST process's PATH, so the
	// binary is named absolutely; the curated PATH still shapes the child.
	cmd := exec.Command(Binary(t), "status")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seeding the store: %v\n%s", err, out)
	}
}

// seedProject enrolls the project with template/agent set (develop with
// onboarding flags, expected to stop at the engine boundary — the curated
// PATH has no engine) and returns the project id the store recorded.
func seedProject(t *testing.T, proj, home, path string) string {
	t.Helper()
	cmd := exec.Command(Binary(t), "develop", "--template", "go", "--agent", "claude")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+path)
	out, _ := cmd.CombinedOutput() // exits nonzero at the engine boundary
	if !bytes.Contains(out, []byte("byre.config")) {
		t.Fatalf("seeding develop wrote no config:\n%s", out)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".byre", "projects"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly one enrolled project (err %v)", err)
	}
	return entries[0].Name()
}

// demoEnv is the recorded session's environment: fake home, curated PATH,
// display variables cleared unless a scenario stubs its own.
func demoEnv(home, path string) Opts {
	return Opts{
		Env:   map[string]string{"HOME": home, "PATH": path},
		Unset: []string{"DISPLAY", "WAYLAND_DISPLAY", "BYRE_HOME", "EDITOR", "VISUAL"},
	}
}

// hold keeps the current screen on film — pacing between beats is what makes
// a recording watchable rather than a keystroke dump.
func hold() { time.Sleep(1500 * time.Millisecond) }

func TestDemoConfigTUIWalk(t *testing.T) {
	RequireDemo(t)
	home := demoHome(t)
	path := demoPath(t)
	proj := demoProject(t, home)
	seedStore(t, home, path)
	seedProject(t, proj, home, path)

	o := demoEnv(home, path)
	o.Dir = proj
	o.RecordTo = filepath.Join(t.TempDir(), "walk.cast")
	s := Start(t, o, "byre", "config")

	// The form: grants first, the exposure summary on top.
	s.WaitFor("GRANTS")
	s.WaitFor("exposure:")
	hold()

	// Add a host mount: ~/notes, target suggested, read-only accepted.
	e := s.Keys("Enter")
	s.WaitForAfter(e, "a add")
	hold()
	e = s.Keys("a")
	s.WaitForAfter(e, "Add Extra mount")
	s.TypeHuman("~/notes")
	s.Keys("Tab")
	s.WaitFor("/home/dev/notes") // the suggested in-box target
	hold()
	s.Keys("Right") // accept the suggestion
	e = s.Keys("Enter")
	s.WaitForAfter(e, "~/notes -> /home/dev/notes (ro)")
	hold()
	e = s.Keys("Escape")
	s.WaitForAfter(e, "$EDITOR")

	// Add a package: Mounts → Packages is eight rows down (Ports, Egress,
	// Env, Base, Template, Agent, Engine, Packages).
	for i := 0; i < 8; i++ {
		s.Keys("Down")
		time.Sleep(150 * time.Millisecond)
	}
	e = s.Keys("Enter")
	s.WaitForAfter(e, "a add")
	s.Keys("a")
	s.TypeHuman("postgresql-client")
	e = s.Keys("Enter")
	s.WaitForAfter(e, "▸ postgresql-client")
	hold()

	// Save, then leave; the exit line names the file the edit landed in.
	e = s.Keys("C-s")
	s.WaitForAfter(e, "Saved ✓")
	hold()
	s.Keys("Escape")
	s.WaitFor("$EDITOR")
	s.Keys("C-q")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}
	s.WaitFor("byre: wrote")
	WriteDemo(t, "config-tui-walk", s.EndCast("byre: wrote"))
}

func TestDemoQuickstartPickerStatus(t *testing.T) {
	RequireDemo(t)
	home := demoHome(t)
	path := demoPath(t)
	proj := demoProject(t, home)
	seedStore(t, home, path)

	// Scene 1: the first-run picker, recorded to the engine boundary — the
	// trim ends the scene on the config-written line, the picker's payoff.
	o := demoEnv(home, path)
	o.Dir = proj
	o.RecordTo = filepath.Join(t.TempDir(), "picker.cast")
	s := Start(t, o, "byre", "develop")
	s.WaitFor("No byre.config here")
	s.WaitFor("Template")
	hold()
	s.TypeHuman("go")
	s.Keys("Enter")
	s.WaitFor("Agent")
	s.TypeHuman("claude")
	s.Keys("Enter")
	s.WaitFor("machine-wide credentials")
	hold()
	s.TypeHuman("y")
	s.Keys("Enter")
	s.WaitFor("Save these as your default")
	s.TypeHuman("y")
	s.Keys("Enter")
	s.WaitFor("skills=claude-shared-auth")
	s.WaitForExit() // the engine boundary; everything after the write is trimmed
	picker := s.EndCast("skills=claude-shared-auth")

	// Scene 2: `byre status` on the box scene 1 configured. The stub engine
	// answers the container lookup with "nothing running" — every other line
	// is the real config the picker wrote.
	stub := writeEngineStub(t, "", 0, 0)
	o2 := demoEnv(home, demoPath(t, stub))
	o2.Dir = proj
	o2.RecordTo = filepath.Join(t.TempDir(), "status.cast")
	s2 := Start(t, o2, "byre", "status")
	s2.WaitFor("Project id:")
	s2.WaitFor("Container:")
	if st := s2.WaitForExit(); st != 0 {
		t.Fatalf("status exit = %d\n%s", st, s2.CaptureNow())
	}
	status := s2.EndCast("not running")

	WriteDemo(t, "quickstart-picker-status", picker, status)
}

func TestDemoDeliverPasteFlow(t *testing.T) {
	RequireDemo(t)
	home := demoHome(t)
	path := demoPath(t)
	proj := demoProject(t, home)
	seedStore(t, home, path)
	id := seedProject(t, proj, home, path)

	stub := writeEngineStub(t, id, os.Getuid(), os.Getgid())
	writeClipboardStub(t, stub)

	o := demoEnv(home, demoPath(t, stub))
	o.Env["WAYLAND_DISPLAY"] = "byre-demo"
	o.Unset = []string{"DISPLAY", "BYRE_HOME"}
	o.Dir = proj
	o.RecordTo = filepath.Join(t.TempDir(), "deliver.cast")
	s := Start(t, o, "byre", "deliver")

	// The beat sees the screenshot on the (played) pasteboard.
	s.WaitFor("image on the clipboard")
	hold()
	hold()
	s.Keys("C-v")
	s.WaitFor("delivering to")
	s.WaitFor("→ /inbox/clipboard-")
	s.WaitFor("path copied to the clipboard")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("deliver exit = %d\n%s", st, s.CaptureNow())
	}
	WriteDemo(t, "deliver-paste-flow", s.EndCast("path copied to the clipboard"))
}

func TestDemoCompletionTabWalk(t *testing.T) {
	RequireDemo(t)
	home := demoHome(t)
	path := demoPath(t)
	proj := demoProject(t, home)

	rc := filepath.Join(t.TempDir(), "demorc")
	rcContent := fmt.Sprintf(`. %s
eval "$(byre completion bash)"
PS1='\[\e[1;32m\]pete@studio\[\e[0m\]:\[\e[1;34m\]~/code/my-app\[\e[0m\]$ '
`, bashCompletionLib(t))
	if err := os.WriteFile(rc, []byte(rcContent), 0o644); err != nil {
		t.Fatal(err)
	}

	o := demoEnv(home, path)
	o.Dir = proj
	o.RecordTo = filepath.Join(t.TempDir(), "completion.cast")
	s := Start(t, o, "bash", "--rcfile", rc, "-i")
	s.WaitFor("pete@studio")
	hold()

	// The commands, with their one-line descriptions.
	s.TypeHuman("byre ")
	s.Keys("Tab", "Tab")
	s.WaitFor("develop") // the completion list paints
	hold()
	hold()

	// Completion finishes the command, then lists develop's flags.
	s.TypeHuman("dev")
	s.Keys("Tab")
	s.WaitFor("byre develop")
	s.TypeHuman("--")
	s.Keys("Tab", "Tab")
	s.WaitFor("--template")
	hold()
	hold()

	// One more: byre completion itself completes its shells.
	s.Keys("C-u")
	time.Sleep(400 * time.Millisecond)
	s.TypeHuman("byre completion ")
	s.Keys("Tab", "Tab")
	s.WaitFor("powershell")
	hold()
	WriteDemo(t, "completion-tab-walk", s.EndCast("powershell"))
}

// writeEngineStub writes a fake `docker` into a fresh dir and returns the
// dir. It answers exactly the calls the recorded flows make — discovery
// (ps/inspect) and deliver's exec transport (consume stdin, print the landed
// path). An empty boxID plays a machine with nothing running. Positional
// arithmetic in the exec arm rides execInputArgs' pinned argv shape.
func writeEngineStub(t *testing.T, boxID string, uid, gid int) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the demo stubs play linux host capabilities")
	}
	dir := t.TempDir()
	labels := ""
	ps := ""
	if boxID != "" {
		ps = "echo f3a9c2d71e04"
		labels = fmt.Sprintf(`{"byre.project":"%s","byre.workdir":"%s"}`, boxID, boxID)
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  ps) %s ;;
  inspect)
    case "$3" in
      *Labels*) printf '%%s\n' '%s' ;;
      *Env*) printf 'BYRE_UID=%d\nBYRE_GID=%d\n' ;;
      *) exit 1 ;;
    esac ;;
  exec)
    cat > /dev/null
    shift 8
    printf '%%s/%%s%%s\n' "$4" "$5" "$6" ;;
  *) exit 0 ;;
esac
`, ps, labels, uid, gid)
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeClipboardStub adds wl-paste/wl-copy to the stub dir: the pasteboard
// carries a PNG "screenshot" (the deliver demo's premise), and the copy-back
// succeeds silently (the landed path onto the clipboard).
func writeClipboardStub(t *testing.T, dir string) {
	t.Helper()
	clip := t.TempDir()
	if err := os.WriteFile(filepath.Join(clip, "types"), []byte("image/png\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x42}, 47<<10)...)
	if err := os.WriteFile(filepath.Join(clip, "image"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	paste := fmt.Sprintf(`#!/bin/sh
case "$1" in
  --list-types) exec cat %[1]s/types ;;
  --type) case "$2" in image/png*) exec cat %[1]s/image ;; esac ;;
esac
exit 1
`, clip)
	if err := os.WriteFile(filepath.Join(dir, "wl-paste"), []byte(paste), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wl-copy"), []byte("#!/bin/sh\nexec cat > /dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// bashCompletionLib locates bash-completion's main library — cobra's bash
// script needs its helpers. The demo gate set without it is a configuration
// error naming both fixes.
func bashCompletionLib(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("BYRE_DEMO_BASHCOMP"),
		"/usr/share/bash-completion/bash_completion",
		"/etc/bash_completion",
		filepath.Join(os.Getenv("HOME"), "scratch", "bash_completion"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Fatal("BYRE_DEMO_REC=1 but no bash-completion library found — apt-get install bash-completion, or point BYRE_DEMO_BASHCOMP at a copy")
	return ""
}
