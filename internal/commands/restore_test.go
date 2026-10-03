package commands

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/backup"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/gen"
	"github.com/pjlsergeant/byre/internal/hostexec"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/lock"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// ---------------------------------------------------------------- harness

// testVol is one carried volume as a backup file holds it: the logical name
// and a REAL plain tar (volumeTar, from the backup tests), so the reader's
// validation runs over bytes it could have produced itself.
type testVol struct {
	name string
	tar  []byte
}

// backupFileWith publishes a real backup file through backup.Write: the index
// rows are DERIVED from the payloads (lengths, digests, entry counts from the
// validator), so a test states content rather than bookkeeping. index mutates
// the index after that, for the forged-file arms.
func backupFileWith(t *testing.T, dir, prefix, cfgText string, vols []testVol, index func(*backup.Index)) string {
	t.Helper()
	out := filepath.Join(dir, "in.byre-backup.tar.gz")
	var rows []backup.Volume
	var payloads []backup.Payload
	for _, v := range vols {
		entries := int64(0)
		if rep, err := backup.Validate(bytes.NewReader(v.tar)); err == nil {
			entries = rep.Entries
		}
		rows = append(rows, backup.Volume{
			Name: v.name, Bytes: int64(len(v.tar)), SHA256: backup.DigestBytes(v.tar), Entries: entries,
		})
		raw := v.tar
		payloads = append(payloads, backup.Payload{
			Name: v.name, Size: int64(len(raw)),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil },
		})
	}
	state, err := backup.CredentialState([]byte(cfgText))
	if err != nil {
		t.Fatal(err)
	}
	ix := backup.Index{
		Format:         backup.Format,
		ByreVersion:    "v1.12.0",
		MinByreVersion: backup.MinByreVersion,
		Folder:         "source-project",
		Engine:         "docker",
		Config: backup.ConfigRow{
			Bytes: int64(len(cfgText)), SHA256: backup.DigestBytes([]byte(cfgText)), Credentials: state,
		},
		Volumes: rows,
		References: backup.References{
			Layers: []string{}, Skills: []string{}, Mounts: []string{}, Context: []string{},
			ClaudeSkills: []string{}, Seeds: []string{}, Base: gen.DefaultBase,
		},
	}
	if index != nil {
		index(&ix)
	}
	if err := backup.Write(out, prefix, ix, []byte(cfgText), payloads, nil); err != nil {
		t.Fatal(err)
	}
	return out
}

// pourStdout is the fake engine's helper stdout for restore: the GNU tar
// banner for the preflight (the helper with no volume), nothing for a pour
// (whose output is the extraction, not a stream).
func pourStdout(h runner.Helper, w io.Writer) error {
	if h.Volume == "" {
		_, err := io.WriteString(w, "tar (GNU tar) 1.34\n")
		return err
	}
	return nil
}

type restoreOpts struct {
	cfg   string
	vols  []testVol
	in    string // prompt answers; "y\n" confirms the review
	index func(*backup.Index)
	// dir, when set, is the target directory instead of a fresh temp one.
	dir string
}

type restoreFixture struct {
	rr    *restoreRun
	f     *fakeRunner
	file  string
	dir   string
	paths project.Paths
	errb  *bytes.Buffer
}

// restoreHarness wires one restore over the fake engine: a byre home, a
// project directory that is NOT enrolled (restore's own case), and a real
// backup file whose payloads the reader will verify.
func restoreHarness(t *testing.T, f *fakeRunner, opts restoreOpts) *restoreFixture {
	t.Helper()
	t.Setenv("BYRE_HOME", t.TempDir())
	dir := opts.dir
	if dir == "" {
		dir = t.TempDir()
	}
	paths, err := project.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.helperStdout == nil {
		f.helperStdout = pourStdout
	}
	if f.images == nil {
		// The base is already here: the ordinary case, no pull.
		f.images = map[string]bool{gen.DefaultBase: true}
	}
	file := backupFileWith(t, t.TempDir(), volumePrefix(paths.ID), opts.cfg, opts.vols, opts.index)
	s, _, errb := testStreams(opts.in, true)
	rr := &restoreRun{
		s: s, target: dir,
		detect: func(string) (engineRunner, error) { return f, nil },
	}
	return &restoreFixture{rr: rr, f: f, file: file, dir: dir, paths: paths, errb: errb}
}

// run is Restore's own tail: the run, then the cleanup every exit performs
// (staging always; the created directory while the store is not enrolled).
func (fx *restoreFixture) run() error {
	err := fx.rr.run(fx.file)
	fx.rr.cleanup(err)
	return err
}

// storeConfig reads the config restore wrote, or "" when there is none.
func storeConfig(t *testing.T, paths project.Paths) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(paths.Dir, config.ProjectConfigName))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(b)
}

// pourHelpers are the helper specs that poured a volume (the preflight, which
// mounts none, is not one).
func pourHelpers(f *fakeRunner) []runner.Helper {
	var out []runner.Helper
	for _, h := range f.helperRuns {
		if h.Volume != "" {
			out = append(out, h)
		}
	}
	return out
}

// pouredEntries reads back the stream a pour fed the helper: the REBUILT
// archive, parsed as tar, as name -> content.
func pouredEntries(t *testing.T, f *fakeRunner, which int) map[string]string {
	t.Helper()
	n := -1
	for i, h := range f.helperRuns {
		if h.Volume == "" {
			continue
		}
		n++
		if n == which {
			out := map[string]string{}
			tr := tar.NewReader(bytes.NewReader(f.helperStdin[i]))
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("the poured stream is not a tar: %v", err)
				}
				body, _ := io.ReadAll(tr)
				out[hdr.Name] = string(body)
			}
			return out
		}
	}
	t.Fatalf("no pour helper %d ran", which)
	return nil
}

// wantExit1 is the failure path's contract: the whole account is on stderr,
// printed through the funnel, and the error is the silent exit code -- byre
// never prints the cause a second time under its own banner.
func wantExit1(t *testing.T, err error) {
	t.Helper()
	var ee ExitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("error = %v, want ExitError{Code: 1}", err)
	}
}

const claudeVolumeConfig = "[[volumes]]\nname = \".claude\"\nrole = \"state\"\ntarget = \"/home/dev/.claude\"\n"

// --------------------------------------------------- step 1: order, refusals

func TestRestoreRefusesOffATerminal(t *testing.T) {
	s, _, _ := testStreams("", false)
	err := Restore(s, "/nope.byre-backup.tar.gz", t.TempDir(), RestoreOptions{})
	if err == nil || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("off-terminal restore error = %v, want the interactive-only refusal", err)
	}
}

func TestRestoreRefusesANonDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	// A plain file, and a symlink to a real directory: both are existing
	// entries, judged without following.
	plain := filepath.Join(root, "file")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{plain, link} {
		_, created, err := restoreTarget(p)
		if err == nil {
			t.Fatalf("%s: restoreTarget accepted a non-directory", p)
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("%s: error = %v, want the not-a-directory rule", p, err)
		}
		if created {
			t.Errorf("%s: reported as created", p)
		}
		// The victim is untouched.
		if _, serr := os.Lstat(p); serr != nil {
			t.Errorf("%s: %v", p, serr)
		}
	}
}

func TestRestoreRefusesAMissingParentAndCreatesTheDirectory(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "nothing", "here")
	if _, _, err := restoreTarget(missing); err == nil {
		t.Fatal("restoreTarget accepted a missing parent")
	} else if !strings.Contains(err.Error(), filepath.Join(root, "nothing")) {
		t.Errorf("error = %v, want the missing parent named", err)
	}
	fresh := filepath.Join(root, "fresh")
	got, created, err := restoreTarget(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !created || got != fresh {
		t.Fatalf("restoreTarget(%q) = %q, created=%v", fresh, got, created)
	}
	if fi, serr := os.Stat(fresh); serr != nil || !fi.IsDir() {
		t.Fatalf("stat %s: %v", fresh, serr)
	}
}

func TestRestoreRefusesALinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	wt := filepath.Join(root, "feature")
	gd := filepath.Join(main, ".git", "worktrees", "feature")
	for _, d := range []string{gd, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, content string) {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(gd, "commondir"), "../..\n")
	write(filepath.Join(gd, "gitdir"), filepath.Join(wt, ".git")+"\n")
	write(filepath.Join(wt, ".git"), "gitdir: "+gd+"\n")

	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, dir: wt})
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "restore in the main worktree") {
		t.Fatalf("error = %v, want the main-worktree refusal", err)
	}
}

// An existing config refuses BEFORE the file is read: the path handed in here
// does not exist, and a restore that got as far as opening it would say so.
func TestRestoreRefusesAnExistingConfigBeforeReadingTheFile(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig})
	if err := fx.paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := config.AtomicWrite(filepath.Join(fx.paths.Dir, config.ProjectConfigName), "base = \"debian:bookworm\"\n"); err != nil {
		t.Fatal(err)
	}
	err := fx.rr.run(filepath.Join(t.TempDir(), "no-such-file"))
	if err == nil || !strings.Contains(err.Error(), "already has a config") {
		t.Fatalf("error = %v, want the existing-config refusal", err)
	}
	if !strings.Contains(err.Error(), "fresh checkout") {
		t.Errorf("error = %v, want the remedy", err)
	}
}

func TestRestoreRefusesALeftoverHelperOnTheDestinationEngine(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	f.allContainers = map[string][]string{helperKey + "=" + fx.paths.ID: {"helper000001"}}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "rm -f helper000001") {
		t.Fatalf("error = %v, want the leftover helper's rm -f line", err)
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("a config was written")
	}
}

func TestRestoreRefusesAContainerOfThisProject(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	f.allContainers = map[string][]string{labelKey + "=" + fx.paths.ID: {"boxcontainer"}}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "still present") {
		t.Fatalf("error = %v, want the container refusal", err)
	}
	if !strings.Contains(err.Error(), "docker rm boxcontainer") {
		t.Errorf("error = %v, want the engine's rm line", err)
	}
	if out := fx.errb.String(); !strings.Contains(out, "byre shell") {
		t.Errorf("the remedies were not printed:\n%s", out)
	}
}

// Both directions of the volume-name ownership rule, computed as if this
// project were not enrolled.
func TestRestoreRefusesVolumeNamesAnotherProjectOwns(t *testing.T) {
	t.Run("a longer id claims the name", func(t *testing.T) {
		f := &fakeRunner{}
		cfg := "[[volumes]]\nname = \"my-state\"\nrole = \"state\"\ntarget = \"/home/dev/.state\"\n"
		fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
			vols: []testVol{{name: "my-state", tar: volumeTar(t, nil)}}})
		// Another project whose id extends this one spells the same physical
		// name: byre-<id>-my-state == byre-<id>-my + "-" + state.
		longer := fx.paths.ID + "-my"
		if err := os.MkdirAll(filepath.Join(fx.paths.Home, "projects", longer), 0o755); err != nil {
			t.Fatal(err)
		}
		err := fx.run()
		if err == nil || !strings.Contains(err.Error(), longer) || !strings.Contains(err.Error(), fx.paths.ID) {
			t.Fatalf("error = %v, want both projects named", err)
		}
		if len(f.created) != 0 {
			t.Errorf("volumes created: %v", f.created)
		}
	})
	t.Run("a shorter id lists an existing name", func(t *testing.T) {
		f := &fakeRunner{}
		fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
			vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
		// This project's id extends an enrolled one, so that project's own
		// listing claims the physical name -- and it already exists there.
		shorter := strings.SplitN(fx.paths.ID, "-", 2)[0]
		if shorter == fx.paths.ID {
			t.Skip("this project id has no hyphen to split on")
		}
		if err := os.MkdirAll(filepath.Join(fx.paths.Home, "projects", shorter), 0o755); err != nil {
			t.Fatal(err)
		}
		f.vols = map[string]bool{volumeName(fx.paths.ID, ".claude"): true}
		err := fx.run()
		if err == nil || !strings.Contains(err.Error(), shorter) {
			t.Fatalf("error = %v, want the shorter project named", err)
		}
		if storeConfig(t, fx.paths) != "" {
			t.Error("a config was written")
		}
	})
}

// A payload the nested-tar contract refuses stops the restore in step 2:
// nothing on this machine has been asked anything, and nothing is written.
func TestRestoreRefusesABadPayloadBeforeAnyProjectState(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "/etc/shadow", Mode: 0o644, Format: tar.FormatPAX}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: buf.Bytes()}}})
	err := fx.run()
	if err == nil {
		t.Fatal("a payload with an absolute name was accepted")
	}
	if !strings.Contains(err.Error(), ".claude") {
		t.Errorf("error = %v, want the volume named", err)
	}
	if storeConfig(t, fx.paths) != "" || len(f.created) != 0 || len(f.helperRuns) != 0 {
		t.Errorf("project state was written: config=%q created=%v helpers=%d", storeConfig(t, fx.paths), f.created, len(f.helperRuns))
	}
}

// Declining an offered install is not a clean end: it is the reason the
// package is still missing, so restore stops with the missing-package line.
func TestRestoreStopsOnAStillMissingPackage(t *testing.T) {
	cfg := "skills = [\"acme/nope\"]\n\n[sources]\n\"acme/nope\" = { uri = \"file:///does/not/exist.toml\" }\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "n\ny\n"})
	err := fx.run()
	if err == nil {
		t.Fatal("restore continued past a missing skill")
	}
	if !strings.Contains(err.Error(), "acme/nope") || !strings.Contains(err.Error(), "run byre restore again") {
		t.Fatalf("error = %v, want the missing-package line and the remedy", err)
	}
	if !strings.Contains(err.Error(), "byre skill install") {
		t.Errorf("error = %v, want the install command", err)
	}
	// The offer really was made: a failed fetch is the same outcome as a
	// decline, and either way restore stops.
	if out := fx.errb.String(); !strings.Contains(out, "this backup references skill") {
		t.Errorf("the install was never offered:\n%s", out)
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("a config was written")
	}
}

// ----------------------------------------------------------- step 4: review

func TestRestoreReviewStatesWhatTheFileAndTheMachineBring(t *testing.T) {
	cfg := claudeVolumeConfig +
		"\n[[mounts]]\nhost = \"/host/data\"\ntarget = \"/data\"\n" +
		"\n[[volumes]]\nname = \".gemini\"\nrole = \"state\"\ntarget = \"/home/dev/.gemini\"\nseed = { host = \"/host/seed\" }\n"
	archive := volumeTar(t, func(tw *tar.Writer) {
		tarFile(t, tw, "memory.md", "remember this")
		tarSymlink(t, tw, "abs", "/etc/passwd")
	})
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "n\n",
		vols: []testVol{{name: ".claude", tar: archive}},
		index: func(ix *backup.Index) {
			// A fiction the review prints as the source's note and asserts
			// nothing from: a base this restore does not use.
			ix.References.Base = "fiction:latest"
			ix.References.Layers = []string{"invented"}
		}})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	out := fx.errb.String()
	for _, want := range []string{
		"Restore backup -- the box this composes:",
		"--- backup ---",
		"Names this machine must satisfy",
		"mount /host/data -> /data",
		"seed source /host/seed for volume .gemini",
		"engine: the config names none; this restore uses docker",
		"What the source saw",
		"fiction:latest",
		"invented",
		"From the backup:",
		".claude: will be restored",
		".gemini: not in the backup; the first develop handles it as today",
		"abs -> /etc/passwd",
		restoreAuthorshipLine,
		"Restore this backup? byre.config will be written and 1 volume(s) created.",
		"byre: not restored; nothing written.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the review is missing %q:\n%s", want, out)
		}
	}
	// Declining writes nothing, pulls nothing, and leaves no staging behind.
	if storeConfig(t, fx.paths) != "" || len(f.created) != 0 || len(f.pulls) != 0 {
		t.Errorf("declining wrote state: config=%q created=%v pulls=%v", storeConfig(t, fx.paths), f.created, f.pulls)
	}
	if dirs := stagingDirs(t, fx.paths.Home); len(dirs) != 0 {
		t.Errorf("staging left behind: %v", dirs)
	}
	if ok, _ := hostopen.ExistsNoFollow(fx.paths.Dir); ok {
		t.Error("declining enrolled the project")
	}
}

// Report.Entries counts the entries the validator DROPPED as well as the ones
// it kept, so a volume whose only entries were FIFOs or devices rebuilds to an
// empty stream. The review has to call that what it is -- and still list every
// dropped entry.
func TestRestoreReviewCallsAFifoOnlyPayloadEmpty(t *testing.T) {
	archive := volumeTar(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeFifo, Name: "./pipe", Mode: 0o644, Format: tar.FormatPAX}); err != nil {
			t.Fatal(err)
		}
	})
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "n\n",
		vols: []testVol{{name: ".claude", tar: archive}}})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	out := fx.errb.String()
	if !strings.Contains(out, ".claude: will be restored, empty") {
		t.Errorf("a payload whose only entry was dropped must review as empty:\n%s", out)
	}
	if !strings.Contains(out, "pipe (FIFO)") {
		t.Errorf("the dropped FIFO must still be listed:\n%s", out)
	}
}

// The credential state the review prints is DERIVED from the verified config
// bytes, not taken from the index -- a file whose index says "none" over a
// config full of rows shows the rows.
func TestRestoreReviewShowsTheDerivedCredentialState(t *testing.T) {
	cfg := credBlock(credIdentityA, credRecipientA) + "[env_from_host]\nSTRIPE_KEY = " + fmt.Sprintf("%q", credRow("YWJj")) + "\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "n\n",
		index: func(ix *backup.Index) { ix.Config.Credentials = backup.CredNone }})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	out := fx.errb.String()
	if !strings.Contains(out, credentialStateLine(backup.CredRows)) {
		t.Errorf("the review did not print the derived credential state:\n%s", out)
	}
	if strings.Contains(out, "credentials: "+credentialStateLine(backup.CredNone)) {
		t.Errorf("the review repeated the index's claim:\n%s", out)
	}
	// The identity line names the BACKUP, not a preset -- the one apply string
	// restore's path had to reword (ADR 0059's P0 note). Pinned twice: the
	// literal fragment the ADR quotes, and the exported owner of the prose, so
	// neither a reword nor a wrong subject can pass.
	if !strings.Contains(out, "this backup brings its own credentials identity") {
		t.Errorf("the review does not say \"this backup brings its own credentials identity\":\n%s", out)
	}
	if strings.Contains(out, "this preset brings") {
		t.Errorf("the restore review calls the backup a preset:\n%s", out)
	}
	if !strings.Contains(out, CredentialsIdentityBroughtLine(backupSubject)) {
		t.Errorf("the review is missing %q:\n%s", CredentialsIdentityBroughtLine(backupSubject), out)
	}
}

// The three spellings the design names, plus the fourth legal state, each
// distinct: the review must never print the same sentence for two of them.
func TestCredentialStateLinesAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, state := range []string{backup.CredRows, backup.CredRowsNoIdentity, backup.CredIdentityOnly, backup.CredNone} {
		line := credentialStateLine(state)
		if line == "" || strings.Contains(line, "unknown") {
			t.Errorf("%s has no line of its own: %q", state, line)
		}
		if prev, dup := seen[line]; dup {
			t.Errorf("%s and %s share a line: %q", state, prev, line)
		}
		seen[line] = state
	}
}

// ----------------------------------------------------------- step 5: commit

func TestRestoreCommitWritesTheConfigAndPoursTheVolumes(t *testing.T) {
	cfg := claudeVolumeConfig + "\n[[volumes]]\nname = \".codex\"\nrole = \"state\"\ntarget = \"/home/dev/.codex\"\n"
	claude := volumeTar(t, func(tw *tar.Writer) { tarFile(t, tw, "memory.md", "remember this") })
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
		vols: []testVol{{name: ".claude", tar: claude}, {name: ".codex", tar: volumeTar(t, nil)}}})
	// .codex is already here: kept, its payload dropped.
	f.vols = map[string]bool{volumeName(fx.paths.ID, ".codex"): true}

	if err := fx.run(); err != nil {
		t.Fatal(err)
	}

	// The config landed byte-for-byte, with no applied marker beside it.
	if got := storeConfig(t, fx.paths); got != cfg {
		t.Errorf("the restored config is not the backup's bytes:\n%q\nwant\n%q", got, cfg)
	}
	if ok, _ := hostopen.ExistsNoFollow(filepath.Join(fx.paths.Dir, appliedRecord)); ok {
		t.Error("restore wrote an applied marker; no preset was applied")
	}
	// One volume created and poured; the existing one untouched.
	if want := []string{volumeName(fx.paths.ID, ".claude")}; len(f.created) != 1 || f.created[0] != want[0] {
		t.Fatalf("created = %v, want %v", f.created, want)
	}
	if len(f.removed) != 0 {
		t.Errorf("volumes removed: %v", f.removed)
	}
	pours := pourHelpers(f)
	if len(pours) != 1 {
		t.Fatalf("%d pour helpers ran, want 1", len(pours))
	}
	// The pour's contract: the labels, the pinned environment, the mount path
	// that is also the -C argument and the chown's target, the flags, and a
	// writable mount.
	h := pours[0]
	if h.Image != gen.DefaultBase {
		t.Errorf("pour image = %q, want the effective base", h.Image)
	}
	if h.ReadOnly {
		t.Error("the pour mounted the volume read-only")
	}
	if h.Env["TAR_OPTIONS"] != "" {
		t.Errorf("TAR_OPTIONS = %q, want it cleared", h.Env["TAR_OPTIONS"])
	}
	if _, ok := h.Env["TAR_OPTIONS"]; !ok {
		t.Error("TAR_OPTIONS is not pinned at all")
	}
	wantLabels := []string{helperKey + "=" + fx.paths.ID, helperRunKey + "=" + fx.rr.runID}
	if strings.Join(h.Labels, " ") != strings.Join(wantLabels, " ") {
		t.Errorf("labels = %v, want %v", h.Labels, wantLabels)
	}
	for _, l := range h.Labels {
		if strings.HasPrefix(l, labelKey+"=") {
			t.Errorf("a helper carries the project label: %v", h.Labels)
		}
	}
	if !strings.Contains(h.Script, "-C "+h.MountPath) {
		t.Errorf("the -C argument is not the mount path: %q at %q", h.Script, h.MountPath)
	}
	for _, flag := range []string{"tar -x", "-f -", "--no-same-owner", "--delay-directory-restore"} {
		if !strings.Contains(h.Script, flag) {
			t.Errorf("the pour script is missing %q: %q", flag, h.Script)
		}
	}
	if want := fmt.Sprintf("chown -R %d:%d %s", os.Getuid(), os.Getgid(), h.MountPath); !strings.Contains(h.Script, want) {
		t.Errorf("the pour script is missing %q: %q", want, h.Script)
	}
	// What the helper was fed is the REBUILT stream, not the file's bytes.
	entries := pouredEntries(t, f, 0)
	if entries["memory.md"] != "remember this" {
		t.Errorf("the poured stream does not hold the volume's file: %v", entries)
	}
	// The summary says what happened to each volume, and what comes next.
	out := fx.errb.String()
	for _, want := range []string{"volumes restored: .claude", "volumes kept", ".codex", "next: byre develop"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
	if dirs := stagingDirs(t, fx.paths.Home); len(dirs) != 0 {
		t.Errorf("staging left behind: %v", dirs)
	}
	// The restored project loads: `byre config` and `byre develop` read this
	// cascade with no further step.
	merged, err := config.Load(fx.dir)
	if err != nil {
		t.Fatalf("the restored project does not load: %v", err)
	}
	if len(merged.Volumes) != 2 {
		t.Errorf("the restored cascade declares %d volumes, want 2", len(merged.Volumes))
	}
}

// The preflight: no volume, the empty archive on stdin, and the GNU tar check
// -- all before the store is enrolled.
func TestRestoreProvesTheBaseBeforeWritingAnything(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.helperRuns) < 2 {
		t.Fatalf("%d helpers ran, want a preflight and a pour", len(f.helperRuns))
	}
	pre, stdin := f.helperRuns[0], f.helperStdin[0]
	if pre.Volume != "" {
		t.Errorf("the preflight mounted volume %q", pre.Volume)
	}
	if !bytes.Equal(stdin, backup.EmptyArchive()) {
		t.Errorf("the preflight was fed %d bytes, want the 1024-byte empty archive", len(stdin))
	}
	if !strings.Contains(pre.Script, "tar --version") {
		t.Errorf("the preflight does not ask what tar it is: %q", pre.Script)
	}
	if !strings.Contains(pre.Script, "chown -R") {
		t.Errorf("the preflight does not run the chown: %q", pre.Script)
	}
}

func TestRestoreRefusesABaseThatCannotPour(t *testing.T) {
	t.Run("a tar that is not GNU tar", func(t *testing.T) {
		f := &fakeRunner{helperStdout: func(h runner.Helper, w io.Writer) error {
			_, err := io.WriteString(w, "tar: busybox multi-call binary\n")
			return err
		}}
		fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
			vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
		err := fx.run()
		if err == nil || !strings.Contains(err.Error(), "GNU tar") || !strings.Contains(err.Error(), gen.DefaultBase) {
			t.Fatalf("error = %v, want the GNU tar refusal naming the image", err)
		}
		if storeConfig(t, fx.paths) != "" || len(f.created) != 0 {
			t.Error("project state was written")
		}
		if ok, _ := hostopen.ExistsNoFollow(fx.paths.Dir); ok {
			t.Error("the store was enrolled before the base was proven")
		}
	})
	t.Run("a base that cannot run the command", func(t *testing.T) {
		f := &fakeRunner{helperErr: func(h runner.Helper) error { return fmt.Errorf("chown: not found") }}
		fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
			vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
		err := fx.run()
		if err == nil || !strings.Contains(err.Error(), "cannot run byre's pour command") {
			t.Fatalf("error = %v, want the preflight refusal", err)
		}
		if storeConfig(t, fx.paths) != "" {
			t.Error("a config was written")
		}
	})
	t.Run("an unpullable base", func(t *testing.T) {
		f := &fakeRunner{images: map[string]bool{}, pullErr: fmt.Errorf("manifest unknown")}
		fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
			vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
		err := fx.run()
		if err == nil || !strings.Contains(err.Error(), gen.DefaultBase) {
			t.Fatalf("error = %v, want the pull failure naming the base", err)
		}
		if storeConfig(t, fx.paths) != "" {
			t.Error("a config was written")
		}
	})
}

// A volume that appeared between the review and the lock changes the review's
// own text, which is the comparison.
func TestRestoreRefusesWhenTheReviewChangedUnderTheLock(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	// The preflight runs after the review and before the lock: a volume
	// appearing there moves .claude from "will be restored" to "exists here".
	f.helperHook = func(h runner.Helper) {
		if h.Volume == "" {
			f.vols = map[string]bool{volumeName(fx.paths.ID, ".claude"): true}
		}
	}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "re-run byre restore") {
		t.Fatalf("error = %v, want the re-run refusal", err)
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("a config was written")
	}
	// A refusal under the lock leaves the enrolled store, by design.
	if ok, _ := hostopen.ExistsNoFollow(fx.paths.Dir); !ok {
		t.Error("the store vanished; the under-lock refusal is supposed to leave it")
	}
}

// ---------------------------------------------------------- step 6: failure

func TestRestorePourFailureRemovesTheVolumeAndTheConfigAndKeepsWhatLanded(t *testing.T) {
	cfg := claudeVolumeConfig + "\n[[volumes]]\nname = \".codex\"\nrole = \"state\"\ntarget = \"/home/dev/.codex\"\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}, {name: ".codex", tar: volumeTar(t, nil)}}})
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == volumeName(fx.paths.ID, ".codex") {
			return fmt.Errorf("no space left on device")
		}
		return nil
	}
	err := fx.run()
	wantExit1(t, err)
	if out := fx.errb.String(); !strings.Contains(out, ".codex") || !strings.Contains(out, "no space left on device") {
		t.Errorf("the summary does not name the volume and the cause:\n%s", out)
	}
	// The failing volume and the config are gone; the first volume stays.
	if got := storeConfig(t, fx.paths); got != "" {
		t.Errorf("the config survived the failure: %q", got)
	}
	if want := volumeName(fx.paths.ID, ".codex"); len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want just %s", f.removed, want)
	}
	if !f.vols[volumeName(fx.paths.ID, ".claude")] {
		t.Error("the volume poured whole before the failure was removed too")
	}
	out := fx.errb.String()
	for _, want := range []string{"already restored, and kept: .claude", "fix the cause and run byre restore again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreVolumeCreateFailureCleansUpTheSameWay(t *testing.T) {
	f := &fakeRunner{failCreate: map[string]bool{}}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	f.failCreate[volumeName(fx.paths.ID, ".claude")] = true
	err := fx.run()
	wantExit1(t, err)
	if out := fx.errb.String(); !strings.Contains(out, "creating volume") {
		t.Errorf("the summary does not name the create failure:\n%s", out)
	}
	if got := storeConfig(t, fx.paths); got != "" {
		t.Errorf("the config survived the failure: %q", got)
	}
	// Nothing was created, so nothing is removed -- and no scary
	// could-not-remove line is printed for a volume that never existed.
	if out := fx.errb.String(); strings.Contains(out, "could not be removed") {
		t.Errorf("the summary invented a removal failure:\n%s", out)
	}
}

func TestRestoreReportsAFailedVolumeRemoval(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	phys := volumeName(fx.paths.ID, ".claude")
	f.failRemove = map[string]bool{phys: true}
	f.helperErr = func(h runner.Helper) error {
		if h.Volume != "" {
			return fmt.Errorf("engine gone")
		}
		return nil
	}
	if err := fx.run(); err == nil {
		t.Fatal("the failed pour reported success")
	}
	out := fx.errb.String()
	for _, want := range []string{"docker volume rm " + phys, "byre forget", "EVERY volume and image"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreReportsAFailedConfigRemoval(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == "" {
			return nil
		}
		// The store becomes unwritable while the pour is failing, so the
		// rollback cannot unlink the config it wrote.
		if err := os.Chmod(fx.paths.Dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(fx.paths.Dir, 0o700) })
		return fmt.Errorf("extraction failed")
	}
	if err := fx.run(); err == nil {
		t.Fatal("the failed pour reported success")
	}
	out := fx.errb.String()
	for _, want := range []string{"this project now has a config", "byre forget"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
}

func TestRestoreReportsAFailedHelperRemoval(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	f.allContainers = map[string][]string{}
	f.forceRmErr = map[string]bool{"stuckhelper1": true}
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == "" {
			return nil
		}
		// A helper of THIS run is alive and cannot be removed.
		f.allContainers[helperRunKey+"="+fx.rr.runID] = []string{"stuckhelper1"}
		return fmt.Errorf("engine stopped answering")
	}
	if err := fx.run(); err == nil {
		t.Fatal("the failed pour reported success")
	}
	out := fx.errb.String()
	for _, want := range []string{"stuckhelper1", "docker rm -f stuckhelper1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
}

// Cancellation is the failure path: whatever was in flight goes, the config
// goes, and what was poured whole stays. commit installs the SIGINT-notified
// context these arms inject.
func TestRestoreCancellationCleansUpLikeAFailure(t *testing.T) {
	cfg := claudeVolumeConfig + "\n[[volumes]]\nname = \".codex\"\nrole = \"state\"\ntarget = \"/home/dev/.codex\"\n"

	t.Run("before the first pour", func(t *testing.T) {
		f := &fakeRunner{}
		fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
			vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}, {name: ".codex", tar: volumeTar(t, nil)}}})
		ctx, cancel := context.WithCancel(context.Background())
		fx.rr.cancelled = ctx
		// The interrupt lands while the base is being proven, which is after
		// the review and before the lock.
		f.helperHook = func(h runner.Helper) {
			if h.Volume == "" {
				cancel()
			}
		}
		err := fx.run()
		wantExit1(t, err)
		if out := fx.errb.String(); !strings.Contains(out, "cancelled") {
			t.Errorf("the summary does not say it was cancelled:\n%s", out)
		}
		if got := storeConfig(t, fx.paths); got != "" {
			t.Errorf("the config survived the cancellation: %q", got)
		}
		if len(f.created) != 0 || len(pourHelpers(f)) != 0 {
			t.Errorf("a volume was poured after the cancellation: created=%v pours=%d", f.created, len(pourHelpers(f)))
		}
	})

	t.Run("mid pour", func(t *testing.T) {
		f := &fakeRunner{}
		fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
			vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}, {name: ".codex", tar: volumeTar(t, nil)}}})
		ctx, cancel := context.WithCancel(context.Background())
		fx.rr.cancelled = ctx
		codex := volumeName(fx.paths.ID, ".codex")
		// The interrupt arrives while the second volume is being extracted,
		// and the helper ends the way a cancelled one really ends: its
		// container taken away under it.
		f.helperStdout = func(h runner.Helper, w io.Writer) error {
			if h.Volume == "" {
				_, err := io.WriteString(w, "tar (GNU tar) 1.34\n")
				return err
			}
			if h.Volume == codex {
				cancel()
				<-ctx.Done()
				return fmt.Errorf("container removed")
			}
			return nil
		}
		err := fx.run()
		wantExit1(t, err)
		if out := fx.errb.String(); !strings.Contains(out, ".codex") {
			t.Errorf("the summary does not name the volume in flight:\n%s", out)
		}
		if got := storeConfig(t, fx.paths); got != "" {
			t.Errorf("the config survived the cancellation: %q", got)
		}
		if len(f.removed) != 1 || f.removed[0] != codex {
			t.Errorf("removed = %v, want just %s", f.removed, codex)
		}
		if !f.vols[volumeName(fx.paths.ID, ".claude")] {
			t.Error(".claude was poured whole and should stay")
		}
	})
}

// -------------------------------------------------------- posture and notes

// Restore never enters the credential decrypt path: the resolved view it
// builds carries no credential rows at all, which is the input that path
// takes.
func TestRestoreNeverEntersTheCredentialDecryptPath(t *testing.T) {
	cfg := credBlock(credIdentityA, credRecipientA) + "[env_from_host]\nSTRIPE_KEY = " + fmt.Sprintf("%q", credRow("YWJj")) + "\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n"})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if fx.rr.rv.credFiles != nil || fx.rr.rv.credErr != nil {
		t.Errorf("the resolved view carries credential rows (%d files, err %v); restore must never collect them",
			len(fx.rr.rv.credFiles), fx.rr.rv.credErr)
	}
	if got := storeConfig(t, fx.paths); got != cfg {
		t.Error("the carried ciphertext did not land byte-for-byte")
	}
	// A login file inside a volume is restored without a word of warning
	// (Pete's ruling): nothing here reads the host, and nothing lectures.
	if out := fx.errb.String(); strings.Contains(strings.ToLower(out), "warning") {
		t.Errorf("restore printed a warning:\n%s", out)
	}
}

func TestRestoreSummaryNamesAPresetInTheCheckout(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	if err := os.WriteFile(filepath.Join(fx.dir, PresetName), []byte("base = \"debian:bookworm\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The preset makes the target non-empty and it is no git checkout, so this
	// fixture rides the switch; the summary line it adds is pinned elsewhere.
	fx.rr.allowNonempty = true
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	out := fx.errb.String()
	if !strings.Contains(out, PresetName) || !strings.Contains(out, "byre preset apply") {
		t.Errorf("the summary does not name the repo's preset:\n%s", out)
	}
	if !strings.Contains(out, "REPLACES") {
		t.Errorf("the summary does not say applying it replaces the restored config:\n%s", out)
	}
}

// A carried volume this machine declares machine-scoped is not poured: develop
// here would mount the machine volume, so the project volume would be an
// orphan.
func TestRestoreDoesNotPourAMachineScopedName(t *testing.T) {
	cfg := "[[volumes]]\nname = \".byre-identity\"\nrole = \"state\"\nscope = \"machine\"\ntarget = \"/home/dev/.byre-identity\"\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
		vols: []testVol{{name: ".byre-identity", tar: volumeTar(t, nil)}}})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 || len(pourHelpers(f)) != 0 {
		t.Errorf("a machine-scoped name was poured: created=%v pours=%d", f.created, len(pourHelpers(f)))
	}
	out := fx.errb.String()
	if !strings.Contains(out, "not restored: this machine declares .byre-identity machine-scoped") {
		t.Errorf("the review does not say why:\n%s", out)
	}
}

// A carried volume the destination declares as a cache is poured anyway, and
// noted: only backup's own classification drops a cache volume.
func TestRestorePoursACacheVolumeWithANote(t *testing.T) {
	cfg := "[[volumes]]\nname = \"npm\"\nrole = \"cache\"\ntarget = \"/home/dev/.npm\"\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
		vols: []testVol{{name: "npm", tar: volumeTar(t, nil)}}})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 {
		t.Fatalf("created = %v, want the cache volume poured", f.created)
	}
	if out := fx.errb.String(); !strings.Contains(out, "cache volume; poured all the same") {
		t.Errorf("the review does not note the cache role:\n%s", out)
	}
}

// ------------------------------------------------- the rest of step 1 and 3

// The collision fence every verb runs: an id whose record names another path
// refuses before the file is read.
func TestRestoreRefusesAnIDCollision(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	if err := os.MkdirAll(fx.paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fx.paths.PathRecord, []byte("/somewhere/else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("error = %v, want the id-collision refusal", err)
	}
}

// The shorter-id refusal holds on a RETRY too, when this project already has a
// store directory: the ownership question is asked as if this id were not
// enrolled, because projectVolumes' longest-id rule would otherwise hand the
// disputed name to this project and hide the collision.
func TestRestoreRefusesAShorterProjectsVolumeOnARetry(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	shorter, _, found := strings.Cut(fx.paths.ID, "-")
	if !found {
		t.Skip("this project id has no hyphen to split on")
	}
	if err := os.MkdirAll(filepath.Join(fx.paths.Home, "projects", shorter), 0o755); err != nil {
		t.Fatal(err)
	}
	// The retry: this project is already enrolled from an earlier attempt.
	if err := fx.paths.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	f.vols = map[string]bool{volumeName(fx.paths.ID, ".claude"): true}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), shorter) {
		t.Fatalf("error = %v, want the shorter project named on the retry too", err)
	}
}

// A missing layer stops at the review with the chain walk's own message: the
// path to create. Layers are not packages and there is nothing to offer.
func TestRestoreStopsOnAMissingLayer(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: "extends = \"work\"\n", in: "y\n"})
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "work") {
		t.Fatalf("error = %v, want the missing layer named", err)
	}
	if !strings.Contains(err.Error(), config.LayerPath(fx.paths.Home, "work")) {
		t.Errorf("error = %v, want the path to create", err)
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("a config was written")
	}
}

// The template is offered from the file's own hint BEFORE the cascade loads --
// the cascade cannot resolve without it -- and a template still missing after
// the offer stops restore.
func TestRestoreOffersTheTemplateThenStops(t *testing.T) {
	cfg := "template = \"acme/node\"\n\n[sources]\n\"acme/node\" = { uri = \"file:///does/not/exist.toml\" }\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\ny\n"})
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "acme/node") {
		t.Fatalf("error = %v, want the missing template named", err)
	}
	if !strings.Contains(err.Error(), "template") || !strings.Contains(err.Error(), "byre template install") {
		t.Errorf("error = %v, want the template's own kind and install command", err)
	}
	if out := fx.errb.String(); !strings.Contains(out, "this backup references template") {
		t.Errorf("the offer did not name the backup as the source of the hint:\n%s", out)
	}
}

// A skill a LAYER enables, with the hint that layer carries: the effective
// check reads the merged config and the merged [sources], so an inherited
// reference is offered and then stops restore like a direct one.
func TestRestoreStopsOnASkillInheritedFromALayer(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: "extends = \"work\"\n", in: "n\n"})
	layer := config.LayerPath(fx.paths.Home, "work")
	if err := os.MkdirAll(filepath.Dir(layer), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layer, []byte("skills = [\"acme/inherited\"]\n\n[sources]\n\"acme/inherited\" = { uri = \"file:///nope.toml\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "acme/inherited") {
		t.Fatalf("error = %v, want the inherited skill named", err)
	}
	if !strings.Contains(err.Error(), "byre skill install") {
		t.Errorf("error = %v, want the layer's own hint as the install command", err)
	}
}

// A skills.Resolve error that is NOT a missing package stops restore with that
// error: the review is never shown with skill grants omitted.
func TestRestoreStopsOnASkillSetErrorThatIsNotAMissingPackage(t *testing.T) {
	f := &fakeRunner{}
	// An installed skill that is not an agent skill, named as the agent.
	fx := restoreHarness(t, f, restoreOpts{cfg: "agent = \"firewall\"\n", in: "y\n"})
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "[agent] command") {
		t.Fatalf("error = %v, want the skill-set error", err)
	}
	if out := fx.errb.String(); strings.Contains(out, "Restore backup") {
		t.Errorf("the review was shown anyway:\n%s", out)
	}
}

// An engine that cannot be asked refuses, naming the engine: one byre could
// not inspect cannot be declared ready for a restore.
func TestRestoreRefusesAnUnreachableEngine(t *testing.T) {
	f := &fakeRunner{allErr: fmt.Errorf("Cannot connect to the Docker daemon")}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "docker") || !strings.Contains(err.Error(), "Cannot connect") {
		t.Fatalf("error = %v, want the engine and its complaint", err)
	}
}

func TestRestoreRefusesADeclinedDestinationEngine(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	fx.rr.detect = func(string) (engineRunner, error) {
		return nil, fmt.Errorf("refusing to run docker resolved out of a box-writable directory")
	}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "box-writable") {
		t.Fatalf("error = %v, want the declined engine's own refusal", err)
	}
}

// Restore picks the identity develop picks (resolveIdentity, not the
// never-refusing lifecycle fallback), so rootless Podman without the keep-id
// mapping refuses here exactly as it refuses there.
func TestRestoreRefusesRootlessPodmanWithoutKeepID(t *testing.T) {
	f := &fakeRunner{engine: runner.Podman, rootless: true}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "rootless Podman") {
		t.Fatalf("error = %v, want develop's rootless-Podman refusal", err)
	}
}

// Encrypted rows with no [credentials] identity: the third spelling, and the
// first develop refuses them as it does today.
func TestRestoreReviewShowsRowsWithNoIdentity(t *testing.T) {
	cfg := "[env_from_host]\nSTRIPE_KEY = " + fmt.Sprintf("%q", credRow("YWJj")) + "\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "n\n"})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if out := fx.errb.String(); !strings.Contains(out, credentialStateLine(backup.CredRowsNoIdentity)) {
		t.Errorf("the review did not print the rows-without-identity line:\n%s", out)
	}
}

// ------------------------------------------------ the directory restore made

func TestRestoreRemovesTheDirectoryItCreatedOnDecline(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "fresh")
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "n\n", dir: root})
	// The directory restore itself would create, created the way it creates it.
	got, created, err := restoreTarget(target)
	if err != nil || !created {
		t.Fatalf("restoreTarget: %v (created=%v)", err, created)
	}
	fx.rr.target, fx.rr.created = got, true
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := hostopen.ExistsNoFollow(target); ok {
		t.Errorf("%s survived a declined restore", target)
	}
	if out := fx.errb.String(); !strings.Contains(out, "removed the empty directory") {
		t.Errorf("the summary does not say the directory went:\n%s", out)
	}
}

func TestRestoreLeavesADirectoryTheUserHad(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "n\n"})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := hostopen.ExistsNoFollow(fx.dir); !ok {
		t.Errorf("%s was removed, and byre did not create it", fx.dir)
	}
}

// A post-bootstrap refusal leaves the directory and the enrolled store, and
// says so: the next restore of that path carries on (no config, same id).
func TestRestorePostBootstrapRefusalLeavesTheDirectoryAndSaysSo(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "fresh")
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}, dir: root})
	got, created, err := restoreTarget(target)
	if err != nil || !created {
		t.Fatalf("restoreTarget: %v (created=%v)", err, created)
	}
	fx.rr.target, fx.rr.created = got, true
	// A volume appears between the review and the lock: the under-lock
	// re-render refuses, after the bootstrap.
	f.helperHook = func(h runner.Helper) {
		if h.Volume == "" {
			paths, perr := project.Resolve(target)
			if perr != nil {
				t.Fatal(perr)
			}
			f.vols = map[string]bool{volumeName(paths.ID, ".claude"): true}
		}
	}
	if err := fx.run(); err == nil {
		t.Fatal("the under-lock re-check accepted a changed review")
	}
	if ok, _ := hostopen.ExistsNoFollow(target); !ok {
		t.Errorf("%s was removed after the store was enrolled", target)
	}
	if out := fx.errb.String(); !strings.Contains(out, "stay (byre had enrolled the project") {
		t.Errorf("the summary does not say what stays:\n%s", out)
	}
}

// A layer edit that moves only a GRANT row refuses under the lock: consent was
// to the whole review text, and egress is as much of it as a volume.
func TestRestoreRefusesAGrantOnlyLayerEditUnderTheLock(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: "extends = \"work\"\n" + claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	layer := config.LayerPath(fx.paths.Home, "work")
	if err := os.MkdirAll(filepath.Dir(layer), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(entry string) {
		if err := os.WriteFile(layer, []byte("egress = [\""+entry+"\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("reviewed.example")
	f.helperHook = func(h runner.Helper) {
		if h.Volume == "" {
			write("sneaked.example")
		}
	}
	err := fx.run()
	if err == nil || !strings.Contains(err.Error(), "re-run byre restore") {
		t.Fatalf("error = %v, want the re-run refusal", err)
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("a config was written")
	}
	if out := fx.errb.String(); !strings.Contains(out, "reviewed.example") {
		t.Errorf("the review did not show the grant it was asked about:\n%s", out)
	}
}

// The failure story end to end: a failed pour leaves the first volume, and a
// plain re-run then succeeds and reports that volume as kept.
func TestRestoreSucceedsOnARerunAfterAFailure(t *testing.T) {
	cfg := claudeVolumeConfig + "\n[[volumes]]\nname = \".codex\"\nrole = \"state\"\ntarget = \"/home/dev/.codex\"\n"
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}, {name: ".codex", tar: volumeTar(t, nil)}}})
	codex := volumeName(fx.paths.ID, ".codex")
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == codex {
			return fmt.Errorf("no space left on device")
		}
		return nil
	}
	if err := fx.run(); err == nil {
		t.Fatal("the failed pour reported success")
	}

	// The re-run: same project, same file, the cause fixed.
	f.helperErr = nil
	s, _, errb := testStreams("y\n", true)
	again := &restoreRun{s: s, target: fx.dir, detect: func(string) (engineRunner, error) { return f, nil }}
	err := again.run(fx.file)
	again.cleanup(err)
	if err != nil {
		t.Fatalf("the re-run failed: %v", err)
	}
	if got := storeConfig(t, fx.paths); got != cfg {
		t.Errorf("the re-run did not write the config: %q", got)
	}
	out := errb.String()
	if !strings.Contains(out, ".claude: exists here; the backed-up copy is dropped, and it is not seeded (it exists)") {
		t.Errorf("the re-run did not report the kept volume:\n%s", out)
	}
	if !strings.Contains(out, "volumes restored: .codex") {
		t.Errorf("the re-run did not pour the failing volume:\n%s", out)
	}
}

// A base the engine does not have is pulled, once, and the pull is said out
// loud -- it is durable state this restore left behind.
func TestRestorePullsTheBaseAndSaysSo(t *testing.T) {
	f := &fakeRunner{images: map[string]bool{}}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.pulls) != 1 || f.pulls[0] != gen.DefaultBase {
		t.Fatalf("pulls = %v, want one pull of %s", f.pulls, gen.DefaultBase)
	}
	if out := fx.errb.String(); !strings.Contains(out, "it stays, as any pull does") {
		t.Errorf("the pull was not reported:\n%s", out)
	}
}

// A cancellation whose helper cannot be ended must not hang: removeRunHelpers
// failed, so RunHelper will never return, and the wait for it is bounded by the
// engine cleanup deadline (pinned short here). The rollback then runs, saying
// what may still be holding the volume.
func TestRestoreCancellationDoesNotWaitForeverOnAStuckHelper(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	fx.rr.cleanupWait = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	fx.rr.cancelled = ctx
	// A helper of this run the engine will not remove, and which therefore
	// never returns. The run id is only known once the run has started, so it
	// is recorded from the preflight -- which runs on this goroutine, before
	// the pour, so nothing races the cleanup's own query.
	f.forceRmErr = map[string]bool{"stuckhelper1": true}
	claude := volumeName(fx.paths.ID, ".claude")
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	f.helperStdout = func(h runner.Helper, w io.Writer) error {
		if h.Volume == "" {
			f.allContainers = map[string][]string{helperRunKey + "=" + fx.rr.runID: {"stuckhelper1"}}
			_, err := io.WriteString(w, "tar (GNU tar) 1.34\n")
			return err
		}
		if h.Volume == claude {
			cancel()
			<-stuck
		}
		return nil
	}

	wantExit1(t, fx.run())
	got := fx.errb.String()
	for _, want := range []string{"stuckhelper1", "docker rm -f stuckhelper1", "may still be running", "removed once it is gone"} {
		if !strings.Contains(got, want) {
			t.Errorf("the account is missing %q:\n%s", want, got)
		}
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("the config survived the cancellation")
	}
}

// A rollback whose volume removal never answers must not hang holding the setup
// lock: the two engine calls on that path are bounded, so the config still
// goes, the summary still names the volume and its `volume rm` line, the lock
// is released, and the exit is 1 -- all of it at once, nowhere near the real
// thirty-second deadline. The PLAIN volume calls block here forever, so a
// rollback that reached for one instead of its bounded form hangs this test.
func TestRestoreRollbackDoesNotHangOnAStalledVolumeRemoval(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	claude := volumeName(fx.paths.ID, ".claude")
	stalled := make(chan struct{})
	t.Cleanup(func() { close(stalled) })
	// The pour fails, and from that moment the engine answers nothing about
	// volumes -- the ordinary shape of this failure. The plan's own existence
	// queries ran before it, so they are not stalled.
	f.helperErr = func(h runner.Helper) error {
		if h.Volume != claude {
			return nil
		}
		f.mu.Lock()
		f.volStall, f.volStallErr = stalled, errors.New("the engine is not answering")
		f.mu.Unlock()
		return errors.New("extraction died")
	}

	wantExit1(t, fx.run())
	got := fx.errb.String()
	for _, want := range []string{"could not be removed", "docker volume rm " + claude, forgetFallback} {
		if !strings.Contains(got, want) {
			t.Errorf("the account is missing %q:\n%s", want, got)
		}
	}
	if cfg := storeConfig(t, fx.paths); cfg != "" {
		t.Errorf("the config survived the rollback: %q", cfg)
	}
	// The lock went with the rollback: the next holder takes it at once.
	lk, ok, err := lock.TryAcquire(fx.paths.LockFile)
	if err != nil || !ok {
		t.Fatalf("the setup lock was not released: ok=%v err=%v", ok, err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
}

// Restore enters its CRITICAL phase under the lock, the moment before the
// config is written -- never before. The cancellation context belongs to that
// phase alone: a verb that is only WAITING (an install offer, the review, the
// lock queue) watches no context, so a Ctrl-C there is handled by the
// pre-commit action instead (TestRestoreCancellationBeforeTheCommitLeavesNothing)
// and a context armed over those waits would have swallowed the interrupt and
// left byre unkillable. Pinned where the fake engine can see it: the preflight
// and both plan queries run with no context, and the pour -- the first thing
// that fills a volume -- runs with one.
func TestRestoreArmsTheCancellationOnlyUnderTheLock(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	var timeline []string
	f.helperHook = func(h runner.Helper) {
		timeline = append(timeline, helperStageState(h, fx.rr.cancelled))
	}
	// The plan's volume queries run twice -- once for the review, once under the
	// lock -- and the second is the last thing before the arming point, so it is
	// where a handler installed one step too early shows up.
	f.probeHook = func(what string) {
		timeline = append(timeline, armedState(what, fx.rr.cancelled))
	}

	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(timeline, " "); got != "query:unarmed preflight:unarmed query:unarmed volume:armed" {
		t.Errorf("handler timeline = %q, want everything before the pour unarmed", got)
	}
}

// A Ctrl-C while the engine is stalled inside `volume create` must not be
// swallowed: the create is watched like the pour is, so the cancellation runs
// the rollback, the config goes, the lock is released and the exit is 1 -- all
// of it at once, nowhere near the real thirty-second deadline. The create never
// returns here, so a pour that waited for it unwatched hangs this test.
func TestRestoreCancellationDuringAStalledVolumeCreateRollsBack(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	fx.rr.cleanupWait = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	fx.rr.cancelled = ctx
	stalled := make(chan struct{})
	t.Cleanup(func() { close(stalled) })
	claude := volumeName(fx.paths.ID, ".claude")
	f.createHook = func(name string) {
		if name == claude {
			cancel() // the interrupt lands with the create in flight
		}
	}
	f.volCreateStall = stalled

	wantExit1(t, fx.run())
	got := fx.errb.String()
	for _, want := range []string{"cancelled while creating volume .claude", "rolling back", "has not finished creating volume " + claude} {
		if !strings.Contains(got, want) {
			t.Errorf("the account is missing %q:\n%s", want, got)
		}
	}
	if cfg := storeConfig(t, fx.paths); cfg != "" {
		t.Errorf("the config survived the cancellation: %q", cfg)
	}
	// The lock went with the rollback: the next holder takes it at once.
	lk, ok, err := lock.TryAcquire(fx.paths.LockFile)
	if err != nil || !ok {
		t.Fatalf("the setup lock was not released: ok=%v err=%v", ok, err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
}

// A stalled `volume create` can land AFTER the rollback has looked for it: byre
// cannot remove a volume the engine has not made yet, and a volume that IS at
// that name is one the next restore reports as already here and keeps -- this
// backup's copy of it dropped. That outcome is silent, so the rollback warns
// about exactly it and names the removal to run before re-running. Pinned end
// to end: the warning, the create landing after byre has gone, and the second
// restore keeping the empty volume the first run warned about.
func TestRestoreWarnsAStalledVolumeCreateMayLandAfterTheRollback(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}})
	fx.rr.cleanupWait = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	fx.rr.cancelled = ctx
	stalled := make(chan struct{})
	landed := make(chan string, 1)
	f.volCreateDone = landed
	claude := volumeName(fx.paths.ID, ".claude")
	f.createHook = func(name string) {
		if name == claude {
			cancel() // the interrupt lands with the create in flight
		}
	}
	f.volCreateStall = stalled

	wantExit1(t, fx.run())
	got := fx.errb.String()
	for _, want := range []string{
		"may still create volume " + claude,
		"keep whatever occupies that name",
		"docker volume rm " + claude,
		"byre forget",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the rollback account is missing %q:\n%s", want, got)
		}
	}

	// The engine finishes the create byre stopped waiting for, with byre gone:
	// an empty volume now stands at that name.
	close(stalled)
	select {
	case <-landed:
	case <-time.After(10 * time.Second):
		t.Fatal("the released create never landed")
	}
	if !f.vols[claude] {
		t.Fatalf("the released create left no volume: %v", f.vols)
	}

	// The rule the warning exists for: a volume that is here is kept, and the
	// backup's copy of it is dropped -- so this second restore pours nothing
	// into the empty volume the stalled create left.
	s2, _, errb2 := testStreams("y\n", true)
	rr2 := &restoreRun{s: s2, target: fx.dir, detect: func(string) (engineRunner, error) { return f, nil }}
	err2 := rr2.run(fx.file)
	rr2.cleanup(err2)
	if err2 != nil {
		t.Fatalf("the second restore failed: %v", err2)
	}
	out2 := errb2.String()
	var kept string
	for _, line := range strings.Split(out2, "\n") {
		if strings.Contains(line, "volumes kept") {
			kept = line
		}
	}
	if !strings.Contains(kept, ".claude") {
		t.Errorf("the second restore did not report volume .claude as kept (%q):\n%s", kept, out2)
	}
	if !strings.Contains(out2, "volumes restored: none") {
		t.Errorf("the second restore restored something:\n%s", out2)
	}
	if pours := pourHelpers(f); len(pours) != 0 {
		t.Errorf("a pour ran over the kept volume: %v", pours)
	}
}

// An interrupt BEFORE the critical section -- at an install offer, the review,
// the lock wait -- runs the handler's pre-commit action, which must leave
// nothing: no staging directory (it holds the backup's volume contents in
// plaintext) and no project directory byre created for this restore. Driven by
// calling that action where the review prompt blocks, because a test has no
// terminal to interrupt from.
func TestRestoreCancellationBeforeTheCommitLeavesNothing(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "fresh")
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "",
		vols: []testVol{{name: ".claude", tar: volumeTar(t, nil)}}, dir: root})
	got, created, err := restoreTarget(target)
	if err != nil || !created {
		t.Fatalf("restoreTarget: %v (created=%v)", err, created)
	}
	fx.rr.target, fx.rr.created = got, true
	// The prompt's own read is where the handler would fire, and what it left is
	// judged THERE: the real handler ends the process next, so the ordinary
	// exit's cleanup never runs and cannot stand in for this one.
	fx.rr.s.In = readerFunc(func([]byte) (int, error) {
		fx.rr.cancelBeforeTheCommit()
		if dirs := stagingDirs(t, fx.paths.Home); len(dirs) != 0 {
			t.Errorf("staging survived the cancellation: %v", dirs)
		}
		if ok, _ := hostopen.ExistsNoFollow(target); ok {
			t.Errorf("the directory byre created for this restore stays: %s", target)
		}
		if out := fx.errb.String(); !strings.Contains(out, restoreCancelledLine) {
			t.Errorf("the cancellation does not say nothing was written:\n%s", out)
		}
		return 0, io.EOF
	})

	// The read answers EOF after that, so the declined run unwinds as it would
	// have: nothing written, and no second complaint about what is already gone.
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if storeConfig(t, fx.paths) != "" {
		t.Error("a config was written")
	}
	if out := fx.errb.String(); strings.Contains(out, "the directory byre created for this restore stays") {
		t.Errorf("the ordinary exit complained about a directory the cancellation had removed:\n%s", out)
	}
}

// readerFunc is a Streams.In whose Read a test drives.
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// ------------------------------------------- where restore may be run at all

// The rule: the target must be an empty directory or the clean root of a git
// checkout. The field report is a `byre restore FILE` with no DIR, run from
// HOME, which made the home directory the project.

func TestRestoreProceedsInAnEmptyTarget(t *testing.T) {
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n"})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if storeConfig(t, fx.paths) == "" {
		t.Error("an empty target did not proceed to the config write")
	}
}

// A directory restore itself just created is empty by construction, so the
// no-DIR-into-a-fresh-path flow is unaffected by the rule.
func TestRestoreTargetItCreatedIsEmpty(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	target, created, err := restoreTarget(filepath.Join(t.TempDir(), "fresh"))
	if err != nil || !created {
		t.Fatalf("restoreTarget = %v, created=%v", err, created)
	}
	paths, err := project.Resolve(target)
	if err != nil {
		t.Fatal(err)
	}
	reason, err := restoreTargetState(target, paths)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Errorf("a directory restore just created was judged %q", reason)
	}
}

func TestRestoreRefusesANonEmptyNonCheckoutBeforeReadingTheFile(t *testing.T) {
	dir := t.TempDir()
	// A dotfile counts: the field report's HOME was "empty" by every listing
	// that hides them.
	for _, name := range []string{".bash_history", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n", dir: dir})
	err := fx.run()
	if err == nil {
		t.Fatal("restore accepted a directory that is neither empty nor a checkout")
	}
	for _, want := range []string{
		"empty directory or the clean root of a git checkout",
		"it holds 2 entries and is not a git checkout",
		"--allow-nonempty",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want %q in it", err, want)
		}
	}
	// The pin that the refusal lands before any reading: staging is created the
	// moment restore opens the file, and its parent directory survives the
	// run's own cleanup, so its ABSENCE means backup.NewStaging never ran.
	if _, serr := os.Stat(filepath.Join(fx.paths.Home, backup.StagingDirName)); !os.IsNotExist(serr) {
		t.Errorf("staging exists (%v): the file was read before the target was judged", serr)
	}
}

func TestRestoreProceedsInACleanGitRoot(t *testing.T) {
	repo := initRepo(t)
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n", dir: repo})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	if storeConfig(t, fx.paths) == "" {
		t.Error("a clean git root did not proceed to the config write")
	}
}

func TestRestoreRefusesADirtyGitRoot(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n", dir: repo})
	err := fx.run()
	if err == nil {
		t.Fatal("restore accepted a checkout with uncommitted changes")
	}
	if !strings.Contains(err.Error(), "it is a git checkout with uncommitted changes (1 files)") {
		t.Errorf("error = %v, want the dirty-tree rule with the count", err)
	}
}

func TestRestoreRefusesASubdirectoryOfACheckout(t *testing.T) {
	repo := initRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n", dir: sub})
	err := fx.run()
	if err == nil {
		t.Fatal("restore accepted a subdirectory of a checkout as its root")
	}
	// git prints its own resolution of the root, which is what the refusal
	// names: the test resolves the fixture the same way rather than assuming
	// the temp path contains no symlink.
	root, rerr := filepath.EvalSymlinks(repo)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(err.Error(), "it is inside a git checkout whose root is "+root) {
		t.Errorf("error = %v, want the enclosing root named (%s)", err, root)
	}
}

// No git to run is not "clean": byre cannot prove the tree clean, so it refuses
// the same way and says that is why.
func TestRestoreRefusesWhenGitCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n", dir: dir})
	// The pinned resolver finds no git at all. The pin set is process-wide and
	// an earlier test may have pinned git already, so it is cleared both ways.
	t.Setenv("PATH", "")
	hostexec.ResetPins()
	t.Cleanup(hostexec.ResetPins)
	err := fx.run()
	if err == nil {
		t.Fatal("restore accepted a directory it could not check")
	}
	if !strings.Contains(err.Error(), "byre could not check it with git (") {
		t.Errorf("error = %v, want the could-not-check reason", err)
	}
	if !strings.Contains(err.Error(), "--allow-nonempty") {
		t.Errorf("error = %v, want the switch named", err)
	}
}

// --allow-nonempty proceeds on every refusing case, and the run states the
// reason the refusal would have given -- in the review, before the y/n, and
// again in the summary.
func TestRestoreAllowNonemptyProceedsAndStatesTheReason(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) (dir, reason string)
	}{
		{"not a checkout", func(t *testing.T) (string, string) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			return dir, "it holds 1 entries and is not a git checkout"
		}},
		{"dirty checkout", func(t *testing.T) (string, string) {
			repo := initRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			return repo, "it is a git checkout with uncommitted changes (1 files)"
		}},
		{"inside a checkout", func(t *testing.T) (string, string) {
			repo := initRepo(t)
			sub := filepath.Join(repo, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			root, rerr := filepath.EvalSymlinks(repo)
			if rerr != nil {
				t.Fatal(rerr)
			}
			return sub, "it is inside a git checkout whose root is " + root
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, reason := tc.setup(t)
			f := &fakeRunner{}
			fx := restoreHarness(t, f, restoreOpts{cfg: claudeVolumeConfig, in: "y\n", dir: dir})
			fx.rr.allowNonempty = true
			if err := fx.run(); err != nil {
				t.Fatal(err)
			}
			if storeConfig(t, fx.paths) == "" {
				t.Error("--allow-nonempty did not proceed to the config write")
			}
			want := restoreNonEmptyLine(dir, reason)
			if n := strings.Count(fx.errb.String(), want); n != 2 {
				t.Errorf("the line %q appears %d times, want it in the review AND the summary:\n%s", want, n, fx.errb.String())
			}
		})
	}
}

// The review MARKS a host name that is not on this machine now. A mark, not a
// gate: restore refuses on none of them.
func TestRestoreReviewMarksHostNamesMissingNow(t *testing.T) {
	present := t.TempDir()
	absent := filepath.Join(t.TempDir(), "gone")
	cfg := fmt.Sprintf("[[mounts]]\nhost = %q\ntarget = \"/here\"\n\n[[mounts]]\nhost = %q\ntarget = \"/gone\"\n", present, absent)
	f := &fakeRunner{}
	fx := restoreHarness(t, f, restoreOpts{cfg: cfg, in: "n\n"})
	if err := fx.run(); err != nil {
		t.Fatal(err)
	}
	const mark = "(missing on this machine now)"
	var marked, clean bool
	for _, line := range strings.Split(fx.errb.String(), "\n") {
		if !strings.Contains(line, "- mount ") {
			continue
		}
		switch {
		case strings.Contains(line, absent):
			marked = strings.Contains(line, mark)
		case strings.Contains(line, present):
			clean = !strings.Contains(line, mark)
		}
	}
	if !marked {
		t.Errorf("the absent mount host carries no mark:\n%s", fx.errb.String())
	}
	if !clean {
		t.Errorf("a host that IS here was marked missing:\n%s", fx.errb.String())
	}
}
