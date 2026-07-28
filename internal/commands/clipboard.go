package commands

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/hostexec"
)

// Host clipboard probing for deliver. Capabilities are probed per-axis and
// each degrades on its own (ADR 0021): a write TOOL is preferred (pbcopy /
// wl-copy / xclip — shelling out is house style, ADR 0002); with no tool but
// a terminal on stderr, OSC 52 sets the USER'S terminal's clipboard through
// SSH — write-only and fire-and-forget, so it's marked best-effort and the
// paths always print regardless. Nil means no path at all: deliver prints
// "clipboard unavailable" and stdout remains the contract.

// Seams for tests: tool lookup, tool execution, and the OSC 52 sink.
//
// clipLookPath answers with the ABSOLUTE path to run, and every spawn here
// and in clipread/pick/notify runs that path rather than the name it probed
// for: probing a name and then executing the name is two PATH reads with a
// window between them. It rides hostexec, so the answer is pinned for the
// invocation and a helper resolved out of a directory the box writes is
// declined at the probe.
var (
	clipLookPath = hostexec.Look
	clipRunTool  = func(name string, args []string, stdin string) error {
		cmd := exec.Command(name, args...)
		// Out of the fg process group, like clipRunOut: keeps clipboard
		// helpers out of Terminal's active-process title.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return err
	}
)

// clipboardWriter probes the host for a clipboard write path. goos and env
// and the OSC 52 sink are parameters so the probe order is unit-testable.
func clipboardWriter(goos string, getenv func(string) string, osc52 io.Writer, roots hostexec.Roots) *deliver.Clipboard {
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
		if exe, err := clipLookPath(c.name, roots); err == nil {
			c, exe := c, exe
			return &deliver.Clipboard{
				Name:  c.name, // the NAME is what the user is told; exe is what runs
				Write: func(text string) error { return clipRunTool(exe, c.args, text) },
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
func hostClipboardWriter(roots hostexec.Roots) *deliver.Clipboard {
	var osc io.Writer
	if isTTY(os.Stderr) {
		osc = os.Stderr
	}
	return clipboardWriter(runtime.GOOS, os.Getenv, osc, roots)
}
