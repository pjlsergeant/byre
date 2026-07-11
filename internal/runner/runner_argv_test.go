package runner

import (
	"io"
	"strings"
	"testing"
)

// argvRunner returns a Runner whose stream seam records the full argv
// (engine name first) into the returned slice pointer.
func argvRunner(e Engine) (*Runner, *[]string) {
	var gotArgs []string
	r := &Runner{engine: e, stream: func(name string, args ...string) error {
		gotArgs = append([]string{name}, args...)
		return nil
	}}
	return r, &gotArgs
}

func TestBuildArgv(t *testing.T) {
	r, gotArgs := argvRunner(Docker)
	if err := r.Build("byre-img.v0", "/ctx/.byre/Dockerfile", "/ctx", false, nil); err != nil {
		t.Fatal(err)
	}
	want := "docker build -t byre-img.v0 -f /ctx/.byre/Dockerfile /ctx"
	if got := strings.Join(*gotArgs, " "); got != want {
		t.Fatalf("Build argv = %q, want %q", got, want)
	}
}

func TestBuildArgvNoCacheAndBuildArgs(t *testing.T) {
	r, gotArgs := argvRunner(Podman)
	err := r.Build("byre-img.v0", "/ctx/Dockerfile", "/ctx", true,
		[]string{"BYRE_UID=1234", "BYRE_GID=5678"})
	if err != nil {
		t.Fatal(err)
	}
	want := "podman build -t byre-img.v0 -f /ctx/Dockerfile --no-cache" +
		" --build-arg BYRE_UID=1234 --build-arg BYRE_GID=5678 /ctx"
	if got := strings.Join(*gotArgs, " "); got != want {
		t.Fatalf("Build argv = %q, want %q", got, want)
	}
}

func TestRunPassesArgsThrough(t *testing.T) {
	r, gotArgs := argvRunner(Docker)
	if err := r.Run([]string{"run", "--rm", "-it", "img.dev-1"}); err != nil {
		t.Fatal(err)
	}
	if want := "docker run --rm -it img.dev-1"; strings.Join(*gotArgs, " ") != want {
		t.Fatalf("Run argv = %q, want %q", strings.Join(*gotArgs, " "), want)
	}
}

func TestSeedVolumeArgv(t *testing.T) {
	r, gotArgs := argvRunner(Docker)
	if err := r.SeedVolume("byre-vol.claude-state", "/home/pete/.claude", "byre-img.v0", 1234, 5678); err != nil {
		t.Fatal(err)
	}
	want := "docker run --rm --entrypoint sh -u 0:0" +
		" --mount type=volume,source=byre-vol.claude-state,target=/dest" +
		" --mount type=bind,source=/home/pete/.claude,target=/src,readonly" +
		" byre-img.v0 -c cp -a /src/. /dest/ && chown -R 1234:5678 /dest"
	if got := strings.Join(*gotArgs, " "); got != want {
		t.Fatalf("SeedVolume argv = %q, want %q", got, want)
	}
}

func TestSeedFilesArgv(t *testing.T) {
	r, gotArgs := argvRunner(Docker)
	files := []string{".claude/settings.json", ".claude.json"}
	if err := r.SeedFiles("byre-vol.prefs", "/home/pete", files, "byre-img.v0", 1234, 5678); err != nil {
		t.Fatal(err)
	}
	args := *gotArgs
	// The file list must be positional argv, never interpolated into the script.
	if got, want := args[len(args)-3:], []string{"seed-prefs", ".claude/settings.json", ".claude.json"}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("trailing argv = %q, want %q", got, want)
	}
	script := args[len(args)-4]
	if strings.Contains(script, ".claude") {
		t.Fatalf("file paths leaked into the script: %q", script)
	}
	prefix := "docker run --rm --entrypoint sh -u 0:0 -e BYRE_OWNER=1234:5678" +
		" --mount type=volume,source=byre-vol.prefs,target=/dest" +
		" --mount type=bind,source=/home/pete,target=/src,readonly" +
		" byre-img.v0 -c "
	if got := strings.Join(args, " "); !strings.HasPrefix(got, prefix) {
		t.Fatalf("SeedFiles argv = %q, want prefix %q", got, prefix)
	}
}

func TestMigrateVolumeArgv(t *testing.T) {
	r, gotArgs := argvRunner(Docker)
	if err := r.MigrateVolume("byre-vol.old-name", "byre-vol.new-name", "byre-img.v0", 1234, 5678); err != nil {
		t.Fatal(err)
	}
	want := "docker run --rm --entrypoint sh -u 0:0" +
		" --mount type=volume,source=byre-vol.old-name,target=/from,readonly" +
		" --mount type=volume,source=byre-vol.new-name,target=/to" +
		" byre-img.v0 -c cp -a /from/. /to/ && chown -R 1234:5678 /to"
	if got := strings.Join(*gotArgs, " "); got != want {
		t.Fatalf("MigrateVolume argv = %q, want %q", got, want)
	}
}

func TestSeedLiteralArgvAndStdin(t *testing.T) {
	const content = "secret-token = \"abc.123\"\n"
	var gotArgs []string
	var gotStdin string
	r := &Runner{engine: Docker, streamIn: func(stdin io.Reader, name string, args ...string) error {
		gotArgs = append([]string{name}, args...)
		b, err := io.ReadAll(stdin)
		if err != nil {
			t.Fatal(err)
		}
		gotStdin = string(b)
		return nil
	}}
	if err := r.SeedLiteral("byre-vol.creds", ".codex/auth.json", content, "byre-img.v0", 1234, 5678); err != nil {
		t.Fatal(err)
	}
	want := "docker run --rm -i --entrypoint sh -u 0:0" +
		" -e BYRE_DEST=.codex/auth.json" +
		" --mount type=volume,source=byre-vol.creds,target=/dest" +
		` byre-img.v0 -c mkdir -p "/dest/$(dirname "$BYRE_DEST")" && cat > "/dest/$BYRE_DEST" && chown -R 1234:5678 /dest`
	if got := strings.Join(gotArgs, " "); got != want {
		t.Fatalf("SeedLiteral argv = %q, want %q", got, want)
	}
	if gotStdin != content {
		t.Fatalf("stdin = %q, want %q", gotStdin, content)
	}
	// Injection safety: the literal content must reach the container only via
	// stdin, never as part of the command line.
	for _, a := range gotArgs {
		if strings.Contains(a, content) {
			t.Fatalf("content leaked into argv element %q", a)
		}
	}
}

func TestNetnsInitArgv(t *testing.T) {
	var gotArgs []string
	r := &Runner{engine: Docker, capture: func(name string, args ...string) (string, error) {
		gotArgs = append([]string{name}, args...)
		return "", nil
	}}
	err := r.NetnsInit("byre-img.v0", "byre-myproj", "/usr/local/bin/byre-firewall",
		map[string]string{"BYRE_EGRESS": "grafana.com:443", "A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	// -u 0:0 + --cap-add NET_ADMIN live HERE, on the throwaway helper joining
	// the box's netns — never on the box itself. Env keys sorted.
	want := "docker run --rm -u 0:0 --net container:byre-myproj --cap-add NET_ADMIN" +
		" --entrypoint /usr/local/bin/byre-firewall -e A=1 -e BYRE_EGRESS=grafana.com:443 byre-img.v0"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Fatalf("NetnsInit argv = %q, want %q", got, want)
	}
}

func TestExecInputArgv(t *testing.T) {
	var gotArgs []string
	var gotStdin string
	r := &Runner{engine: Docker, captureIn: func(stdin io.Reader, name string, args ...string) (string, error) {
		b, _ := io.ReadAll(stdin)
		gotStdin = string(b)
		gotArgs = append([]string{name}, args...)
		return "/inbox/report.pdf\n", nil
	}}
	out, err := r.ExecInput("ctr1", 501, 20, strings.NewReader("payload"), "sh", "-c", "script", "byre-deliver", "report", ".pdf")
	if err != nil {
		t.Fatal(err)
	}
	if out != "/inbox/report.pdf\n" {
		t.Fatalf("ExecInput out = %q", out)
	}
	if gotStdin != "payload" {
		t.Fatalf("ExecInput stdin = %q, want payload", gotStdin)
	}
	want := "docker exec -i -u 501:20 ctr1 sh -c script byre-deliver report .pdf"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Fatalf("ExecInput argv = %q, want %q", got, want)
	}
}

func TestContainerLabels(t *testing.T) {
	r := &Runner{engine: Docker, capture: func(name string, args ...string) (string, error) {
		want := "docker inspect -f {{json .Config.Labels}} ctr1"
		if got := strings.Join(append([]string{name}, args...), " "); got != want {
			t.Fatalf("ContainerLabels argv = %q, want %q", got, want)
		}
		return `{"byre.project":"proj-abc123","byre.workdir":"proj-abc123"}` + "\n", nil
	}}
	labels, err := r.ContainerLabels("ctr1")
	if err != nil {
		t.Fatal(err)
	}
	if labels["byre.project"] != "proj-abc123" || labels["byre.workdir"] != "proj-abc123" {
		t.Fatalf("ContainerLabels = %v", labels)
	}
}

func TestContainerLabelsNull(t *testing.T) {
	r := &Runner{engine: Docker, capture: func(string, ...string) (string, error) {
		return "null\n", nil
	}}
	labels, err := r.ContainerLabels("ctr1")
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Fatalf("ContainerLabels(null) = %v, want empty", labels)
	}
}
