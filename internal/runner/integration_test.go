package runner

// Gated engine integration smoke: real docker/podman, run host-side with
//
//	BYRE_DOCKER_TESTS=1 go test ./internal/runner/ -run Integration -v
//
// It exercises the Runner methods whose argv the unit tests pin, against a
// live engine: volume lifecycle, the three seeding paths (ownership included),
// volume migration, and label queries. Needs network for the busybox pull on
// first run.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const smokeImage = "busybox"

func requireEngine(t *testing.T) *Runner {
	t.Helper()
	if os.Getenv("BYRE_DOCKER_TESTS") != "1" {
		t.Skip("set BYRE_DOCKER_TESTS=1 to run engine integration tests")
	}
	eng, err := Detect("auto", nil)
	if err != nil {
		t.Fatalf("BYRE_DOCKER_TESTS=1 but no engine: %v", err)
	}
	t.Logf("engine: %s", eng)
	return New(eng)
}

// smokeName returns a unique, recognizable name so parallel/aborted runs
// can't collide and leftovers are attributable.
func smokeName(t *testing.T, kind string) string {
	return fmt.Sprintf("byre-inttest-%s-%d-%d", kind, os.Getpid(), time.Now().UnixNano()%1_000_000)
}

// engineOut runs the engine CLI directly (test-side plumbing, deliberately not
// through the Runner under test) and returns trimmed stdout.
func engineOut(t *testing.T, r *Runner, args ...string) string {
	t.Helper()
	out, err := exec.Command(string(r.Engine()), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", r.Engine(), args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// catInVolume reads a file from a named volume via a throwaway container.
func catInVolume(t *testing.T, r *Runner, vol, path string) string {
	t.Helper()
	return engineOut(t, r, "run", "--rm", "-v", vol+":/v:ro", smokeImage, "cat", "/v/"+path)
}

// statInVolume returns "uid gid" of a path in a named volume.
func statInVolume(t *testing.T, r *Runner, vol, path string) string {
	t.Helper()
	return engineOut(t, r, "run", "--rm", "-v", vol+":/v:ro", smokeImage, "stat", "-c", "%u %g", "/v/"+path)
}

func TestIntegrationVolumeLifecycle(t *testing.T) {
	r := requireEngine(t)
	name := smokeName(t, "vol")
	if err := r.VolumeCreate(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.VolumeRemove(name) })

	if ok, err := r.VolumeExists(name); err != nil || !ok {
		t.Fatalf("VolumeExists(%s) = (%v, %v), want (true, nil)", name, ok, err)
	}
	vols, err := r.VolumesByPrefix("byre-inttest-vol-")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vols {
		found = found || v == name
	}
	if !found {
		t.Fatalf("VolumesByPrefix should list %s, got %v", name, vols)
	}
	if err := r.VolumeRemove(name); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.VolumeExists(name); ok {
		t.Fatalf("%s still exists after remove", name)
	}
}

func TestIntegrationSeedingAndMigration(t *testing.T) {
	r := requireEngine(t)
	uid, gid := os.Getuid(), os.Getgid()

	// SeedVolume: a host dir lands in the volume, chowned to uid:gid.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "cred.json"), []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	volA := smokeName(t, "seed")
	if err := r.VolumeCreate(volA); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.VolumeRemove(volA) })
	if err := r.SeedVolume(volA, src, smokeImage, Identity{UID: uid, GID: gid}); err != nil {
		t.Fatal(err)
	}
	if got := catInVolume(t, r, volA, "cred.json"); got != `{"k":"v"}` {
		t.Fatalf("seeded content = %q", got)
	}
	if got, want := statInVolume(t, r, volA, "cred.json"), fmt.Sprintf("%d %d", uid, gid); got != want {
		t.Fatalf("seeded ownership = %q, want %q", got, want)
	}

	// SeedLiteral: content arrives via stdin, parent dirs created, chowned.
	volB := smokeName(t, "lit")
	if err := r.VolumeCreate(volB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.VolumeRemove(volB) })
	const literal = "token = \"s3cr3t\"\n"
	if err := r.SeedLiteral(volB, "etc/deep/auth.toml", literal, smokeImage, Identity{UID: uid, GID: gid}); err != nil {
		t.Fatal(err)
	}
	if got := catInVolume(t, r, volB, "etc/deep/auth.toml"); got != strings.TrimSpace(literal) {
		t.Fatalf("literal content = %q", got)
	}

	// SeedFiles: only the listed subset is copied; a missing entry is skipped.
	prefSrc := t.TempDir()
	os.WriteFile(filepath.Join(prefSrc, "keep.json"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(prefSrc, "skip.json"), []byte("skip"), 0o644)
	volC := smokeName(t, "prefs")
	if err := r.VolumeCreate(volC); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.VolumeRemove(volC) })
	if err := r.SeedFiles(volC, prefSrc, []string{"keep.json", "not-there.json"}, smokeImage, Identity{UID: uid, GID: gid}); err != nil {
		t.Fatal(err)
	}
	if got := catInVolume(t, r, volC, "keep.json"); got != "keep" {
		t.Fatalf("curated file content = %q", got)
	}
	if ls := engineOut(t, r, "run", "--rm", "-v", volC+":/v:ro", smokeImage, "ls", "/v"); strings.Contains(ls, "skip.json") {
		t.Fatalf("unlisted file was copied: %s", ls)
	}

	// MigrateVolume: contents move volA -> fresh volD with ownership intact.
	volD := smokeName(t, "mig")
	if err := r.VolumeCreate(volD); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.VolumeRemove(volD) })
	if err := r.MigrateVolume(volA, volD, smokeImage, Identity{UID: uid, GID: gid}); err != nil {
		t.Fatal(err)
	}
	if got := catInVolume(t, r, volD, "cred.json"); got != `{"k":"v"}` {
		t.Fatalf("migrated content = %q", got)
	}
}

func TestIntegrationLabelQueries(t *testing.T) {
	r := requireEngine(t)
	label := "byre.inttest=" + smokeName(t, "lbl")
	id := engineOut(t, r, "run", "-d", "--rm", "--label", label, smokeImage, "sleep", "30")
	// rm -f, not stop: busybox sleep ignores SIGTERM, so a plain stop stalls
	// the suite for the full 10s grace period.
	t.Cleanup(func() { _ = exec.Command(string(r.Engine()), "rm", "-f", id).Run() })

	ids, err := r.RunningContainersByLabel(label)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || !strings.HasPrefix(id, ids[0]) {
		t.Fatalf("RunningContainersByLabel = %v, want prefix of %s", ids, id)
	}
	env, err := r.ContainerEnv(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if env["PATH"] == "" {
		t.Fatalf("ContainerEnv missing PATH: %v", env)
	}
	if none, err := r.RunningContainersByLabel("byre.inttest=absent"); err != nil || len(none) != 0 {
		t.Fatalf("absent label = (%v, %v), want none", none, err)
	}
}
