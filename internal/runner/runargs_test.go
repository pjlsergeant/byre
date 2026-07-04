package runner

import (
	"strings"
	"testing"
)

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func lastIndexOf(s []string, v string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == v {
			return i
		}
	}
	return -1
}

func TestRunArgsCoreFlagsAndOrder(t *testing.T) {
	args := RunArgs(RunParams{
		Image:           "byre-abc",
		Name:            "byre-abc",
		Labels:          []string{"byre.project=abc"},
		WorkspaceHost:   "/home/me/proj",
		WorkspaceTarget: "/workspace",
		Env:             map[string]string{"BYRE_UID": "1000", "AAA": "1"},
		Binds:           []BindMount{{Host: "/data", Target: "/data"}},
		Volumes:         []NamedVolume{{Name: "byre-abc-cache", Target: "/cache"}},
		RunArgs:         []string{"--cap-add=SYS_PTRACE", "--label", "byre.project=spoof"},
		Command:         []string{"bash", "-l"},
	})
	joined := strings.Join(args, " ")

	if args[0] != "run" || indexOf(args, "--rm") < 0 || indexOf(args, "-i") < 0 {
		t.Fatalf("missing core flags: %v", args)
	}
	if indexOf(args, "-t") >= 0 {
		t.Errorf("-t should not be present when TTY is unset: %v", args)
	}
	if i := indexOf(args, "--name"); i < 0 || args[i+1] != "byre-abc" {
		t.Errorf("container --name missing: %v", args)
	}
	if !strings.Contains(joined, "type=bind,source=/home/me/proj,target=/workspace") {
		t.Errorf("workspace bind missing: %v", args)
	}
	if !strings.Contains(joined, "type=bind,source=/data,target=/data,readonly") {
		t.Errorf("bind should default to readonly: %v", args)
	}
	if !strings.Contains(joined, "type=volume,source=byre-abc-cache,target=/cache") {
		t.Errorf("named volume missing: %v", args)
	}

	// run_args must come before the image. (The image string also appears as
	// the --name value, so use the last occurrence for the image position.)
	capIdx := indexOf(args, "--cap-add=SYS_PTRACE")
	img := lastIndexOf(args, "byre-abc")
	if capIdx < 0 || capIdx > img {
		t.Errorf("run_args should appear before the image: %v", args)
	}

	// The identity --label is re-asserted immediately before the image, so it
	// wins over the spoof label injected via run_args.
	if img < 2 || args[img-2] != "--label" || args[img-1] != "byre.project=abc" {
		t.Errorf("identity label not re-asserted just before image: %v", args)
	}
	spoof := indexOf(args, "byre.project=spoof")
	if spoof < 0 || spoof > img-1 {
		t.Errorf("spoof label should precede the re-asserted byre label: %v", args)
	}

	// Command follows the image at the very end.
	if got := args[img+1:]; len(got) != 2 || got[0] != "bash" || got[1] != "-l" {
		t.Errorf("command should follow image at the end: %v", got)
	}
}

func TestRunArgsTTY(t *testing.T) {
	// TTY=false (the CI/non-interactive default): -i present, -t absent.
	off := RunArgs(RunParams{Image: "img"})
	if indexOf(off, "-i") < 0 {
		t.Errorf("-i should always be present: %v", off)
	}
	if indexOf(off, "-t") >= 0 {
		t.Errorf("-t should be absent when TTY is false: %v", off)
	}

	// TTY=true (an actual interactive terminal): both -i and -t present.
	on := RunArgs(RunParams{Image: "img", TTY: true})
	if indexOf(on, "-i") < 0 || indexOf(on, "-t") < 0 {
		t.Errorf("-i and -t should both be present when TTY is true: %v", on)
	}
}

func TestRunArgsEnvSortedAndDeterministic(t *testing.T) {
	p := RunParams{Image: "img", Env: map[string]string{"B": "2", "A": "1", "C": "3"}}
	a1 := strings.Join(RunArgs(p), " ")
	a2 := strings.Join(RunArgs(p), " ")
	if a1 != a2 {
		t.Fatal("RunArgs not deterministic")
	}
	if strings.Index(a1, "A=1") > strings.Index(a1, "B=2") {
		t.Errorf("env not sorted: %s", a1)
	}
}

func TestRunArgsExplicitBindMode(t *testing.T) {
	args := RunArgs(RunParams{Image: "img", Binds: []BindMount{{Host: "/h", Target: "/t", Mode: "rw"}}})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "type=bind,source=/h,target=/t") {
		t.Errorf("rw bind missing: %v", args)
	}
	if strings.Contains(joined, "/h,target=/t,readonly") {
		t.Errorf("rw bind should not be readonly: %v", args)
	}
}

func TestExecArgs(t *testing.T) {
	args := execArgs("abc123", 501, 20, "/workspace",
		map[string]string{"HOME": "/home/dev", "CODEX_HOME": "/home/dev/.codex"}, true,
		"bash", "-l")
	joined := strings.Join(args, " ")
	want := "exec -i -t -u 501:20 -w /workspace -e CODEX_HOME=/home/dev/.codex -e HOME=/home/dev abc123 bash -l"
	if joined != want {
		t.Fatalf("execArgs wrong:\n got: %s\nwant: %s", joined, want)
	}
}

func TestExecArgsNoEnv(t *testing.T) {
	args := execArgs("c1", 1000, 1000, "/workspace", nil, true, "bash", "-l")
	want := "exec -i -t -u 1000:1000 -w /workspace c1 bash -l"
	if strings.Join(args, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(args, " "), want)
	}
}

func TestExecArgsNoTTY(t *testing.T) {
	args := execArgs("c1", 1000, 1000, "/workspace", nil, false, "bash", "-l")
	want := "exec -i -u 1000:1000 -w /workspace c1 bash -l"
	if strings.Join(args, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(args, " "), want)
	}
}

func TestParseEnvLines(t *testing.T) {
	env := parseEnvLines("PATH=/usr/bin\nBYRE_UID=501\nBYRE_GID=20\nMALFORMED\n=noKey\n")
	if env["BYRE_UID"] != "501" || env["BYRE_GID"] != "20" || env["PATH"] != "/usr/bin" {
		t.Fatalf("parse wrong: %+v", env)
	}
	if _, ok := env["MALFORMED"]; ok {
		t.Errorf("a line without '=' must be skipped: %+v", env)
	}
	if len(env) != 3 {
		t.Errorf("empty-key line must be skipped; got %+v", env)
	}
}

func TestContainerEnvUsesCaptureSeam(t *testing.T) {
	r := New(Docker)
	r.capture = func(name string, args ...string) (string, error) {
		return "BYRE_UID=1000\nBYRE_GID=1000\nCODEX_HOME=/home/dev/.codex\n", nil
	}
	env, err := r.ContainerEnv("abc")
	if err != nil {
		t.Fatal(err)
	}
	if env["BYRE_UID"] != "1000" || env["CODEX_HOME"] != "/home/dev/.codex" {
		t.Fatalf("ContainerEnv parse wrong: %+v", env)
	}
}

func TestPortSpec(t *testing.T) {
	// Publications arrive normalized (interface + host always set) — the
	// upstream defaulting lives in commands' normalizePort.
	cases := []struct {
		p    PortPublish
		want string
	}{
		{PortPublish{Interface: "127.0.0.1", Host: 8080, Container: 8080}, "127.0.0.1:8080:8080"},
		{PortPublish{Interface: "0.0.0.0", Host: 8080, Container: 80}, "0.0.0.0:8080:80"},
	}
	for _, c := range cases {
		if got := portSpec(c.p); got != c.want {
			t.Errorf("portSpec(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestRunArgsPublishesPorts(t *testing.T) {
	args := RunArgs(RunParams{
		Image: "img",
		Ports: []PortPublish{{Interface: "127.0.0.1", Host: 8080, Container: 8080}},
	})
	var found bool
	for i, a := range args {
		if a == "-p" && i+1 < len(args) && args[i+1] == "127.0.0.1:8080:8080" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected -p 127.0.0.1:8080:8080 in %v", args)
	}
}
