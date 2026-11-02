package builtins

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
// runs in: a bin dir holding the stub agent, the REAL launch adapter, and a
// stub timeout; a tools dir of links to the host programs the scripts use; and
// the chassis's three files, with regular files standing in for its
// descriptors. PATH is exactly bin:tools, so the box's own claude or codex can
// never answer in place of the stub, and the environment is built from
// scratch, so the suite box's real credentials never reach a case.
type headlessBox struct {
	t              *testing.T
	dir, bin, tmp  string
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
		tmp:        filepath.Join(dir, "tmp"),
		task:       filepath.Join(dir, "run", "task.md"),
		result:     filepath.Join(dir, "run", "result.md"),
		status:     filepath.Join(dir, "run", "status"),
		record:     filepath.Join(dir, "record"),
		timeoutLog: filepath.Join(dir, "timeout.log"),
		env:        map[string]string{},
	}
	tools := filepath.Join(dir, "tools")
	for _, d := range []string{b.bin, tools, b.tmp, b.record, filepath.Dir(b.task), filepath.Join(dir, "home")} {
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
		if err := os.Symlink(p, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	realTimeout, err := exec.LookPath("timeout")
	if err != nil {
		t.Fatal(err)
	}
	// The stub timeout records the bound it was asked for and enforces 2 s
	// instead, so a hung probe proves the bound without a 30 s wait. A -k is
	// honoured as a 1 s KILL follow-up, and only when asked for: a probe that
	// ignores TERM is then cut off only if the script passed -k.
	b.writeExec("timeout", "#!/bin/sh\nprintf '%s\\n' \"$*\" >>'"+b.timeoutLog+"'\n"+
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
	}
	return b
}

func (b *headlessBox) writeExec(name, body string) {
	b.t.Helper()
	if err := os.WriteFile(filepath.Join(b.bin, name), []byte(body), 0o755); err != nil {
		b.t.Fatal(err)
	}
}

// installAdapter stages a skill's real launch adapter on the box PATH under
// its in-box name.
func (b *headlessBox) installAdapter(src, name string) {
	b.t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		b.t.Fatal(err)
	}
	b.writeExec(name, string(body))
}

// run executes a headless script and returns its exit code, stdout, stderr.
func (b *headlessBox) run(script string) (int, string, string) {
	b.t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
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

// agentHeldNothing fails when the stub agent saw the headless command's
// result or status descriptor, or a variable naming one.
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

// The stub agent's run arm: argv NUL-separated, stdin byte-exact, any of
// the headless command's held descriptors (5, 6) or the variables naming them
// that reached it, then the canned stream on stdout and the canned exit
// status.
const stubRunArm = `for a in "$@"; do printf '%s\0' "$a"; done >"$STUB_RECORD/argv"
for n in 5 6; do [ ! -e /dev/fd/$n ] || echo "fd $n" >>"$STUB_RECORD/held"; done
env | grep '^BYRE_RUN_\(RESULT\|STATUS\)_FILE=' >>"$STUB_RECORD/held"
cat >"$STUB_RECORD/stdin"
[ -z "${STUB_STREAM:-}" ] || cat "$STUB_STREAM"
exit "${STUB_RC:-0}"
`

// stubClaude answers `--version`, `auth status` per STUB_AUTH (in / out /
// hang -- ignoring TERM, so only a KILL ends it), and records everything else as the run.
const stubClaude = `#!/bin/sh
case "$1" in
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
	b.installAdapter(filepath.Join("skills", "claude", "claude-launch.sh"), "byre-claude-launch")
	b.env["CLAUDE_CONFIG_DIR"] = filepath.Join(b.dir, "claude-config")
	script, err := filepath.Abs(filepath.Join("skills", "claude", "claude-headless.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return b, script
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
		if !strings.Contains(b.file(b.timeoutLog), "-k 5 30 claude auth status\n") {
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
		if !strings.Contains(b.file(b.timeoutLog), "-k 5 10 claude --version\n") {
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
