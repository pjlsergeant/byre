package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pjlsergeant/byre/internal/runner"
)

func TestReportRunning(t *testing.T) {
	var b bytes.Buffer
	reportRunning(&b, runner.Engine("docker"), []string{"abc123def456"}, true)
	out := b.String()
	if !strings.Contains(out, "already running") {
		t.Errorf("missing running notice:\n%s", out)
	}
	if !strings.Contains(out, "byre shell") {
		t.Errorf("should point at byre shell:\n%s", out)
	}
	if !strings.Contains(out, "docker stop ") {
		t.Errorf("should give the engine stop command:\n%s", out)
	}
	if !strings.Contains(out, "docker attach --detach-keys=ctrl-p,ctrl-q ") {
		t.Errorf("should give the engine re-attach command with pinned detach keys:\n%s", out)
	}
	if !strings.Contains(out, "Ctrl-P Ctrl-Q") {
		t.Errorf("re-attach line should carry the detach keys:\n%s", out)
	}
}

// The cross-engine caller matches via ps -a, so its container can be an
// exited ownership marker: the lead line must not assert "running" above the
// caller's own "not running? remove it" bullet.
func TestReportRunningNotLiveLeadDoesNotAssertRunning(t *testing.T) {
	var b bytes.Buffer
	reportRunning(&b, runner.Engine("podman"), []string{"abc123def456"}, false)
	out := b.String()
	if strings.Contains(out, "already running") {
		t.Errorf("not-live lead asserts a session is running:\n%s", out)
	}
	if !strings.Contains(out, "running or stopped") {
		t.Errorf("not-live lead should state the unknown liveness:\n%s", out)
	}
	if !strings.Contains(out, "podman attach --detach-keys=ctrl-p,ctrl-q ") {
		t.Errorf("not-live report should keep the pinned re-attach command:\n%s", out)
	}
}
