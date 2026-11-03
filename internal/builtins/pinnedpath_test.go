package builtins

import (
	"os"
	"strings"
	"testing"
)

// pinnedSystemPath is the PATH the launch adapters and the codex reconciler
// pin for their own helpers, the image's system directories.
const pinnedSystemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// stagePinned copies a script that pins its helpers' PATH to dst, the pinned
// PATH replaced by pin and then each from/to pair in rewrites applied. Every
// replacement must match exactly once, so a script that stops pinning, or
// stops naming a program by the absolute path a rewrite targets, fails here.
func stagePinned(t *testing.T, src, dst, pin string, rewrites ...string) string {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	pairs := append([]string{"\nPATH=" + pinnedSystemPath + "\n", "\nPATH=" + pin + "\n"}, rewrites...)
	for i := 0; i+1 < len(pairs); i += 2 {
		if n := strings.Count(body, pairs[i]); n != 1 {
			t.Fatalf("%s: %q appears %d times, want once", src, strings.TrimSpace(pairs[i]), n)
		}
		body = strings.Replace(body, pairs[i], pairs[i+1], 1)
	}
	if err := os.WriteFile(dst, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}
