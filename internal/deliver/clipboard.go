package deliver

import (
	"fmt"
	"strings"
)

// Clipboard is the host's clipboard-write capability, probed by the caller
// (the tool set is a host concern; the payload contract lives here). A nil
// Clipboard in Config means no write path exists — deliver degrades to a
// printed claim, never a refusal: stdout is the contract, the clipboard is
// garnish.
type Clipboard struct {
	// Write puts text on the host clipboard.
	Write func(text string) error
	// Name is the mechanism, for the feedback line ("pbcopy", "wl-copy",
	// "xclip", "OSC 52").
	Name string
	// BestEffort marks fire-and-forget mechanisms (OSC 52 has no failure
	// signal), so the feedback line doesn't overclaim.
	BestEffort bool
}

// clipboardPayload is what lands on the clipboard: the delivered paths, one
// per line, lazily quoted — the string you'd have typed by hand if you were
// being careful, built for pasting into an agent prompt. stdout stays
// unquoted; the two formats are deliberately different (decisions D12-D13).
func clipboardPayload(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = quoteIfNeeded(p)
	}
	return strings.Join(quoted, "\n")
}

// quoteIfNeeded single-quotes a path only when pasting it bare would break —
// quotes are noise on the common tame path, and boundary markers on the
// macOS-screenshot kind ("Screenshot 2026-07-10 at 3.14.15 PM.png").
func quoteIfNeeded(p string) string {
	if p != "" && !strings.ContainsAny(p, " \t'\"\\!$&()*,;<>?[]^`{|}~#%") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// shipClipboard runs the round-trip's return leg after a delivery: put the
// landed paths on the host clipboard and SAY what happened either way. The
// claim degrades honestly — unavailable or failed writes still leave stdout
// as the contract.
func shipClipboard(cfg Config, opts Options, landed []string) {
	if len(landed) == 0 || opts.NoClip {
		return
	}
	noun := "path"
	if len(landed) > 1 {
		noun = "paths"
	}
	if cfg.Clip == nil || cfg.Clip.Write == nil {
		fmt.Fprintf(cfg.Err, "byre: clipboard unavailable — %s printed above\n", noun)
		return
	}
	if err := cfg.Clip.Write(clipboardPayload(landed)); err != nil {
		fmt.Fprintf(cfg.Err, "byre: clipboard write failed (%v) — %s printed above\n", err, noun)
		return
	}
	if cfg.Clip.BestEffort {
		fmt.Fprintf(cfg.Err, "byre: %s sent to the clipboard via %s (best-effort) — also printed above\n", noun, cfg.Clip.Name)
		return
	}
	fmt.Fprintf(cfg.Err, "byre: %s copied to the clipboard (%s)\n", noun, cfg.Clip.Name)
}
