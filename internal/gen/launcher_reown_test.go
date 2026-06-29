package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLauncherReownStorage exercises the launcher's reown_storage function — the
// privileged re-own that runs as root before the agent drops to the host uid. It
// sources the REAL embedded launcher under bash (BYRE_LAUNCH_TEST=1 defines the
// functions and skips main), with `chown` stubbed on PATH (logs its args) and the
// mount table faked via BYRE_PROC_MOUNTS, so we can assert exactly which paths get
// re-owned without needing root or real bind mounts.
//
// The contract under test (the agent↔host boundary):
//   - byre's own storage IS re-owned: the dev home's own files + named volumes;
//   - a HOST bind mounted ANYWHERE under the home — an immediate child (the
//     --self-edit config) OR nested several levels deep — is pruned, never
//     chowned (the regression Codex caught: find -xdev prints a nested mount's
//     own directory, so chowning it would touch the host file);
//   - a HOST bind nested inside a named VOLUME is likewise pruned;
//   - an already-correctly-owned volume is skipped (idempotent);
//   - chown uses -h, so a symlink can't redirect the re-own onto a host path.
//
// This uses REAL `find` against a single-device temp tree and prunes by PATH
// (from the fake mount table), so the selection logic is exercised for real — no
// fake device numbers needed. The one fact still left to a host-side
// (BYRE_DOCKER_TESTS) run is that a real container bind actually appears in
// /proc/mounts under the home; that it does is standard docker/podman behaviour.
func TestLauncherReownStorage(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	// Home's own files.
	mustMkdir(t, filepath.Join(home, ".local", "bin"))
	mustMkdir(t, filepath.Join(home, ".config"))
	mustWrite(t, filepath.Join(home, ".bashrc"), "x")
	mustWrite(t, filepath.Join(home, ".local", "bin", "agent"), "x")
	// A symlink in the home pointing at a host path — find lists it; chown -h must
	// chown the link itself, never follow it to /etc/shadow.
	if err := os.Symlink("/etc/shadow", filepath.Join(home, ".config", "evil")); err != nil {
		t.Fatal(err)
	}
	// Mounts under the home: a state volume, the --self-edit HOST bind (immediate
	// child), and an already-owned volume.
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(home, ".byre-self"))
	mustWrite(t, filepath.Join(home, ".byre-self", "byre.config"), "HOSTSECRET")
	mustMkdir(t, filepath.Join(home, ".codexok"))
	// A HOST bind nested SEVERAL levels deep, under a same-device home subdir — the
	// case the old immediate-child device check missed. work/ is byre's own (must
	// be re-owned); work/secret is the host bind (must be pruned).
	mustMkdir(t, filepath.Join(home, "work"))
	mustWrite(t, filepath.Join(home, "work", "keep"), "x")
	mustMkdir(t, filepath.Join(home, "work", "secret"))
	mustWrite(t, filepath.Join(home, "work", "secret", "id_rsa"), "HOSTKEY")
	// A volume OUTSIDE the home (node_modules-style), itself containing a nested
	// HOST bind that must be pruned from the volume walk.
	nodeMods := filepath.Join(root, "workspace", "node_modules")
	mustMkdir(t, nodeMods)
	hostLib := filepath.Join(nodeMods, "hostlib")
	mustMkdir(t, hostLib)
	mustWrite(t, filepath.Join(hostLib, "x"), "HOSTLIB")

	// The baked volume list (what gen writes): .claude, .codexok, node_modules.
	volFile := filepath.Join(root, "volume-dirs")
	mustWrite(t, volFile, strings.Join([]string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".codexok"),
		nodeMods,
	}, "\n")+"\n")

	// The fake kernel mount table: every HOST bind / named volume, as the kernel
	// would list it (we only read the device + mountpoint fields).
	procMounts := filepath.Join(root, "proc-mounts")
	mounts := []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".byre-self"),
		filepath.Join(home, ".codexok"),
		filepath.Join(home, "work", "secret"),
		nodeMods,
		hostLib,
	}
	var pm strings.Builder
	for _, m := range mounts {
		pm.WriteString("tmpfs " + m + " tmpfs rw,relatime 0 0\n")
	}
	mustWrite(t, procMounts, pm.String())

	// Stub bin: chown logs its args; stat fakes uid/gid (needs_chown only) by
	// basename — .codexok is already runtime-owned (501:20), everything else built
	// as 1000.
	stubBin := filepath.Join(root, "bin")
	mustMkdir(t, stubBin)
	chownLog := filepath.Join(root, "chown.log")
	mustWrite(t, filepath.Join(stubBin, "chown"), "#!/usr/bin/env bash\necho \"$*\" >> \"$CHOWN_LOG\"\n")
	mustWrite(t, filepath.Join(stubBin, "stat"), `#!/usr/bin/env bash
fmt="$2"; p="$3"; base="${p##*/}"
case "$fmt" in
  %u) case "$base" in .codexok) echo 501;; *) echo 1000;; esac ;;
  %g) case "$base" in .codexok) echo 20;; *) echo 1000;; esac ;;
  *) echo 0 ;;
esac
`)
	mustChmodX(t, filepath.Join(stubBin, "chown"))
	mustChmodX(t, filepath.Join(stubBin, "stat"))

	// Write the REAL embedded launcher and a driver that sources it + runs reown.
	launcher := filepath.Join(root, "byre-launch")
	if err := os.WriteFile(launcher, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(root, "driver.sh")
	mustWrite(t, driver, "set -e\n. \""+launcher+"\"\nreown_storage\n")

	cmd := exec.Command(bash, driver)
	cmd.Env = append(os.Environ(),
		"BYRE_LAUNCH_TEST=1",
		"DEV_HOME="+home,
		"BYRE_VOLUME_DIRS="+volFile,
		"BYRE_PROC_MOUNTS="+procMounts,
		"BYRE_UID=501", "BYRE_GID=20",
		"CHOWN_LOG="+chownLog,
		"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reown_storage failed: %v\n%s", err, out)
	}

	logb, err := os.ReadFile(chownLog)
	if err != nil {
		t.Fatalf("no chown log: %v", err)
	}
	log := string(logb)

	// chown lines look like "-h 501:20 /p1 /p2 ..." (xargs may batch paths), so
	// match p as a whole whitespace-delimited token — not a substring, so
	// /home/work doesn't spuriously match /home/work/secret.
	chowned := func(p string) bool {
		for _, tok := range strings.Fields(log) {
			if tok == p {
				return true
			}
		}
		return false
	}

	// byre's own storage IS re-owned — including a deep same-device file next to a
	// pruned nested bind.
	for _, p := range []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".local", "bin", "agent"),
		filepath.Join(home, ".config"),
		filepath.Join(home, "work"),
		filepath.Join(home, "work", "keep"),
		filepath.Join(home, ".claude"), // volume under the home
		nodeMods,                       // volume outside the home
	} {
		if !chowned(p) {
			t.Errorf("expected re-own of %s\nlog:\n%s", p, log)
		}
	}

	// HOST binds are NEVER touched — the core boundary property — wherever they
	// sit: an immediate child of the home, nested deep under it, or nested inside
	// a named volume.
	for _, p := range []string{
		filepath.Join(home, ".byre-self"),     // --self-edit config (immediate child)
		filepath.Join(home, "work", "secret"), // nested deep under the home
		filepath.Join(home, "work", "secret", "id_rsa"),
		hostLib, // nested inside a volume
		filepath.Join(hostLib, "x"),
	} {
		if chowned(p) {
			t.Errorf("HOST bind path was chowned (host files!): %s\nlog:\n%s", p, log)
		}
	}

	// An already-correct volume is skipped (idempotent).
	if chowned(filepath.Join(home, ".codexok")) {
		t.Errorf("already-owned volume should not be re-chowned:\n%s", log)
	}
	// Every chown is -h, so a symlink can't redirect the re-own to its target.
	if !chowned(filepath.Join(home, ".config", "evil")) {
		t.Errorf("symlink itself should be chowned (as a link):\n%s", log)
	}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if line != "" && !strings.HasPrefix(line, "-h ") {
			t.Errorf("chown without -h (would dereference symlinks): %q", line)
		}
	}
}

// TestLauncherReownStorageIdempotentExitZero pins the regression that left a
// silent `exit status 1`: on the steady-state run every dir is ALREADY owned by
// the runtime user, so needs_chown returns non-zero for each and the volume loop
// short-circuits on its last iteration. reown_storage must still exit 0 — it is
// called bare under `set -e` in main, so a leaked non-zero kills the launcher
// before it ever execs the agent (no output, no shell, just "exit status 1").
func TestLauncherReownStorageIdempotentExitZero(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	mustMkdir(t, filepath.Join(home, ".config"))
	mustWrite(t, filepath.Join(home, ".bashrc"), "x")
	// Two volumes, both ALREADY owned by the runtime user — the last one being a
	// no-op is what made the loop (and the function) return non-zero.
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(home, ".codex"))

	volFile := filepath.Join(root, "volume-dirs")
	mustWrite(t, volFile, strings.Join([]string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".codex"),
	}, "\n")+"\n")
	procMounts := filepath.Join(root, "proc-mounts")
	mustWrite(t, procMounts, "")

	// stat reports EVERYTHING as already runtime-owned (501:20), so needs_chown is
	// false for every dir and no chown should ever run.
	stubBin := filepath.Join(root, "bin")
	mustMkdir(t, stubBin)
	chownLog := filepath.Join(root, "chown.log")
	mustWrite(t, filepath.Join(stubBin, "chown"), "#!/usr/bin/env bash\necho \"$*\" >> \"$CHOWN_LOG\"\n")
	mustWrite(t, filepath.Join(stubBin, "stat"), `#!/usr/bin/env bash
case "$2" in %u) echo 501;; %g) echo 20;; *) echo 0;; esac
`)
	mustChmodX(t, filepath.Join(stubBin, "chown"))
	mustChmodX(t, filepath.Join(stubBin, "stat"))

	launcher := filepath.Join(root, "byre-launch")
	if err := os.WriteFile(launcher, LauncherScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	// `set -e` is the whole point: it must NOT trip on reown_storage's status.
	driver := filepath.Join(root, "driver.sh")
	mustWrite(t, driver, "set -euo pipefail\n. \""+launcher+"\"\nreown_storage\necho REOWN_OK\n")

	cmd := exec.Command(bash, driver)
	cmd.Env = append(os.Environ(),
		"BYRE_LAUNCH_TEST=1",
		"DEV_HOME="+home,
		"BYRE_VOLUME_DIRS="+volFile,
		"BYRE_PROC_MOUNTS="+procMounts,
		"BYRE_UID=501", "BYRE_GID=20",
		"CHOWN_LOG="+chownLog,
		"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reown_storage leaked non-zero under set -e (launcher would die before exec): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "REOWN_OK") {
		t.Fatalf("driver did not reach the post-reown statement:\n%s", out)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustChmodX(t *testing.T, p string) {
	t.Helper()
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
