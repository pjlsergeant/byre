package commands

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// OS notifications are deliver's feedback channel for GRAPHICAL launches
// (decisions D19): the deliver app and .desktop entry run byre with no
// terminal, so stdout/stderr land nowhere a human looks. When there's no TTY
// but a GUI session exists, the outcome — success summary or failure — goes
// to the notification center (osascript on macOS, notify-send on Linux;
// shelled out per ADR 0002). Never attempted without a GUI session, and a
// failed notification is swallowed: it's garnish on top of the printed
// truth, same doctrine as the clipboard leg.

// guiSession reports whether a graphical session exists to draw on — the
// same gates the picker adapter uses.
func guiSession(goos string, getenv func(string) string) bool {
	if goos == "darwin" {
		return getenv("SSH_CONNECTION") == ""
	}
	return getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != ""
}

// notify shows one outcome. Best-effort by design. On macOS this is a
// DIALOG, not a notification banner: `display notification` from a bare
// osascript is permission-gated (and silently no-ops ungranted —
// field-found 2026-07-10: a successful Quick Action showed nothing), while
// `display dialog` needs no permission and is guaranteed visible. Successes
// auto-dismiss ("giving up after"); failures stay until acknowledged. If
// dialogs are refused in some context (-1713 no-user-interaction), the
// banner is still attempted as a fallback.
func notify(goos string, title, body string, sticky bool) {
	switch goos {
	case "darwin":
		esc := func(s string) string { // AppleScript string literal escaping
			s = strings.ReplaceAll(s, `\`, `\\`)
			return strings.ReplaceAll(s, `"`, `\"`)
		}
		icon, dismiss := "note", " giving up after 4"
		if sticky {
			icon, dismiss = "caution", ""
		}
		script := fmt.Sprintf(`display dialog "%s" with title "%s" buttons {"OK"} default button 1 with icon %s%s`,
			esc(body), esc(title), icon, dismiss)
		if _, err := clipRunOut("osascript", "-e", script); err != nil {
			banner := fmt.Sprintf(`display notification "%s" with title "%s"`, esc(body), esc(title))
			_, _ = clipRunOut("osascript", "-e", banner)
		}
	default:
		if _, err := clipLookPath("notify-send"); err == nil {
			_, _ = clipRunOut("notify-send", title, body)
		}
	}
}

// deliverNotify reports a deliver outcome on the notification channel when —
// and only when — nothing else reaches the user: no TTY, GUI present.
func deliverNotify(s Streams, landed []string, err error) {
	if s.TTY || !guiSession(runtime.GOOS, os.Getenv) {
		return
	}
	title := "byre deliver"
	switch {
	case err != nil && len(landed) == 0:
		notify(runtime.GOOS, title, firstNotifyLine(err.Error()), true)
	case err != nil:
		notify(runtime.GOOS, title, fmt.Sprintf("%s — but %s", notifySummary(landed), firstNotifyLine(err.Error())), true)
	case len(landed) > 0:
		notify(runtime.GOOS, title, notifySummary(landed)+" — path copied to the clipboard", false)
	}
}

// notifySummary names what landed, compactly.
func notifySummary(landed []string) string {
	if len(landed) == 1 {
		return landed[0]
	}
	return fmt.Sprintf("%d files delivered to the inbox", len(landed))
}

// firstNotifyLine keeps multi-line errors notification-sized.
func firstNotifyLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
