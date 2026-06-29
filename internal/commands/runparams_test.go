package commands

import (
	"strings"
	"testing"

	"byre/internal/config"
	"byre/internal/project"
	"byre/internal/runner"
	"byre/internal/skills"
)

func TestRunParamsRunArgsAndCapsPrecedence(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{RunArgs: []string{"--project-arg"}}
	res := skills.Resolved{
		RunArgs: []string{"--skill-arg"},
		Caps:    []string{"SYS_PTRACE"},
		Env:     map[string]string{"SKILLENV": "1"},
	}
	p, err := runParams(paths, cfg, res, "byre-x", "byre-x", "byre.project=x", false)
	if err != nil {
		t.Fatal(err)
	}

	// Project run_args must come AFTER skill run_args, so the project escape
	// hatch wins last.
	si := indexOf(p.RunArgs, "--skill-arg")
	pi := indexOf(p.RunArgs, "--project-arg")
	if si < 0 || pi < 0 || si > pi {
		t.Errorf("project run_args should follow skill run_args: %v", p.RunArgs)
	}
	if len(p.Caps) != 1 || p.Caps[0] != "SYS_PTRACE" {
		t.Errorf("skill caps not threaded: %v", p.Caps)
	}
	if p.Env["SKILLENV"] != "1" {
		t.Errorf("skill env not threaded: %v", p.Env)
	}
	// Sanity: the assembled argv keeps that ordering through to docker run.
	argv := strings.Join(runner.RunArgs(p), " ")
	if strings.Index(argv, "--skill-arg") > strings.Index(argv, "--project-arg") {
		t.Errorf("argv ordering wrong: %s", argv)
	}
}

func TestRunParamsSelfEditMount(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	paths, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Without --self-edit, no ~/.byre bind.
	p, err := runParams(paths, config.Config{}, skills.Resolved{}, "n", "i", "l", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range p.Binds {
		if b.Target == selfEditTarget {
			t.Fatalf("self-edit mount present without the flag: %+v", b)
		}
	}

	// With --self-edit, the host ~/.byre is bound rw at the dev home.
	p, err = runParams(paths, config.Config{}, skills.Resolved{}, "n", "i", "l", true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range p.Binds {
		if b.Target == selfEditTarget {
			found = true
			if b.Host != paths.Dir || b.Mode != "rw" {
				t.Fatalf("self-edit mount should be this project's store dir rw, got %+v", b)
			}
		}
	}
	if !found {
		t.Fatalf("--self-edit should add a %s mount: %+v", selfEditTarget, p.Binds)
	}
	// And it must reach the docker argv as a writable bind (no readonly). The
	// launcher detects self-edit from this mount (the byre.config it exposes), so
	// no separate env signal is needed.
	argv := strings.Join(runner.RunArgs(p), " ")
	if !strings.Contains(argv, "target="+selfEditTarget) || strings.Contains(argv, "target="+selfEditTarget+",readonly") {
		t.Fatalf("self-edit bind should be rw in argv: %s", argv)
	}
}

func TestStatusRendersSelfEditGrant(t *testing.T) {
	var off, on strings.Builder
	RenderStatus(&off, StatusInfo{ID: "x", Agent: "claude"})
	if strings.Contains(off.String(), "Self-edit") {
		t.Errorf("self-edit line shown without the grant:\n%s", off.String())
	}
	RenderStatus(&on, StatusInfo{ID: "x", Agent: "claude", SelfEdit: "/home/u/.byre"})
	s := on.String()
	if !strings.Contains(s, "Self-edit") || !strings.Contains(s, "GRANT via --self-edit") || !strings.Contains(s, "(rw)") {
		t.Errorf("self-edit grant not announced:\n%s", s)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
