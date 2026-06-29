package commands

import (
	"bytes"
	"strings"
	"testing"

	"byre/internal/runner"
)

func TestReportRunning(t *testing.T) {
	var b bytes.Buffer
	reportRunning(&b, runner.Engine("docker"), []string{"abc123def456"})
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
}
