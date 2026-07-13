package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

func TestApplySockGroupsInjectsGroupAdd(t *testing.T) {
	f := &fakeRunner{probeGID: 989}
	params := runner.RunParams{
		Image: "img",
		Binds: []runner.BindMount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}},
	}
	var sf skills.File
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	sf.Runtime.Mounts = []config.Mount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	var buf bytes.Buffer
	gids := applySockGroups(f, &buf, "img", &params, res)
	if gids["/var/run/docker.sock"] != 989 {
		t.Fatalf("gids = %v", gids)
	}
	if len(params.GroupAdds) != 1 || params.GroupAdds[0] != 989 {
		t.Fatalf("GroupAdds = %v", params.GroupAdds)
	}
	if len(f.probes) != 1 || !strings.Contains(f.probes[0], "/var/run/docker.sock") {
		t.Fatalf("probe not called: %v", f.probes)
	}
	// Argv carries --group-add.
	argv := strings.Join(runner.CreateArgs(params), " ")
	if !strings.Contains(argv, "--group-add 989") {
		t.Fatalf("create argv missing group-add: %s", argv)
	}
}

func TestApplySockGroupsProbeFailureAttributed(t *testing.T) {
	f := &fakeRunner{probeErr: os.ErrPermission}
	params := runner.RunParams{
		Binds: []runner.BindMount{{Host: "/var/run/docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}},
	}
	var sf skills.File
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	var buf bytes.Buffer
	applySockGroups(f, &buf, "img", &params, res)
	if len(params.GroupAdds) != 0 {
		t.Fatalf("failed probe must not inject group-add: %v", params.GroupAdds)
	}
	if !strings.Contains(buf.String(), "docker-host") || !strings.Contains(buf.String(), "could not probe") {
		t.Fatalf("probe failure not attributed: %s", buf.String())
	}
}

func TestWarnSockSourcesMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such.sock")
	f := &fakeRunner{}
	params := runner.RunParams{
		Binds: []runner.BindMount{{Host: missing, Target: "/var/run/docker.sock", Mode: "rw"}},
	}
	var sf skills.File
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	var buf bytes.Buffer
	warnSockSources(f, &buf, params, res)
	out := buf.String()
	if !strings.Contains(out, "docker-host") || !strings.Contains(out, "missing") {
		t.Fatalf("missing source warning: %s", out)
	}
}

func TestWarnSockSourcesDesktopSuppressed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such.sock")
	f := &fakeRunner{desktop: true}
	params := runner.RunParams{
		Binds: []runner.BindMount{{Host: missing, Target: "/var/run/docker.sock", Mode: "rw"}},
	}
	var sf skills.File
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	var buf bytes.Buffer
	warnSockSources(f, &buf, params, res)
	if buf.Len() != 0 {
		t.Fatalf("Desktop must suppress host-stat warning: %s", buf.String())
	}
}

func TestWarnSockSourcesNotASocket(t *testing.T) {
	// A regular file at the source path.
	path := filepath.Join(t.TempDir(), "not-a-sock")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	params := runner.RunParams{
		Binds: []runner.BindMount{{Host: path, Target: "/var/run/docker.sock", Mode: "rw"}},
	}
	var sf skills.File
	sf.Runtime.SockGroups = []string{"/var/run/docker.sock"}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "docker-host", File: sf}}}
	var buf bytes.Buffer
	warnSockSources(f, &buf, params, res)
	if !strings.Contains(buf.String(), "not a socket") {
		t.Fatalf("not-a-socket warning: %s", buf.String())
	}
}
