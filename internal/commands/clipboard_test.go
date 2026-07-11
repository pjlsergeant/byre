package commands

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// stubClipTools makes only the named tools findable and records invocations.
func stubClipTools(t *testing.T, available ...string) *[]string {
	t.Helper()
	origLook, origRun := clipLookPath, clipRunTool
	t.Cleanup(func() { clipLookPath, clipRunTool = origLook, origRun })
	var calls []string
	clipLookPath = func(name string) (string, error) {
		for _, a := range available {
			if a == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", fmt.Errorf("not found")
	}
	clipRunTool = func(name string, args []string, stdin string) error {
		calls = append(calls, name+" "+strings.Join(args, " ")+" <-"+stdin)
		return nil
	}
	return &calls
}

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestClipboardWriterDarwin(t *testing.T) {
	calls := stubClipTools(t, "pbcopy")
	c := clipboardWriter("darwin", env(nil), nil)
	if c == nil || c.Name != "pbcopy" || c.BestEffort {
		t.Fatalf("writer = %+v", c)
	}
	if err := c.Write("hi"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != "pbcopy  <-hi" {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestClipboardWriterWaylandBeforeX11(t *testing.T) {
	stubClipTools(t, "wl-copy", "xclip")
	c := clipboardWriter("linux", env(map[string]string{"WAYLAND_DISPLAY": "w-0", "DISPLAY": ":0"}), nil)
	if c == nil || c.Name != "wl-copy" {
		t.Fatalf("writer = %+v", c)
	}
}

func TestClipboardWriterX11UsesClipboardSelection(t *testing.T) {
	calls := stubClipTools(t, "xclip")
	c := clipboardWriter("linux", env(map[string]string{"DISPLAY": ":0"}), nil)
	if c == nil || c.Name != "xclip" {
		t.Fatalf("writer = %+v", c)
	}
	_ = c.Write("x")
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "-selection clipboard") {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestClipboardWriterNoGUIEnvNoTools(t *testing.T) {
	stubClipTools(t) // nothing available
	if c := clipboardWriter("linux", env(nil), nil); c != nil {
		t.Fatalf("expected nil writer, got %+v", c)
	}
}

func TestClipboardWriterOSC52Fallback(t *testing.T) {
	stubClipTools(t) // no tools
	var term bytes.Buffer
	c := clipboardWriter("linux", env(nil), &term)
	if c == nil || c.Name != "OSC 52" || !c.BestEffort {
		t.Fatalf("writer = %+v", c)
	}
	if err := c.Write("/inbox/a.png"); err != nil {
		t.Fatal(err)
	}
	// base64("/inbox/a.png") framed as ESC ] 52 ; c ; ... BEL
	if got := term.String(); got != "\x1b]52;c;L2luYm94L2EucG5n\a" {
		t.Fatalf("osc52 = %q", got)
	}
}

func TestClipboardWriterToolBeatsOSC52(t *testing.T) {
	stubClipTools(t, "pbcopy")
	var term bytes.Buffer
	c := clipboardWriter("darwin", env(nil), &term)
	if c == nil || c.Name != "pbcopy" {
		t.Fatalf("writer = %+v", c)
	}
}
