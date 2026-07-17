package commands

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// clientGone upgrades "running" to "running, orphaned" only on positive
// evidence of a dead client pid; every unknown state stays plain running.
func TestClientGone(t *testing.T) {
	// A pid that HAS existed and is now dead: spawn-and-reap.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid
	for name, tc := range map[string]struct {
		labels map[string]string
		want   bool
	}{
		"alive client": {map[string]string{clientKey: strconv.Itoa(os.Getpid())}, false},
		"dead client":  {map[string]string{clientKey: strconv.Itoa(dead)}, true},
		"no label":     {map[string]string{"byre.project": "x"}, false},
		"garbage pid":  {map[string]string{clientKey: "not-a-pid"}, false},
		"negative pid": {map[string]string{clientKey: "-7"}, false},
		"empty labels": {map[string]string{}, false},
	} {
		if got := clientGone(tc.labels); got != tc.want {
			t.Errorf("%s: clientGone = %v, want %v", name, got, tc.want)
		}
	}
}
