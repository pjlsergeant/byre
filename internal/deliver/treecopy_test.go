package deliver

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/pjlsergeant/byre/internal/testtools"
	"github.com/pjlsergeant/byre/internal/treecopytest"
)

// This file is deliver's half of the shared tree-copy expectation table
// (internal/treecopytest). internal/deliver and internal/build each implement
// a race-hardened rooted tree copy against a DIFFERENT contract -- deliver
// skips-and-reports per entry where build refuses the whole operation -- and
// the table says which answer each route owes each adversarial fixture. The
// build half is internal/build/treecopy_test.go.
//
// The table is data. This file is fixtures and observation only: every case in
// Cases() is run, and a case is passed over only via an explicit
// NotApplicable cell in the data -- never by filtering here.
//
// deliver.local is one route with two entry points, so each arm records which
// one it drives and the loop refuses to run an arm whose entry disagrees with
// the case's declaration.

const (
	treePayload = "TREECOPY-PAYLOAD"
	treeSecret  = "TREECOPY-OUTSIDE-SECRET"
)

// deliverArm is one case's fixture plus the entry point it poses the case to.
type deliverArm struct {
	entry treecopytest.Entry
	run   func(t *testing.T) treecopytest.Outcome
}

func TestTreeCopyTableDeliverLocal(t *testing.T) {
	arms := deliverArms()
	for _, c := range treecopytest.Cases() {
		e := c.For(treecopytest.DeliverLocal)
		t.Run(c.Name, func(t *testing.T) {
			if e.Outcome == treecopytest.NotApplicable {
				t.Skip(e.Why)
			}
			arm, ok := arms[c.Name]
			if !ok {
				t.Fatalf("no fixture for shared case %q -- the table gained a case; add its fixture here", c.Name)
			}
			// The declared entry point is part of the expectation: the same
			// threat gets different answers at deliverPath and deliverDir, so
			// an arm posing the case to the other one would be testing a
			// different contract under this case's name.
			if arm.entry != e.Entry {
				t.Fatalf("case %q declares entry point %q; this harness drives %q", c.Name, e.Entry, arm.entry)
			}
			if err := c.CheckOutcome(treecopytest.DeliverLocal, arm.run(t)); err != nil {
				t.Error(err)
			}
		})
	}
}

// delivery is one local delivery's observables: what reached the box, the
// error the caller got, and what was reported.
type delivery struct {
	eng  *fakeEngine
	err  error
	errw string
}

func deliverLocal(t *testing.T, src string) delivery {
	t.Helper()
	return deliverLocalHooked(t, src, nil)
}

// deliverLocalHooked runs one local delivery, optionally mutating the source
// mid-flight through the fake engine's existing exec hook -- the seam the race
// cases need, and the only one; no production seam is added for this table.
func deliverLocalHooked(t *testing.T, src string, hook func(argv []string)) delivery {
	t.Helper()
	eng := box("docker", "aaa")
	eng.hook = hook
	cfg, _, errw := testConfig(eng)
	_, err := RunSources(cfg, Options{}, PathSources([]string{src}))
	return delivery{eng: eng, err: err, errw: errw.String()}
}

// streamed reports whether the box received a stream matching needle. The
// fake records "<id> <rel><-<content>" per delivered file, so this asks about
// STATE -- what landed -- not about what was printed.
func (d delivery) streamed(needle string) bool {
	return strings.Contains(strings.Join(d.eng.streams, "|"), needle)
}

// failedEntries reads the per-entry failure COUNT out of deliverDir's existing
// summary line. Only the numeric field is parsed -- the sentence around it is
// not part of any contract this table asserts.
var failedEntriesRE = regexp.MustCompile(`; (\d+) entr`)

func failedEntries(errw string) int {
	m := failedEntriesRE.FindStringSubmatch(errw)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// observeDeliver is the whole observation mapping for deliver.local, built
// from observables that already exist: the returned error, the existing
// summary's count, and whether the entry under test reached the box.
func observeDeliver(d delivery, entryLanded bool) treecopytest.Outcome {
	switch {
	case d.err != nil && failedEntries(d.errw) > 0:
		return treecopytest.CountedFailure
	case d.err != nil:
		return treecopytest.Refusal
	case entryLanded:
		return treecopytest.Success
	default:
		return treecopytest.SkipEntry
	}
}

// plantDevice makes a character device (the /dev/null pair). Only the DEVICE
// sub-case skips when the platform or the caller's privileges refuse it --
// every other case, FIFO included, runs everywhere.
func plantDevice(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mknod(p, syscall.S_IFCHR|0o600, 0x103); err != nil {
		t.Skipf("cannot create a device node here (%v) -- the device sub-case needs mknod privileges; the FIFO case covers the same classification", err)
	}
}

// outsideSecret writes the "host secret" into dir's parent, i.e. outside the
// tree a deliverDir case delivers.
func outsideSecret(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "host-secret")
	mustWrite(t, p, treeSecret)
	return p
}

func deliverArms() map[string]deliverArm {
	return map[string]deliverArm{
		"top-level in-root symlink to a regular file": {treecopytest.DeliverPath, func(t *testing.T) treecopytest.Outcome {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "real.txt"), treePayload)
			mustSymlink(t, "real.txt", filepath.Join(dir, "link.txt"))
			d := deliverLocal(t, filepath.Join(dir, "link.txt"))
			return observeDeliver(d, d.streamed("link.txt<-"+treePayload))
		}},

		"top-level in-root symlink to a directory": {treecopytest.DeliverPath, func(t *testing.T) treecopytest.Outcome {
			dir := t.TempDir()
			mustMkdir(t, filepath.Join(dir, "realdir"))
			mustWrite(t, filepath.Join(dir, "realdir", "inner.txt"), treePayload)
			mustSymlink(t, "realdir", filepath.Join(dir, "dirlink"))
			d := deliverLocal(t, filepath.Join(dir, "dirlink"))
			return observeDeliver(d, d.streamed(treePayload))
		}},

		"interior in-root symlink": {treecopytest.DeliverDir, func(t *testing.T) treecopytest.Outcome {
			src := filepath.Join(t.TempDir(), "proj")
			mustMkdir(t, src)
			mustWrite(t, filepath.Join(src, "real.txt"), treePayload)
			mustSymlink(t, "real.txt", filepath.Join(src, "link.txt"))
			d := deliverLocal(t, src)
			if !d.streamed("proj/real.txt<-" + treePayload) {
				t.Error("the tree's plain file must deliver regardless of what the link does")
			}
			return observeDeliver(d, d.streamed("proj/link.txt<-"+treePayload))
		}},

		"escaping symlink (top-level)": {treecopytest.DeliverPath, func(t *testing.T) treecopytest.Outcome {
			dir := t.TempDir()
			secret := outsideSecret(t, t.TempDir())
			mustSymlink(t, secret, filepath.Join(dir, "leak.txt"))
			d := deliverLocal(t, filepath.Join(dir, "leak.txt"))
			// The invariant's other half: where a route DOES follow the
			// user's top-level link, the content lands under the name they
			// gave it and nowhere else.
			if d.err == nil && !d.streamed("leak.txt<-"+treeSecret) {
				t.Error("a followed top-level symlink must land under the name the user named")
			}
			return observeDeliver(d, d.streamed(treeSecret))
		}},

		"escaping symlink (interior)": {treecopytest.DeliverDir, func(t *testing.T) treecopytest.Outcome {
			dir := t.TempDir()
			secret := outsideSecret(t, dir)
			src := filepath.Join(dir, "proj")
			mustMkdir(t, src)
			mustWrite(t, filepath.Join(src, "ok.txt"), treePayload)
			mustSymlink(t, secret, filepath.Join(src, "leak.txt"))
			d := deliverLocal(t, src)
			if !d.streamed("proj/ok.txt<-" + treePayload) {
				t.Error("the tree's honest entries must still deliver")
			}
			if d.streamed(treeSecret) {
				t.Error("an escaping interior symlink pulled outside content into the box")
			}
			return observeDeliver(d, d.streamed(treeSecret))
		}},

		"broken symlink (top-level)": {treecopytest.DeliverPath, func(t *testing.T) treecopytest.Outcome {
			dir := t.TempDir()
			mustSymlink(t, "nowhere", filepath.Join(dir, "broken.txt"))
			d := deliverLocal(t, filepath.Join(dir, "broken.txt"))
			if len(d.eng.streams) != 0 {
				t.Errorf("nothing may land for a dangling name: %v", d.eng.streams)
			}
			return observeDeliver(d, len(d.eng.streams) > 0)
		}},

		"broken symlink (interior)": {treecopytest.DeliverDir, func(t *testing.T) treecopytest.Outcome {
			src := filepath.Join(t.TempDir(), "proj")
			mustMkdir(t, src)
			mustWrite(t, filepath.Join(src, "ok.txt"), treePayload)
			mustSymlink(t, "nowhere", filepath.Join(src, "broken.txt"))
			d := deliverLocal(t, src)
			if !d.streamed("proj/ok.txt<-" + treePayload) {
				t.Error("the tree's honest entries must still deliver")
			}
			return observeDeliver(d, d.streamed("broken.txt"))
		}},

		"FIFO": {treecopytest.DeliverPath, func(t *testing.T) treecopytest.Outcome {
			fifo := filepath.Join(t.TempDir(), "pipe")
			if err := mkfifo(fifo); err != nil {
				testtools.Unavailable(t, "mkfifo", err)
			}
			d := deliverLocal(t, fifo)
			if len(d.eng.streams) != 0 {
				t.Errorf("a FIFO must not be delivered as if it were a file: %v", d.eng.streams)
			}
			return observeDeliver(d, len(d.eng.streams) > 0)
		}},

		"device node": {treecopytest.DeliverPath, func(t *testing.T) treecopytest.Outcome {
			dev := filepath.Join(t.TempDir(), "dev")
			plantDevice(t, dev)
			d := deliverLocal(t, dev)
			if len(d.eng.streams) != 0 {
				t.Errorf("a device node must not be delivered as if it were a file: %v", d.eng.streams)
			}
			return observeDeliver(d, len(d.eng.streams) > 0)
		}},

		"mid-walk symlink swap": {treecopytest.DeliverDir, func(t *testing.T) treecopytest.Outcome {
			dir := t.TempDir()
			secret := outsideSecret(t, dir)
			src := filepath.Join(dir, "proj")
			mustMkdir(t, src)
			mustWrite(t, filepath.Join(src, "a.txt"), treePayload)
			mustWrite(t, filepath.Join(src, "b.txt"), treePayload)
			// fs.WalkDir enumerated both entries as regular files before the
			// first one was streamed; swapping b.txt for an escaping symlink
			// at that moment is the check/open race in its exact shape.
			swapped := false
			d := deliverLocalHooked(t, src, func(argv []string) {
				if swapped || !strings.Contains(argv[2], "cat >>") {
					return
				}
				swapped = true
				if err := os.Remove(filepath.Join(src, "b.txt")); err != nil {
					t.Error(err)
					return
				}
				if err := os.Symlink(secret, filepath.Join(src, "b.txt")); err != nil {
					t.Error(err)
				}
			})
			if !swapped {
				t.Fatal("the swap never fired -- the fixture no longer poses the race")
			}
			if !d.streamed("proj/a.txt<-" + treePayload) {
				t.Error("the entries handled before the swap must be unaffected")
			}
			if d.streamed(treeSecret) {
				t.Error("the swapped entry pulled outside content into the box")
			}
			return observeDeliver(d, d.streamed(treeSecret))
		}},
	}
}
