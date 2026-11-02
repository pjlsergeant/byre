package builtins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// ptyScript runs its argv with stdin, stdout and stderr on a fresh pty and
// exits with the child's status. python3's pty module is the portable pty on
// both CI legs (util-linux and BSD `script` disagree on their arguments).
const ptyScript = "import os, pty, sys\nsys.exit(os.waitstatus_to_exitcode(pty.spawn(sys.argv[1:])))\n"

// runHookOnPTY is runHook with the hook's stdio on a real terminal, the case
// the BYRE_ASSUME_TTY seam only stands in for. stdin is forwarded into the
// pty. Returns the terminal's output (stdout and stderr share it).
func runHookOnPTY(t *testing.T, shell, hook, stdin string, env ...string) string {
	t.Helper()
	testtools.NeedTool(t, "python3")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-c", ptyScript, shell, hook)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("hook did not finish within 5s on the pty: %s", out)
	}
	if err != nil {
		t.Fatalf("hook failed on the pty: %v (%s)", err, out)
	}
	return string(out)
}

// argvLogStub writes an executable `name` into bin that appends its argv,
// one invocation per line, to the returned log and exits 0.
func argvLogStub(t *testing.T, bin, name string) string {
	t.Helper()
	log := filepath.Join(bin, name+".argv")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

// stubCalls returns the invocations an argvLogStub recorded.
func stubCalls(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// The grok, opencode and grok-shared-auth login hooks run a 600 s interactive
// login whenever their credential is absent. With nobody to answer -- no
// terminal on stdin, or a headless launch (BYRE_HEADLESS, which must win
// over a terminal and over the BYRE_ASSUME_TTY seam) -- each exits 0 with a
// remedy naming the env key and `byre shell`, and never runs the login. The
// attended cases are the control: the same fixture DOES reach the login, so
// the guard is what stopped it.
func TestLoginHooksNeverPromptWithoutSomeoneToAnswer(t *testing.T) {
	_, cat := testCat(t)
	for _, fx := range []struct {
		skill, script string
		cli           string   // the binary whose login must not run
		remedy        []string // stable fragments of the remedy
		// env is the credential-absent environment; bin is the stub dir.
		env func(t *testing.T, bin string) []string
	}{
		{"grok", "grok-login.sh", "grok", []string{"XAI_API_KEY", "byre shell"},
			func(t *testing.T, bin string) []string {
				return []string{"GROK_HOME=" + t.TempDir(), "XAI_API_KEY=", "GROK_AUTH_PROVIDER_COMMAND="}
			}},
		{"opencode", "opencode-login.sh", "opencode", []string{"ANTHROPIC_API_KEY", "OPENCODE_API_KEY", "byre shell"},
			func(t *testing.T, bin string) []string {
				return []string{sharedAuthLibSeam(t, cat, "opencode"), "XDG_DATA_HOME=" + physTempDir(t),
					"ANTHROPIC_API_KEY=", "OPENCODE_API_KEY="}
			}},
		{"grok-shared-auth", "00-grok-shared-auth.sh", "grok", []string{"XAI_API_KEY", "byre shell"},
			func(t *testing.T, bin string) []string {
				testtools.NeedTool(t, "jq")
				// curl keeps the endpoint-cache warmer off the network.
				if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nexit 6\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				return []string{"BYRE_IDENTITY_BASE=" + t.TempDir(), "GROK_HOME=" + t.TempDir(), "XAI_API_KEY="}
			}},
	} {
		t.Run(fx.skill, func(t *testing.T) {
			hook := filepath.Join(skillDir(t, cat, fx.skill), fx.script)
			run := func(t *testing.T, onPTY bool, terminal ...string) (out, calls string) {
				t.Helper()
				bin := t.TempDir()
				log := argvLogStub(t, bin, fx.cli)
				env := append([]string{"PATH=" + bin + ":" + os.Getenv("PATH")}, fx.env(t, bin)...)
				env = append(env, terminal...)
				if onPTY {
					out = runHookOnPTY(t, "sh", hook, "", env...)
				} else {
					out = runHook(t, "sh", hook, env...)
				}
				return out, stubCalls(t, log)
			}
			refused := func(t *testing.T, onPTY bool, terminal ...string) {
				t.Helper()
				out, calls := run(t, onPTY, terminal...)
				if strings.Contains(calls, "login") {
					t.Fatalf("login ran with nobody to answer it: %q", calls)
				}
				for _, frag := range fx.remedy {
					if !strings.Contains(out, frag) {
						t.Fatalf("remedy must name %q: %s", frag, out)
					}
				}
			}
			t.Run("no terminal", func(t *testing.T) {
				refused(t, false, noTerminalEnv...)
			})
			t.Run("headless beats the terminal seam", func(t *testing.T) {
				refused(t, false, "BYRE_HEADLESS=1", "BYRE_ASSUME_TTY=1")
			})
			t.Run("headless on a real terminal", func(t *testing.T) {
				refused(t, true, "BYRE_HEADLESS=1", "BYRE_ASSUME_TTY=")
			})
			t.Run("control: attended terminal seam reaches the login", func(t *testing.T) {
				if _, calls := run(t, false, attendedTerminalEnv...); !strings.Contains(calls, "login") {
					t.Fatalf("fixture never reaches the login, so the refusals above prove nothing: %q", calls)
				}
			})
			t.Run("control: real terminal reaches the login", func(t *testing.T) {
				if _, calls := run(t, true, noTerminalEnv...); !strings.Contains(calls, "login") {
					t.Fatalf("a real terminal must still get the login: %q", calls)
				}
			})
		})
	}
}
