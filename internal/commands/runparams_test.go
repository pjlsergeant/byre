package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"os"

	"github.com/pjlsergeant/byre/internal/skills"
)

func TestRunParamsRunArgsAndCapsPrecedence(t *testing.T) {
	paths, _ := testPaths(t)

	cfg := config.Config{RunArgs: []string{"--project-arg"}}
	var sf skills.File
	sf.Runtime.RunArgs = []string{"--skill-arg"}
	sf.Runtime.Caps = []string{"SYS_PTRACE"}
	sf.Runtime.Env = map[string]string{"SKILLENV": "1"}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "s", File: sf}}}
	p, err := runParams(paths, combine(cfg, res), "byre-x", false, false, hostIdentity())
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

// A machine-scoped volume mounts under its uid-qualified machine name (no
// project id) while project-scoped siblings keep the historical name — the
// wiring behind ADR 0017's identity volumes.
func TestRunParamsMachineScopedVolumeName(t *testing.T) {
	paths, _ := testPaths(t)
	var sf skills.File
	sf.Volumes = []config.Volume{
		{Name: "claude-identity", Role: "state", Target: "/home/dev/.byre-identity/claude", Scope: "machine"},
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}
	res := skills.Resolved{Skills: []skills.Skill{{Name: "s", File: sf}}}
	p, err := runParams(paths, combine(config.Config{}, res), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	want := machineVolumeName(os.Getuid(), "claude-identity")
	var haveMachine, haveProject bool
	for _, v := range p.Volumes {
		if v.Name == want && v.Target == "/home/dev/.byre-identity/claude" {
			haveMachine = true
		}
		if v.Name == volumeName(paths.ID, ".claude") {
			haveProject = true
		}
	}
	if !haveMachine {
		t.Errorf("machine-scoped volume not mounted under %q: %+v", want, p.Volumes)
	}
	if !haveProject {
		t.Errorf("project-scoped volume lost its historical name: %+v", p.Volumes)
	}
}

func TestRunParamsSkipsDisabledMounts(t *testing.T) {
	paths, _ := testPaths(t)
	cfg := config.Config{Mounts: []config.Mount{
		{Host: "/live", Target: "/live", Mode: "rw"},
		// The disabled entry's host path is one expandHostPath would REJECT
		// (relative) — proving the skip happens before expansion, so a mount
		// whose host path is currently bogus can be switched off harmlessly.
		{Host: "not-absolute", Target: "/off", Mode: "rw", Disabled: true},
	}}
	p, err := runParams(paths, combine(cfg, skills.Resolved{}), "i", false, false, hostIdentity())
	if err != nil {
		t.Fatalf("disabled mount must not block runParams: %v", err)
	}
	for _, b := range p.Binds {
		if b.Target == "/off" {
			t.Fatalf("disabled mount produced a bind: %+v", b)
		}
	}
	found := false
	for _, b := range p.Binds {
		if b.Target == "/live" {
			found = true
		}
	}
	if !found {
		t.Fatalf("enabled mount missing from binds: %+v", p.Binds)
	}
}

func TestRunParamsSelfEditMount(t *testing.T) {
	paths, _ := testPaths(t)

	// Without --self-edit, no ~/.byre bind.
	p, err := runParams(paths, combine(config.Config{}, skills.Resolved{}), "i", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range p.Binds {
		if b.Target == selfEditTarget {
			t.Fatalf("self-edit mount present without the flag: %+v", b)
		}
	}

	// With --self-edit, the host ~/.byre is bound rw at the dev home.
	p, err = runParams(paths, combine(config.Config{}, skills.Resolved{}), "i", true, false, hostIdentity())
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
	// Named layers are OUTSIDE the self-edit writable set: a layer propagates
	// into every extending project's sandbox, so a boxed agent must never be
	// able to edit one. The writable grant is this project's store dir alone;
	// the layers dir must not appear in any bind (the escape hatch is an
	// explicit rw mount of ~/.byre, a visible grant that documents itself).
	layers := config.LayersDir(paths.Home)
	for _, b := range p.Binds {
		if b.Host == layers || strings.HasPrefix(b.Host, layers+string(filepath.Separator)) {
			t.Fatalf("--self-edit must not bind the layers dir: %+v", b)
		}
	}
}

func TestRunParamsWorktreeMountsAndLabels(t *testing.T) {
	paths := project.Paths{
		ID:         "byre-main-000000",
		Canonical:  "/home/me/main",
		WorkDir:    "/home/me/wt",
		WorktreeID: "byre-wt-111111",
		IsWorktree: true,
		// Target is the git-recorded path; the source is the symlink-resolved
		// host path. They differ here (a symlinked recorded path) to pin that
		// the bind uses the resolved source but the recorded target.
		CommonGitDir:     "/home/me/main/.git",
		CommonGitDirHost: "/real/main/.git",
	}
	p, err := runParams(paths, combine(config.Config{}, skills.Resolved{}), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	// Container name + labels: name keyed on the worktree id; both project and
	// workdir labels present so lifecycle (project) and single-session (workdir)
	// queries both resolve.
	if p.Name != "byre-byre-wt-111111" {
		t.Errorf("container name = %q, want worktree-keyed", p.Name)
	}
	if got := strings.Join(p.Labels, " "); !strings.Contains(got, "byre.project=byre-main-000000") || !strings.Contains(got, "byre.workdir=byre-wt-111111") {
		t.Errorf("labels missing project/workdir: %v", p.Labels)
	}
	// Workspace bind is the worktree, not the main tree.
	if p.WorkspaceHost != "/home/me/wt" {
		t.Errorf("workspace host = %q, want the worktree dir", p.WorkspaceHost)
	}
	// Git binds (rw): the worktree is same-path; the common git dir mounts its
	// symlink-resolved SOURCE at the git-recorded TARGET.
	var sawWorktree, sawCommon bool
	for _, b := range p.Binds {
		switch b.Target {
		case "/home/me/wt":
			sawWorktree = true
			if b.Host != b.Target || b.Mode != "rw" {
				t.Errorf("worktree bind should be same-path rw, got %+v", b)
			}
		case "/home/me/main/.git":
			sawCommon = true
			if b.Host != "/real/main/.git" || b.Mode != "rw" {
				t.Errorf("common git bind should mount the resolved source at the recorded target, got %+v", b)
			}
		}
	}
	if !sawWorktree {
		t.Error("missing same-path git bind for the worktree")
	}
	if !sawCommon {
		t.Error("missing common git dir bind")
	}

	// A plain project adds neither git bind and keeps name/labels keyed on the id.
	t.Setenv("BYRE_HOME", t.TempDir())
	plain, err := project.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pp, err := runParams(plain, combine(config.Config{}, skills.Resolved{}), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if pp.WorkspaceHost != plain.Canonical || pp.Name != "byre-"+plain.ID {
		t.Errorf("plain project wiring changed: host=%q name=%q", pp.WorkspaceHost, pp.Name)
	}
}

func TestStatusRendersSelfEditGrant(t *testing.T) {
	var off, on strings.Builder
	renderStatus(&off, statusInfo{ID: "x", Agent: "claude"})
	if strings.Contains(off.String(), "Self-edit") {
		t.Errorf("self-edit line shown without the grant:\n%s", off.String())
	}
	renderStatus(&on, statusInfo{ID: "x", Agent: "claude", SelfEdit: "/home/u/.byre"})
	s := on.String()
	if !strings.Contains(s, "Self-edit") || !strings.Contains(s, "GRANT via --self-edit") || !strings.Contains(s, "(rw)") {
		t.Errorf("self-edit grant not announced:\n%s", s)
	}
}

func TestStatusRendersWorktreeLine(t *testing.T) {
	var plain, wt strings.Builder
	renderStatus(&plain, statusInfo{ID: "x", Canonical: "/home/me/proj"})
	if strings.Contains(plain.String(), "Worktree of") {
		t.Errorf("plain project should not show a worktree line:\n%s", plain.String())
	}
	renderStatus(&wt, statusInfo{ID: "x", Canonical: "/home/me/wt", WorktreeOf: "/home/me/main"})
	s := wt.String()
	if !strings.Contains(s, "Worktree of") || !strings.Contains(s, "/home/me/main") || !strings.Contains(s, "inherited") {
		t.Errorf("worktree relationship not shown:\n%s", s)
	}
	if !strings.Contains(s, "/home/me/wt -> /workspace") {
		t.Errorf("project row should show the worktree as the /workspace source:\n%s", s)
	}
}

func TestStatusRendersSiblingSessions(t *testing.T) {
	// No siblings -> no sibling-sessions line (the plain-project common case).
	var none strings.Builder
	renderStatus(&none, statusInfo{ID: "x", Canonical: "/p"})
	if strings.Contains(none.String(), "other session") {
		t.Errorf("should not show sibling sessions when there are none:\n%s", none.String())
	}
	// Siblings present -> a line naming them, so status doesn't imply nothing is
	// running while reset/forget (project label) would refuse.
	var kin strings.Builder
	renderStatus(&kin, statusInfo{ID: "x", Canonical: "/p", SiblingSessions: []string{"abc123def456", "789beefcafe0"}})
	s := kin.String()
	if !strings.Contains(s, "2 other session") || !strings.Contains(s, "abc123def456") || !strings.Contains(s, "789beefcafe0") {
		t.Errorf("sibling sessions not surfaced:\n%s", s)
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

func TestRunParamsProjectAndWorktreeEnv(t *testing.T) {
	paths, _ := testPaths(t)
	p, err := runParams(paths, combine(config.Config{}, skills.Resolved{}), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["BYRE_PROJECT"] != paths.ID {
		t.Errorf("BYRE_PROJECT = %q, want %q", p.Env["BYRE_PROJECT"], paths.ID)
	}
	if p.Env["BYRE_WORKTREE"] != paths.WorktreeID {
		t.Errorf("BYRE_WORKTREE = %q, want %q", p.Env["BYRE_WORKTREE"], paths.WorktreeID)
	}
	// Plain project: WorktreeID == ID.
	if paths.WorktreeID != paths.ID {
		t.Fatalf("testPaths should be a plain project; got ID=%q WorktreeID=%q", paths.ID, paths.WorktreeID)
	}
}

func TestRunParamsWorktreeDistinctEnv(t *testing.T) {
	// Worktree paths: WorktreeID differs from ID so compose can key on it.
	paths := project.Paths{
		ID: "projid", WorktreeID: "wtid", WorkDir: "/wt", Canonical: "/main",
		Home: t.TempDir(), Dir: t.TempDir(),
	}
	p, err := runParams(paths, combine(config.Config{}, skills.Resolved{}), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["BYRE_PROJECT"] != "projid" || p.Env["BYRE_WORKTREE"] != "wtid" {
		t.Fatalf("env: PROJECT=%q WORKTREE=%q", p.Env["BYRE_PROJECT"], p.Env["BYRE_WORKTREE"])
	}
	if p.Env["BYRE_PROJECT"] == p.Env["BYRE_WORKTREE"] {
		t.Fatal("worktree must keep PROJECT and WORKTREE distinct")
	}
}
