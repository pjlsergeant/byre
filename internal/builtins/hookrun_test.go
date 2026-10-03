package builtins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/pjlsergeant/byre/internal/packages"
)

// physTempDir is t.TempDir resolved to its physical path. The codex,
// opencode and gemini shared-auth hooks refuse an identity dir whose
// physical path (`pwd -P`) differs from its spelling -- a symlinked ancestor
// would carry the shared login off -- and a platform temp dir can itself
// sit under a symlink (macOS: /var -> /private/var), which would make every
// seam value look planted and fail the macOS CI leg for the wrong reason.
// Use it for every temp dir a hook test feeds into BYRE_IDENTITY_BASE,
// XDG_DATA_HOME, BYRE_GEMINI_DIR or CODEX_HOME.
func physTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// sharedAuthLibSeam points a hook at the shared-auth library copy its own
// skill ships. In a box the hooks source /usr/local/lib/byre-shared-auth-lib.sh,
// which five skills stage there byte-identical (ADR 0056); under test
// BYRE_SHARED_AUTH_LIB names the shipped copy instead. A hook that cannot
// read the library refuses to do anything, so every hook invocation needs
// this.
func sharedAuthLibSeam(t *testing.T, cat *packages.Catalog, skill string) string {
	t.Helper()
	return "BYRE_SHARED_AUTH_LIB=" + filepath.Join(skillDir(t, cat, skill), "byre-shared-auth-lib.sh")
}

// runHook runs one hook script under shell with env added to the process
// environment, and returns its combined output. Bounded: a hook that opens a
// FIFO blocks forever, and the containment cases that plant one must fail as
// a timeout instead of hanging the suite. The shared-auth and login hooks are
// best-effort and exit 0 on every path, so a non-zero exit fails the test.
func runHook(t *testing.T, shell, hook string, env ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, hook)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("hook did not finish within 5s (blocked on a non-regular path?): %s", out)
	}
	if err != nil {
		t.Fatalf("hook failed: %v (%s)", err, out)
	}
	return string(out)
}

// isRegularWith fails unless p is a regular file holding body -- the
// untouched-victim half of a containment assertion.
func isRegularWith(t *testing.T, p, body string) {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("%s must still be a regular file: %v %v", p, fi, err)
	}
	if b, _ := os.ReadFile(p); string(b) != body {
		t.Fatalf("%s content changed: %q", p, b)
	}
}
