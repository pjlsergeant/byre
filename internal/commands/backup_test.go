package commands

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/backup"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// ---------------------------------------------------------------- harness

const testRunID = "0123456789abcdef"

// testRecipient is a real age public key: the [credentials] block's recipient
// is validated as one, so a placeholder would fail the parse rather than the
// rule under test.
const testRecipient = "age1pk7hww3hv92agzfn67t5wprsurn88dtz5kxt6dg2l0y7ysjwmufqvlj74r"

// volumeTar builds a REAL plain tar of the shape GNU tar writes for `-C /vol
// .`: the "./" root header first, then whatever build adds. The validator
// reads these bytes, so a fake capture has to produce a real archive --
// otherwise the test would pass over an archive byre could never restore.
func volumeTar(t *testing.T, build func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(volumeRootHeader()); err != nil {
		t.Fatal(err)
	}
	if build != nil {
		build(tw)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// volumeRootHeader is GNU tar's "./" volume-root header, the one spelling every
// built payload and the empty one start from.
func volumeRootHeader() *tar.Header {
	return &tar.Header{Typeflag: tar.TypeDir, Name: "./", Mode: 0o755, Format: tar.FormatPAX}
}

func tarFile(t *testing.T, tw *tar.Writer, name, content string) {
	t.Helper()
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: "./" + name, Mode: 0o644, Size: int64(len(content)), Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, content); err != nil {
		t.Fatal(err)
	}
}

func tarSymlink(t *testing.T, tw *tar.Writer, name, target string) {
	t.Helper()
	hdr := &tar.Header{Typeflag: tar.TypeSymlink, Name: "./" + name, Linkname: target, Mode: 0o777, Format: tar.FormatPAX}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
}

// captureStdout is the fake engine's helper stdout: the GNU tar banner for the
// preflight (the helper with no volume) and a per-volume archive for each
// capture. A volume with no entry in archives gets an empty-but-valid one.
func captureStdout(archives map[string][]byte) func(runner.Helper, io.Writer) error {
	return func(h runner.Helper, w io.Writer) error {
		if h.Volume == "" {
			_, err := io.WriteString(w, "tar (GNU tar) 1.34\n")
			return err
		}
		if b, ok := archives[h.Volume]; ok {
			_, err := w.Write(b)
			return err
		}
		// An empty volume: GNU tar still emits the root header.
		_, err := w.Write(emptyVolumeTar)
		return err
	}
}

// emptyVolumeTar is what GNU tar writes for an empty volume: the root header
// and nothing else.
var emptyVolumeTar = func() []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(volumeRootHeader())
	_ = tw.Close()
	return buf.Bytes()
}()

// backupHarness wires one backup over the fake engine: a project with a store
// config, a hand-built resolved view (so a test states the set develop would
// run without installing packages), an output path under a temp dir, and a
// fixed run id so the helper labels are predictable.
func backupHarness(t *testing.T, cfgText string, rv resolved, f *fakeRunner, s Streams, opts BackupOptions) (*backupRun, string, project.Paths) {
	t.Helper()
	p, proj := testPaths(t)
	writeStoreConfig(t, proj, cfgText)
	b, out := backupHarnessAt(t, p, rv, f, s, opts)
	return b, out, p
}

// backupHarnessAt is backupHarness's second half, over a project the caller
// already built: the arm that has to write layers on disk and run the REAL
// resolver needs the byre home to exist before the view is resolved.
func backupHarnessAt(t *testing.T, p project.Paths, rv resolved, f *fakeRunner, s Streams, opts BackupOptions) (*backupRun, string) {
	t.Helper()
	if f.helperStdout == nil {
		f.helperStdout = captureStdout(nil)
	}
	if f.images == nil {
		// A project image already on this engine is the ordinary case: the
		// capture runs in the box's own image, no pull.
		f.images = map[string]bool{imageTag(p.ID, 1000, 1000): true}
	}
	if opts.Output == "" {
		opts.Output = filepath.Join(t.TempDir(), "out.byre-backup.tar.gz")
	}
	b := &backupRun{
		s: s, paths: p, rv: rv, source: f,
		ident: runner.Identity{UID: 1000, GID: 1000},
		uid:   1000, gid: 1000, runID: testRunID, opts: opts,
	}
	return b, opts.Output
}

// stagingDirs lists the run directories under the byre home's staging dir --
// the assertion that staging is gone on every exit.
func stagingDirs(t *testing.T, home string) []string {
	t.Helper()
	dirs, err := backup.StaleStaging(home, "")
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

// readBack reads a published backup file the way `byre restore` will: the same
// reader, the same bounds, the same per-payload digests.
func readBack(t *testing.T, path, prefix string) *backup.File {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A staging home of its own: the test still asserts that the BACKUP's
	// staging is gone, and the reader's own staging must not be mistaken for it.
	st, err := backup.NewStaging(t.TempDir(), "readbackrunid000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Remove() })
	f, err := backup.Read(bytes.NewReader(raw), st, prefix)
	if err != nil {
		t.Fatalf("the file byre wrote is not one byre reads: %v", err)
	}
	return f
}

// --------------------------------------------------------------- the file

// The round trip is the whole contract: what backup publishes is what the
// reader accepts, with the config byte-for-byte and the index's rows derived
// from the staged bytes rather than asserted by the writer.
func TestBackupRoundTripsThroughTheReader(t *testing.T) {
	cfg := "[[volumes]]\nname = \".claude\"\nrole = \"state\"\ntarget = \"/home/dev/.claude\"\n"
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	archive := volumeTar(t, func(tw *tar.Writer) {
		tarFile(t, tw, "memory.md", "remember this")
		tarSymlink(t, tw, "abs", "/etc/passwd")
		tarSymlink(t, tw, "up", "../../escape")
	})
	f := &fakeRunner{vols: map[string]bool{volumeName("x", ".claude"): true}}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, cfg, rv, f, s, BackupOptions{})
	// The fake's volume map is keyed on the real project id.
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.helperStdout = captureStdout(map[string][]byte{volumeName(p.ID, ".claude"): archive})

	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	if file.Index.Format != backup.Format || file.Index.MinByreVersion != backup.MinByreVersion {
		t.Fatalf("index format/min version = %d/%q", file.Index.Format, file.Index.MinByreVersion)
	}
	if file.Index.Folder != filepath.Base(p.Canonical) {
		t.Errorf("folder = %q, want the project directory's base name %q", file.Index.Folder, filepath.Base(p.Canonical))
	}
	if file.Index.Engine != "docker" {
		t.Errorf("engine = %q, want the engine the backup read", file.Index.Engine)
	}
	stored, err := os.ReadFile(filepath.Join(p.Dir, config.ProjectConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file.Config, stored) {
		t.Errorf("the config must travel byte-for-byte:\ngot  %q\nwant %q", file.Config, stored)
	}
	if len(file.Volumes) != 1 || file.Volumes[0].Name != ".claude" {
		t.Fatalf("volumes = %+v, want one row for .claude", file.Volumes)
	}
	rep := file.Volumes[0].Report
	if len(rep.Absolute) != 1 || rep.Absolute[0].Target != "/etc/passwd" {
		t.Errorf("absolute symlink target must be carried verbatim: %+v", rep.Absolute)
	}
	if len(rep.Traversing) != 1 {
		t.Errorf("a traversing symlink must be listed: %+v", rep.Traversing)
	}
	if rep.Entries != file.Index.Volumes[0].Entries {
		t.Errorf("entry count %d does not match the index's %d", rep.Entries, file.Index.Volumes[0].Entries)
	}
	// 0600: the file holds a project's volumes.
	fi, err := hostopen.StatNoFollow(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("published mode = %v, want 0600", fi.Mode().Perm())
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("staging must be removed on success, found %v", dirs)
	}
	if got := errb.String(); !strings.Contains(got, "/etc/passwd") || !strings.Contains(got, "leaves the volume") {
		t.Errorf("the summary must list both symlink kinds:\n%s", got)
	}
}

// A refusal, not an overwrite: a second backup the same day names --output.
func TestBackupRefusesAnExistingOutputEntryAndAMissingParent(t *testing.T) {
	p, _ := testPaths(t)
	dir := t.TempDir()
	taken := filepath.Join(dir, "taken.tar.gz")
	if err := os.WriteFile(taken, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := backupOutput(p, taken, time.Now())
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--output") {
		t.Fatalf("an existing entry must refuse naming --output, got %v", err)
	}
	// A symlink at the name is an existing entry too: judged without following.
	link := filepath.Join(dir, "link.tar.gz")
	if err := os.Symlink("/nowhere", link); err != nil {
		t.Fatal(err)
	}
	if _, err := backupOutput(p, link, time.Now()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a dangling symlink at the output path must refuse, got %v", err)
	}
	_, err = backupOutput(p, filepath.Join(dir, "nope", "x.tar.gz"), time.Now())
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), filepath.Join(dir, "nope")) {
		t.Fatalf("a missing output parent must refuse naming it, got %v", err)
	}
}

// The default name is the PROJECT's: a backup taken from a linked worktree is
// a backup of the project (ADR 0009), so it carries the main directory's name,
// not the side checkout's.
func TestBackupDefaultNameIsTheMainDirectoryAndTheLocalDate(t *testing.T) {
	p := project.Paths{Canonical: "/home/u/work/torn", WorkDir: "/home/u/wt/torn-fix", IsWorktree: true}
	got := defaultBackupName(p, time.Date(2026, 10, 2, 23, 30, 0, 0, time.Local))
	if got != "torn-2026-10-02.byre-backup.tar.gz" {
		t.Fatalf("default name = %q, want the main directory's base name and the local date", got)
	}
}

// --------------------------------------------------------- volume selection

// Every project volume on the source engine comes along -- including one no
// declaration covers -- and everything left behind says why, in the stated
// order of reasons.
func TestBackupVolumeSelectionAndNotCarriedReasons(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
		{Name: "cache-cfg", Role: "cache", Target: "/home/dev/.cache/a"},
	}}), skills.Resolved{Skills: []skills.Skill{skillWithVolumes("pete/one",
		config.Volume{Name: "cache-skill", Role: "cache", Target: "/home/dev/.cache/b"},
		config.Volume{Name: "login", Role: "state", Scope: "machine", Target: "/home/dev/.byre-identity/x"},
	)}})
	f := &fakeRunner{}
	other := &fakeRunner{engine: runner.Podman}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	b.others = []engineRunner{other}
	// A longer project id owns byre-<id>-other-state: it spells that project's
	// volume, not this one's.
	longer := p.ID + "-other"
	if err := os.MkdirAll(filepath.Join(p.Home, "projects", longer), 0o755); err != nil {
		t.Fatal(err)
	}
	f.vols = map[string]bool{
		volumeName(p.ID, ".claude"):             true, // declared, carried
		volumeName(p.ID, "oneoff"):              true, // declared by nothing, still carried
		volumeName(p.ID, "cache-cfg"):           true, // cache by the config
		volumeName(p.ID, "cache-skill"):         true, // cache by a skill
		volumeName(longer, "state"):             true, // another project's
		machineVolumeName(1000, "orphan-login"): true, // machine-scoped by its NAME alone
	}
	other.vols = map[string]bool{volumeName(p.ID, "podmanonly"): true}

	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	var carried []string
	for _, v := range file.Index.Volumes {
		carried = append(carried, v.Name)
	}
	if strings.Join(carried, ",") != ".claude,oneoff" {
		t.Fatalf("carried = %v, want .claude and oneoff (cache roles, another project's and the other engine's left out)", carried)
	}
	got := errb.String()
	for _, want := range []string{
		"oneoff",
		"this project's set does not declare it",
		"cache-cfg: not carried (cache)",
		"cache-skill: not carried (cache)",
		"login: not carried; the destination binds its own machine-scoped login",
		"orphan-login: not carried; the destination binds its own machine-scoped orphan-login",
		"state: not carried: owned by project " + longer,
		"podmanonly: not carried (on podman; this backup reads docker)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary does not say %q:\n%s", want, got)
		}
	}
}

// Left-behind rows are one per VOLUME, not one per logical name. A logical
// name is the part after a prefix, so a machine-scoped `login` and this
// project's `login` on the engine the backup is not reading are two different
// volumes, as are a cache declaration and a longer project's volume that trims
// to the same name. ADR 0059 has each of them named on both surfaces, so a row
// for one must never hide another.
func TestBackupNamesEveryLeftBehindVolumeSharingALogicalName(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		// Named for what the longer project's volume below trims to under THIS
		// project's prefix: the two rows collide by logical name.
		{Name: "other-cache1", Role: "cache", Target: "/home/dev/.cache/a"},
	}}), skills.Resolved{Skills: []skills.Skill{skillWithVolumes("pete/one",
		config.Volume{Name: "login", Role: "state", Scope: "machine", Target: "/home/dev/.byre-identity/x"})}})
	f := &fakeRunner{}
	other := &fakeRunner{engine: runner.Podman}
	// A terminal, so the preview prints too: every row has to appear on BOTH
	// surfaces, and the summary alone would pass while the preview hid one.
	s, _, errb := testStreams("y\n", true)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	b.others = []engineRunner{other}
	longer := p.ID + "-other"
	if err := os.MkdirAll(filepath.Join(p.Home, "projects", longer), 0o755); err != nil {
		t.Fatal(err)
	}
	f.vols = map[string]bool{
		volumeName(p.ID, ".claude"):      true, // carried
		machineVolumeName(1000, "login"): true, // the machine's login
		volumeName(longer, "cache1"):     true, // byre-<id>-other-cache1
	}
	// This project's own login, on the engine this backup is not reading.
	other.vols = map[string]bool{volumeName(p.ID, "login"): true}

	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	if len(file.Index.Volumes) != 1 || file.Index.Volumes[0].Name != ".claude" {
		t.Fatalf("carried = %+v, want .claude alone", file.Index.Volumes)
	}
	got := errb.String()
	for _, want := range []string{
		"login: not carried; the destination binds its own machine-scoped login",
		"login: not carried (on podman; this backup reads docker)",
		"other-cache1: not carried (cache)",
		"other-cache1: not carried: owned by project " + longer,
	} {
		// Twice: the preview and the summary print the same lists.
		if n := strings.Count(got, want); n != 2 {
			t.Errorf("%q appears %d times, want 2 (preview and summary):\n%s", want, n, got)
		}
	}
}

// skillWithVolumes is a skill that declares volumes and nothing else.
func skillWithVolumes(name string, vols ...config.Volume) skills.Skill {
	var sk skills.Skill
	sk.Name = name
	sk.File.Volumes = vols
	return sk
}

// --no-volume takes a LOGICAL name. A repeat is harmless; a name that is
// already not carried is refused with its reason; an unknown one is refused
// naming the candidates.
func TestBackupNoVolumeDropsRepeatsAndRefusals(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
		{Name: "cache", Role: "cache", Target: "/home/dev/.cache"},
	}}), skills.Resolved{Skills: []skills.Skill{skillWithVolumes("pete/one",
		config.Volume{Name: "login", Role: "state", Scope: "machine", Target: "/home/dev/.byre-identity/x"})}})

	// Dropped, and a repeat of the same name changes nothing.
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{NoVolumes: []string{".claude", ".claude"}})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, "keepme"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	if len(file.Index.Volumes) != 1 || file.Index.Volumes[0].Name != "keepme" {
		t.Fatalf("--no-volume must drop exactly .claude: %+v", file.Index.Volumes)
	}
	if !strings.Contains(errb.String(), ".claude: not carried (--no-volume)") {
		t.Errorf("a dropped volume must be named as such:\n%s", errb.String())
	}

	// Each already-not-carried reason, and the stated precedence when two
	// apply (cache before another engine).
	for _, tc := range []struct {
		name, want string
		setup      func(f, other *fakeRunner, p project.Paths)
	}{
		{"cache", "not carried (cache)", nil},
		{"login", "the destination binds its own machine-scoped", nil},
		{"podmanonly", "not carried (on podman", func(f, other *fakeRunner, p project.Paths) {
			other.vols = map[string]bool{volumeName(p.ID, "podmanonly"): true}
		}},
		{"cache", "not carried (cache)", func(f, other *fakeRunner, p project.Paths) {
			// Both cache and present on the other engine: cache is printed.
			other.vols = map[string]bool{volumeName(p.ID, "cache"): true}
		}},
	} {
		f := &fakeRunner{}
		other := &fakeRunner{engine: runner.Podman}
		b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{NoVolumes: []string{tc.name}})
		b.others = []engineRunner{other}
		f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
		if tc.setup != nil {
			tc.setup(f, other, p)
		}
		err := b.run()
		if err == nil || !strings.Contains(err.Error(), "--no-volume "+tc.name) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--no-volume %s must refuse with %q, got %v", tc.name, tc.want, err)
		}
		if ok, _ := hostopen.ExistsNoFollow(out); ok {
			t.Errorf("--no-volume %s refused but wrote a file", tc.name)
		}
	}

	// An unknown name names what IS carried.
	f2 := &fakeRunner{}
	b2, _, p2 := backupHarness(t, "", rv, f2, discardStreams(), BackupOptions{NoVolumes: []string{"typo"}})
	f2.vols = map[string]bool{volumeName(p2.ID, ".claude"): true}
	err := b2.run()
	if err == nil || !strings.Contains(err.Error(), "--no-volume typo") || !strings.Contains(err.Error(), ".claude") {
		t.Fatalf("an unknown --no-volume name must be refused naming the candidates, got %v", err)
	}
}

// A project with no volumes backs up its config alone, and says so.
func TestBackupWithNoVolumesCarriesTheConfigAlone(t *testing.T) {
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "base = \"debian:trixie\"\n", combine(merged(config.Config{}), skills.Resolved{}), f, s, BackupOptions{})
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	if len(file.Index.Volumes) != 0 {
		t.Fatalf("expected no volume rows, got %+v", file.Index.Volumes)
	}
	if !strings.Contains(errb.String(), "the config alone") {
		t.Errorf("a volumeless backup must say so:\n%s", errb.String())
	}
	if len(f.helperRuns) != 1 || f.helperRuns[0].Volume != "" {
		t.Errorf("only the preflight helper should have run: %+v", f.helperRuns)
	}
}

// ------------------------------------------------------------- the references

// The references come from the set develop runs -- inherited keys, skill
// contributions and all -- and the layer chain is named leaf-first.
func TestBackupReferencesComeFromTheResolvedSet(t *testing.T) {
	cfgDir := t.TempDir()
	ctxFile := filepath.Join(cfgDir, "notes.md")
	if err := os.WriteFile(ctxFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Engine:       "docker",
		Template:     "byre/go",
		Agent:        "byre/claude",
		Base:         "debian:trixie",
		WorktreeBase: "~/wt",
		Mounts: []config.Mount{
			{Host: "/host/on", Target: "/t/on"},
			{Host: "/host/off", Target: "/t/off", Disabled: true},
		},
		Contexts: []config.ContextDecl{{Name: "notes", File: ctxFile}},
		Files:    map[string]string{"secrets/.netrc": "/home/dev/.netrc"},
		ClaudeSkills: []config.ClaudeSkill{
			{Name: "cs", Path: filepath.Join(cfgDir, "cs")},
		},
		Volumes: []config.Volume{
			{Name: "seeded", Role: "state", Target: "/home/dev/.s", Seed: &config.Seed{Host: "/host/seed"}},
		},
	}
	rv := combine(merged(cfg), skills.Resolved{Skills: []skills.Skill{skillWithVolumes("byre/claude",
		config.Volume{Name: ".claude", Role: "state", Target: "/home/dev/.claude"})}})
	rv.claudeSkills = []skills.ClaudeSkillDecl{{CS: cfg.ClaudeSkills[0]}}

	f := &fakeRunner{}
	b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, "seeded"): true}

	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	refs := readBack(t, out, volumePrefix(p.ID)).Index.References
	// The layer chain is NOT asserted here: a hand-built resolved view carries
	// `extends`, which a real one never does, so this fixture cannot tell a
	// recorded chain from an empty one. The arm below runs the real resolver.
	if refs.Template != "byre/go" || refs.Agent != "byre/claude" {
		t.Errorf("template/agent = %q/%q", refs.Template, refs.Agent)
	}
	if strings.Join(refs.Skills, ",") != "byre/claude" {
		t.Errorf("skills = %v", refs.Skills)
	}
	if strings.Join(refs.Mounts, ",") != "/host/on" {
		t.Errorf("mounts = %v, want the enabled one only (a disabled mount is inert)", refs.Mounts)
	}
	if strings.Join(refs.Context, ",") != ctxFile {
		t.Errorf("context = %v", refs.Context)
	}
	if strings.Join(refs.ClaudeSkills, ",") != filepath.Join(cfgDir, "cs") {
		t.Errorf("claude_skills = %v", refs.ClaudeSkills)
	}
	if strings.Join(refs.Seeds, ",") != "/host/seed" {
		t.Errorf("seeds = %v", refs.Seeds)
	}
	// A [files] source is project-relative: it is a name the RESTORED tree has
	// to carry, and the review lists it, so the index records what the source
	// saw of it too.
	if strings.Join(refs.Files, ",") != "secrets/.netrc" {
		t.Errorf("files = %v, want the config's [files] source", refs.Files)
	}
	if refs.Engine != "docker" || refs.Base != "debian:trixie" {
		t.Errorf("engine/base = %q/%q", refs.Engine, refs.Base)
	}
	if refs.WorktreeBase != "~/wt" {
		t.Errorf("worktree_base = %q (carried as the config spells it)", refs.WorktreeBase)
	}
}

// An unset base is the generator's default, never "": the destination pours
// with the base the source would have built from.
func TestBackupReferencesBaseDefaultsToTheGeneratorsBase(t *testing.T) {
	f := &fakeRunner{}
	b, out, p := backupHarness(t, "", combine(merged(config.Config{}), skills.Resolved{}), f, discardStreams(), BackupOptions{})
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, out, volumePrefix(p.ID)).Index.References.Base; got != "debian:bookworm" {
		t.Fatalf("references.base = %q, want the generator's default", got)
	}
}

// The layer chain is read off the PROJECT config's own bytes, never the
// resolved view: config.resolveWithCatalog blanks `extends` on every resolved
// config (the chain walk consumed it), so a backup that asked the view would
// record an empty list for every layered project. Real resolution over real
// layers on disk is the only fixture that can tell the two apart.
func TestBackupReferencesRecordTheLayerChainUnderRealResolution(t *testing.T) {
	p, proj := testPaths(t)
	// base <- work <- the project: the chain is recorded leaf-first.
	writeLayer(t, p.Home, "base", "")
	writeLayer(t, p.Home, "work", "extends = \"base\"\n")
	writeStoreConfig(t, proj, "extends = \"work\"\n")
	rv, err := resolve(p, proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rv.cfg.Extends != "" {
		t.Fatalf("resolved extends = %q; this arm exists because a resolved config blanks it", rv.cfg.Extends)
	}
	f := &fakeRunner{}
	b, out := backupHarnessAt(t, p, rv, f, discardStreams(), BackupOptions{})
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, out, volumePrefix(p.ID)).Index.References.Layers; strings.Join(got, ",") != "work,base" {
		t.Fatalf("layers = %v, want the chain leaf-first", got)
	}
}

func writeLayer(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "layers", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layer.config"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A missing layer refuses with the loader's own message -- the path to create
// -- because the classification and the references come from that chain.
func TestBackupRefusesAMissingLayerWithTheLoadersMessage(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	// In the STORE config, not the resolved view: a resolved config never
	// carries `extends`, so the chain pointer can only come from these bytes.
	b, out, p := backupHarness(t, "extends = \"gone\"\n", rv, f, discardStreams(), BackupOptions{})
	err := b.run()
	if err == nil || !strings.Contains(err.Error(), "gone") || !strings.Contains(err.Error(), config.LayerPath(p.Home, "gone")) {
		t.Fatalf("a missing layer must refuse naming it and the path to create, got %v", err)
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a refused backup must publish nothing")
	}
}

// ------------------------------------------------------------ the helper image

func TestBackupPrefersABuiltImageOnTheSourceEngine(t *testing.T) {
	f := &fakeRunner{}
	b, _, p := backupHarness(t, "", combine(merged(config.Config{}), skills.Resolved{}), f, discardStreams(), BackupOptions{})
	f.images = map[string]bool{imageTag(p.ID, 1000, 1000): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.pulls) != 0 {
		t.Errorf("a built image was present; nothing should be pulled: %v", f.pulls)
	}
	if f.helperRuns[0].Image != imageTag(p.ID, 1000, 1000) {
		t.Errorf("helper image = %q, want the project's built image", f.helperRuns[0].Image)
	}
}

func TestBackupPullsTheBaseAndSaysSo(t *testing.T) {
	f := &fakeRunner{images: map[string]bool{}}
	s, _, errb := testStreams("", false)
	b, _, p := backupHarness(t, "", combine(merged(config.Config{Base: "debian:trixie"}), skills.Resolved{}), f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.pulls) != 1 || f.pulls[0] != "debian:trixie" {
		t.Fatalf("pulls = %v, want the resolved base", f.pulls)
	}
	if !strings.Contains(errb.String(), "pulled debian:trixie") {
		t.Errorf("the summary must say a pull happened:\n%s", errb.String())
	}
}

func TestBackupRefusesAnImageItCannotProve(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fakeRunner)
		want  string
	}{
		{"unpullable base", func(f *fakeRunner) {
			f.images = map[string]bool{}
			f.pullErr = errors.New("manifest unknown")
		}, "debian:bookworm"},
		{"image-exists query failure", func(f *fakeRunner) {
			// A daemon that ANSWERED and refused: not an unreachability, so it
			// keeps this wrapping rather than the start-it-or-ignore-it refusal.
			f.imageExistsErr = errors.New("permission denied while trying to connect to the docker daemon socket")
		}, "checking image"},
		{"not GNU tar", func(f *fakeRunner) {
			f.helperStdout = func(h runner.Helper, w io.Writer) error {
				_, err := io.WriteString(w, "bsdtar 3.5.1\n")
				return err
			}
		}, "GNU tar"},
		{"tar rejects the capture flags", func(f *fakeRunner) {
			f.helperErr = func(h runner.Helper) error {
				if h.Volume == "" {
					return errors.New("exit status 2: tar: unrecognized option '--format=posix'")
				}
				return nil
			}
		}, "cannot run byre's capture command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			b, out, p := backupHarness(t, "", combine(merged(config.Config{}), skills.Resolved{}), f, discardStreams(), BackupOptions{})
			f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
			tc.setup(f)
			err := b.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected a refusal naming %q, got %v", tc.want, err)
			}
			if ok, _ := hostopen.ExistsNoFollow(out); ok {
				t.Error("a refused backup must publish nothing")
			}
			if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
				t.Errorf("no staging should survive a refusal: %v", dirs)
			}
		})
	}
}

// Cancelling at the prompt leaves nothing but the pulled image, and says so.
func TestBackupCancelAfterAPullLeavesTheImageAndSaysSo(t *testing.T) {
	f := &fakeRunner{images: map[string]bool{}}
	s, _, errb := testStreams("n\n", true)
	b, out, p := backupHarness(t, "", combine(merged(config.Config{}), skills.Resolved{}), f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	if err := b.run(); err != nil {
		t.Fatalf("a decline is a clean end, got %v", err)
	}
	if len(f.pulls) != 1 {
		t.Fatalf("pulls = %v", f.pulls)
	}
	got := errb.String()
	if !strings.Contains(got, "not backed up; nothing written") || !strings.Contains(got, "stays") {
		t.Errorf("a decline must say nothing was written and that the pull stays:\n%s", got)
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a declined backup must publish nothing")
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("a decline happens before staging exists: %v", dirs)
	}
	// Only the preflight ran: no volume was ever mounted.
	if len(f.helperRuns) != 1 || f.helperRuns[0].Volume != "" {
		t.Errorf("a decline must capture nothing: %+v", f.helperRuns)
	}
}

// ---------------------------------------------------------------- stillness

func TestBackupRefusesUnlessTheProjectIsCompletelyStill(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	t.Run("running session", func(t *testing.T) {
		f := &fakeRunner{}
		s, _, errb := testStreams("", false)
		b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
		f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
		f.live = liveProject(p, "abcdef0123456789")
		err := b.run()
		if err == nil || !strings.Contains(err.Error(), "completely still") {
			t.Fatalf("a running session must refuse, got %v", err)
		}
		if !strings.Contains(errb.String(), "re-attach to it") || !strings.Contains(errb.String(), "stop it") {
			t.Errorf("the refusal must print the engine's remedies:\n%s", errb.String())
		}
		// Nothing is stopped or removed on the user's behalf.
		if len(f.stops) != 0 || len(f.rmContainers) != 0 || len(f.forceRemoved) != 0 {
			t.Errorf("backup must remove nothing: stops=%v rm=%v rmf=%v", f.stops, f.rmContainers, f.forceRemoved)
		}
		if ok, _ := hostopen.ExistsNoFollow(out); ok {
			t.Error("a refused backup must publish nothing")
		}
	})
	t.Run("container in any other state", func(t *testing.T) {
		f := &fakeRunner{allContainers: map[string][]string{labelKey + "=": nil}}
		b, _, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
		f.allContainers = map[string][]string{labelKey + "=" + p.ID: {"abcdef0123456789"}}
		err := b.run()
		if err == nil || !strings.Contains(err.Error(), "docker rm abcdef012345") {
			t.Fatalf("a stopped container must refuse with the engine's rm line, got %v", err)
		}
		if len(f.rmContainers) != 0 {
			t.Errorf("backup removes nothing: %v", f.rmContainers)
		}
	})
	t.Run("container on the other engine", func(t *testing.T) {
		f := &fakeRunner{}
		other := &fakeRunner{engine: runner.Podman}
		b, _, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
		b.others = []engineRunner{other}
		other.allContainers = map[string][]string{labelKey + "=" + p.ID: {"fedcba9876543210"}}
		err := b.run()
		if err == nil || !strings.Contains(err.Error(), "podman rm") {
			t.Fatalf("a container on the other engine must refuse, got %v", err)
		}
	})
	t.Run("leftover helper", func(t *testing.T) {
		f := &fakeRunner{}
		b, _, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
		f.allContainers = map[string][]string{helperKey + "=" + p.ID: {"aaaabbbbcccc"}}
		err := b.run()
		if err == nil || !strings.Contains(err.Error(), "rm -f aaaabbbbcccc") {
			t.Fatalf("a leftover helper must refuse with its rm -f line, got %v", err)
		}
	})
	t.Run("a query byre could not make", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			set  func(f *fakeRunner)
			want string
		}{
			{"running query", func(f *fakeRunner) { f.liveErr = errors.New("daemon not responding") }, "checking for a running session"},
			{"any-state query", func(f *fakeRunner) { f.allErr = errors.New("daemon not responding") }, "checking for session containers"},
			{"volume query", func(f *fakeRunner) { f.volQueryErr = errors.New("daemon not responding") }, "listing volumes"},
		} {
			f := &fakeRunner{}
			b, _, _ := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
			tc.set(f)
			if err := b.run(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: an engine byre cannot account for must refuse, got %v", tc.name, err)
			}
		}
	})
}

// ------------------------------------------------- an unreachable engine

// unreachableEngineErr is what an engine whose daemon was never started answers
// every query with, in each CLI's own words -- deliver.IsUnreachable is the only
// classifier in play, and it reads these messages.
func unreachableEngineErr(eng runner.Engine) error {
	if eng == runner.Podman {
		return errors.New("exit status 125: Cannot connect to Podman. Please verify your connection to the Linux system using `podman system connection list`, or try `podman machine init` and `podman machine start`")
	}
	return errors.New("exit status 1: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
}

// downEngine is an engine that answers every query byre makes of it with that
// unreachability -- a stopped podman machine does not answer one of them.
func downEngine(eng runner.Engine) *fakeRunner {
	down := unreachableEngineErr(eng)
	return &fakeRunner{engine: eng, volQueryErr: down, liveErr: down, allErr: down, imageExistsErr: down}
}

// An engine byre cannot query is still a refusal -- backup speaks in totals --
// but the refusal names the two ways past it.
func TestBackupRefusesAnUnreachableEngineNamingTheIgnoreFlag(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	other := downEngine(runner.Podman)
	b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
	b.others = []engineRunner{other}
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	err := b.run()
	if err == nil {
		t.Fatal("an unreachable engine must still refuse")
	}
	for _, want := range []string{"podman isn't reachable", "--ignore-podman", "start podman"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q: %v", want, err)
		}
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a refused backup must publish nothing")
	}
}

// The engine backup READS has only one way forward: it is what the file is made
// of, so the refusal says start it and never offers to skip it.
func TestBackupRefusesAnUnreachableSourceEngineWithTheStartRemedyOnly(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	down := unreachableEngineErr(runner.Docker)
	for _, tc := range []struct {
		name string
		set  func(f *fakeRunner)
	}{
		{"volume query", func(f *fakeRunner) { f.volQueryErr = down }},
		{"running query", func(f *fakeRunner) { f.liveErr = down }},
		{"any-state query", func(f *fakeRunner) { f.allErr = down }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
			f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
			tc.set(f)
			err := b.run()
			if err == nil {
				t.Fatal("an unreachable source engine must refuse")
			}
			for _, want := range []string{"backup reads docker", "docker isn't reachable", "start docker"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "--ignore-docker") {
				t.Errorf("the source engine must never be offered as skippable: %v", err)
			}
			if ok, _ := hostopen.ExistsNoFollow(out); ok {
				t.Error("a refused backup must publish nothing")
			}
		})
	}
}

// --ignore-<engine> is the user taking the risk byre declined to take for them
// (PRINCIPLES.md P1): the engine is not queried AT ALL -- this one answers
// every query with a failure, so a single query would fail the backup -- and
// both surfaces say what that cost.
func TestBackupIgnoredEngineIsNeverQueriedAndEverySurfaceSaysSo(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	for _, tc := range []struct {
		name string
		tty  bool
		in   string
	}{
		{"preview", true, "y\n"},
		{"summary", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			other := downEngine(runner.Podman)
			s, _, errb := testStreams(tc.in, tc.tty)
			b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{Ignore: IgnoreEngines{Podman: true}})
			b.others = []engineRunner{other}
			f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
			if err := b.run(); err != nil {
				t.Fatalf("an ignored engine must not fail the backup: %v", err)
			}
			if other.liveCalls != 0 {
				t.Errorf("the ignored engine was queried %d times", other.liveCalls)
			}
			if ok, _ := hostopen.ExistsNoFollow(out); !ok {
				t.Error("the backup must still be written")
			}
			if !strings.Contains(errb.String(), "podman ignored (--ignore-podman)") {
				t.Errorf("the %s must name the ignored engine:\n%s", tc.name, errb.String())
			}
			// Which SURFACE carried it: on a terminal the line has to be in the
			// preview, which is everything before the one y/n; off a terminal
			// there is no preview and the summary is all there is.
			prompt := strings.Index(errb.String(), "Proceed?")
			note := strings.Index(errb.String(), "podman ignored")
			if tc.tty && !(note >= 0 && note < prompt) {
				t.Errorf("the preview must carry the line before the prompt:\n%s", errb.String())
			}
			if !tc.tty && prompt >= 0 {
				t.Errorf("off a terminal there is no prompt:\n%s", errb.String())
			}
		})
	}
}

// The engine backup reads cannot be skipped: every volume in the file comes
// from it, so the flag is refused rather than honoured.
func TestBackupRefusesIgnoringTheEngineItReads(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	b, out, _ := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{Ignore: IgnoreEngines{Docker: true}})
	err := b.run()
	if err == nil || !strings.Contains(err.Error(), "--ignore-docker") ||
		!strings.Contains(err.Error(), "the engine this backup reads") {
		t.Fatalf("ignoring the source engine must be refused naming it, got %v", err)
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a refused backup must publish nothing")
	}
}

// Only a CLEAN unreachability gets the flag's remedy. A daemon that answered
// and refused is an engine byre could not account for, and its refusal stays
// the engine problem it is -- offering to skip it would be advice to ignore a
// misconfigured daemon.
func TestBackupRefusesANonUnreachableFailureWithoutTheIgnoreRemedy(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	denied := errors.New("dial unix /run/podman/podman.sock: connect: permission denied")
	for _, tc := range []struct {
		name string
		set  func(f *fakeRunner)
		want string
	}{
		{"volume query", func(f *fakeRunner) { f.volQueryErr = denied }, "listing volumes (podman)"},
		{"running query", func(f *fakeRunner) { f.liveErr = denied }, "checking for a running session (podman)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			other := &fakeRunner{engine: runner.Podman}
			tc.set(other)
			b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
			b.others = []engineRunner{other}
			f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
			err := b.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("a permission failure must refuse with %q, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "--ignore-podman") {
				t.Errorf("a daemon that IS running must not be offered as skippable: %v", err)
			}
			if ok, _ := hostopen.ExistsNoFollow(out); ok {
				t.Error("a refused backup must publish nothing")
			}
		})
	}
}

// ---------------------------------------------------------- under-lock re-check

// One resolution is what the file records. A layer edit or a config save that
// lands while the user reads the preview refuses, on a terminal and off it.
func TestBackupRefusesWhenTheResolutionChangedUnderTheLock(t *testing.T) {
	base := config.Config{Volumes: []config.Volume{{Name: ".claude", Role: "state", Target: "/home/dev/.claude"}}}
	cases := []struct {
		name  string
		fresh config.Config
		want  string
	}{
		{"the carried set", config.Config{Volumes: []config.Volume{
			{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
			{Name: "oneoff", Role: "cache", Target: "/home/dev/.cache"},
		}}, "volumes this backup carries"},
		{"the engine", config.Config{Engine: "podman", Volumes: base.Volumes}, "configured engine"},
		{"a reference", config.Config{Base: "debian:trixie", Volumes: base.Volumes}, "references"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			rv := combine(merged(base), skills.Resolved{})
			b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
			f.vols = map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, "oneoff"): true}
			fresh := combine(merged(tc.fresh), skills.Resolved{})
			b.rv.reread = func() (resolved, error) { return fresh, nil }
			err := b.run()
			if err == nil || !strings.Contains(err.Error(), "changed while you were reviewing; re-run byre backup") {
				t.Fatalf("expected the re-run refusal, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal should name what changed (%s): %v", tc.want, err)
			}
			if ok, _ := hostopen.ExistsNoFollow(out); ok {
				t.Error("a refused backup must publish nothing")
			}
			if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
				t.Errorf("staging must not survive: %v", dirs)
			}
			if len(f.helperRuns) != 1 {
				t.Errorf("no capture should have run: %+v", f.helperRuns)
			}
		})
	}
}

// A config saved between the preview and the lock is the fourth drift, and the
// one no engine fake can stage: the two reads are of the same file, moments
// apart. Pinned on the comparison itself, which is where the rule lives.
func TestBackupPlanDriftNamesTheConfigBytes(t *testing.T) {
	a := backupPlan{cfg: []byte("base = \"debian:bookworm\"\n")}
	b := backupPlan{cfg: []byte("base = \"debian:trixie\"\n")}
	if got := a.drift(b); !strings.Contains(got, "config") {
		t.Fatalf("a changed config must be named as the drift, got %q", got)
	}
	if got := a.drift(a); got != "" {
		t.Fatalf("an unchanged plan must not drift, got %q", got)
	}
}

// ------------------------------------------------------------ helper lifecycle

// The capture helper's spec IS the contract: the two labels (never the project
// label), the cleared tar environment, the read-only nocopy mount, and MNT as
// both the mount target and the -C argument.
func TestBackupCaptureHelperSpecIsPinned(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	b, _, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.helperRuns) != 2 {
		t.Fatalf("want a preflight and one capture, got %d: %+v", len(f.helperRuns), f.helperRuns)
	}
	mnt := "/.byre-helper-" + testRunID
	for i, h := range f.helperRuns {
		if strings.Join(h.Labels, " ") != helperKey+"="+p.ID+" "+helperRunKey+"="+testRunID {
			t.Errorf("helper %d labels = %v, want byre.helper plus the run id", i, h.Labels)
		}
		for _, l := range h.Labels {
			if strings.HasPrefix(l, labelKey+"=") {
				t.Errorf("helper %d carries the PROJECT label (%q): a helper must never read as a session", i, l)
			}
		}
		if v, ok := h.Env["TAR_OPTIONS"]; !ok || v != "" {
			t.Errorf("helper %d must clear TAR_OPTIONS, env = %v", i, h.Env)
		}
		if h.MountPath != mnt {
			t.Errorf("helper %d mount path = %q, want MNT %q", i, h.MountPath, mnt)
		}
		if !strings.Contains(h.Script, "-C "+mnt) {
			t.Errorf("helper %d script must use MNT as -C: %q", i, h.Script)
		}
		if !strings.Contains(h.Script, "-f -") {
			t.Errorf("helper %d script must name its archive explicitly (-f -): %q", i, h.Script)
		}
		if h.Identity != (runner.Identity{UID: 1000, GID: 1000}) {
			t.Errorf("helper %d identity = %+v", i, h.Identity)
		}
	}
	pre, cap := f.helperRuns[0], f.helperRuns[1]
	if pre.Volume != "" || pre.ReadOnly {
		t.Errorf("the preflight mounts no volume: %+v", pre)
	}
	if !strings.Contains(pre.Script, "tar --version") {
		t.Errorf("the preflight must ask tar what it is: %q", pre.Script)
	}
	if cap.Volume != volumeName(p.ID, ".claude") || !cap.ReadOnly {
		t.Errorf("the capture mounts its volume read-only: %+v", cap)
	}
	if cap.Script != "tar --format=posix --numeric-owner -c -f - -C "+mnt+" ." {
		t.Errorf("capture script = %q", cap.Script)
	}
}

// A failed capture is a failed backup: no helper left, no staging, nothing at
// the output path.
func TestBackupFailedCaptureLeavesNothing(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == "" {
			return nil
		}
		return errors.New("exit status 2: tar: Cannot open: Permission denied")
	}
	// The engine reports this run's helper as still present, so the cleanup
	// has something to remove.
	f.helperHook = func(h runner.Helper) {
		f.allContainers = map[string][]string{helperRunKey + "=" + testRunID: {"helper1"}}
	}
	err := b.run()
	if err == nil || !strings.Contains(err.Error(), "archiving volume .claude") {
		t.Fatalf("a nonzero tar exit must fail the backup, got %v", err)
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("nothing is published on a failed capture")
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("staging must be removed on failure: %v", dirs)
	}
	if strings.Join(f.forceRemoved, ",") != "helper1" {
		t.Errorf("the failure path must force-remove this run's helper, got %v", f.forceRemoved)
	}
	if !strings.Contains(errb.String(), "removed helper container helper1") {
		t.Errorf("the cleanup should be legible:\n%s", errb.String())
	}
}

// A helper byre could not remove is named with the line that removes it, and
// the next sweep refuses on it.
func TestBackupNamesAHelperItCouldNotRemove(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{forceRmErr: map[string]bool{"helper1": true}}
	s, _, errb := testStreams("", false)
	b, _, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == "" {
			return nil
		}
		return errors.New("engine gone")
	}
	f.helperHook = func(h runner.Helper) {
		f.allContainers = map[string][]string{helperRunKey + "=" + testRunID: {"helper1"}}
	}
	if err := b.run(); err == nil {
		t.Fatal("expected the capture failure")
	}
	if !strings.Contains(errb.String(), "docker rm -f helper1") {
		t.Errorf("an unremovable helper must be named with its rm -f line:\n%s", errb.String())
	}
}

// Cleanup is this run's business only: another invocation's preflight helper
// is left exactly where it is.
func TestBackupCleanupTouchesOnlyItsOwnRunsHelpers(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	b, _, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.helperErr = func(h runner.Helper) error {
		if h.Volume == "" {
			return nil
		}
		return errors.New("boom")
	}
	f.helperHook = func(h runner.Helper) {
		f.allContainers = map[string][]string{
			helperRunKey + "=" + testRunID: {"mine"},
			helperRunKey + "=otherrun":     {"theirs"},
		}
	}
	if err := b.run(); err == nil {
		t.Fatal("expected the capture failure")
	}
	if strings.Join(f.forceRemoved, ",") != "mine" {
		t.Fatalf("cleanup must remove only this run's helpers, got %v", f.forceRemoved)
	}
}

// An archive the validator rejects is a failed backup: the file byre writes
// must be one byre reads.
func TestBackupFailsOnAnArchiveItCouldNotRestore(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	b, out, p := backupHarness(t, "", rv, f, discardStreams(), BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	bad := volumeTar(t, func(tw *tar.Writer) {
		// An absolute name: refused by the nested-tar contract.
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "/etc/shadow", Mode: 0o600, Format: tar.FormatPAX}); err != nil {
			t.Fatal(err)
		}
	})
	f.helperStdout = captureStdout(map[string][]byte{volumeName(p.ID, ".claude"): bad})
	err := b.run()
	if err == nil || !strings.Contains(err.Error(), "not one byre could restore") {
		t.Fatalf("expected the validator's refusal, got %v", err)
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("nothing is published")
	}
}

// A stale staging directory from another run is NAMED and LEFT: byre cannot
// tell a crash's leftovers from a concurrent verb's live staging.
func TestBackupNamesAStaleStagingDirectoryAndLeavesIt(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, _, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	stale := filepath.Join(p.Home, backup.StagingDirName, "otherrun00000000")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errb.String(), stale) {
		t.Errorf("a stale staging directory must be named:\n%s", errb.String())
	}
	if ok, _ := hostopen.ExistsNoFollow(stale); !ok {
		t.Error("byre must leave another run's staging directory alone")
	}
}

// -------------------------------------------------------------- the surfaces

// tar's own lines reach the summary as DATA: an escape sequence in them never
// reaches the terminal raw (P4).
func TestBackupSummaryEscapesTarsOwnLines(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{helperStderr: "tar: ./\x1b[31msock\x1b[0m: socket ignored"}
	s, _, errb := testStreams("", false)
	b, _, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	got := errb.String()
	if !strings.Contains(got, "socket ignored") {
		t.Errorf("tar's own lines belong in the summary:\n%s", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("an escape sequence from tar reached the terminal raw:\n%q", got)
	}
}

// A helper's stderr is captured under a cap, and the runner marks a list it had
// to cut off. The summary prints tar's lines as the socket list, so the marker
// has to arrive among them -- byre matches nothing for it, it rides the funnel
// like any other line (P4).
func TestBackupSummaryCarriesTheTruncationMarkerWithTarsLines(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	marker := "[byre: 65536 more bytes of stderr not shown; output exceeded 64 KiB]"
	f := &fakeRunner{helperStderr: "tar: ./sock: socket ignored\n" + marker}
	s, _, errb := testStreams("", false)
	b, _, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	got := errb.String()
	if !strings.Contains(got, ".claude: "+marker) {
		t.Errorf("the summary must carry the truncation marker under the volume that said it:\n%s", got)
	}
	if !strings.Contains(got, ".claude: tar: ./sock: socket ignored") {
		t.Errorf("the lines the cap DID keep must still be there:\n%s", got)
	}
}

// Off a terminal there is no prompt, and every list the preview would have
// shown prints in the summary instead -- nothing left out is silent.
func TestBackupOffTerminalRunsWithoutAPromptAndNamesWhatItLeft(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: "cache", Role: "cache", Target: "/home/dev/.cache"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, "cache"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	got := errb.String()
	if strings.Contains(got, "Proceed?") {
		t.Errorf("off a terminal there is no prompt:\n%s", got)
	}
	for _, want := range []string{"wrote " + out, "engine:  docker", "volumes carried:", ".claude", "cache: not carried (cache)", "references"} {
		if !strings.Contains(got, want) {
			t.Errorf("the off-terminal summary must carry %q:\n%s", want, got)
		}
	}
}

func TestBackupYesSkipsThePromptOnATerminal(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	// No answer on stdin at all: a prompt would decline and write nothing.
	s, _, errb := testStreams("", true)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{Yes: true})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errb.String(), "Proceed?") {
		t.Errorf("--yes must skip the prompt:\n%s", errb.String())
	}
	if ok, _ := hostopen.ExistsNoFollow(out); !ok {
		t.Error("--yes must write the file")
	}
	if !strings.Contains(errb.String(), "byre backup will write") {
		t.Errorf("--yes still shows what it is about to do:\n%s", errb.String())
	}
}

// ------------------------------------------------------------- credentials

// --no-credentials is a statement about the FILE: the copy carries no rows and
// no block, the source file is byte-identical afterwards, and nothing decrypts
// at any point (no passphrase is ever asked for).
func TestBackupNoCredentialsStripsTheCopyAndLeavesTheSource(t *testing.T) {
	cfgText := "[env_from_host]\nTOKEN = \"encrypted:YWJj\"\nPLAIN = \"HOME\"\n\n[credentials]\nrecipient = \"" + testRecipient + "\"\nidentity = \"c2NyeXB0LWJsb2I=\"\n"
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, cfgText, rv, f, s, BackupOptions{NoCredentials: true})
	src := filepath.Join(p.Dir, config.ProjectConfigName)
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	body := string(file.Config)
	for _, gone := range []string{"encrypted:YWJj", "[credentials]", testRecipient, "c2NyeXB0LWJsb2I="} {
		if strings.Contains(body, gone) {
			t.Errorf("--no-credentials left %q in the carried config:\n%s", gone, body)
		}
	}
	if !strings.Contains(body, "PLAIN") {
		t.Errorf("--no-credentials must leave every other row alone:\n%s", body)
	}
	if file.CredentialState != backup.CredNone || file.Index.Config.Credentials != backup.CredNone {
		t.Errorf("credential state = %q/%q, want %q", file.CredentialState, file.Index.Config.Credentials, backup.CredNone)
	}
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the source config must be untouched")
	}
	if !strings.Contains(errb.String(), "credentials dropped") {
		t.Errorf("the summary must say the credentials were dropped:\n%s", errb.String())
	}
	// Every surface counts the bytes that go IN the file, never the source's:
	// the screen, the index row and the member all speak of one copy.
	if file.Index.Config.Bytes != int64(len(file.Config)) {
		t.Errorf("index config.bytes = %d, want the carried %d", file.Index.Config.Bytes, len(file.Config))
	}
	if len(file.Config) == len(before) {
		t.Fatalf("the stripped copy is the same length as the source (%d); this arm cannot tell them apart", len(before))
	}
	if want := fmt.Sprintf("(%d bytes, credentials dropped", len(file.Config)); !strings.Contains(errb.String(), want) {
		t.Errorf("the config line must quote the carried length %q, not the source's %d:\n%s", want, len(before), errb.String())
	}
	if strings.Contains(strings.ToLower(errb.String()), "passphrase") {
		t.Errorf("backup never decrypts anything, so it never asks for a passphrase:\n%s", errb.String())
	}
}

// By default the credentials travel as they are -- ciphertext and all -- and
// the index says which of the four legal states the file is in. No login
// warning, no policing of a copied token (P1).
func TestBackupCarriesCredentialsAsTheyAreAndStatesTheirKind(t *testing.T) {
	cfgText := "[env_from_host]\nTOKEN = \"encrypted:YWJj\"\n\n[credentials]\nrecipient = \"" + testRecipient + "\"\nidentity = \"c2NyeXB0LWJsb2I=\"\n"
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, cfgText, rv, f, s, BackupOptions{})
	// An agent login file inside the carried volume: byre reads nothing from
	// the host about it and says nothing about it either.
	archive := volumeTar(t, func(tw *tar.Writer) { tarFile(t, tw, ".credentials.json", "{\"token\":\"t\"}") })
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.helperStdout = captureStdout(map[string][]byte{volumeName(p.ID, ".claude"): archive})
	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	file := readBack(t, out, volumePrefix(p.ID))
	if !strings.Contains(string(file.Config), "encrypted:YWJj") {
		t.Error("the ciphertext travels with the config")
	}
	if file.Index.Config.Credentials != backup.CredRows {
		t.Errorf("credential state = %q, want %q", file.Index.Config.Credentials, backup.CredRows)
	}
	got := strings.ToLower(errb.String())
	if !strings.Contains(got, "1 credential row") {
		t.Errorf("the summary should count the rows:\n%s", errb.String())
	}
	for _, nope := range []string{"passphrase for", "login", "warning"} {
		if strings.Contains(got, nope) {
			t.Errorf("backup says nothing about logins and asks for nothing (%q):\n%s", nope, errb.String())
		}
	}
}

// ---------------------------------------------------------------- top level

// The exported verb's early refusals: a project with no config, and the
// collision fence.
func TestBackupRefusesWithoutAConfigAndOnAnIDCollision(t *testing.T) {
	t.Setenv("BYRE_HOME", t.TempDir())
	proj := t.TempDir()
	p, err := project.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	err = Backup(discardStreams(), proj, BackupOptions{})
	if err == nil || !strings.Contains(err.Error(), "nothing to back up") {
		t.Fatalf("a project with no config must refuse, got %v", err)
	}
	// The fence: a path record naming another directory.
	if err := os.WriteFile(p.PathRecord, []byte("/somewhere/else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, config.ProjectConfigName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Backup(discardStreams(), proj, BackupOptions{})
	if err == nil || !strings.Contains(err.Error(), "/somewhere/else") {
		t.Fatalf("an id collision must refuse before the store is read, got %v", err)
	}
}

// The disk check is on the filesystem the file lands on, and it refuses before
// anything is published.
func TestBackupRefusesWhenTheOutputFilesystemCannotHoldIt(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.byre-backup.tar.gz")
	if err := checkOutputSpace(out, 1); err != nil {
		t.Fatalf("a temp dir with room must pass: %v", err)
	}
	// An absurd requirement names the directory and the two figures.
	err := checkOutputSpace(out, 1<<62)
	if err == nil || !strings.Contains(err.Error(), "not enough room") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("expected a refusal naming the directory, got %v", err)
	}
	// A path whose parent is not a real directory cannot be measured.
	if _, err := backup.FreeBytesIn(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing directory should report as missing, got %v", err)
	}
}

// The reasons a --no-volume refusal picks from are ordered: cache, then
// machine-scoped, then another project, then another engine.
func TestLowestRankReasonFollowsTheStatedOrder(t *testing.T) {
	rows := []notCarried{
		{name: "v", reason: "other engine", rank: rankOtherEngine},
		{name: "v", reason: "cache", rank: rankCache},
		{name: "w", reason: "machine", rank: rankMachine},
	}
	if got, ok := lowestRankReason(rows, "v"); !ok || got != "cache" {
		t.Fatalf("reason for v = %q/%v, want the cache reason", got, ok)
	}
	if got, ok := lowestRankReason(rows, "w"); !ok || got != "machine" {
		t.Fatalf("reason for w = %q/%v", got, ok)
	}
	if _, ok := lowestRankReason(rows, "absent"); ok {
		t.Fatal("a name with no row has no reason")
	}
}

// A publish whose directory fsync failed leaves the COMPLETE file and exits
// non-zero saying exactly that -- pinned on the sentinel, since only the
// kernel produces the condition.
func TestBackupUnsyncedPublishIsAnErrorThatNamesTheFile(t *testing.T) {
	// The wrapping, not the syscall: errors.Is must still see the sentinel so
	// the caller can tell "written but unconfirmed" from "not written".
	err := fmt.Errorf("%s is written, but %w", "/tmp/x.tar.gz", hostopen.ErrPublishedUnsynced)
	if !errors.Is(err, hostopen.ErrPublishedUnsynced) {
		t.Fatal("the sentinel must survive the wrapping")
	}
	if !strings.Contains(err.Error(), "durability is unconfirmed") {
		t.Fatalf("the message must say what is unconfirmed: %v", err)
	}
}

// ------------------------------------------------------------ cancellation

// A Ctrl-C mid capture is not a kill: this run's helper (which is holding a
// volume open) goes, staging (which holds volume bytes and the plaintext
// secrets with them) goes, nothing is published, and the exit is 1 having said
// so. run installs the SIGINT-notified context this arm injects.
func TestBackupCancellationClearsTheRunAndWritesNothing(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
		{Name: ".codex", Role: "state", Target: "/home/dev/.codex"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true, volumeName(p.ID, ".codex"): true}
	// A helper container of THIS run is on the engine, as one is while a
	// capture runs: the cancellation has to force-remove exactly it.
	f.allContainers = map[string][]string{helperRunKey + "=" + testRunID: {"capturehelper1"}}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelled = ctx
	claude := volumeName(p.ID, ".claude")
	f.helperHook = func(h runner.Helper) {
		if h.Volume == claude {
			cancel()
		}
	}

	wantExit1(t, b.run())
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a cancelled backup published a file")
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("staging survived the cancellation: %v", dirs)
	}
	if strings.Join(f.forceRemoved, ",") != "capturehelper1" {
		t.Errorf("forceRemoved = %v, want exactly this run's helper", f.forceRemoved)
	}
	if got := errb.String(); !strings.Contains(got, backupCancelledLine) {
		t.Errorf("the exit must say the backup was cancelled:\n%s", got)
	}
	// The second volume is never read: a cancellation stops the loop.
	for _, h := range f.helperRuns {
		if h.Volume == volumeName(p.ID, ".codex") {
			t.Error(".codex was captured after the cancellation")
		}
	}
}

// A cancellation whose helper cannot be ended must not hang: the wait for
// RunHelper is bounded by the engine cleanup deadline (pinned short here), and
// what may still be running is named with its own `rm -f` line.
func TestBackupCancellationDoesNotWaitForeverOnAStuckHelper(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	b.cleanupWait = 10 * time.Millisecond
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.allContainers = map[string][]string{helperRunKey + "=" + testRunID: {"stuckhelper1"}}
	// The engine cannot remove it, and the helper therefore never returns.
	f.forceRmErr = map[string]bool{"stuckhelper1": true}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelled = ctx
	claude := volumeName(p.ID, ".claude")
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	f.helperStdout = func(h runner.Helper, w io.Writer) error {
		if h.Volume == "" {
			_, err := io.WriteString(w, "tar (GNU tar) 1.34\n")
			return err
		}
		if h.Volume == claude {
			cancel()
			<-stuck // a container the engine will not take away
		}
		return nil
	}

	wantExit1(t, b.run())
	got := errb.String()
	for _, want := range []string{"stuckhelper1", "docker rm -f stuckhelper1", "may still be running", backupCancelledLine} {
		if !strings.Contains(got, want) {
			t.Errorf("the account is missing %q:\n%s", want, got)
		}
	}
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a cancelled backup published a file")
	}
}

// A Ctrl-C while the archive is streaming must not leave a file: the publish
// reads every payload through the run's cancellation, so backup.Write fails
// before the link, hostopen removes the staged temp it was filling, and the
// exit is the one a cancelled capture takes -- this run's helper gone, staging
// gone, nothing published.
func TestBackupCancellationDuringThePublishWritesNothing(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.allContainers = map[string][]string{helperRunKey + "=" + testRunID: {"publishhelper1"}}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelled = ctx
	// The captures are over and the index is built; the Ctrl-C lands as the
	// first payload is opened for the stream.
	b.payloadOpened = cancel

	wantExit1(t, b.run())
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a backup cancelled mid-publish published a file")
	}
	// Not even hostopen's staged temp: the output directory is as it was.
	if entries, err := os.ReadDir(filepath.Dir(out)); err != nil || len(entries) != 0 {
		t.Errorf("the output directory holds %v (err %v), want nothing", entries, err)
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("staging survived the cancellation: %v", dirs)
	}
	if strings.Join(f.forceRemoved, ",") != "publishhelper1" {
		t.Errorf("forceRemoved = %v, want exactly this run's helper", f.forceRemoved)
	}
	got := errb.String()
	if !strings.Contains(got, backupCancelledLine) {
		t.Errorf("the exit must say the backup was cancelled:\n%s", got)
	}
	if strings.Contains(got, "byre: wrote") {
		t.Errorf("a cancelled publish must not report a written file:\n%s", got)
	}
}

// A Ctrl-C that lands AFTER the last payload byte -- during the staged file's
// fsync, the slowest stretch of a large publish -- is past every payload reader
// and still in front of the link, which is the commit point. So the publish asks
// the cancellation once more there and fails: no file, staging gone, exit 1.
func TestBackupCancellationBeforeTheLinkWritesNothing(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	f.allContainers = map[string][]string{helperRunKey + "=" + testRunID: {"linkhelper1"}}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancelled = ctx
	// Every payload byte is written and fsynced; the Ctrl-C lands with the link
	// still to come.
	b.streamWritten = cancel

	wantExit1(t, b.run())
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a backup cancelled before the link published a file")
	}
	// Not even hostopen's staged temp: the output directory is as it was.
	if entries, err := os.ReadDir(filepath.Dir(out)); err != nil || len(entries) != 0 {
		t.Errorf("the output directory holds %v (err %v), want nothing", entries, err)
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("staging survived the cancellation: %v", dirs)
	}
	got := errb.String()
	if !strings.Contains(got, backupCancelledLine) {
		t.Errorf("the exit must say the backup was cancelled:\n%s", got)
	}
	if strings.Contains(got, "byre: wrote") {
		t.Errorf("a cancelled publish must not report a written file:\n%s", got)
	}
}

// A config-only backup carries no payload, so nothing in the stream can watch
// the cancellation for it: the check between the captures and the publish is
// the one that catches a Ctrl-C there, and it publishes nothing.
func TestBackupCancellationBeforeAConfigOnlyPublishWritesNothing(t *testing.T) {
	rv := combine(merged(config.Config{}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, errb := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the interrupt landed while the lock was being waited for
	b.cancelled = ctx

	wantExit1(t, b.run())
	if ok, _ := hostopen.ExistsNoFollow(out); ok {
		t.Error("a cancelled config-only backup published a file")
	}
	if dirs := stagingDirs(t, p.Home); len(dirs) != 0 {
		t.Errorf("staging survived the cancellation: %v", dirs)
	}
	if got := errb.String(); !strings.Contains(got, backupCancelledLine) {
		t.Errorf("the exit must say the backup was cancelled:\n%s", got)
	}
}

// Backup enters its CRITICAL phase at the first capture, under the lock --
// never before. The cancellation context belongs to that phase alone: a backup
// that is only WAITING (the preview's prompt, the lock queue) watches no
// context, so a context armed over those waits would have swallowed the Ctrl-C
// and left byre unkillable; an interrupt before the captures is handled by the
// pre-capture action instead. Pinned where the fake engine can see it: the
// preflight and the stillness sweep run with no context, and the capture -- the
// first thing under the lock that reads project state -- runs with one.
func TestBackupArmsTheCancellationOnlyUnderTheLock(t *testing.T) {
	rv := combine(merged(config.Config{Volumes: []config.Volume{
		{Name: ".claude", Role: "state", Target: "/home/dev/.claude"},
	}}), skills.Resolved{})
	f := &fakeRunner{}
	s, _, _ := testStreams("", false)
	b, out, p := backupHarness(t, "", rv, f, s, BackupOptions{})
	f.vols = map[string]bool{volumeName(p.ID, ".claude"): true}
	var timeline []string
	f.helperHook = func(h runner.Helper) {
		timeline = append(timeline, helperStageState(h, b.cancelled))
	}
	// The stillness sweep is the last thing UNDER the lock before the arming
	// point, so it is where a handler installed one step too early shows up.
	f.probeHook = func(what string) {
		timeline = append(timeline, armedState(what, b.cancelled))
	}

	if err := b.run(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(timeline, " "); got != "preflight:unarmed still:unarmed volume:armed" {
		t.Errorf("handler timeline = %q, want everything before the captures unarmed", got)
	}
	if ok, _ := hostopen.ExistsNoFollow(out); !ok {
		t.Errorf("the backup did not publish %s", out)
	}
}

// helperStageState names which helper ran and whether the verb's cancellation
// was armed by then: one entry of the timeline the arming tests compare.
func helperStageState(h runner.Helper, cancelled context.Context) string {
	stage := "volume"
	if h.Volume == "" {
		stage = "preflight"
	}
	return armedState(stage, cancelled)
}

// armedState is one timeline entry: what the engine was asked to do, and
// whether the verb had entered its critical phase -- the one that watches a
// cancellation -- when it asked.
func armedState(stage string, cancelled context.Context) string {
	if cancelled == nil {
		return stage + ":unarmed"
	}
	return stage + ":armed"
}
