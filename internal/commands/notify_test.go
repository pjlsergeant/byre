package commands

import (
	"strings"
	"testing"
)

func TestGUISession(t *testing.T) {
	if guiSession("darwin", env(map[string]string{"SSH_CONNECTION": "1.2.3.4"})) {
		t.Fatal("SSH'd darwin is not a GUI session")
	}
	if !guiSession("darwin", env(nil)) {
		t.Fatal("local darwin is a GUI session")
	}
	if guiSession("linux", env(nil)) {
		t.Fatal("linux without DISPLAY/WAYLAND is not a GUI session")
	}
	if !guiSession("linux", env(map[string]string{"WAYLAND_DISPLAY": "w-0"})) {
		t.Fatal("wayland is a GUI session")
	}
}

func stubRunOut(t *testing.T) *[]string {
	t.Helper()
	orig := clipRunOut
	t.Cleanup(func() { clipRunOut = orig })
	var calls []string
	clipRunOut = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	return &calls
}

func TestNotifyDarwinEscapesAppleScript(t *testing.T) {
	calls := stubRunOut(t)
	notify("darwin", "byre deliver", `path "with" quotes \ and slash`)
	if len(*calls) != 1 {
		t.Fatalf("calls = %v", *calls)
	}
	got := (*calls)[0]
	if !strings.Contains(got, `\"with\"`) || !strings.Contains(got, `\\ and slash`) {
		t.Fatalf("AppleScript string not escaped: %q", got)
	}
}

func TestNotifyLinuxUsesNotifySend(t *testing.T) {
	stubClipTools(t, "notify-send") // lookup succeeds
	calls := stubRunOut(t)
	notify("linux", "byre deliver", "/inbox/a.png")
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "notify-send ") {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestNotifyLinuxSilentWithoutTool(t *testing.T) {
	stubClipTools(t) // nothing available
	calls := stubRunOut(t)
	notify("linux", "t", "b")
	if len(*calls) != 0 {
		t.Fatalf("should not exec anything: %v", *calls)
	}
}

func TestNotifySummary(t *testing.T) {
	if got := notifySummary([]string{"/inbox/a.png"}); got != "/inbox/a.png" {
		t.Fatalf("got %q", got)
	}
	if got := notifySummary([]string{"a", "b", "c"}); got != "3 files delivered to the inbox" {
		t.Fatalf("got %q", got)
	}
}
