package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"byre/internal/gen"
	"byre/internal/project"
)

type fakeRootless struct {
	rootless bool
	err      error
}

func (f fakeRootless) IsRootlessPodman() (bool, error) { return f.rootless, f.err }

func TestVolumeName(t *testing.T) {
	const id = "proj-abc123"
	if got := VolumeName(id, "cache"); got != "byre-"+id+"-cache" {
		t.Errorf("VolumeName = %q, want byre-%s-cache", got, id)
	}
}

func TestWarnRootlessPodman(t *testing.T) {
	cases := []struct {
		name string
		c    fakeRootless
		warn bool
	}{
		{"rootless warns", fakeRootless{rootless: true}, true},
		{"rootful is quiet", fakeRootless{rootless: false}, false},
		{"detection error is quiet", fakeRootless{err: errors.New("boom")}, false},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		warnRootlessPodman(&buf, tc.c)
		if got := strings.Contains(buf.String(), "rootless Podman detected"); got != tc.warn {
			t.Errorf("%s: warned=%v, want %v (out=%q)", tc.name, got, tc.warn, buf.String())
		}
	}
}

func TestDockerfileWritesPersistsAndPrints(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	var out bytes.Buffer
	if err := Dockerfile(&out, proj); err != nil {
		t.Fatal(err)
	}

	// Printed bytes must equal the generator output...
	want := gen.Dockerfile(gen.Input{})
	if out.String() != want {
		t.Fatalf("printed output != generator output:\n%s", out.String())
	}

	// ...and the persisted file must match byte-for-byte.
	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(paths.Dockerfile)
	if err != nil {
		t.Fatalf("Dockerfile.generated not written: %v", err)
	}
	if string(onDisk) != want {
		t.Fatalf("persisted file != generator output:\n%s", string(onDisk))
	}
}

func TestDockerfileOptOutPrintsHandWritten(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "Dockerfile"), []byte("FROM scratch\n# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The opt-out Dockerfile lives in the project; its byre.config lives host-side.
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "byre.config"), []byte("dockerfile = \"Dockerfile\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Dockerfile(&out, proj); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "opted out") || !strings.Contains(out.String(), "FROM scratch") {
		t.Fatalf("opt-out dockerfile output wrong:\n%s", out.String())
	}
}

func TestDockerfileHonorsByreHomeAndCollision(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()

	paths, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-claim this id with a different recorded path -> collision.
	if err := os.MkdirAll(paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PathRecord, []byte("/some/other/project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Dockerfile(&out, proj); err == nil {
		t.Fatal("expected collision error from Dockerfile, got nil")
	}
}

func TestShellArgQuoting(t *testing.T) {
	cases := map[string]string{
		"plain":                         "plain",
		"type=bind,source=/a,target=/b": "type=bind,source=/a,target=/b", // = and , stay bare
		"127.0.0.1:8080:8080":           "127.0.0.1:8080:8080",
		"has space":                     "'has space'",
		"a'b":                           `'a'\''b'`,
		"":                              "''",
	}
	for in, want := range cases {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %q, want %q", in, got, want)
		}
	}
}
