package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

// Staging is one invocation's private scratch directory under the byre home:
// <home>/staging/<run id>/, 0700, with 0600 payload files.
//
// Three properties the verbs depend on, none of them incidental:
//
//   - It is never under the output directory, which by default is the project
//     tree the next box mounts: a volume's bytes -- plaintext secrets
//     included -- do not pass through a path an agent can read.
//   - Every file is created through an anchored root (os.Root, openat-relative
//     to one verified descriptor) and NAMED BY POSITION: 0.tar, 1.tar. No
//     name the backup file supplies ever chooses a path, so ADR 0040's rule
//     holds without a per-call-site judgement.
//   - The run id makes two concurrent verbs disjoint. A crash that runs no
//     cleanup leaves the directory behind; StaleStaging is how the next verb
//     names it and leaves it alone, since it cannot tell a crash from a verb
//     still running.
type Staging struct {
	root  *os.Root
	home  string
	runID string
	path  string
}

// errRemoved reports use of a Staging after Remove took its directory away:
// a wiring mistake, not a filesystem condition.
var errRemoved = errors.New("staging: this staging directory has been removed")

// StagingDirName is the byre-home subdirectory holding every run's staging
// directory.
const StagingDirName = "staging"

// NewStaging creates <home>/staging/<run id>/ and returns it. The staging
// parent is created if absent, the way the store's bootstrap creates its
// directories (MkdirAllIn: home is the user's and may be a symlink out of a
// dotfiles repo, the tail is byre's own and a symlink there is refused). The
// run directory itself must not already exist -- the name is fresh
// randomness, so a collision is a collision.
func NewStaging(home, runID string) (*Staging, error) {
	if home == "" {
		return nil, errors.New("staging: no byre home given")
	}
	if err := checkRunID(runID); err != nil {
		return nil, err
	}
	if err := hostopen.MkdirAllIn(home, StagingDirName, 0o700); err != nil {
		return nil, fmt.Errorf("staging: %w", err)
	}
	parent := filepath.Join(home, StagingDirName)
	root, err := hostopen.MkdirPrivateIn(parent, runID)
	if err != nil {
		return nil, fmt.Errorf("staging: %w", err)
	}
	return &Staging{root: root, home: home, runID: runID, path: filepath.Join(parent, runID)}, nil
}

// Path is the staging directory's pathname, for a message. Opens go through
// the root, never through this string.
func (s *Staging) Path() string { return s.path }

// Create makes one payload file, 0600, refusing an existing name. name is a
// POSITIONAL name byre chose (0.tar, config); nothing from the backup file
// reaches it.
func (s *Staging) Create(name string) (*os.File, error) {
	if err := checkMember(name); err != nil {
		return nil, err
	}
	if s.root == nil {
		return nil, errRemoved
	}
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("staging: %w", err)
	}
	// O_CREAT's mode goes through the umask; these bytes are a volume's, so
	// 0600 means 0600 whatever the shell was set to.
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("staging: %w", err)
	}
	return f, nil
}

// Open reads back a file Create made, through the same anchored root.
func (s *Staging) Open(name string) (*os.File, error) {
	if err := checkMember(name); err != nil {
		return nil, err
	}
	if s.root == nil {
		return nil, errRemoved
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("staging: %w", err)
	}
	return f, nil
}

// FreeBytes is the space left on the filesystem holding staging, measured
// through the root's own descriptor rather than by pathname, so the answer
// describes the directory the writes will land in.
func (s *Staging) FreeBytes() (uint64, error) {
	if s.root == nil {
		return 0, errRemoved
	}
	return freeBytes(s.root, s.path)
}

// FreeBytesIn is the same measurement for a directory this package did not
// create: the backup file's own output directory, which is a second
// filesystem the disk check has to cover. Anchored with
// OpenDirRootNoFollow, the same anchor the publish itself uses, so the
// figure describes the directory the file will land in and a symlink at that
// name is refused here rather than measured.
func FreeBytesIn(dir string) (uint64, error) {
	root, err := hostopen.OpenDirRootNoFollow(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	return freeBytes(root, dir)
}

// freeBytes measures the filesystem behind an anchored root through the
// root's own descriptor rather than by pathname.
func freeBytes(root *os.Root, path string) (uint64, error) {
	d, err := root.Open(".")
	if err != nil {
		return 0, fmt.Errorf("staging: %w", err)
	}
	defer d.Close()
	var st syscall.Statfs_t
	if err := syscall.Fstatfs(int(d.Fd()), &st); err != nil {
		return 0, fmt.Errorf("measuring free space on %s: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// Remove takes the staging directory away: its contents through the root,
// then the directory itself through the anchored parent. Called on every
// exit, success or failure, so it tolerates a directory that is already
// gone. Nothing is resolved by pathname and no symlink is followed, so a
// swapped name cannot redirect a removal.
func (s *Staging) Remove() error {
	if s.root != nil {
		entries, err := s.contents()
		if err != nil {
			return err
		}
		// Deepest first: os.Root.Remove unlinks an empty directory, so a
		// stray one (byre creates none) cannot wedge the cleanup.
		for _, rel := range entries {
			if err := s.root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("staging: removing %s: %w", filepath.Join(s.path, rel), err)
			}
		}
		s.root.Close()
		s.root = nil
	}
	proot, err := hostopen.OpenDirRootNoFollow(filepath.Join(s.home, StagingDirName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("staging: %w", err)
	}
	defer proot.Close()
	if err := proot.Remove(s.runID); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("staging: removing %s: %w", s.path, err)
	}
	return nil
}

// contents lists everything under the staging root, deepest first.
func (s *Staging) contents() ([]string, error) {
	var out []string
	err := fs.WalkDir(s.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("staging: listing %s: %w", s.path, err)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "/"), strings.Count(out[j], "/")
		if di != dj {
			return di > dj
		}
		return out[i] < out[j]
	})
	return out, nil
}

// StaleStaging lists the run directories under <home>/staging that are not
// runID's, newest-first order not attempted: they are names, and the caller
// NAMES one and leaves it. byre cannot tell a crashed verb's leftovers from a
// concurrent verb's live staging, so it never removes one -- it says it is
// there, and that it holds volume bytes.
//
// A missing staging directory is not stale, it is the ordinary first run.
// Pass "" for runID to list every run directory.
func StaleStaging(home, runID string) ([]string, error) {
	if home == "" {
		return nil, errors.New("staging: no byre home given")
	}
	dir := filepath.Join(home, StagingDirName)
	entries, err := hostopen.ReadDirNoFollow(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("staging: listing %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == runID {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

func checkRunID(id string) error {
	if id == "" {
		return errors.New("staging: no run id given")
	}
	return plainName("run id", id)
}

func checkMember(name string) error {
	if name == "" {
		return errors.New("staging: no member name given")
	}
	return plainName("member", name)
}

// plainName holds a staging path component to one plain name, for both names
// this package builds a path from. Each is byre's own -- the run id is
// hostopen.NewRunID's randomness, members are named by POSITION and never by
// anything the backup file supplies -- so the guard keeps that true of every
// future caller instead of trusting the comment, and what names which one it
// was.
func plainName(what, name string) error {
	if name == filepath.Base(name) && name != "." && name != ".." && !strings.ContainsRune(name, '/') {
		return nil
	}
	return fmt.Errorf("staging: %s %q is not a plain name", what, name)
}
