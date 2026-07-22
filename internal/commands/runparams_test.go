package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

func TestCheckContainedHostSource(t *testing.T) {
	// paths.WorkDir is canonical (project.Canonicalize), so mirror that here --
	// on macOS t.TempDir() is under a symlinked /var, and a lexical workDir
	// would defeat the whole containment check.
	canon := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	tree := canon(t.TempDir())    // stands in for the project tree (paths.WorkDir)
	outside := canon(t.TempDir()) // an unrelated host dir, e.g. where ~/.ssh lives

	mk := func(p string) string {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sym := func(target, link string) string {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return link
	}

	// An in-tree symlink escaping to an outside tree -- the attack.
	mk(filepath.Join(outside, "secrets"))
	escape := sym(filepath.Join(outside, "secrets"), filepath.Join(tree, "data"))
	// An in-tree symlink that stays in the tree -- benign (agent already has the tree).
	mk(filepath.Join(tree, "real"))
	inTreeLink := sym(filepath.Join(tree, "real"), filepath.Join(tree, "alias"))
	// An interior component escaping: tree/via -> outside, then .../via/x (which
	// exists as outside/x, so EvalSymlinks resolves it out of the tree).
	sym(outside, filepath.Join(tree, "via"))
	mk(filepath.Join(outside, "x"))

	// An ALIAS spelling of the project root: aliasRoot -> tree, and the escape
	// lives under it. Lexically aliasRoot/data is "outside" tree; canonically it
	// is the in-tree escape symlink, and must still be refused.
	aliasRoot := filepath.Join(t.TempDir(), "alias")
	sym(tree, aliasRoot)

	cases := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{"outside the tree is the user's choice", filepath.Join(outside, "anything"), false},
		{"plain in-tree dir is fine", mk(filepath.Join(tree, "plain")), false},
		{"in-tree path that does not exist is fine", filepath.Join(tree, "willbecreated"), false},
		{"in-tree symlink escaping the tree is refused", escape, true},
		{"in-tree symlink staying in the tree is fine", inTreeLink, false},
		{"interior symlink escaping the tree is refused", filepath.Join(tree, "via", "x"), true},
		{"alias spelling of the root still catches the escape", filepath.Join(aliasRoot, "data"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkContainedHostSource(tc.host, tree)
			if tc.wantErr != (err != nil) {
				t.Errorf("checkContainedHostSource(%q) err=%v, wantErr=%v", tc.host, err, tc.wantErr)
			}
		})
	}
}

// The in-tree judgment is by file IDENTITY (os.SameFile over the ancestor
// chain), not spelling — that is what makes a case-variant spelling on a
// case-insensitive filesystem (untestable here on ext4) classify correctly.
// Pin the mechanism with a symlink alias, and the lexical fallback for an
// unstattable workDir.
func TestInTreeByIdentity(t *testing.T) {
	tree := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(tree, alias); err != nil {
		t.Fatal(err)
	}
	if !inTreeByIdentity(tree, filepath.Join(alias, "sub")) {
		t.Error("an alias spelling through a symlinked ancestor must classify in-tree")
	}
	if inTreeByIdentity(tree, filepath.Dir(tree)) {
		t.Error("the tree's parent must not classify in-tree")
	}
	// workDir unstattable: degrade to the lexical judgment, not a panic or a
	// blanket false (which would skip the escape check for lexically-in-tree
	// spellings).
	gone := filepath.Join(t.TempDir(), "gone")
	if !inTreeByIdentity(gone, filepath.Join(gone, "sub")) {
		t.Error("lexical fallback must still classify a spelled-under path in-tree")
	}
}

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
	// The client pid label is how status tells an orphaned box (terminal
	// gone, box surviving) from a reachable session.
	if got := strings.Join(p.Labels, " "); !strings.Contains(got, "byre.client="+strconv.Itoa(os.Getpid())) {
		t.Errorf("labels missing the byre.client pid: %v", p.Labels)
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

// Siblings are named by workdir id — the bare container id said "something
// else is running" without saying which worktree (QA pass-2 finding).
func TestSiblingNamesUseWorkdirID(t *testing.T) {
	f := &fakeRunner{labels: map[string]string{workdirKey: "proj-wt1-abc123"}}
	got := siblingNames(f, []string{"mine00000000"}, []string{"mine00000000", "sib000000000"})
	want := []string{"proj-wt1-abc123 (sib000000000)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("siblingNames = %v, want %v", got, want)
	}
	// No label (older box, or lookup failure) falls back to the bare id.
	bare := &fakeRunner{}
	got = siblingNames(bare, nil, []string{"sib000000000"})
	if !reflect.DeepEqual(got, []string{"sib000000000"}) {
		t.Fatalf("label-less sibling should show its id: %v", got)
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

// Under an allowlist posture the box env carries BYRE_EGRESS — the same
// enforced union the netns helper gets — so the launcher can announce the
// allowlist in agent memory. No posture (open) and open-denylist boxes must
// NOT carry it: the launcher would announce a wall that isn't an allowlist.
func TestRunParamsEgressAnnouncementEnv(t *testing.T) {
	paths, _ := testPaths(t)

	var fw skills.File
	fw.Runtime.NetworkPosture = "deny-by-default"
	var ag skills.File
	ag.Runtime.Egress = []string{"api.anthropic.com"}
	res := skills.Resolved{Skills: []skills.Skill{
		{Name: "firewall", File: fw},
		{Name: "claude", File: ag},
	}}
	cfg := config.Config{Egress: []string{"github.com:443"}}
	p, err := runParams(paths, combine(cfg, res), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	got := p.Env["BYRE_EGRESS"]
	for _, want := range []string{"api.anthropic.com:443", "github.com:443"} {
		if !strings.Contains(got, want) {
			t.Errorf("BYRE_EGRESS missing %q: %q", want, got)
		}
	}

	// open-denylist: no allowlist exists, so no announcement env.
	var od skills.File
	od.Runtime.NetworkPosture = "open-denylist"
	p, err = runParams(paths, combine(config.Config{}, skills.Resolved{Skills: []skills.Skill{{Name: "fw-open", File: od}}}), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Env["BYRE_EGRESS"]; ok {
		t.Errorf("open-denylist box must not carry BYRE_EGRESS, got %q", v)
	}

	// No posture at all: same.
	p, err = runParams(paths, combine(config.Config{Egress: []string{"github.com"}}, skills.Resolved{}), "img", false, false, hostIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Env["BYRE_EGRESS"]; ok {
		t.Errorf("postureless box must not carry BYRE_EGRESS, got %q", v)
	}
}
