package build

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/testtools"
	"github.com/pjlsergeant/byre/internal/treecopytest"
)

// This file is build's half of the shared tree-copy expectation table
// (internal/treecopytest). internal/build and internal/deliver each implement
// a race-hardened rooted tree copy against a DIFFERENT contract; the table
// says what each route must do with each adversarial fixture, and these two
// tests pose all of them to build's two routes. The deliver half is
// internal/deliver/treecopy_test.go.
//
// The table is data. This file is fixtures and observation only: every case
// in Cases() is run, and a case is passed over only via an explicit
// NotApplicable cell in the data -- never by filtering here.

// Distinctive contents, so "did this land?" is a content question rather than
// a path question: a staged path can exist for reasons a fixture did not
// intend, but these strings can only have come from the file they were
// written into.
const (
	treePayload = "TREECOPY-PAYLOAD"
	treeSecret  = "TREECOPY-OUTSIDE-SECRET"
)

// treeArm plants one case's fixture, drives one build route, asserts the
// case's containment invariant on STATE, and reports the outcome it observed.
type treeArm func(t *testing.T) treecopytest.Outcome

func TestTreeCopyTableStageCopy(t *testing.T) {
	runTreeCopyTable(t, treecopytest.BuildStageCopy, stageCopyArms())
}

func TestTreeCopyTableCopyPath(t *testing.T) {
	runTreeCopyTable(t, treecopytest.BuildCopyPath, copyPathArms())
}

// runTreeCopyTable iterates the FULL shared case slice. An unknown case name
// is fatal rather than skipped: a case added to the table with no fixture here
// would otherwise look covered.
func runTreeCopyTable(t *testing.T, route treecopytest.Route, arms map[string]treeArm) {
	t.Helper()
	for _, c := range treecopytest.Cases() {
		e := c.For(route)
		t.Run(c.Name, func(t *testing.T) {
			if e.Outcome == treecopytest.NotApplicable {
				t.Skip(e.Why)
			}
			arm, ok := arms[c.Name]
			if !ok {
				t.Fatalf("no fixture for shared case %q on route %s -- the table gained a case; add its fixture here", c.Name, route)
			}
			if err := c.CheckOutcome(route, arm(t)); err != nil {
				t.Error(err)
			}
		})
	}
}

// buildOutcome is the whole observation mapping for both build routes. Neither
// has a benign skip or a per-entry count: staging either completes or fails
// the operation, so the error alone carries the answer. Each arm asserts what
// did or did not land separately.
func buildOutcome(err error) treecopytest.Outcome {
	if err != nil {
		return treecopytest.Refusal
	}
	return treecopytest.Success
}

// runBounded runs one staging attempt under a timeout. A FIFO or device that
// slipped past the fd-fstat gate would block the open forever, and that has to
// fail loudly rather than wedge the suite.
func runBounded(t *testing.T, run func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("the staging attempt blocked -- O_NONBLOCK / fd-fstat regression")
		return nil // unreachable
	}
}

// stageProject bootstraps a project, lets plant populate it, and stages `files`
// key through the project-root-anchored route (stageCopy).
func stageProject(t *testing.T, key string, plant func(t *testing.T, proj string)) (project.Paths, error) {
	t.Helper()
	paths := bootstrapped(t)
	plant(t, paths.Canonical)
	err := runBounded(t, func() error {
		_, aerr := Assemble(paths, config.Config{
			Base:  "debian:bookworm",
			Files: map[string]string{key: "/opt/staged"},
		}, skills.Resolved{})
		return aerr
	})
	return paths, err
}

// copyPathAttempt stages src (an absolute pathname) through the by-pathname
// route and returns the destination for the invariant assertions.
func copyPathAttempt(t *testing.T, src string) (string, error) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "staged")
	dr, base := dstAt(t, dst)
	return dst, runBounded(t, func() error { return copyPath(src, dr, base, nil) })
}

// fixture helpers. These live in a _test.go file deliberately: os.Symlink and
// syscall.Mkfifo are exactly what the shared table may not call.

func plantFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func plantDir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func plantSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		testtools.Unavailable(t, "symlink", err)
	}
}

func plantFIFO(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		testtools.Unavailable(t, "mkfifo", err)
	}
}

// plantDevice makes a character device (the /dev/null pair). Only the DEVICE
// sub-case skips when the platform or the caller's privileges refuse it --
// every other case, FIFO included, runs everywhere.
func plantDevice(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mknod(p, syscall.S_IFCHR|0o600, 0x103); err != nil {
		t.Skipf("cannot create a device node here (%v) -- the device sub-case needs mknod privileges; the FIFO case covers the same fd-judged rule", err)
	}
}

// outsideSecret writes the "host secret" into a directory the fixture's copy
// root does not contain, and returns its path.
func outsideSecret(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "host-secret")
	plantFile(t, p, treeSecret)
	return p
}

// stagedFiles is where the `files` route lands things in the build context.
func stagedFiles(paths project.Paths, rel string) string {
	return filepath.Join(paths.ContextDir, "files", rel)
}

func assertContent(t *testing.T, p, want string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Errorf("%s did not land: %v", p, err)
		return
	}
	if string(b) != want {
		t.Errorf("%s = %q, want %q", p, b, want)
	}
}

func assertAbsent(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); err == nil {
		t.Errorf("%s landed, and nothing should have", p)
	}
}

// noContentUnder is the state form of "nothing escaped": it fails if want
// appears in any file beneath dir. Asserted on the filesystem, never on a
// refusal message.
func noContentUnder(t *testing.T, dir, want string) {
	t.Helper()
	if _, err := os.Lstat(dir); err != nil {
		return // nothing was staged at all
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(b), want) {
			t.Errorf("%s carries content from outside the copy root", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCopyExactlyRefusesGrowthAndShrink pins the size contract both build
// routes funnel into (stageRegularFromFD -> copyExactly): a source whose size
// disagrees with the promise is refused in either direction, never staged
// torn or short. This is a plain unit test, NOT a treecopytest table arm: a
// real mid-copy write cannot be posed through the routes deterministically,
// and the table's cells only claim what their harness actually executes —
// its growth row says N/A and names this test.
func TestCopyExactlyRefusesGrowthAndShrink(t *testing.T) {
	if err := copyExactly(io.Discard, strings.NewReader(treePayload), int64(len(treePayload))-4, "src"); err == nil {
		t.Error("a source larger than its observed size must be refused, not staged torn")
	}
	if err := copyExactly(io.Discard, strings.NewReader(treePayload), int64(len(treePayload))+4, "src"); err == nil {
		t.Error("a source smaller than its observed size must be refused, not staged short")
	}
}

func stageCopyArms() map[string]treeArm {
	return map[string]treeArm{
		"top-level in-root symlink to a regular file": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "link.txt", func(t *testing.T, proj string) {
				plantFile(t, filepath.Join(proj, "real.txt"), treePayload)
				plantSymlink(t, "real.txt", filepath.Join(proj, "link.txt"))
			})
			if err == nil {
				assertContent(t, stagedFiles(paths, "link.txt"), treePayload)
			}
			return buildOutcome(err)
		},
		"top-level in-root symlink to a directory": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "dirlink", func(t *testing.T, proj string) {
				plantDir(t, filepath.Join(proj, "realdir"))
				plantFile(t, filepath.Join(proj, "realdir", "inner.txt"), treePayload)
				plantSymlink(t, "realdir", filepath.Join(proj, "dirlink"))
			})
			if err == nil {
				assertContent(t, stagedFiles(paths, filepath.Join("dirlink", "inner.txt")), treePayload)
			}
			return buildOutcome(err)
		},
		"interior in-root symlink": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "assets", func(t *testing.T, proj string) {
				d := filepath.Join(proj, "assets")
				plantDir(t, d)
				plantFile(t, filepath.Join(d, "real.txt"), treePayload)
				plantSymlink(t, "real.txt", filepath.Join(d, "link.txt"))
			})
			assertAbsent(t, stagedFiles(paths, filepath.Join("assets", "link.txt")))
			return buildOutcome(err)
		},
		"escaping symlink (top-level)": func(t *testing.T) treecopytest.Outcome {
			secret := outsideSecret(t)
			paths, err := stageProject(t, "leak.txt", func(t *testing.T, proj string) {
				plantSymlink(t, secret, filepath.Join(proj, "leak.txt"))
			})
			noContentUnder(t, paths.ContextDir, treeSecret)
			return buildOutcome(err)
		},
		"escaping symlink (interior)": func(t *testing.T) treecopytest.Outcome {
			secret := outsideSecret(t)
			paths, err := stageProject(t, "assets", func(t *testing.T, proj string) {
				d := filepath.Join(proj, "assets")
				plantDir(t, d)
				plantFile(t, filepath.Join(d, "ok.txt"), treePayload)
				plantSymlink(t, secret, filepath.Join(d, "leak.txt"))
			})
			noContentUnder(t, paths.ContextDir, treeSecret)
			return buildOutcome(err)
		},
		"broken symlink (top-level)": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "broken.txt", func(t *testing.T, proj string) {
				plantSymlink(t, "nowhere", filepath.Join(proj, "broken.txt"))
			})
			assertAbsent(t, stagedFiles(paths, "broken.txt"))
			return buildOutcome(err)
		},
		"broken symlink (interior)": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "assets", func(t *testing.T, proj string) {
				d := filepath.Join(proj, "assets")
				plantDir(t, d)
				plantFile(t, filepath.Join(d, "ok.txt"), treePayload)
				plantSymlink(t, "nowhere", filepath.Join(d, "broken.txt"))
			})
			assertAbsent(t, stagedFiles(paths, filepath.Join("assets", "broken.txt")))
			return buildOutcome(err)
		},
		"FIFO": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "pipe", func(t *testing.T, proj string) {
				plantFIFO(t, filepath.Join(proj, "pipe"))
			})
			assertAbsent(t, stagedFiles(paths, "pipe"))
			return buildOutcome(err)
		},
		"device node": func(t *testing.T) treecopytest.Outcome {
			paths, err := stageProject(t, "dev", func(t *testing.T, proj string) {
				plantDevice(t, filepath.Join(proj, "dev"))
			})
			assertAbsent(t, stagedFiles(paths, "dev"))
			return buildOutcome(err)
		},
	}
}

func copyPathArms() map[string]treeArm {
	return map[string]treeArm{
		"top-level in-root symlink to a regular file": func(t *testing.T) treecopytest.Outcome {
			src := t.TempDir()
			plantFile(t, filepath.Join(src, "real.txt"), treePayload)
			link := filepath.Join(src, "link.txt")
			plantSymlink(t, "real.txt", link)
			dst, err := copyPathAttempt(t, link)
			assertAbsent(t, dst)
			return buildOutcome(err)
		},
		"top-level in-root symlink to a directory": func(t *testing.T) treecopytest.Outcome {
			src := t.TempDir()
			plantDir(t, filepath.Join(src, "realdir"))
			plantFile(t, filepath.Join(src, "realdir", "inner.txt"), treePayload)
			link := filepath.Join(src, "dirlink")
			plantSymlink(t, "realdir", link)
			dst, err := copyPathAttempt(t, link)
			assertAbsent(t, filepath.Join(dst, "inner.txt"))
			return buildOutcome(err)
		},
		"interior in-root symlink": func(t *testing.T) treecopytest.Outcome {
			src := t.TempDir()
			plantFile(t, filepath.Join(src, "real.txt"), treePayload)
			plantSymlink(t, "real.txt", filepath.Join(src, "link.txt"))
			dst, err := copyPathAttempt(t, src)
			assertAbsent(t, filepath.Join(dst, "link.txt"))
			return buildOutcome(err)
		},
		"escaping symlink (top-level)": func(t *testing.T) treecopytest.Outcome {
			secret := outsideSecret(t)
			link := filepath.Join(t.TempDir(), "leak.txt")
			plantSymlink(t, secret, link)
			dst, err := copyPathAttempt(t, link)
			noContentUnder(t, dst, treeSecret)
			return buildOutcome(err)
		},
		"escaping symlink (interior)": func(t *testing.T) treecopytest.Outcome {
			secret := outsideSecret(t)
			src := t.TempDir()
			plantFile(t, filepath.Join(src, "ok.txt"), treePayload)
			plantSymlink(t, secret, filepath.Join(src, "leak.txt"))
			dst, err := copyPathAttempt(t, src)
			noContentUnder(t, dst, treeSecret)
			return buildOutcome(err)
		},
		"broken symlink (top-level)": func(t *testing.T) treecopytest.Outcome {
			link := filepath.Join(t.TempDir(), "broken.txt")
			plantSymlink(t, "nowhere", link)
			dst, err := copyPathAttempt(t, link)
			assertAbsent(t, dst)
			return buildOutcome(err)
		},
		"broken symlink (interior)": func(t *testing.T) treecopytest.Outcome {
			src := t.TempDir()
			plantFile(t, filepath.Join(src, "ok.txt"), treePayload)
			plantSymlink(t, "nowhere", filepath.Join(src, "broken.txt"))
			dst, err := copyPathAttempt(t, src)
			assertAbsent(t, filepath.Join(dst, "broken.txt"))
			return buildOutcome(err)
		},
		"FIFO": func(t *testing.T) treecopytest.Outcome {
			src := filepath.Join(t.TempDir(), "pipe")
			plantFIFO(t, src)
			dst, err := copyPathAttempt(t, src)
			assertAbsent(t, dst)
			return buildOutcome(err)
		},
		"device node": func(t *testing.T) treecopytest.Outcome {
			src := filepath.Join(t.TempDir(), "dev")
			plantDevice(t, src)
			dst, err := copyPathAttempt(t, src)
			assertAbsent(t, dst)
			return buildOutcome(err)
		},
	}
}
