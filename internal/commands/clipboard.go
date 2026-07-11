package commands

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// Host clipboard probing for deliver. Capabilities are probed per-axis and
// each degrades on its own (ADR 0021): a write TOOL is preferred (pbcopy /
// wl-copy / xclip — shelling out is house style, ADR 0002); with no tool but
// a terminal on stderr, OSC 52 sets the USER'S terminal's clipboard through
// SSH — write-only and fire-and-forget, so it's marked best-effort and the
// paths always print regardless. Nil means no path at all: deliver prints
// "clipboard unavailable" and stdout remains the contract.

// Seams for tests: tool lookup, tool execution, and the OSC 52 sink.
var (
	clipLookPath = exec.LookPath
	clipRunTool  = func(name string, args []string, stdin string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) > 0 {
			return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
		}
		return err
	}
)

// clipboardWriter probes the host for a clipboard write path. goos and env
// and the OSC 52 sink are parameters so the probe order is unit-testable.
func clipboardWriter(goos string, getenv func(string) string, osc52 io.Writer) *deliver.Clipboard {
	type tool struct {
		name string
		args []string
	}
	var candidates []tool
	switch goos {
	case "darwin":
		candidates = []tool{{"pbcopy", nil}}
	default:
		// Wayland first when a Wayland session is up; X11 selection otherwise.
		if getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, tool{"wl-copy", nil})
		}
		if getenv("DISPLAY") != "" {
			candidates = append(candidates, tool{"xclip", []string{"-selection", "clipboard"}})
		}
	}
	for _, c := range candidates {
		if _, err := clipLookPath(c.name); err == nil {
			c := c
			return &deliver.Clipboard{
				Name:  c.name,
				Write: func(text string) error { return clipRunTool(c.name, c.args, text) },
			}
		}
	}
	if osc52 != nil {
		return &deliver.Clipboard{
			Name:       "OSC 52",
			BestEffort: true,
			Write: func(text string) error {
				// ESC ] 52 ; c ; <base64> BEL — sets the terminal's clipboard;
				// terminals disable the read half for security, and give no
				// success signal, hence best-effort.
				_, err := fmt.Fprintf(osc52, "\x1b]52;c;%s\a", base64.StdEncoding.EncodeToString([]byte(text)))
				return err
			},
		}
	}
	return nil
}

// hostClipboardWriter is clipboardWriter wired to the real host: OSC 52 only
// when stderr is a terminal (the sequence must reach a terminal to mean
// anything; into a pipe it's just bytes).
func hostClipboardWriter() *deliver.Clipboard {
	var osc io.Writer
	if isTTY(os.Stderr) {
		osc = os.Stderr
	}
	return clipboardWriter(runtime.GOOS, os.Getenv, osc)
}
