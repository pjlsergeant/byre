package packages

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

// hostDirCache keeps extracted bundled package trees for the process lifetime
// so Resolve/build can use ordinary host paths without reading ~/.byre/bundled.
// All extractions live under ONE process-scoped root (byre-embed-<pid>-*),
// removed by CleanupHostDirs at process exit — without that, every invocation
// that touched a bundled package leaked its extraction until the OS got
// around to /tmp -- 2,201 leaked dirs were counted in one such tree.
var (
	hostDirMu    sync.Mutex
	hostDirCache = map[string]string{} // key: version+"\x00"+id
	hostDirRoot  string                // the one process-scoped extraction root
)

// HostDir returns a filesystem directory containing the package payloads.
// Local (and future installed) packages return Dir. Bundled packages are
// extracted once per process into the shared temp root keyed by byre
// version + id -- the user store is never written.
func (e *Entry) HostDir() (string, error) {
	if e.Dir != "" {
		return e.Dir, nil
	}
	if e.FS == nil || e.Sub == "" {
		return "", fmt.Errorf("package %q has no load location", e.ID)
	}
	key := e.Version + "\x00" + e.ID
	hostDirMu.Lock()
	defer hostDirMu.Unlock()
	if d, ok := hostDirCache[key]; ok {
		if _, err := hostopen.PlainStat(d, hostopen.ByreCreated); err == nil {
			return d, nil
		}
	}
	base, err := hostDirBase()
	if err != nil {
		return "", err
	}
	// Sub-dir per package under the shared root; ids are [a-z0-9-] with one
	// owner slash, flattened. The counter disambiguates version re-extracts.
	root := filepath.Join(base, fmt.Sprintf("%d-%s", len(hostDirCache), strings.ReplaceAll(BareName(e.ID), "/", "-")))
	if err := hostopen.PlainMkdirAll(root, 0o755, hostopen.ByreCreated); err != nil {
		return "", err
	}
	sub, err := fs.Sub(e.FS, e.Sub)
	if err != nil {
		hostopen.PlainRemoveAll(root, hostopen.ByreCreated)
		return "", err
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		out := filepath.Join(root, p)
		if d.IsDir() {
			return hostopen.PlainMkdirAll(out, 0o755, hostopen.ByreCreated)
		}
		b, rerr := fs.ReadFile(sub, p)
		if rerr != nil {
			return rerr
		}
		if err := hostopen.PlainMkdirAll(filepath.Dir(out), 0o755, hostopen.ByreCreated); err != nil {
			return err
		}
		return hostopen.PlainWriteFile(out, b, 0o644, hostopen.ByreCreated)
	})
	if err != nil {
		hostopen.PlainRemoveAll(root, hostopen.ByreCreated)
		return "", err
	}
	hostDirCache[key] = root
	return root, nil
}

// hostDirBase lazily creates the process's one extraction root, reaping
// leftovers from dead processes first. Caller holds hostDirMu.
func hostDirBase() (string, error) {
	if hostDirRoot != "" {
		return hostDirRoot, nil
	}
	reapStaleEmbedRoots()
	root, err := hostopen.PlainMkdirTemp("", fmt.Sprintf("byre-embed-%d-", os.Getpid()), hostopen.ProcessTemp)
	if err != nil {
		return "", err
	}
	hostDirRoot = root
	return root, nil
}

// CleanupHostDirs removes the process's extraction root. Deferred from the
// CLI's top-level exit path; safe to call with nothing extracted, and a
// forced kill just leaves residue for the next invocation's reap.
func CleanupHostDirs() {
	hostDirMu.Lock()
	defer hostDirMu.Unlock()
	if hostDirRoot != "" {
		hostopen.PlainRemoveAll(hostDirRoot, hostopen.ByreCreated)
		hostDirRoot = ""
		hostDirCache = map[string]string{}
	}
}

// reapStaleEmbedRoots removes byre-embed-* dirs no live process owns.
// Pid-carrying names (byre-embed-<pid>-*) are removed on proven death only
// (ESRCH/ErrProcessDone — an EPERM probe keeps the dir), except a dir
// bearing THIS pid, which is a prior incarnation's and goes
// unconditionally; pre-scheme names carry no owner, so they are removed on
// age instead: older than a day is no live CLI invocation. Same discipline
// as tuitest's build-dir reap.
func reapStaleEmbedRoots() {
	entries, err := hostopen.PlainReadDir(os.TempDir(), hostopen.HostUserOwned)
	if err != nil {
		return
	}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), "byre-embed-")
		if !ok || !e.IsDir() {
			continue
		}
		full := filepath.Join(os.TempDir(), e.Name())
		pidStr, _, hasPid := strings.Cut(rest, "-")
		pid, perr := strconv.Atoi(pidStr)
		if !hasPid || perr != nil || pid <= 0 {
			// Legacy layout: age-gated best-effort cleanup.
			if info, ierr := e.Info(); ierr == nil && time.Since(info.ModTime()) > 24*time.Hour {
				hostopen.PlainRemoveAll(full, hostopen.ByreCreated)
			}
			continue
		}
		if pid == os.Getpid() {
			// The reap runs only before this process creates its root
			// (hostDirBase's early return), so a same-pid dir is residue
			// from a dead pid-reused incarnation — a liveness probe would
			// misread it as ours. Remove unconditionally, like tuitest.
			hostopen.PlainRemoveAll(full, hostopen.ByreCreated)
			continue
		}
		proc, ferr := os.FindProcess(pid)
		if ferr != nil {
			continue
		}
		serr := proc.Signal(syscall.Signal(0))
		if !errors.Is(serr, os.ErrProcessDone) && !errors.Is(serr, syscall.ESRCH) {
			continue // running, or not provably dead — keep
		}
		hostopen.PlainRemoveAll(full, hostopen.ByreCreated)
	}
}
