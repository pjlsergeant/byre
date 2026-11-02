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
// WaitDelay bounds the wait as runHook's does: a descendant left holding the
// output pipe or the stdin copy after the kill cannot outlast the bound.
func runHookOnPTY(t *testing.T, shell, hook, stdin string, env ...string) string {
	t.Helper()
	testtools.NeedTool(t, "python3")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-I", "-c", ptyScript, shell, hook)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = time.Second
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
// login whenever their credential is absent. With no terminal on stdin each
// exits 0 with a remedy naming the env key and `byre shell`, and never runs
// the login. A headless launch (BYRE_HEADLESS, which must win over a
// terminal and over the BYRE_ASSUME_TTY seam) never invokes the CLI at all
// -- TestFirstRunHooksDoNoCredentialWorkWhenHeadless pins that no helper
// runs either. The attended cases are the control: the same fixture DOES
// reach the login, so the guard is what stopped it.
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
			standsDown := func(t *testing.T, onPTY bool, terminal ...string) {
				t.Helper()
				if _, calls := run(t, onPTY, terminal...); calls != "" {
					t.Fatalf("headless launch must not invoke %s at all: %q", fx.cli, calls)
				}
			}
			t.Run("no terminal", func(t *testing.T) {
				refused(t, false, noTerminalEnv...)
			})
			t.Run("headless beats the terminal seam", func(t *testing.T) {
				standsDown(t, false, "BYRE_HEADLESS=1", "BYRE_ASSUME_TTY=1")
			})
			t.Run("headless on a real terminal", func(t *testing.T) {
				standsDown(t, true, "BYRE_HEADLESS=1", "BYRE_ASSUME_TTY=")
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

// recordedHelpers are the external commands the first-run hooks reach for:
// the agent CLIs, the credential probes (grep, tail, jq, curl) and the store
// writes (flock, cp, mv, mkdir, mktemp, ln, rm, chmod).
var recordedHelpers = []string{"grok", "opencode", "claude", "jq", "curl", "grep", "tail", "head",
	"cat", "tr", "flock", "cp", "mv", "rm", "mkdir", "mktemp", "ln", "chmod", "timeout", "env",
	"dirname", "basename", "readlink", "stat"}

// A headless launch has nobody to answer a login, and its headless command
// owns its own bounded preflight, so these first-run hooks stand down before
// any credential work: with every helper they use replaced by a recorder on
// an otherwise empty PATH, a headless run records nothing and finds nothing
// missing, and writes nothing -- grok-shared-auth never promotes the box's
// login into the machine-wide store. Each fixture holds credential state the
// hook would act on; the no-terminal control shows the same fixture does
// reach the recorders, so the stand-down is what kept them quiet.
func TestFirstRunHooksDoNoCredentialWorkWhenHeadless(t *testing.T) {
	_, cat := testCat(t)
	write := func(t *testing.T, p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const pair = `{"scope":{"key":"at","refresh_token":"rt","oidc_issuer":"https://issuer.invalid"}}`
	for _, fx := range []struct {
		skill, script, shell string
		// fixture stages the credential state and returns the hook's env
		// and the paths a headless run must leave absent.
		fixture func(t *testing.T) (env, untouched []string)
	}{
		{"grok-shared-auth", "00-grok-shared-auth.sh", "sh", func(t *testing.T) ([]string, []string) {
			base, home := t.TempDir(), t.TempDir()
			write(t, filepath.Join(home, "auth.json"), pair)
			return []string{"BYRE_IDENTITY_BASE=" + base, "GROK_HOME=" + home, "XAI_API_KEY="},
				[]string{filepath.Join(base, "grok")}
		}},
		{"grok", "grok-login.sh", "sh", func(t *testing.T) ([]string, []string) {
			home := t.TempDir()
			write(t, filepath.Join(home, "auth.json"), "{}")
			return []string{"GROK_HOME=" + home, "XAI_API_KEY=", "GROK_AUTH_PROVIDER_COMMAND="}, nil
		}},
		{"opencode", "opencode-login.sh", "sh", func(t *testing.T) ([]string, []string) {
			data := physTempDir(t)
			write(t, filepath.Join(data, "opencode", "auth.json"), "{}")
			return []string{sharedAuthLibSeam(t, cat, "opencode"), "XDG_DATA_HOME=" + data,
				"ANTHROPIC_API_KEY=", "OPENCODE_API_KEY="}, nil
		}},
		{"claude-shared-auth", "firstrun.sh", "bash", func(t *testing.T) ([]string, []string) {
			base, cfg := t.TempDir(), t.TempDir()
			write(t, filepath.Join(base, "claude", "token"), "sk-ant-oat01-x\n")
			write(t, filepath.Join(cfg, ".credentials.json"), `{"claudeAiOauth":{}}`)
			return []string{"BYRE_IDENTITY_BASE=" + base, "CLAUDE_CONFIG_DIR=" + cfg},
				[]string{filepath.Join(cfg, ".claude.json")}
		}},
	} {
		t.Run(fx.skill, func(t *testing.T) {
			shell, err := exec.LookPath(fx.shell)
			if err != nil {
				t.Skipf("no %s: %v", fx.shell, err)
			}
			hook := filepath.Join(skillDir(t, cat, fx.skill), fx.script)
			run := func(t *testing.T, terminal ...string) (out, calls string, untouched []string) {
				t.Helper()
				bin := t.TempDir()
				log := filepath.Join(t.TempDir(), "calls")
				rec := "#!" + shell + "\nprintf '%s %s\\n' \"${0##*/}\" \"$*\" >> '" + log + "'\nexit 1\n"
				for _, name := range recordedHelpers {
					if err := os.WriteFile(filepath.Join(bin, name), []byte(rec), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				env, untouched := fx.fixture(t)
				env = append(append([]string{"PATH=" + bin}, env...), terminal...)
				return runHook(t, shell, hook, env...), stubCalls(t, log), untouched
			}
			t.Run("headless", func(t *testing.T) {
				out, calls, untouched := run(t, "BYRE_HEADLESS=1", "BYRE_ASSUME_TTY=1")
				if calls != "" {
					t.Errorf("a headless launch ran first-run helpers: %q", calls)
				}
				if strings.Contains(out, "not found") {
					t.Errorf("a headless launch looked up a command beyond the recorders: %s", out)
				}
				for _, p := range untouched {
					if _, err := os.Lstat(p); !os.IsNotExist(err) {
						t.Errorf("a headless launch wrote %s (%v)", p, err)
					}
				}
			})
			t.Run("control: an unattended launch reaches the helpers", func(t *testing.T) {
				if _, calls, _ := run(t, noTerminalEnv...); calls == "" {
					t.Fatal("the fixture never reaches a helper, so the headless case proves nothing")
				}
			})
		})
	}
}
