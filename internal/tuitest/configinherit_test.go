package tuitest

// The scalar pickers' inherit rows follow the extends picker live, and a
// none chosen against a freshly inherited agent is what the next develop
// gets. Driven on a real pty because the bug was a paint-and-save
// interaction: the agent row kept its stale option list after the extends
// row moved, and the save wrote what that stale list meant.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestIntegrationTUIConfigNoneBeatsALayerAgentPickedMidSession(t *testing.T) {
	Require(t)
	store, env := storeEnv(t)
	// A named layer that sets the agent: the one cascade position that can
	// put an agent below a project (templates may not carry one, and
	// default.config's agent is an onboarding favourite resolution strips).
	layerDir := filepath.Join(store, "layers", "team")
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "layer.config"), []byte("agent = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	// Tall enough for the whole project form: the Agent row must be on
	// screen while the Extends row is driven, or its repaint proves nothing.
	s := Start(t, Opts{Env: env, Dir: proj, Rows: 60}, Binary(t), "config")
	s.WaitFor("EXTENDS")

	// Extends is the seventeenth field of the project editor's focus order
	// (pinned by TestProjectEditorFocusOrder in package configui).
	downs := make([]string, 16)
	for i := range downs {
		downs[i] = "Down"
	}
	s.Keys(downs...)
	// Left from none reaches the one layer. The proof is the AGENT row: it
	// grows the inherit row naming the layer's agent, on the spot.
	e := s.Keys("Left")
	s.WaitForAfter(e, "(inherit: claude)")

	// Up to Agent (ten rows: Instructions .. Engine, then Agent), and Left
	// from the inherit row lands on none. Keys are processed in order, so
	// the save that follows is a save of that choice.
	ups := make([]string, 10)
	for i := range ups {
		ups[i] = "Up"
	}
	s.Keys(ups...)
	s.Keys("Left")
	e = s.Keys("C-s")
	s.WaitForAfter(e, "Saved ✓")
	s.Keys("Escape")
	if st := s.WaitForExit(); st != 0 {
		t.Fatalf("exit = %d\n%s", st, s.CaptureNow())
	}

	// The file says the off-switch, beside the parent it was chosen against.
	matches, err := filepath.Glob(filepath.Join(store, "projects", "*", "byre.config"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("want one enrolled project config, got %v (err %v)", matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `agent = "none"`) || !strings.Contains(string(b), `extends = "team"`) {
		t.Fatalf("saved config must carry the explicit none and the parent:\n%s", b)
	}

	// And resolution agrees: status reads the same cascade develop does.
	cmd := exec.Command(Binary(t), "status")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "BYRE_HOME="+store)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !regexp.MustCompile(`(?m)^\s*Agent:\s+\(none\)`).Match(out) {
		t.Fatalf("status must resolve no agent after the explicit none:\n%s", out)
	}
}
