package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
)

func writeLayerFile(t *testing.T, home, name, content string) {
	t.Helper()
	p := config.LayerPath(home, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLayerNewScaffoldsAndGates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)

	s, _, errBuf := testStreams("", false)
	if err := LayerNew(s, "torn"); err != nil {
		t.Fatal(err)
	}
	path := config.LayerPath(home, "torn")
	if !strings.Contains(errBuf.String(), path) {
		t.Errorf("LayerNew should print the created path, got: %s", errBuf.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The stub must parse as a valid (empty) layer.
	if _, err := config.ParseLayerBody(raw); err != nil {
		t.Errorf("stub does not parse as a layer: %v", err)
	}

	// Existing layer: refuse rather than overwrite.
	s2, _, _ := testStreams("", false)
	if err := LayerNew(s2, "torn"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("re-creating an existing layer must refuse, got: %v", err)
	}

	// Bundled bare names are gated at creation (the commands package wires
	// the real bundled catalog, so "go" is a live alias here).
	s3, _, _ := testStreams("", false)
	if err := LayerNew(s3, "go"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("bundled name must be refused with a reason, got: %v", err)
	}

	// Retired names are deliberately NOT reserved (layers are a new
	// namespace; ruled 2026-07-16): "codereview" is a legal layer.
	s5, _, _ := testStreams("", false)
	if err := LayerNew(s5, "codereview"); err != nil {
		t.Errorf("retired name must be a legal layer name, got: %v", err)
	}

	// Name grammar is enforced before any path is built.
	s4, _, _ := testStreams("", false)
	if err := LayerNew(s4, "../evil"); err == nil {
		t.Error("path-shaped layer name must be refused")
	}
}

func TestLayerListShowsChainAndBroken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)

	writeLayerFile(t, home, "torn", "apt = [\"jq\"]\n")
	writeLayerFile(t, home, "torn-frontend", "extends = \"torn\"\n")
	writeLayerFile(t, home, "orphan", "extends = \"missing\"\n")

	s, out, _ := testStreams("", false)
	if err := LayerList(s); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "torn") {
		t.Errorf("list missing torn: %s", text)
	}
	if !strings.Contains(text, "extends torn") {
		t.Errorf("list should show the parent pointer: %s", text)
	}
	if !strings.Contains(text, "BROKEN") || !strings.Contains(text, config.LayerPath(home, "missing")) {
		t.Errorf("broken layer should be flagged with the dangling path: %s", text)
	}
}

func TestLayerListEmpty(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	s, out, errBuf := testStreams("", false)
	if err := LayerList(s); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" || !strings.Contains(errBuf.String(), "byre layer new") {
		t.Errorf("empty list should hint at layer new on stderr, got out=%q err=%q", out.String(), errBuf.String())
	}
}

func TestLayerValidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)

	writeLayerFile(t, home, "torn", "")
	writeLayerFile(t, home, "torn-frontend", "extends = \"torn\"\n")

	// One layer: reports the chain root-first.
	s, _, errBuf := testStreams("", false)
	if err := LayerValidate(s, "torn-frontend"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "torn -> torn-frontend") {
		t.Errorf("validate should print the chain root-first, got: %s", errBuf.String())
	}

	// All layers: ok.
	s2, _, err2 := testStreams("", false)
	if err := LayerValidate(s2, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(err2.String(), "2 layer(s) ok") {
		t.Errorf("validate-all summary missing, got: %s", err2.String())
	}

	// A broken layer fails validate-all with its reason.
	writeLayerFile(t, home, "broken", "template = \"go\"\n")
	s3, _, err3 := testStreams("", false)
	if err := LayerValidate(s3, ""); err == nil {
		t.Fatal("validate-all with a broken layer must fail")
	}
	if !strings.Contains(err3.String(), "template is not allowed in a layer file") {
		t.Errorf("broken reason missing, got: %s", err3.String())
	}
}

func TestStatusRendersExtendsChain(t *testing.T) {
	var plain, chained strings.Builder
	renderStatus(&plain, statusInfo{ID: "x", Agent: "claude"})
	if strings.Contains(plain.String(), "Extends") {
		t.Errorf("no chain: no Extends row expected:\n%s", plain.String())
	}
	renderStatus(&chained, statusInfo{ID: "x", Agent: "claude", Chain: []string{"torn", "torn-frontend"}})
	if !strings.Contains(chained.String(), "torn -> torn-frontend -> project") {
		t.Errorf("Extends row should print the chain root-first:\n%s", chained.String())
	}
}

// End-to-end: Status reads the pointer back off the raw project layer (the
// resolved config no longer carries it) and renders the chain.
func TestStatusPopulatesExtendsChain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	proj := t.TempDir()

	writeLayerFile(t, home, "torn", "")
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, config.ProjectConfigName), []byte("extends = \"torn\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, out, _ := testStreams("", false)
	if err := Status(s, proj, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "torn -> project") {
		t.Errorf("status should render the extends chain:\n%s", out.String())
	}
}

// Validation errors quote layer-file bytes (a file someone sent you); the
// print boundary must escape control characters so a hostile key can't
// forge output or drive the terminal.
func TestLayerValidateEscapesHostileBytes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BYRE_HOME", home)
	writeLayerFile(t, home, "evil", "\"\x1b[2Jkey\" = \"x\"\n")

	s, _, _ := testStreams("", false)
	err := LayerValidate(s, "evil")
	if err == nil {
		t.Fatal("unknown key must fail validate")
	}
	if strings.Contains(err.Error(), "\x1b") {
		t.Errorf("error text must not carry raw control bytes: %q", err.Error())
	}
}
