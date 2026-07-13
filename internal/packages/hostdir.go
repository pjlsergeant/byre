package packages

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// hostDirCache keeps extracted bundled package trees for the process lifetime
// so Resolve/build can use ordinary host paths without reading ~/.byre/bundled.
var (
	hostDirMu    sync.Mutex
	hostDirCache = map[string]string{} // key: version+"\x00"+id
)

// HostDir returns a filesystem directory containing the package payloads.
// Local (and future installed) packages return Dir. Bundled packages are
// extracted once per process into a temp cache keyed by byre version + id --
// the user store is never written.
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
		if _, err := os.Stat(d); err == nil {
			return d, nil
		}
	}
	root, err := os.MkdirTemp("", "byre-embed-*")
	if err != nil {
		return "", err
	}
	sub, err := fs.Sub(e.FS, e.Sub)
	if err != nil {
		os.RemoveAll(root)
		return "", err
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		out := filepath.Join(root, p)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, rerr := fs.ReadFile(sub, p)
		if rerr != nil {
			return rerr
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		os.RemoveAll(root)
		return "", err
	}
	hostDirCache[key] = root
	return root, nil
}
