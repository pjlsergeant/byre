package builtins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// stubGrok answers `--version` per STUB_VERSION (ok / hang -- ignoring TERM,
// so only a KILL ends it) and records everything else as the run.
const stubGrok = `#!/bin/sh
` + stubHeldCheck + `case "$1" in
--version)
  [ "${STUB_VERSION:-ok}" = hang ] && { trap '' TERM; exec sleep 60; }
  echo "grok 9.9.9"; exit 0 ;;
esac
` + stubRunArm

const (
	grokOK     = `{"text":"pong","stopReason":"end_turn","sessionId":"s","num_turns":1}`
	grokFailed = `{"type":"error","message":"Couldn't set model 'nonexistent-model-xyz': Invalid params","cwd":"/workspace"}`
	// grokPretty is the success document as grok prints it: pretty, many lines.
	grokPretty = "{\n  \"text\": \"pong\",\n  \"stopReason\": \"end_turn\",\n  \"usage\": {\n    \"total_tokens\": 3\n  }\n}"
)

// newGrokBox stages the stub, the real adapter, and an empty GROK_HOME.
func newGrokBox(t *testing.T) (*headlessBox, string) {
	t.Helper()
	b := newHeadlessBox(t)
	b.writeExec("grok", stubGrok)
	home := filepath.Join(b.dir, "grok-home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	b.env["GROK_HOME"] = home
	return b, b.stage(filepath.Join("skills", "grok", "grok-headless.sh"),
		filepath.Join("skills", "grok", "grok-launch.sh"), "byre-grok-launch")
}

// The run argv is a contract with the grok CLI: the adapter is what runs (its
// context flag comes first), then a single --prompt-file turn naming the task
// file, the interactive command's approval flag, and the json document.
// Stdin is /dev/null, so the stub reads nothing.
func TestGrokHeadlessRunsTheAdapter(t *testing.T) {
	b, script := newGrokBox(t)
	b.env["XAI_API_KEY"] = "xai-test"
	body := b.stream(grokPretty)
	code, stdout, stderr := b.run(script)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, stderr)
	}
	want := []string{
		"--append-system-prompt", "test context",
		"--prompt-file", b.task, "--always-approve", "--output-format", "json",
	}
	if got := b.argv(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv mismatch:\n got %q\nwant %q", got, want)
	}
	b.agentHeldNothing()
	if got, _ := b.recorded("stdin"); got != "" {
		t.Fatalf("stdin must be /dev/null, the stub read %q", got)
	}
	if got := b.file(b.result); got != "pong" {
		t.Fatalf("answer = %q, want the document's .text exactly once", got)
	}
	if stdout != body {
		t.Fatalf("grok's output must pass through on stdout:\n got %q\nwant %q", stdout, body)
	}
	if st := b.file(b.status); !strings.Contains(st, "agent_version=grok 9.9.9\n") || strings.Contains(st, "backend_exit") {
		t.Fatalf("status = %q", st)
	}
	if !strings.Contains(b.file(b.timeoutLog), "-k 5 10 "+b.agentCmd("grok --version")+"\n") {
		t.Fatalf("the version probe is not bounded at 10 s with a KILL follow-up: %q", b.file(b.timeoutLog))
	}
	if ents, _ := os.ReadDir(b.tmp); len(ents) != 0 {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestGrokHeadlessModelFlagOnlyWhenSet(t *testing.T) {
	b, script := newGrokBox(t)
	b.env["XAI_API_KEY"] = "xai-test"
	b.stream(grokOK)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, a := range b.argv() {
		if a == "-m" {
			t.Fatalf("-m passed without BYRE_RUN_MODEL: %q", b.argv())
		}
	}
	b.env["BYRE_RUN_MODEL"] = "grok-test-model"
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	argv := b.argv()
	if n := len(argv); n < 2 || argv[n-2] != "-m" || argv[n-1] != "grok-test-model" {
		t.Fatalf("BYRE_RUN_MODEL not passed as -m: %q", argv)
	}
}

// grok exits 1 when it reports a failure, so the document decides, not the
// status: an error document is 10 with either status, and its message is the
// answer.
func TestGrokHeadlessReportedFailureIs10(t *testing.T) {
	for _, rc := range []string{"1", "0"} {
		b, script := newGrokBox(t)
		b.env["XAI_API_KEY"] = "xai-test"
		b.env["STUB_RC"] = rc
		b.stream(grokFailed)
		code, _, stderr := b.run(script)
		if code != 10 {
			t.Fatalf("rc %s: exit %d, want 10: %s", rc, code, stderr)
		}
		if got := b.file(b.result); !strings.HasPrefix(got, "Couldn't set model") {
			t.Fatalf("rc %s: the error message must be the answer: %q", rc, got)
		}
		if st := b.file(b.status); strings.Contains(st, "backend_exit") {
			t.Fatalf("rc %s: a reported failure is not a backend exit: %q", rc, st)
		}
	}
}

// Without an error document, a nonzero status -- or no answer document at
// all -- is the process failing: 11, backend_exit first.
func TestGrokHeadlessProcessFailureIs11(t *testing.T) {
	cases := []struct {
		rc    string
		lines []string
	}{
		{"3", nil},
		{"137", []string{"not json"}},
		{"2", []string{grokOK}},
		{"0", nil},
		{"0", []string{`{"stopReason":"end_turn"}`}},
		{"10", nil},
	}
	for _, c := range cases {
		b, script := newGrokBox(t)
		b.env["XAI_API_KEY"] = "xai-test"
		b.env["STUB_RC"] = c.rc
		if c.lines != nil {
			b.stream(c.lines...)
		}
		code, _, stderr := b.run(script)
		if code != 11 {
			t.Fatalf("rc %s %q: exit %d, want 11: %s", c.rc, c.lines, code, stderr)
		}
		if st := b.file(b.status); !strings.Contains(st, "backend_exit="+c.rc+"\n") {
			t.Fatalf("rc %s: status %q lacks backend_exit=%s", c.rc, st, c.rc)
		}
	}
}

func TestGrokHeadlessCredentialPreflight(t *testing.T) {
	writeAuth := func(t *testing.T, b *headlessBox, body string) string {
		t.Helper()
		p := filepath.Join(b.env["GROK_HOME"], "auth.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	refused := func(t *testing.T, b *headlessBox, script string) {
		t.Helper()
		code, _, stderr := b.run(script)
		if code != 13 {
			t.Fatalf("exit %d, want 13: %s", code, stderr)
		}
		for _, want := range []string{"env_from_host", "byre credentials set", "grok-shared-auth"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the remedy must name %s: %s", want, stderr)
			}
		}
		if _, ran := b.recorded("argv"); ran {
			t.Fatal("grok ran without a credential")
		}
	}
	accepted := func(t *testing.T, b *headlessBox, script string) {
		t.Helper()
		b.stream(grokOK)
		if code, _, stderr := b.run(script); code != 0 {
			t.Fatalf("exit %d, want 0: %s", code, stderr)
		}
	}

	t.Run("none", func(t *testing.T) {
		b, script := newGrokBox(t)
		b.env["XAI_API_KEY"] = ""
		refused(t, b, script)
	})
	t.Run("XAI_API_KEY", func(t *testing.T) {
		b, script := newGrokBox(t)
		b.env["XAI_API_KEY"] = "xai-test"
		accepted(t, b, script)
	})
	t.Run("shared-auth broker", func(t *testing.T) {
		b, script := newGrokBox(t)
		b.env["GROK_AUTH_PROVIDER_COMMAND"] = "/usr/local/bin/broker"
		accepted(t, b, script)
	})
	t.Run("stored login", func(t *testing.T) {
		b, script := newGrokBox(t)
		writeAuth(t, b, `{"https://auth.x.ai":{"key":"tok"}}`)
		accepted(t, b, script)
	})
	t.Run("auth.json without a key", func(t *testing.T) {
		b, script := newGrokBox(t)
		writeAuth(t, b, `{}`)
		refused(t, b, script)
	})
	t.Run("symlinked auth.json", func(t *testing.T) {
		b, script := newGrokBox(t)
		real := filepath.Join(b.dir, "elsewhere.json")
		if err := os.WriteFile(real, []byte(`{"scope":{"key":"tok"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(b.env["GROK_HOME"], "auth.json")); err != nil {
			testtools.Unavailable(t, "symlink", err)
		}
		refused(t, b, script)
	})
	// The one probe the script runs is the version; a hung one is cut off
	// and the run goes on without an agent_version.
	t.Run("version probe hangs", func(t *testing.T) {
		b, script := newGrokBox(t)
		b.env["XAI_API_KEY"] = "xai-test"
		b.env["STUB_VERSION"] = "hang"
		accepted(t, b, script)
		if st := b.file(b.status); strings.Contains(st, "agent_version") {
			t.Fatalf("a hung version probe has no version: %q", st)
		}
		if !strings.Contains(b.file(b.timeoutLog), "-k 5 10 "+b.agentCmd("grok --version")+"\n") {
			t.Fatalf("the version probe is not bounded: %q", b.file(b.timeoutLog))
		}
	})
}

func TestGrokHeadlessBackendAbsentIs15(t *testing.T) {
	for _, gone := range []string{filepath.Join("bin", "grok"), filepath.Join("sys", "byre-grok-launch")} {
		b, script := newGrokBox(t)
		b.env["XAI_API_KEY"] = "xai-test"
		if err := os.Remove(filepath.Join(b.dir, gone)); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := b.run(script); code != 15 {
			t.Fatalf("%s absent: exit %d, want 15: %s", gone, code, stderr)
		}
	}
}
