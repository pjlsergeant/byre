package builtins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCodex answers `--version`; `login status` per STUB_STATUS (in / out /
// hang), recording the CODEX_HOME it was asked about; `login --with-*` per
// STUB_LOGIN (ok / fail / hang -- a hang ignores TERM, so only a KILL ends
// it), writing the key it read from stdin into
// CODEX_HOME/auth.json the way codex does; and records everything else as the
// run, including what the run's CODEX_HOME held, and writes STUB_ANSWER to
// the --output-last-message path.
const stubCodex = `#!/bin/sh
case "$1" in
--version) echo "codex-cli 9.9.9"; exit 0 ;;
login)
  case "$2" in
  status)
    printf '%s\n' "$CODEX_HOME" >>"$STUB_RECORD/status-home"
    case "${STUB_STATUS:-out}" in
    in) echo "Logged in using ChatGPT"; exit 0 ;;
    hang) trap '' TERM; exec sleep 60 ;;
    *) echo "Not logged in"; exit 1 ;;
    esac ;;
  *)
    [ "${STUB_LOGIN:-ok}" = hang ] && { trap '' TERM; exec sleep 60; }
    printf '%s %s\n' "$2" "$CODEX_HOME" >>"$STUB_RECORD/login"
    printf '{"OPENAI_API_KEY":"%s","auth_mode":"apikey"}' "$(cat)" >"$CODEX_HOME/auth.json"
    [ "${STUB_LOGIN:-ok}" = fail ] && exit 1
    exit 0 ;;
  esac ;;
esac
printf '%s' "$CODEX_HOME" >"$STUB_RECORD/home"
ls -A "$CODEX_HOME" >"$STUB_RECORD/home-ls"
[ ! -f "$CODEX_HOME/config.toml" ] || cp "$CODEX_HOME/config.toml" "$STUB_RECORD/config.toml"
[ ! -f "$CODEX_HOME/auth.json" ] || cp "$CODEX_HOME/auth.json" "$STUB_RECORD/auth.json"
prev=""
for a in "$@"; do
  if [ "$prev" = --output-last-message ] && [ -n "${STUB_ANSWER:-}" ]; then printf '%s' "$STUB_ANSWER" >"$a"; fi
  prev=$a
done
` + stubRunArm

const (
	codexStarted = `{"type":"thread.started","thread_id":"t"}`
	codexTurn    = `{"type":"turn.started"}`
	codexWarn    = `{"type":"error","message":"Reconnecting... 1/5"}`
	codexWarnErr = `{"type":"item.completed","item":{"id":"item_0","type":"error","message":"Falling back from WebSockets to HTTPS transport"}}`
	codexMessage = `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"pong"}}`
	codexDone    = `{"type":"turn.completed","usage":{}}`
	codexFailed  = `{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized"}}`
	realAuth     = `{"auth_mode":"chatgpt","tokens":{"refresh_token":"REAL"}}`
	realConfig   = "model = \"o9\"\n"
)

// newCodexBox stages the stub, the real adapter, and a real CODEX_HOME that
// already holds a ChatGPT login and the user's config.toml.
func newCodexBox(t *testing.T) (*headlessBox, string) {
	t.Helper()
	b := newHeadlessBox(t)
	b.writeExec("codex", stubCodex)
	b.installAdapter(filepath.Join("skills", "codex", "codex-mcp-launch.sh"), "byre-codex-mcp-launch")
	real := filepath.Join(b.dir, "codex-home")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"auth.json": realAuth, "config.toml": realConfig} {
		if err := os.WriteFile(filepath.Join(real, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b.env["CODEX_HOME"] = real
	script, err := filepath.Abs(filepath.Join("skills", "codex", "codex-headless.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return b, script
}

// realHomeUntouched fails unless the real CODEX_HOME still holds exactly the
// login and config it started with.
func (b *headlessBox) realHomeUntouched() {
	b.t.Helper()
	real := b.env["CODEX_HOME"]
	isRegularWith(b.t, filepath.Join(real, "auth.json"), realAuth)
	isRegularWith(b.t, filepath.Join(real, "config.toml"), realConfig)
	if ents, _ := os.ReadDir(real); len(ents) != 2 {
		b.t.Fatalf("the real CODEX_HOME gained files: %v", ents)
	}
}

// tmpEmpty fails unless every temp file and home the script made is gone.
func (b *headlessBox) tmpEmpty() {
	b.t.Helper()
	if ents, _ := os.ReadDir(b.tmp); len(ents) != 0 {
		b.t.Fatalf("temp files left behind: %v", ents)
	}
}

// With a key in the environment the run gets a CODEX_HOME of its own: the
// key is logged in there, the user's config.toml is copied in, the real home
// is never written, and the run's home is gone afterwards -- and the key wins
// even though the real home's login would pass.
func TestCodexHeadlessKeyGetsItsOwnHome(t *testing.T) {
	b, script := newCodexBox(t)
	b.env["OPENAI_API_KEY"] = "sk-test-key"
	b.env["STUB_STATUS"] = "in"
	b.env["STUB_ANSWER"] = "pong"
	body := b.stream(codexStarted, codexTurn, codexMessage, codexDone)
	code, stdout, stderr := b.run(script)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, stderr)
	}
	runHome, _ := b.recorded("home")
	if runHome == "" || runHome == b.env["CODEX_HOME"] || filepath.Dir(runHome) != b.tmp {
		t.Fatalf("the run's CODEX_HOME = %q, want a fresh dir in TMPDIR, not the real home", runHome)
	}
	if login, _ := b.recorded("login"); login != "--with-api-key "+runHome+"\n" {
		t.Fatalf("login = %q, want --with-api-key into the run's home", login)
	}
	if auth, _ := b.recorded("auth.json"); !strings.Contains(auth, `"sk-test-key"`) {
		t.Fatalf("the run's home did not hold the key's login: %q", auth)
	}
	if cfg, _ := b.recorded("config.toml"); cfg != realConfig {
		t.Fatalf("config.toml not copied into the run's home: %q", cfg)
	}
	if ls, _ := b.recorded("home-ls"); ls != "auth.json\nconfig.toml\n" {
		t.Fatalf("the run's home held more than the login and config: %q", ls)
	}
	if _, asked := b.recorded("status-home"); asked {
		t.Fatal("the real home's login was consulted although a key was given")
	}
	if !strings.Contains(b.file(b.timeoutLog), "-k 5 30 codex login --with-api-key\n") {
		t.Fatalf("the key login is not bounded at 30 s: %q", b.file(b.timeoutLog))
	}
	b.realHomeUntouched()
	if _, err := os.Stat(runHome); !os.IsNotExist(err) {
		t.Fatalf("the run's CODEX_HOME outlived the run: %v", err)
	}
	b.tmpEmpty()

	argv := b.argv()
	want := []string{
		"-c", "developer_instructions=test context",
		"exec", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check",
		"--json", "--output-last-message", "<out>", "-",
	}
	if len(argv) == len(want) && strings.HasPrefix(argv[7], filepath.Join(b.tmp, "byre-codex-answer.")) {
		argv[7] = "<out>"
	}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv mismatch:\n got %q\nwant %q", argv, want)
	}
	if got, _ := b.recorded("stdin"); got != headlessTask {
		t.Fatalf("task not delivered byte-exact on stdin: %q", got)
	}
	if got := b.file(b.result); got != "pong" {
		t.Fatalf("answer = %q, want the --output-last-message content", got)
	}
	if stdout != body {
		t.Fatalf("the live stream must pass through on stdout:\n got %q\nwant %q", stdout, body)
	}
	if st := b.file(b.status); !strings.Contains(st, "agent_version=codex-cli 9.9.9\n") || strings.Contains(st, "backend_exit") {
		t.Fatalf("status = %q", st)
	}
}

func TestCodexHeadlessAccessToken(t *testing.T) {
	b, script := newCodexBox(t)
	b.env["CODEX_ACCESS_TOKEN"] = "at-test"
	b.env["STUB_ANSWER"] = "pong"
	b.stream(codexMessage, codexDone)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	runHome, _ := b.recorded("home")
	if login, _ := b.recorded("login"); login != "--with-access-token "+runHome+"\n" || runHome == b.env["CODEX_HOME"] {
		t.Fatalf("login = %q in %q, want --with-access-token into the run's own home", login, runHome)
	}
	b.realHomeUntouched()
	b.tmpEmpty()
}

// Without a key, a working login in the real CODEX_HOME is used in place.
func TestCodexHeadlessExistingLogin(t *testing.T) {
	b, script := newCodexBox(t)
	b.env["STUB_STATUS"] = "in"
	b.env["STUB_ANSWER"] = "pong"
	b.stream(codexMessage, codexDone)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if home, _ := b.recorded("home"); home != b.env["CODEX_HOME"] {
		t.Fatalf("the run's CODEX_HOME = %q, want the real one", home)
	}
	if _, loggedIn := b.recorded("login"); loggedIn {
		t.Fatal("no key was given, yet a login ran")
	}
	if !strings.Contains(b.file(b.timeoutLog), "-k 5 30 codex login status\n") {
		t.Fatalf("login status not bounded at 30 s: %q", b.file(b.timeoutLog))
	}
	if !strings.Contains(b.file(b.timeoutLog), "-k 5 10 codex --version\n") {
		t.Fatalf("the version probe is not bounded at 10 s with a KILL follow-up: %q", b.file(b.timeoutLog))
	}
	b.realHomeUntouched()
}

func TestCodexHeadlessModelFlag(t *testing.T) {
	b, script := newCodexBox(t)
	b.env["OPENAI_API_KEY"] = "sk-test"
	b.env["STUB_ANSWER"] = "pong"
	b.stream(codexDone)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := strings.Join(b.argv(), " "); strings.Contains(got, " -m ") {
		t.Fatalf("-m passed without BYRE_RUN_MODEL: %q", got)
	}
	b.env["BYRE_RUN_MODEL"] = "gpt-test"
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	argv := b.argv()
	if n := len(argv); n < 3 || argv[n-3] != "-m" || argv[n-2] != "gpt-test" || argv[n-1] != "-" {
		t.Fatalf("BYRE_RUN_MODEL not passed as -m before the stdin marker: %q", argv)
	}
}

func TestCodexHeadlessCredentialPreflight(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		b, script := newCodexBox(t)
		code, _, stderr := b.run(script)
		if code != 13 || !strings.Contains(stderr, "env_from_host") {
			t.Fatalf("exit %d, want 13 naming env_from_host: %s", code, stderr)
		}
		if _, ran := b.recorded("argv"); ran {
			t.Fatal("codex ran without a credential")
		}
	})
	t.Run("login status hangs", func(t *testing.T) {
		b, script := newCodexBox(t)
		b.env["STUB_STATUS"] = "hang"
		code, _, stderr := b.run(script)
		if code != 13 || !strings.Contains(stderr, "env_from_host") || !strings.Contains(stderr, "within 30 s") {
			t.Fatalf("exit %d, want 13 for the timed-out probe: %s", code, stderr)
		}
	})
	for _, mode := range []string{"hang", "fail"} {
		t.Run("key login "+mode, func(t *testing.T) {
			b, script := newCodexBox(t)
			b.env["OPENAI_API_KEY"] = "sk-test"
			b.env["STUB_LOGIN"] = mode
			code, _, stderr := b.run(script)
			if code != 13 || !strings.Contains(stderr, "env_from_host") {
				t.Fatalf("exit %d, want 13: %s", code, stderr)
			}
			if _, ran := b.recorded("argv"); ran {
				t.Fatal("codex ran after the key login failed")
			}
			b.realHomeUntouched()
			b.tmpEmpty()
		})
	}
}

// turn.failed decides 10 whatever the status; the warnings codex emits on
// healthy runs (top-level error events, error items) do not.
func TestCodexHeadlessReportedFailure(t *testing.T) {
	for _, rc := range []string{"1", "0"} {
		b, script := newCodexBox(t)
		b.env["OPENAI_API_KEY"] = "sk-test"
		b.env["STUB_RC"] = rc
		b.stream(codexStarted, codexTurn, codexWarn, codexFailed)
		if code, _, stderr := b.run(script); code != 10 {
			t.Fatalf("rc %s: exit %d, want 10: %s", rc, code, stderr)
		}
		if st := b.file(b.status); strings.Contains(st, "backend_exit") {
			t.Fatalf("rc %s: a reported failure is not a backend exit: %q", rc, st)
		}
		b.tmpEmpty()
	}

	b, script := newCodexBox(t)
	b.env["OPENAI_API_KEY"] = "sk-test"
	b.env["STUB_ANSWER"] = "pong"
	b.stream(codexStarted, codexWarnErr, codexTurn, codexWarn, "not json", codexMessage, codexDone)
	if code, _, stderr := b.run(script); code != 0 {
		t.Fatalf("warnings on a healthy run: exit %d, want 0: %s", code, stderr)
	}
}

func TestCodexHeadlessProcessFailureIs11(t *testing.T) {
	for _, rc := range []string{"3", "10", "124"} {
		b, script := newCodexBox(t)
		b.env["OPENAI_API_KEY"] = "sk-test"
		b.env["STUB_RC"] = rc
		b.stream(codexStarted, codexTurn)
		code, _, stderr := b.run(script)
		if code != 11 {
			t.Fatalf("rc %s: exit %d, want 11: %s", rc, code, stderr)
		}
		if st := b.file(b.status); !strings.Contains(st, "backend_exit="+rc+"\n") {
			t.Fatalf("rc %s: status %q lacks backend_exit=%s", rc, st, rc)
		}
		b.tmpEmpty()
	}
}

func TestCodexHeadlessBackendAbsentIs15(t *testing.T) {
	b, script := newCodexBox(t)
	b.env["OPENAI_API_KEY"] = "sk-test"
	if err := os.Remove(filepath.Join(b.bin, "codex")); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := b.run(script); code != 15 {
		t.Fatalf("exit %d, want 15: %s", code, stderr)
	}
}
