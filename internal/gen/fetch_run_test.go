package gen

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/testtools"
)

// fetchRunResult is one run of the real embedded byre-fetch-run.
type fetchRunResult struct {
	code   int
	stderr string
	tmpDir string // TMPDIR the script's mktemp wrote into
}

// runFetchRun serves body with status on a local server and drives the
// embedded helper under sh (dash in the image) against it. Retries are off so
// a failing fetch returns at once; TMPDIR is private so the test can see the
// helper removed its temp file.
func runFetchRun(t *testing.T, status int, body string) fetchRunResult {
	t.Helper()
	testtools.NeedTool(t, "sh", "curl")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	script := filepath.Join(dir, "fetch-run.sh")
	if err := os.WriteFile(script, FetchRunScript(), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, srv.URL+"/install.sh", "sh")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMPDIR="+tmpDir, "BYRE_FETCH_RUN_RETRIES=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := fetchRunResult{stderr: stderr.String(), tmpDir: tmpDir}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("fetch-run did not run: %v", err)
		}
		res.code = ee.ExitCode()
	}
	return res
}

func assertTempGone(t *testing.T, r fetchRunResult) {
	t.Helper()
	left, err := os.ReadDir(r.tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temp file not removed: %v", left)
	}
}

// markerScript is a valid installer that proves it ran by writing a marker
// (relative to the helper's cwd, the test's dir).
const markerScript = "#!/bin/sh\necho ran > marker\n"

func TestFetchRunRunsScript(t *testing.T) {
	r := runFetchRun(t, http.StatusOK, markerScript)
	if r.code != 0 {
		t.Fatalf("valid script must run and exit 0; exit %d: %s", r.code, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.tmpDir), "marker")); err != nil {
		t.Errorf("installer did not run: %v", err)
	}
	assertTempGone(t, r)
}

// A 200 whose body is not a script (an error page, an empty body) must fail
// the build step without running anything: running it can exit 0 and leave
// Docker a cached layer with no binary.
func TestFetchRunRefusesNonScript(t *testing.T) {
	for name, body := range map[string]string{
		"html":  "<html><body>This workload is drawing too much egress bandwidth</body></html>\necho ran > marker\n",
		"empty": "",
	} {
		t.Run(name, func(t *testing.T) {
			r := runFetchRun(t, http.StatusOK, body)
			if r.code == 0 {
				t.Fatalf("non-script body must fail; stderr: %s", r.stderr)
			}
			if !strings.Contains(r.stderr, "did not return a script") {
				t.Errorf("shebang rule did not fire; stderr: %s", r.stderr)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(r.tmpDir), "marker")); err == nil {
				t.Error("interpreter ran a non-script body")
			}
			assertTempGone(t, r)
		})
	}
}

func TestFetchRunFetchFailure(t *testing.T) {
	r := runFetchRun(t, http.StatusTooManyRequests, markerScript)
	if r.code == 0 {
		t.Fatalf("failed fetch must fail; stderr: %s", r.stderr)
	}
	if !strings.Contains(r.stderr, "fetch failed") {
		t.Errorf("fetch-failure rule did not fire; stderr: %s", r.stderr)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.tmpDir), "marker")); err == nil {
		t.Error("installer ran after a failed fetch")
	}
	assertTempGone(t, r)
}

func TestFetchRunPropagatesInstallerStatus(t *testing.T) {
	r := runFetchRun(t, http.StatusOK, "#!/bin/sh\nexit 7\n")
	if r.code != 7 {
		t.Fatalf("installer exit 7 must surface as 7; got %d: %s", r.code, r.stderr)
	}
	assertTempGone(t, r)
}
