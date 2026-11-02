package builtins

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/testtools"
)

// Each bundled agent with a headless mode declares it, ships the script at the
// declared name on PATH, makes it executable, and carries the jq the script
// parses the agent's events with.
func TestBundledHeadlessCommandsShip(t *testing.T) {
	_, cat := testCat(t)
	for agent, want := range map[string]string{
		"claude": "byre-claude-headless",
		"codex":  "byre-codex-headless",
		"grok":   "byre-grok-headless",
	} {
		res, err := skills.Resolve(config.Config{Agent: agent}, cat)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if got := res.AgentHeadless(); got != want {
			t.Errorf("%s: headless = %q, want %q", agent, got, want)
		}
		dest := "/usr/local/bin/" + want
		var shipped, executable, jq bool
		for _, blk := range res.BuildBlocks() {
			if blk.Name != "byre/"+agent {
				continue
			}
			for _, f := range blk.Files {
				shipped = shipped || f.Dest == dest
			}
			for _, l := range blk.Dockerfile {
				executable = executable || (strings.HasPrefix(l, "RUN chmod +x ") && strings.Contains(l+" ", " "+dest+" "))
			}
			for _, a := range blk.Apt {
				jq = jq || a == "jq"
			}
		}
		if !shipped || !executable || !jq {
			t.Errorf("%s: %s shipped=%v chmod=%v jq=%v", agent, dest, shipped, executable, jq)
		}
	}
}

// headlessBox is a stand-in for the in-box environment a headless command
// runs in: a bin dir holding the stub agent; a tools dir of the host programs
// the scripts and adapters use, each behind a wrapper that reports whether it
// was handed the result or status channel; a sys dir standing in for the
// image's system directories, holding the same wrappers, a stub timeout and
// the REAL launch adapter; and the chassis's three files, with regular files
// standing in for its descriptors. The inherited PATH is exactly bin:tools,
// so the box's own claude or codex can never answer in place of the stub, and
// the environment is built from scratch, so the suite box's real credentials
// never reach a case. stage points the script's and the adapter's pinned PATH
// and absolute paths at sys. The stubs run their own tools from the tools dir
// (STUB_TOOLS), so a decoy planted in bin catches the scripts, not the stubs.
type headlessBox struct {
	t              *testing.T
	dir, bin, tmp  string
	sys, tools     string
	task, result   string
	status, record string
	timeoutLog     string
	env            map[string]string
}

// headlessTask is the task every case delivers: shell metacharacters, quotes,
// a blank line and no trailing newline, so any re-quoting or re-reading of it
// shows as a byte difference.
const headlessTask = "Fix the build.\n\n  $HOME `id` \"quoted\" 'single' \\ back\nno newline at the end"

func newHeadlessBox(t *testing.T) *headlessBox {
	t.Helper()
	testtools.NeedTool(t, "bash", "jq", "timeout")
	dir := physTempDir(t)
	b := &headlessBox{
		t: t, dir: dir,
		bin:        filepath.Join(dir, "bin"),
		sys:        filepath.Join(dir, "sys"),
		tools:      filepath.Join(dir, "tools"),
		tmp:        filepath.Join(dir, "tmp"),
		task:       filepath.Join(dir, "run", "task.md"),
		result:     filepath.Join(dir, "run", "result.md"),
		status:     filepath.Join(dir, "run", "status"),
		record:     filepath.Join(dir, "record"),
		timeoutLog: filepath.Join(dir, "timeout.log"),
		env:        map[string]string{},
	}
	tools := b.tools
	for _, d := range []string{b.bin, tools, b.sys, b.tmp, b.record, filepath.Dir(b.task), filepath.Join(dir, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bash", "sh", "jq", "mktemp", "cp", "rm", "cat", "head", "tail", "tee",
		"grep", "mkdir", "ln", "mv", "sleep", "wc", "iconv", "env", "tr", "ls"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue // the scripts degrade without the optional ones (iconv)
		}
		for _, d := range []string{tools, b.sys} {
			// The shells stay plain links: the wrapper is itself a shell script.
			if name == "bash" || name == "sh" {
				if err := os.Symlink(p, filepath.Join(d, name)); err != nil {
					t.Fatal(err)
				}
				continue
			}
			wrapper := "#!/bin/sh\n" + stubHeldCheck + "exec '" + p + "' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(d, name), []byte(wrapper), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	realTimeout, err := exec.LookPath("timeout")
	if err != nil {
		t.Fatal(err)
	}
	// The stub timeout records the bound it was asked for and enforces 2 s
	// instead, so a hung probe proves the bound without a 30 s wait. A -k is
	// honoured as a 1 s KILL follow-up, and only when asked for: a probe that
	// ignores TERM is then cut off only if the script passed -k. It is the
	// system one: the scripts never look timeout up on the inherited PATH.
	b.writeSys("timeout", "#!/bin/sh\n"+stubHeldCheck+"printf '%s\\n' \"$*\" >>'"+b.timeoutLog+"'\n"+
		"if [ \"$1\" = -k ]; then shift 3; exec '"+realTimeout+"' -k 1 2 \"$@\"; fi\n"+
		"shift\nexec '"+realTimeout+"' 2 \"$@\"\n")
	for _, f := range []string{b.task, b.result, b.status} {
		body := ""
		if f == b.task {
			body = headlessTask
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := filepath.Join(dir, "agent-context.md")
	if err := os.WriteFile(ctx, []byte("test context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b.env = map[string]string{
		"PATH":                 b.bin + ":" + tools,
		"HOME":                 filepath.Join(dir, "home"),
		"TMPDIR":               b.tmp,
		"BYRE_AGENT_CONTEXT":   ctx,
		"BYRE_MCP_CONFIG":      filepath.Join(dir, "no-mcp.json"),
		"BYRE_RUN_TASK_FILE":   b.task,
		"BYRE_RUN_RESULT_FILE": "/dev/fd/5",
		"BYRE_RUN_STATUS_FILE": "/dev/fd/6",
		"STUB_RECORD":          b.record,
		"STUB_TOOLS":           tools,
		"STUB_TOP":             strconv.Itoa(os.Getpid()),
		"STUB_RESULT":          b.result,
		"STUB_STATUS":          b.status,
	}
	return b
}

// writeExec puts a program on the inherited PATH, where the agent lives.
func (b *headlessBox) writeExec(name, body string) {
	b.t.Helper()
	if err := os.WriteFile(filepath.Join(b.bin, name), []byte(body), 0o755); err != nil {
		b.t.Fatal(err)
	}
}

// writeSys puts a program in the stand-in system directory.
func (b *headlessBox) writeSys(name, body string) {
	b.t.Helper()
	if err := os.WriteFile(filepath.Join(b.sys, name), []byte(body), 0o755); err != nil {
		b.t.Fatal(err)
	}
}

// headlessSystemPath is the PATH every headless script pins for its own
// helpers, the image's system directories.
const headlessSystemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// stagePinned copies a script that pins its helpers' PATH to dst, the pinned
// PATH replaced by pin and then each from/to pair in rewrites applied. Every
// replacement must match exactly once, so a script that stops pinning, or
// stops naming a program by the absolute path a rewrite targets, fails here.
func stagePinned(t *testing.T, src, dst, pin string, rewrites ...string) string {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	pairs := append([]string{"\nPATH=" + headlessSystemPath + "\n", "\nPATH=" + pin + "\n"}, rewrites...)
	for i := 0; i+1 < len(pairs); i += 2 {
		if n := strings.Count(body, pairs[i]); n != 1 {
			t.Fatalf("%s: %q appears %d times, want once", src, strings.TrimSpace(pairs[i]), n)
		}
		body = strings.Replace(body, pairs[i], pairs[i+1], 1)
	}
	if err := os.WriteFile(dst, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

// stage installs a skill's real launch adapter in the stand-in system
// directory under its in-box name, its pinned PATH naming that directory, and
// returns a copy of the headless script whose pinned PATH and absolute adapter
// path name it too. rewrites apply to both files: a program both name by
// absolute path.
func (b *headlessBox) stage(script, adapterSrc, adapter string, rewrites ...string) string {
	b.t.Helper()
	stagePinned(b.t, adapterSrc, filepath.Join(b.sys, adapter), b.sys, rewrites...)
	return stagePinned(b.t, script, filepath.Join(b.dir, filepath.Base(script)), b.sys,
		append([]string{"=/usr/local/bin/" + adapter + "\n", "=" + filepath.Join(b.sys, adapter) + "\n"}, rewrites...)...)
}

// agentCmd is how a script's bounded probe names an agent command to
// timeout: through env, with the inherited PATH restored.
func (b *headlessBox) agentCmd(cmd string) string {
	return "env PATH=" + b.env["PATH"] + " " + cmd
}

// run executes a headless script and returns its exit code, stdout, stderr.
func (b *headlessBox) run(script string) (int, string, string) {
	b.t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		b.t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(b.record, "held")); err != nil && !os.IsNotExist(err) {
		b.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, script)
	// The result and status files ride fds 5 and 6, as the chassis holds them.
	var held []*os.File
	for _, p := range []string{b.result, b.status} {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			b.t.Fatal(err)
		}
		defer f.Close()
		held = append(held, f)
	}
	cmd.ExtraFiles = append([]*os.File{nil, nil}, held...)
	for k, v := range b.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		b.t.Fatalf("%s did not finish within 20s: %s", script, stderr.String())
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		b.t.Fatal(err)
	}
	// Whatever the case asserts, no program the script started -- the agent,
	// a probe, a login, a helper -- was handed the result or status channel.
	if held, _ := b.recorded("held"); held != "" {
		b.t.Errorf("a child of the headless command was handed the result/status channel:\n%s", held)
	}
	return code, stdout.String(), stderr.String()
}

// recorded reads a file the stub wrote; "" with ok=false when it never ran.
func (b *headlessBox) recorded(name string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(b.record, name))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func (b *headlessBox) argv() []string {
	b.t.Helper()
	raw, ok := b.recorded("argv")
	if !ok {
		b.t.Fatal("the stub agent never ran")
	}
	return strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
}

// agentHeldNothing fails unless the stub agent ran and no child of the
// headless command saw its result or status descriptor, or a variable naming
// one. run checks the second half on every case; this pins that the agent
// itself was among the children checked.
func (b *headlessBox) agentHeldNothing() {
	b.t.Helper()
	if _, ran := b.recorded("argv"); !ran {
		b.t.Fatal("the stub agent never ran")
	}
	if held, _ := b.recorded("held"); held != "" {
		b.t.Errorf("the agent was handed the result/status channel: %q", held)
	}
}

func (b *headlessBox) file(p string) string {
	b.t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		b.t.Fatal(err)
	}
	return string(raw)
}

// stubHeldCheck opens every stub and tool wrapper: it records, under the
// program's name, any of the headless command's held descriptors (5, 6) or
// the variables naming them that reached it -- and, where /proc shows them,
// any ancestor below the headless command itself that holds the result or
// status file open under any descriptor number. The second half catches a
// forked copy of the script's shell (a subshell, a command substitution, a
// pipeline element) that kept them while the program it started runs. The
// walk stops at the headless command (its parent is the test, STUB_TOP),
// which holds both legitimately. Builtins only, so the check starts nothing
// that could itself be checked.
const stubHeldCheck = `for n in 5 6; do [ ! -e /dev/fd/$n ] || echo "${0##*/}: fd $n" >>"$STUB_RECORD/held"; done
[ -z "${BYRE_RUN_RESULT_FILE+x}" ] || echo "${0##*/}: BYRE_RUN_RESULT_FILE" >>"$STUB_RECORD/held"
[ -z "${BYRE_RUN_STATUS_FILE+x}" ] || echo "${0##*/}: BYRE_RUN_STATUS_FILE" >>"$STUB_RECORD/held"
held_p=$PPID
while [ -n "${STUB_TOP:-}" ] && [ "$held_p" -gt 1 ] && [ "$held_p" != "$STUB_TOP" ] && [ -r "/proc/$held_p/status" ]; do
  held_pp=""
  while read -r held_k held_v; do [ "$held_k" != PPid: ] || held_pp=$held_v; done <"/proc/$held_p/status"
  [ -n "$held_pp" ] || break
  if [ "$held_pp" != "$STUB_TOP" ]; then
    for held_f in /proc/$held_p/fd/*; do
      if [ "$held_f" -ef "$STUB_RESULT" ] || [ "$held_f" -ef "$STUB_STATUS" ]; then
        echo "${0##*/}: ancestor $held_p holds the result or status file (fd ${held_f##*/})" >>"$STUB_RECORD/held"
      fi
    done
  fi
  held_p=$held_pp
done
`

// stubAgentStart opens every stub agent: the held check, then the PATH it was
// given set aside for the run arm to record, and its own tools taken from the
// tools dir, so a decoy planted on the inherited PATH answers only a lookup
// the scripts or adapters made.
const stubAgentStart = "#!/bin/sh\n" + stubHeldCheck + "stub_path=$PATH PATH=$STUB_TOOLS\n"

// The stub agent's run arm: argv NUL-separated, the PATH it was given, stdin
// byte-exact, then the canned stream on stdout and the canned exit status.
const stubRunArm = `for a in "$@"; do printf '%s\0' "$a"; done >"$STUB_RECORD/argv"
printf '%s' "$stub_path" >"$STUB_RECORD/path"
cat >"$STUB_RECORD/stdin"
[ -z "${STUB_STREAM:-}" ] || cat "$STUB_STREAM"
exit "${STUB_RC:-0}"
`

// stubClaude answers `--version`, `auth status` per STUB_AUTH (in / out /
// hang -- ignoring TERM, so only a KILL ends it), and records everything else as the run.
const stubClaude = stubAgentStart + `case "$1" in
--version) echo "9.9.9 (Claude Code)"; exit 0 ;;
auth)
  case "${STUB_AUTH:-out}" in
  in) echo '{"loggedIn":true,"authMethod":"claude.ai"}'; exit 0 ;;
  hang) trap '' TERM; exec sleep 60 ;;
  *) echo '{"loggedIn":false,"authMethod":"none"}'; exit 1 ;;
  esac ;;
esac
` + stubRunArm

func newClaudeBox(t *testing.T) (*headlessBox, string) {
	t.Helper()
	b := newHeadlessBox(t)
	b.writeExec("claude", stubClaude)
	b.env["CLAUDE_CONFIG_DIR"] = filepath.Join(b.dir, "claude-config")
	return b, b.stage(filepath.Join("skills", "claude", "claude-headless.sh"),
		filepath.Join("skills", "claude", "claude-launch.sh"), "byre-claude-launch")
}

// stream writes a canned event stream for the stub to print.
func (b *headlessBox) stream(lines ...string) string {
	b.t.Helper()
	p := filepath.Join(b.dir, "stream.jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		b.t.Fatal(err)
	}
	b.env["STUB_STREAM"] = p
	return body
}

const (
	claudeInit    = `{"type":"system","subtype":"init","model":"claude-test"}`
	claudeOK      = `{"type":"result","subtype":"success","is_error":false,"result":"pong"}`
	claudeFailed  = `{"type":"result","subtype":"success","is_error":true,"result":"There's an issue with the selected model"}`
	claudeEarlier = `{"type":"result","subtype":"success","is_error":true,"result":"an earlier turn"}`
)

// The run argv is a contract with the claude CLI: the adapter is what runs
// (its context flag comes first), then the interactive command's own flags,
// then -p's stream-json pair, which claude refuses without --verbose.
func TestClaudeHeadlessRunsTheAdapter(t *testing.T) {
	b, script := newClaudeBox(t)
	b.env["ANTHROPIC_API_KEY"] = "sk-test"
	body := b.stream(claudeInit, "not json: a stray line", claudeEarlier, claudeOK)
	code, stdout, stderr := b.run(script)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, stderr)
	}
	want := []string{
		"--append-system-prompt-file", filepath.Join(b.tmp, "byre-agent-context.md"),
		"-p", "--dangerously-skip-permissions", "--mcp-config", "/etc/byre/mcp.json",
		"--add-dir", "/etc/byre/claude-skills", "--output-format", "stream-json", "--verbose",
	}
	if got := b.argv(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv mismatch:\n got %q\nwant %q", got, want)
	}
	// The answer and status below arrive through fds 5 and 6, which the agent
	// itself never held.
	b.agentHeldNothing()
	if got, _ := b.recorded("stdin"); got != headlessTask {
		t.Fatalf("task not delivered byte-exact on stdin: %q", got)
	}
	if got := b.file(b.result); got != "pong" {
		t.Fatalf("answer = %q, want the last result event's .result", got)
	}
	if stdout != body {
		t.Fatalf("the live stream must pass through on stdout:\n got %q\nwant %q", stdout, body)
	}
	if st := b.file(b.status); !strings.Contains(st, "agent_version=9.9.9 (Claude Code)\n") || strings.Contains(st, "backend_exit") {
		t.Fatalf("status = %q", st)
	}
	// The private stream copy is gone.
	if ents, _ := os.ReadDir(b.tmp); len(ents) != 1 || ents[0].Name() != "byre-agent-context.md" {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestClaudeHeadlessModelFlagOnlyWhenSet(t *testing.T) {
	b, script := newClaudeBox(t)
	b.env["CLAUDE_CODE_OAUTH_TOKEN"] = "sk-ant-oat01-test"
	b.stream(claudeOK)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := strings.Join(b.argv(), " "); strings.Contains(got, "--model") {
		t.Fatalf("--model passed without BYRE_RUN_MODEL: %q", got)
	}
	b.env["BYRE_RUN_MODEL"] = "haiku"
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	argv := b.argv()
	if n := len(argv); n < 2 || argv[n-2] != "--model" || argv[n-1] != "haiku" {
		t.Fatalf("BYRE_RUN_MODEL not passed as --model: %q", argv)
	}
}

// claude exits 1 when it reports a failure, so the event decides, not the
// status: is_error is 10 with either status, and the message is the answer.
func TestClaudeHeadlessReportedFailureIs10(t *testing.T) {
	for _, rc := range []string{"1", "0"} {
		b, script := newClaudeBox(t)
		b.env["ANTHROPIC_AUTH_TOKEN"] = "tok"
		b.env["STUB_RC"] = rc
		b.stream(claudeInit, claudeFailed)
		code, _, stderr := b.run(script)
		if code != 10 {
			t.Fatalf("rc %s: exit %d, want 10: %s", rc, code, stderr)
		}
		if got := b.file(b.result); !strings.Contains(got, "issue with the selected model") {
			t.Fatalf("rc %s: the failure message must be the answer: %q", rc, got)
		}
		if st := b.file(b.status); strings.Contains(st, "backend_exit") {
			t.Fatalf("rc %s: a reported failure is not a backend exit: %q", rc, st)
		}
	}
}

// Without a reported failure, a nonzero status -- or no result event at all
// -- is the process failing: 11, backend_exit first.
func TestClaudeHeadlessProcessFailureIs11(t *testing.T) {
	cases := []struct {
		rc    string
		lines []string
	}{
		{"3", []string{claudeInit}},
		{"137", nil},
		{"2", []string{claudeOK}},
		{"0", []string{claudeInit}},
		{"10", []string{claudeInit}},
	}
	for _, c := range cases {
		b, script := newClaudeBox(t)
		b.env["ANTHROPIC_API_KEY"] = "sk-test"
		b.env["STUB_RC"] = c.rc
		if c.lines != nil {
			b.stream(c.lines...)
		}
		code, _, stderr := b.run(script)
		if code != 11 {
			t.Fatalf("rc %s: exit %d, want 11: %s", c.rc, code, stderr)
		}
		if st := b.file(b.status); !strings.Contains(st, "backend_exit="+c.rc+"\n") {
			t.Fatalf("rc %s: status %q lacks backend_exit=%s", c.rc, st, c.rc)
		}
	}
}

func TestClaudeHeadlessCredentialPreflight(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		b, script := newClaudeBox(t)
		code, _, stderr := b.run(script)
		if code != 13 || !strings.Contains(stderr, "env_from_host") {
			t.Fatalf("exit %d, want 13 naming env_from_host: %s", code, stderr)
		}
		if _, ran := b.recorded("argv"); ran {
			t.Fatal("claude ran without a credential")
		}
	})
	t.Run("auth status hangs", func(t *testing.T) {
		b, script := newClaudeBox(t)
		b.env["STUB_AUTH"] = "hang"
		code, _, stderr := b.run(script)
		if code != 13 || !strings.Contains(stderr, "env_from_host") || !strings.Contains(stderr, "within 30 s") {
			t.Fatalf("exit %d, want 13 for the timed-out probe: %s", code, stderr)
		}
		if !strings.Contains(b.file(b.timeoutLog), "-k 5 30 "+b.agentCmd("claude auth status")+"\n") {
			t.Fatalf("auth status not bounded at 30 s with a KILL follow-up: %q", b.file(b.timeoutLog))
		}
	})
	t.Run("auth status logged in", func(t *testing.T) {
		b, script := newClaudeBox(t)
		b.env["STUB_AUTH"] = "in"
		b.stream(claudeOK)
		if code, _, stderr := b.run(script); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if !strings.Contains(b.file(b.timeoutLog), "-k 5 10 "+b.agentCmd("claude --version")+"\n") {
			t.Fatalf("the version probe is not bounded at 10 s with a KILL follow-up: %q", b.file(b.timeoutLog))
		}
	})
	t.Run("stored inference login", func(t *testing.T) {
		b, script := newClaudeBox(t)
		cfg := b.env["CLAUDE_CONFIG_DIR"]
		if err := os.MkdirAll(cfg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		b.stream(claudeOK)
		if code, _, stderr := b.run(script); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if strings.Contains(b.file(b.timeoutLog), "auth status") {
			t.Fatal("a stored login needs no status probe")
		}
	})
	t.Run("MCP-only credentials file", func(t *testing.T) {
		b, script := newClaudeBox(t)
		cfg := b.env["CLAUDE_CONFIG_DIR"]
		if err := os.MkdirAll(cfg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(`{"mcpOAuth":{"x|y":{}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := b.run(script); code != 13 {
			t.Fatalf("exit %d, want 13: MCP logins do not authenticate inference: %s", code, stderr)
		}
	})
}

// The onboarding marker is seeded on a fresh config dir for an env
// credential, and never written through a link planted at its name.
func TestClaudeHeadlessSeedsOnboarding(t *testing.T) {
	b, script := newClaudeBox(t)
	b.env["CLAUDE_CODE_OAUTH_TOKEN"] = "sk-ant-oat01-test"
	b.stream(claudeOK)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	marker := filepath.Join(b.env["CLAUDE_CONFIG_DIR"], ".claude.json")
	if got := b.file(marker); !strings.Contains(got, "hasCompletedOnboarding") {
		t.Fatalf("marker not seeded: %q", got)
	}
	if ents, _ := os.ReadDir(b.env["CLAUDE_CONFIG_DIR"]); len(ents) != 1 {
		t.Fatalf("seed residue left: %v", ents)
	}

	b2, script2 := newClaudeBox(t)
	b2.env["CLAUDE_CODE_OAUTH_TOKEN"] = "sk-ant-oat01-test"
	b2.stream(claudeOK)
	cfg := b2.env["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(b2.dir, "victim")
	if err := os.Symlink(victim, filepath.Join(cfg, ".claude.json")); err != nil {
		testtools.Unavailable(t, "symlink", err)
	}
	if code, _, stderr := b2.run(script2); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Fatal("the seed wrote through a planted symlink")
	}
}

func TestClaudeHeadlessBackendAbsentIs15(t *testing.T) {
	b, script := newClaudeBox(t)
	b.env["ANTHROPIC_API_KEY"] = "sk-test"
	if err := os.Remove(filepath.Join(b.bin, "claude")); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := b.run(script); code != 15 {
		t.Fatalf("exit %d, want 15: %s", code, stderr)
	}
}

// The inherited PATH starts with the agent-writable ~/.local/bin, so a
// helper the scripts or the launch adapters found there would run inside the
// verdict, or decide what the agent is told. Decoys of every helper either
// one runs, of the launch adapter, and of grok (which is named by its
// root-owned link) planted first on it never run; the verdict and answer are
// unchanged, the system timeout is the one that bounds the probes -- and the
// agent itself still sees the inherited PATH. The context is over the
// adapters' argv budget, so their truncation chain runs too.
func TestHeadlessHelpersIgnoreTheInheritedPath(t *testing.T) {
	type agentCase struct {
		name    string
		adapter string
		setup   func(t *testing.T) (*headlessBox, string)
		probe   func(b *headlessBox) string
	}
	for _, c := range []agentCase{
		{"claude", "byre-claude-launch", func(t *testing.T) (*headlessBox, string) {
			b, script := newClaudeBox(t)
			b.env["ANTHROPIC_API_KEY"] = "sk-test"
			b.stream(claudeOK)
			return b, script
		}, func(*headlessBox) string { return "claude --version" }},
		{"codex", "byre-codex-mcp-launch", func(t *testing.T) (*headlessBox, string) {
			b, script := newCodexBox(t)
			b.env["OPENAI_API_KEY"] = "sk-test"
			b.env["STUB_ANSWER"] = "pong"
			b.stream(codexMessage, codexDone)
			return b, script
		}, func(*headlessBox) string { return "codex --version" }},
		{"grok", "byre-grok-launch", func(t *testing.T) (*headlessBox, string) {
			b, script := newGrokBox(t)
			b.env["XAI_API_KEY"] = "xai-test"
			b.stream(grokOK)
			return b, script
		}, func(b *headlessBox) string { return b.grokBin() + " --version" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, script := c.setup(t)
			big := filepath.Join(b.dir, "big-context.md")
			if err := os.WriteFile(big, []byte(strings.Repeat("é", 70_000)), 0o644); err != nil {
				t.Fatal(err)
			}
			b.env["BYRE_AGENT_CONTEXT"] = big
			for _, name := range []string{"timeout", "env", "jq", "tee", "cat", "head", "wc", "iconv",
				"mktemp", "mv", "rm", "cp", "flock", "grok", c.adapter} {
				b.writeExec(name, "#!/bin/sh\necho \"${0##*/}\" >>\"$STUB_RECORD/decoy\"\nexit 99\n")
			}
			code, _, stderr := b.run(script)
			if code != 0 {
				t.Fatalf("exit %d, want 0: %s", code, stderr)
			}
			if ran, ok := b.recorded("decoy"); ok {
				t.Fatalf("a helper planted on the inherited PATH ran: %q", ran)
			}
			if got := b.file(b.result); got != "pong" {
				t.Fatalf("answer = %q, want pong", got)
			}
			if !strings.Contains(b.file(b.timeoutLog), " "+b.agentCmd(c.probe(b))+"\n") {
				t.Fatalf("the system timeout did not bound the version probe: %q", b.file(b.timeoutLog))
			}
			if got, _ := b.recorded("path"); got != b.env["PATH"] {
				t.Fatalf("the agent ran with PATH %q, want the inherited %q", got, b.env["PATH"])
			}
			b.agentHeldNothing()
		})
	}
}
