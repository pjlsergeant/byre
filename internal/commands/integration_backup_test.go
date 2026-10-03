package commands

// Gated engine-side arm for ADR 0059 (`byre backup` / `byre restore`), run
// host-side -- never in the dev box, which has no engine:
//
//	BYRE_DOCKER_TESTS=1 go test ./internal/commands/ -run TestIntegrationBackupRestore -v
//
// or, from the box, through the sacrificial runner:
//
//	byre-inttest ./internal/commands/ -run 'TestIntegrationBackupRestore' -v
//
// The promise no fake can vouch for is the TREE: a volume full of the things
// agent state actually contains (sub-second mtimes, a setgid directory, a
// setuid file, two symlinks, a hardlink pair, a FIFO, a socket, a file owned
// by a uid the destination's user namespace does not have) captured by one
// engine's tar and poured by another's, coming out equal to the source under
// the ONE transformation the ADR states: ownership replaced by the box
// identity, setuid/setgid/sticky zeroed, FIFOs and devices absent, everything
// else -- names, types, sizes, permission bits, nanosecond mtimes, link
// targets, contents, hardlink identity -- unchanged.
//
// Every sub-arm reports its own result, pass or skip, so a skipped one cannot
// read as green. What this file deliberately does NOT cover: a real SIGINT
// delivered mid-pour (the design asks for it, but a test has no terminal to
// deliver one from and byre's own signal handler is the thing under test);
// TestRestoreCancellationCleansUpLikeAFailure drives that path on the seam.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// The two volumes the source project declares: one full of specials, one left
// empty (an empty source must round-trip to an empty restored volume, and its
// capture is also where copy-up would show as stray helper-image bytes).
const (
	inttestStateVolume = "state"
	inttestEmptyVolume = "blank"
	// inttestMount is where this file's OWN helpers mount a volume. The verbs'
	// helpers pick a per-run path of their own; this one only has to be a path
	// no base image fills.
	inttestMount = "/.byre-inttest-mnt"
)

func TestIntegrationBackupRestoreRoundTrip(t *testing.T) {
	r := requireEngineRunner(t)
	ident := testIdentity(t, r)
	// Pull the pour/capture image up front so a missing image reads as a pull
	// failure rather than as a helper that would not start. The base image is
	// NOT removed afterwards: it is the engine's shared cache, which every
	// other gated test here builds on top of, and this test creates no image of
	// its own. Volumes are this test's only durable footprint, and every one of
	// them is removed by a t.Cleanup below, failures included.
	if err := r.ImagePull(gen.DefaultBase); err != nil {
		t.Fatalf("pulling %s on %s: %v", gen.DefaultBase, r.Engine(), err)
	}

	t.Run("RoundTripOnOneEngine", func(t *testing.T) {
		runBackupRestoreRoundTrip(t, backupRoundTrip{
			engineKey: string(r.Engine()), // BYRE_TEST_ENGINE pins both phases
			src:       r,
			srcIdent:  ident,
			dst:       r,
			dstIdent:  ident,
		})
	})
	t.Run("VolumeNocopy", func(t *testing.T) { checkVolumeNocopy(t, r, ident) })
	t.Run("TransferBetweenEngines", func(t *testing.T) { transferBetweenEngines(t) })
}

// backupRoundTrip is one source -> destination pair. engineKey is the config's
// own `engine` key: the one-engine arm names it (so BYRE_TEST_ENGINE decides),
// and the transfer arms leave it unset and narrow PATH instead, which is what
// two machines with one engine each actually look like.
type backupRoundTrip struct {
	engineKey          string
	src, dst           *runner.Runner
	srcIdent, dstIdent runner.Identity
	// beforeBackup / beforeRestore run just before each verb, for the PATH
	// narrowing the transfer arms need. The runners above hold absolute engine
	// paths, so this file's own helpers are unaffected by it.
	beforeBackup, beforeRestore func()
}

func runBackupRestoreRoundTrip(t *testing.T, rt backupRoundTrip) {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())

	// ---------------------------------------------------- the source project
	srcDir := t.TempDir()
	sp, err := project.Resolve(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	cfgBody := fmt.Sprintf(`
[[volumes]]
name = %q
role = "state"
target = "/home/dev/state"

[[volumes]]
name = %q
role = "state"
target = "/home/dev/blank"
`, inttestStateVolume, inttestEmptyVolume)
	if rt.engineKey != "" {
		cfgBody = fmt.Sprintf("engine = %q\n", rt.engineKey) + cfgBody
	}
	srcConfig := filepath.Join(sp.Dir, config.ProjectConfigName)
	if err := os.WriteFile(srcConfig, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// --------------------------------------------------- seed the two volumes
	srcState := volumeName(sp.ID, inttestStateVolume)
	srcEmpty := volumeName(sp.ID, inttestEmptyVolume)
	for _, v := range []string{srcState, srcEmpty} {
		if err := rt.src.VolumeCreate(v); err != nil {
			t.Fatalf("creating source volume %s on %s: %v", v, rt.src.Engine(), err)
		}
		t.Cleanup(func() { _ = rt.src.VolumeRemove(v) })
	}
	seeded, _ := runInttestHelper(t, rt.src, rt.srcIdent, srcState, inttestMount, false, seedSpecialsScript())
	socketMade := strings.Contains(seeded, "SOCKET=ok")
	alienOwned := strings.Contains(seeded, "ALIEN=ok")
	t.Logf("source volume seeded on %s: socket=%v alien-uid=%v\n%s", rt.src.Engine(), socketMade, alienOwned, seeded)
	if !socketMade {
		t.Logf("ARM SKIPPED: no tool in %s could make a unix socket, so the `socket ignored` sub-arm does not run", gen.DefaultBase)
	}
	if !alienOwned {
		t.Logf("ARM SKIPPED: chown to uid 65534 failed inside the helper's user namespace, so the out-of-namespace-owner sub-arm does not run")
	}
	srcTree := parseVolumeListing(t, runListing(t, rt.src, rt.srcIdent, srcState))
	assertSourceHasTheSpecials(t, srcTree, socketMade)

	// --------------------------------------------------------------- backup
	out := filepath.Join(t.TempDir(), "box.byre-backup.tar.gz")
	var backupLog bytes.Buffer
	bs := Streams{Out: io.Discard, Err: &backupLog, In: strings.NewReader(""), TTY: false}
	if rt.beforeBackup != nil {
		rt.beforeBackup()
	}
	if err := Backup(bs, srcDir, BackupOptions{Output: out, Yes: true}); err != nil {
		t.Fatalf("backup on %s: %v\n%s", rt.src.Engine(), err, backupLog.String())
	}
	summary := backupLog.String()
	t.Logf("backup summary:\n%s", summary)
	for _, want := range []string{inttestStateVolume, inttestEmptyVolume, "/etc/hosts", "../outside", "fifo"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the backup summary never mentions %q:\n%s", want, summary)
		}
	}
	// GNU tar omits a socket and says so on stderr; byre carries that line
	// through as data. It is the only evidence a socket was there at all.
	if socketMade && !strings.Contains(summary, "socket ignored") {
		t.Errorf("tar's `socket ignored` line never reached the backup summary:\n%s", summary)
	}

	// --------------------------------------------------------------- restore
	dstDir := filepath.Join(t.TempDir(), "restored")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dp, err := project.Resolve(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if dp.ID == sp.ID {
		t.Fatalf("source and destination projects collided on id %q", dp.ID)
	}
	dstState := volumeName(dp.ID, inttestStateVolume)
	dstEmpty := volumeName(dp.ID, inttestEmptyVolume)
	t.Cleanup(func() { _ = rt.dst.VolumeRemove(dstState); _ = rt.dst.VolumeRemove(dstEmpty) })

	var restoreLog bytes.Buffer
	rs := Streams{Out: &restoreLog, Err: &restoreLog, In: strings.NewReader("y\n"), TTY: true}
	if rt.beforeRestore != nil {
		rt.beforeRestore()
	}
	if err := Restore(rs, out, dstDir); err != nil {
		t.Fatalf("restore on %s: %v\n%s", rt.dst.Engine(), err, restoreLog.String())
	}
	t.Logf("restore review and summary:\n%s", restoreLog.String())

	// ------------------------------------------------- what landed host-side
	srcBytes, err := os.ReadFile(srcConfig)
	if err != nil {
		t.Fatal(err)
	}
	dstBytes, err := os.ReadFile(filepath.Join(dp.Dir, config.ProjectConfigName))
	if err != nil {
		t.Fatalf("the restored project has no config: %v", err)
	}
	if !bytes.Equal(srcBytes, dstBytes) {
		t.Errorf("the restored config is not the source's bytes:\n--- restored ---\n%s\n--- source ---\n%s", dstBytes, srcBytes)
	}
	// No preset was applied, so no marker: a restored project must not report
	// itself as applied-and-clean to `byre status`.
	if _, err := os.Lstat(filepath.Join(dp.Dir, appliedRecord)); err == nil {
		t.Error("restore wrote an applied marker; no preset was applied")
	}

	// ------------------------------------------------------ the restored tree
	dstTree := parseVolumeListing(t, runListing(t, rt.dst, rt.dstIdent, dstState))
	for path, e := range dstTree {
		switch e.kind {
		case "p", "s":
			t.Errorf("restore wrote a %s at %s; it writes no FIFOs, devices or sockets", e.kind, path)
		}
		if e.mode&^0o777 != 0 {
			t.Errorf("%s came back with mode %04o; setuid, setgid and sticky are zeroed by the contract", path, e.mode)
		}
		if e.uid != strconv.Itoa(rt.dstIdent.UID) || e.gid != strconv.Itoa(rt.dstIdent.GID) {
			t.Errorf("%s is owned by %s:%s, want this machine's box identity %d:%d", path, e.uid, e.gid, rt.dstIdent.UID, rt.dstIdent.GID)
		}
	}
	want := strings.Join(comparableTree(srcTree), "\n")
	got := strings.Join(comparableTree(dstTree), "\n")
	if want != got {
		t.Errorf("the restored tree is not the source tree under the stated transformation\n--- restored ---\n%s\n--- source (FIFOs, sockets and special bits removed) ---\n%s", got, want)
	} else {
		t.Logf("ARM PASSED: %d entries equal across %s -> %s (types, sizes, modes, nanosecond mtimes, link targets, content hashes, hardlink identity)",
			len(comparableTree(srcTree)), rt.src.Engine(), rt.dst.Engine())
	}

	// ------------------------------------------------------ the empty volume
	empty := parseVolumeListing(t, runListing(t, rt.dst, rt.dstIdent, dstEmpty))
	if len(empty) != 0 {
		t.Errorf("the empty source volume came back with %d entries: %v", len(empty), sortedPaths(empty))
	} else {
		t.Logf("ARM PASSED: an empty source volume round-trips empty (its capture carried no helper-image bytes either)")
	}
}

// ---------------------------------------------------------------- the arms

// assertSourceHasTheSpecials fails if the seeding did not actually produce the
// things this test exists to carry: an arm that proves setgid bits survive
// their zeroing is worthless if no setgid bit was ever set.
func assertSourceHasTheSpecials(t *testing.T, tree map[string]volEntry, socketMade bool) {
	t.Helper()
	cases := []struct {
		path string
		ok   func(volEntry) bool
		what string
	}{
		{"./setgid-dir", func(e volEntry) bool { return e.kind == "d" && e.mode&0o2000 != 0 }, "a setgid directory"},
		{"./setuid-file", func(e volEntry) bool { return e.kind == "f" && e.mode&0o4000 != 0 }, "a setuid file"},
		{"./dir/nested/file.txt", func(e volEntry) bool { return strings.Contains(e.mtime, ".1234") }, "a sub-second mtime"},
		{"./abs-link", func(e volEntry) bool { return e.target == "/etc/hosts" }, "a symlink with an absolute target"},
		{"./trav-link", func(e volEntry) bool { return e.target == "../outside" }, "a symlink whose target leaves the volume"},
		{"./hardlink", func(e volEntry) bool { return e.kind == "f" && e.nlink == "2" }, "a hardlink to an earlier regular file"},
		{"./fifo", func(e volEntry) bool { return e.kind == "p" }, "a FIFO"},
	}
	if socketMade {
		cases = append(cases, struct {
			path string
			ok   func(volEntry) bool
			what string
		}{"./sock", func(e volEntry) bool { return e.kind == "s" }, "a unix socket"})
	}
	for _, c := range cases {
		e, ok := tree[c.path]
		if !ok || !c.ok(e) {
			t.Fatalf("the source volume does not hold %s at %s (%+v) -- the seeding script, not byre, is broken", c.what, c.path, e)
		}
	}
}

// checkVolumeNocopy proves the flag the verbs' helpers pass: a fresh named
// volume mounted over a directory the IMAGE fills is populated by the engine
// (copy-up) unless the mount says otherwise, and byre's helper mount always
// says nocopy -- so a capture can never pick up helper-image bytes and
// a pour can never mix them into a restored volume. /etc/apt stands in for "a
// directory the image fills": the verbs' own mount path is per-run, so no image
// can be built with files already at it, and /etc/apt is full in every Debian
// base while nothing in the container reads it (shadowing /etc itself would
// take the engine's own keep-id passwd handling with it). On Podman the helper
// also carries --image-volume=ignore, so the helper running at all is that flag
// being accepted.
func checkVolumeNocopy(t *testing.T, r *runner.Runner, ident runner.Identity) {
	vol := fmt.Sprintf("byre-inttest-nocopy-%d", os.Getpid())
	_ = r.VolumeRemove(vol) // shed a leftover from an aborted run
	if err := r.VolumeCreate(vol); err != nil {
		t.Fatalf("creating %s on %s: %v", vol, r.Engine(), err)
	}
	t.Cleanup(func() { _ = r.VolumeRemove(vol) })
	out, _ := runInttestHelper(t, r, ident, vol, "/etc/apt", false,
		`if [ -z "$(ls -A "$MNT")" ]; then echo NOCOPY=empty; else echo NOCOPY=populated; ls -A "$MNT" | head -5; fi`)
	if !strings.Contains(out, "NOCOPY=empty") {
		t.Errorf("%s populated a nocopy volume mount from the image:\n%s", r.Engine(), out)
		return
	}
	aside := ""
	if r.Engine() == runner.Podman {
		aside = " (and accepted --image-volume=ignore)"
	}
	t.Logf("ARM PASSED: %s honours nocopy on the helper mount%s", r.Engine(), aside)
}

// transferBetweenEngines is the Docker <-> Podman half of the design's gated
// bullet. The config travels byte-for-byte, so a carried `engine` key would
// pin the destination to the source's engine: a real transfer is a config that
// names no engine and two machines whose detection answers differently. There
// is one machine here, so PATH stands in for the second one -- which is also
// why the arm reports which direction it ran.
func transferBetweenEngines(t *testing.T) {
	ends := map[runner.Engine]*runner.Runner{}
	for _, name := range []string{string(runner.Docker), string(runner.Podman)} {
		eng, exe, err := runner.Detect(name, nil)
		if err != nil {
			t.Logf("%s is not installed here: %v", name, err)
			continue
		}
		ends[eng] = runner.New(eng, exe)
	}
	if len(ends) < 2 {
		t.Skip("ARM SKIPPED: only one engine is installed here, and the Docker <-> Podman transfer needs both — run byre-inttest on a runner carrying docker and rootless podman")
	}
	for _, dir := range [][2]runner.Engine{{runner.Docker, runner.Podman}, {runner.Podman, runner.Docker}} {
		src, dst := ends[dir[0]], ends[dir[1]]
		t.Run(string(dir[0])+"_to_"+string(dir[1]), func(t *testing.T) {
			srcIdent, err := resolveIdentity(io.Discard, src)
			if err != nil {
				t.Skipf("ARM SKIPPED: %s cannot run a session here (%v)", src.Engine(), err)
			}
			dstIdent, err := resolveIdentity(io.Discard, dst)
			if err != nil {
				t.Skipf("ARM SKIPPED: %s cannot run a session here (%v)", dst.Engine(), err)
			}
			if err := src.ImagePull(gen.DefaultBase); err != nil {
				t.Fatalf("pulling %s on %s: %v", gen.DefaultBase, src.Engine(), err)
			}
			if err := dst.ImagePull(gen.DefaultBase); err != nil {
				t.Fatalf("pulling %s on %s: %v", gen.DefaultBase, dst.Engine(), err)
			}
			runBackupRestoreRoundTrip(t, backupRoundTrip{
				engineKey:     "", // detection decides, as it does on two machines
				src:           src,
				srcIdent:      srcIdent,
				dst:           dst,
				dstIdent:      dstIdent,
				beforeBackup:  func() { showOnlyEngine(t, dir[0]) },
				beforeRestore: func() { showOnlyEngine(t, dir[1]) },
			})
			t.Logf("ARM RAN: %s -> %s transfer", dir[0], dir[1])
		})
	}
}

// pristinePATH is the PATH this test process started with, snapshotted before
// any test can narrow it. showOnlyEngine builds its farm from THIS and never
// from the live PATH: a transfer arm narrows twice, once per phase, and a farm
// built from the current PATH would be built from the previous phase's farm --
// which deliberately lacks the engine this phase keeps, so the second narrowing
// could never find it and the arm skipped reporting the engine as missing.
var pristinePATH = os.Getenv("PATH")

// showOnlyEngine narrows PATH so detection can find keep and not the other
// engine. Every executable on the original PATH is symlinked into one directory
// except the engine being hidden, because the two engines usually live in the
// SAME directory (Go's LookPath cannot be shadowed: a non-executable or a
// directory earlier on PATH is skipped, not honoured) and because rootless
// podman needs its own helpers -- newuidmap, conmon, crun -- to stay findable,
// so dropping whole directories is not an option either. The farm applies
// LookPath's own rule for the same reason: only an executable regular file is
// linked, and the FIRST one wins, so a non-executable of that name in an
// earlier directory cannot capture the entry and hide the real binary.
func showOnlyEngine(t *testing.T, keep runner.Engine) {
	t.Helper()
	hide := string(runner.Docker)
	if keep == runner.Docker {
		hide = string(runner.Podman)
	}
	farm := t.TempDir()
	dirs := filepath.SplitList(pristinePATH)
	keptFrom := ""
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Logf("PATH entry %s could not be listed (%v); nothing in it is linked into the farm", dir, err)
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if name == hide {
				continue
			}
			src := filepath.Join(dir, name)
			if !executableFile(src) {
				continue
			}
			link := filepath.Join(farm, name)
			if _, err := os.Lstat(link); err == nil {
				continue // the first PATH directory wins, as PATH order does
			}
			if err := os.Symlink(src, link); err != nil {
				continue
			}
			if name == string(keep) {
				keptFrom = src
			}
		}
	}
	t.Setenv("PATH", farm)
	// byre pins each lookup for its invocation; one test process runs many
	// invocations' worth of code, so the pin from the previous phase has to go.
	hostexec.ResetPins()
	t.Cleanup(hostexec.ResetPins)
	if p, err := exec.LookPath(hide); err == nil {
		t.Skipf("ARM SKIPPED: %s is still findable at %s under the narrowed PATH, so detection would not pick %s", hide, p, keep)
	}
	if _, err := exec.LookPath(string(keep)); err != nil {
		// Say what was examined and what was linked: a skip here is a fact about
		// this machine's PATH, and the next run's log has to carry it.
		where := "no entry held an executable " + string(keep)
		if keptFrom != "" {
			where = string(keep) + " was linked from " + keptFrom
		}
		t.Skipf("ARM SKIPPED: %s is not findable under the narrowed PATH (%v) — PATH entries examined: %s; %s",
			keep, err, strings.Join(dirs, ", "), where)
	}
}

// executableFile reports what exec.LookPath accepts: a regular file (symlinks
// followed) with an execute bit.
func executableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// -------------------------------------------------------------- the helpers

// runInttestHelper runs one of THIS FILE's helpers through the product's own
// helper shape (runner.Helper), so the mount flags under the assertions are
// byre's and not the test's.
func runInttestHelper(t *testing.T, r *runner.Runner, ident runner.Identity, volume, mnt string, readOnly bool, script string) (stdout, stderr string) {
	t.Helper()
	var out strings.Builder
	h := runner.Helper{
		Image:     gen.DefaultBase,
		Identity:  ident,
		Labels:    []string{"byre.inttest=backup-roundtrip"},
		Env:       map[string]string{"MNT": mnt, "TAR_OPTIONS": ""},
		Volume:    volume,
		MountPath: mnt,
		ReadOnly:  readOnly,
		Script:    script,
	}
	msg, err := r.RunHelper(h, nil, &out)
	if err != nil {
		t.Fatalf("test helper on %s failed: %v\nstderr: %s\nstdout: %s", r.Engine(), err, msg, out.String())
	}
	return out.String(), msg
}

// seedSpecialsScript fills a fresh volume with everything the round trip has
// to carry. Two sub-arms can legitimately fail on a given host and say so on
// stdout rather than failing the seeding: the out-of-namespace owner (the
// userns may not map 65534) and the unix socket (nothing in a minimal Debian
// is guaranteed to be able to make one -- no python3, no socat; perl is tried
// because perl-base is Essential).
func seedSpecialsScript() string {
	return `set -e
cd "$MNT"
mkdir -p dir/nested
printf 'alpha\n' > dir/nested/file.txt
touch -d @1700000000.123456789 dir/nested/file.txt
printf 'beta\n' > linked
ln linked hardlink
mkdir setgid-dir
chmod 2775 setgid-dir
printf 'suid\n' > setuid-file
chmod 4755 setuid-file
mkdir sticky-dir
chmod 1777 sticky-dir
ln -s /etc/hosts abs-link
ln -s ../outside trav-link
mkfifo fifo
printf 'alien\n' > alien-owner
chmod 0640 alien-owner
if chown 65534:65534 alien-owner 2>/dev/null; then echo "ALIEN=ok"; else echo "ALIEN=skipped"; fi
if command -v perl >/dev/null 2>&1 && perl -e 'use Socket; socket(S,PF_UNIX,SOCK_STREAM,0) or die "socket"; bind(S,pack_sockaddr_un("sock")) or die "bind";' 2>/dev/null; then
  echo "SOCKET=ok"
else
  echo "SOCKET=none"
fi
echo "SEEDED=ok"`
}

// listVolumeScript renders one volume as lines the test can compare: one
// ENTRY per path (type, mode, nanosecond mtime, size, inode, link count,
// owner, link target) and one HASH per regular file. Read-only mount, so the
// listing cannot change what it is listing.
func listVolumeScript() string {
	return `cd "$MNT" || exit 1
find . -mindepth 1 -printf 'ENTRY\t%p\t%y\t%m\t%T@\t%s\t%i\t%n\t%U\t%G\t%l\n' | LC_ALL=C sort
find . -mindepth 1 -type f | LC_ALL=C sort | while read -r f; do
  printf 'HASH\t%s\t%s\n' "$f" "$(sha256sum "$f" | cut -d' ' -f1)"
done`
}

func runListing(t *testing.T, r *runner.Runner, ident runner.Identity, volume string) string {
	t.Helper()
	out, _ := runInttestHelper(t, r, ident, volume, inttestMount, true, listVolumeScript())
	return out
}

// volEntry is one line of a volume listing.
type volEntry struct {
	kind   string // find's %y: f, d, l, p (FIFO), s (socket)
	mode   int64  // find's %m, octal, special bits included
	mtime  string // find's %T@: seconds.nanoseconds
	size   string
	inode  string
	nlink  string
	uid    string
	gid    string
	target string // symlink target, verbatim
	hash   string // sha256, regular files only
}

func parseVolumeListing(t *testing.T, out string) map[string]volEntry {
	t.Helper()
	entries := map[string]volEntry{}
	hashes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		switch {
		case f[0] == "ENTRY" && len(f) == 11:
			mode, err := strconv.ParseInt(f[3], 8, 64)
			if err != nil {
				t.Fatalf("listing line %q: mode %q is not octal", line, f[3])
			}
			entries[f[1]] = volEntry{kind: f[2], mode: mode, mtime: f[4], size: f[5],
				inode: f[6], nlink: f[7], uid: f[8], gid: f[9], target: f[10]}
		case f[0] == "HASH" && len(f) == 3:
			hashes[f[1]] = f[2]
		default:
			t.Fatalf("unparsable listing line %q", line)
		}
	}
	for path, h := range hashes {
		e, ok := entries[path]
		if !ok {
			t.Fatalf("a hash for %q with no entry for it", path)
		}
		e.hash = h
		entries[path] = e
	}
	return entries
}

func sortedPaths(entries map[string]volEntry) []string {
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// comparableTree renders a listing as the lines the two sides MUST agree on,
// with the stated transformation already applied to the source side: FIFOs and
// sockets dropped (restore writes neither), special mode bits masked off.
// Ownership is not in here -- it is replaced by design, and asserted directly
// against the destination's box identity. Directory and symlink sizes are not
// either: a directory's size is the filesystem's business, and a symlink's is
// its target's length, which the target comparison already covers.
func comparableTree(entries map[string]volEntry) []string {
	groups := hardlinkGroups(entries)
	var lines []string
	for _, path := range sortedPaths(entries) {
		e := entries[path]
		if e.kind == "p" || e.kind == "s" {
			continue
		}
		line := fmt.Sprintf("%s kind=%s perm=%04o mtime=%s", path, e.kind, e.mode&0o777, e.mtime)
		switch e.kind {
		case "f":
			line += fmt.Sprintf(" size=%s sha256=%s links=%s group=%s", e.size, e.hash, e.nlink, groups[path])
		case "l":
			line += " target=" + e.target
		}
		lines = append(lines, line)
	}
	return lines
}

// hardlinkGroups numbers the hardlink sets by first appearance in path order,
// so "these two paths are one inode" survives a comparison between two volumes
// whose inode numbers have nothing to do with each other.
func hardlinkGroups(entries map[string]volEntry) map[string]string {
	group := map[string]string{}
	byInode := map[string]string{}
	for _, path := range sortedPaths(entries) {
		e := entries[path]
		if e.kind != "f" || e.nlink == "1" {
			continue
		}
		id, ok := byInode[e.inode]
		if !ok {
			id = fmt.Sprintf("g%d", len(byInode))
			byInode[e.inode] = id
		}
		group[path] = id
	}
	return group
}
