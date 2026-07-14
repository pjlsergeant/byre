package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
)

// mcpTestProject sets up a scratch BYRE_HOME + bootstrapped project dir and
// returns (projectDir, projectConfigPath, globalConfigPath, streams).
func mcpTestProject(t *testing.T) (string, string, string, Streams, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	dir := t.TempDir()
	paths, err := project.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	errw := &bytes.Buffer{}
	s := Streams{Out: &bytes.Buffer{}, Err: errw, In: strings.NewReader(""), TTY: false}
	return dir, filepath.Join(paths.Dir, config.ProjectConfigName), filepath.Join(home, "default.config"), s, errw
}

func TestMCPAddRemoteAndLocal(t *testing.T) {
	dir, projPath, _, s, errw := mcpTestProject(t)

	if err := MCPAdd(s, dir, false, "linear", []string{"https://mcp.linear.app/mcp"}, nil, []string{"auth.linear.app"}); err != nil {
		t.Fatalf("add remote: %v", err)
	}
	if !strings.Contains(errw.String(), "implies egress to mcp.linear.app:443") {
		t.Errorf("remote add must disclose implied egress: %s", errw)
	}
	if err := MCPAdd(s, dir, false, "github", []string{"github-mcp-server", "stdio"}, []string{"GITHUB_TOKEN"}, nil); err != nil {
		t.Fatalf("add local: %v", err)
	}

	cfg, err := config.ParseFile(projPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCPs) != 2 || cfg.MCPs[0].Name != "linear" || cfg.MCPs[1].Name != "github" {
		t.Fatalf("declarations = %+v", cfg.MCPs)
	}
	if cfg.MCPs[0].URL == "" || len(cfg.MCPs[0].Egress) != 1 {
		t.Fatalf("remote shape wrong: %+v", cfg.MCPs[0])
	}
	if cfg.MCPs[1].Command[0] != "github-mcp-server" || cfg.MCPs[1].Env[0] != "GITHUB_TOKEN" {
		t.Fatalf("local shape wrong: %+v", cfg.MCPs[1])
	}

	// add-or-update: same name replaces in place, no duplicate.
	if err := MCPAdd(s, dir, false, "github", []string{"gh2"}, nil, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	cfg, _ = config.ParseFile(projPath)
	if len(cfg.MCPs) != 2 || cfg.MCPs[1].Command[0] != "gh2" {
		t.Fatalf("update must replace in place: %+v", cfg.MCPs)
	}
	if !strings.Contains(errw.String(), "updated mcp github") {
		t.Errorf("update must say so: %s", errw)
	}

	// A bad declaration is refused before any write.
	if err := MCPAdd(s, dir, false, "Bad_Name", []string{"x"}, nil, nil); err == nil {
		t.Fatal("bad name must refuse")
	}
	if err := MCPAdd(s, dir, false, "creds", []string{"https://tok@h.example/mcp"}, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "credentials") {
		t.Fatalf("url credentials must refuse: %v", err)
	}
}

func TestMCPAddReopensClosure(t *testing.T) {
	dir, projPath, _, s, errw := mcpTestProject(t)
	if err := os.WriteFile(projPath, []byte("[[mcp]]\nname = \"!github\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MCPAdd(s, dir, false, "github", []string{"gh-mcp"}, nil, nil); err != nil {
		t.Fatalf("add over closure: %v", err)
	}
	cfg, _ := config.ParseFile(projPath)
	if len(cfg.MCPs) != 1 || cfg.MCPs[0].Name != "github" {
		t.Fatalf("closure must be re-opened by the add: %+v", cfg.MCPs)
	}
	if !strings.Contains(errw.String(), "closure was removed") {
		t.Errorf("re-open must be disclosed: %s", errw)
	}
}

func TestMCPAddGlobalTargetsDefaultConfig(t *testing.T) {
	dir, projPath, globalPath, s, _ := mcpTestProject(t)
	if err := MCPAdd(s, dir, true, "github", []string{"gh-mcp"}, nil, nil); err != nil {
		t.Fatalf("global add: %v", err)
	}
	g, err := config.ParseFile(globalPath)
	if err != nil || len(g.MCPs) != 1 {
		t.Fatalf("global config: %+v %v", g.MCPs, err)
	}
	if p, _ := config.ParseFile(projPath); len(p.MCPs) != 0 {
		t.Fatalf("project config must be untouched: %+v", p.MCPs)
	}
}

func TestMCPRemoveClosureSmart(t *testing.T) {
	dir, projPath, globalPath, s, errw := mcpTestProject(t)

	// Case 1: declared in the project layer only → delete, no closure.
	if err := MCPAdd(s, dir, false, "own", []string{"srv"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := MCPRemove(s, dir, false, "own"); err != nil {
		t.Fatalf("remove own: %v", err)
	}
	cfg, _ := config.ParseFile(projPath)
	if len(cfg.MCPs) != 0 {
		t.Fatalf("own-layer entry must delete cleanly: %+v", cfg.MCPs)
	}

	// Case 2: declared below (default.config) → the project remove writes
	// the closure, or the inherited entry would just resurface.
	if err := os.WriteFile(globalPath, []byte("[[mcp]]\nname = \"inherited\"\ncommand = [\"srv\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if err := MCPRemove(s, dir, false, "inherited"); err != nil {
		t.Fatalf("remove inherited: %v", err)
	}
	cfg, _ = config.ParseFile(projPath)
	if len(cfg.MCPs) != 1 || cfg.MCPs[0].Name != "!inherited" {
		t.Fatalf("inherited remove must write the closure: %+v", cfg.MCPs)
	}
	if !strings.Contains(errw.String(), "closed mcp inherited") {
		t.Errorf("closure path must be disclosed: %s", errw)
	}

	// Case 3: declared in the layer AND below → delete + closure.
	if err := os.WriteFile(projPath, []byte("[[mcp]]\nname = \"inherited\"\ncommand = [\"override\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if err := MCPRemove(s, dir, false, "inherited"); err != nil {
		t.Fatalf("remove overriding entry: %v", err)
	}
	cfg, _ = config.ParseFile(projPath)
	if len(cfg.MCPs) != 1 || cfg.MCPs[0].Name != "!inherited" {
		t.Fatalf("override remove must delete AND close: %+v", cfg.MCPs)
	}
	if !strings.Contains(errw.String(), "AND closed the name") {
		t.Errorf("double action must be disclosed: %s", errw)
	}

	// Case 4: nowhere → error.
	if err := MCPRemove(s, dir, false, "ghost"); err == nil || !strings.Contains(err.Error(), "nothing to remove") {
		t.Fatalf("ghost remove: %v", err)
	}

	// Already closed → friendly no-op.
	errw.Reset()
	if err := MCPRemove(s, dir, false, "inherited"); err != nil {
		t.Fatalf("re-remove closed: %v", err)
	}
	if !strings.Contains(errw.String(), "already closed") {
		t.Errorf("already-closed must be friendly: %s", errw)
	}
}

func TestMCPListRendersEffectiveSet(t *testing.T) {
	dir, projPath, _, s, _ := mcpTestProject(t)
	if err := os.WriteFile(projPath, []byte(`
[[mcp]]
name = "github"
command = ["gh-mcp"]
env = ["GITHUB_TOKEN"]

[[mcp]]
name = "!closed-one"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	s.Out = out
	if err := MCPList(s, dir); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "github — local: gh-mcp") || !strings.Contains(got, "GITHUB_TOKEN (NOT provided by this box)") {
		t.Errorf("list must render via the status line: %s", got)
	}
	if !strings.Contains(got, "no agent selected") {
		t.Errorf("delivery line missing: %s", got)
	}
	if !strings.Contains(got, "!closed-one") {
		t.Errorf("closures must list: %s", got)
	}

	// Empty set: a pointer, not silence.
	if err := os.WriteFile(projPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := MCPList(s, dir); err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if !strings.Contains(out.String(), "no MCP servers declared") {
		t.Errorf("empty-set pointer missing: %s", out)
	}
}
