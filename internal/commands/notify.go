package commands

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostexec"
)

// OS notifications are deliver's feedback channel for GRAPHICAL launches
// (ADR 0021): the deliver app and .desktop entry run byre with no
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

// notifyTitle is the one title this channel ever shows: only deliver has a
// graphical launch path.
const notifyTitle = "byre deliver"

// notify shows one outcome. Best-effort by design. On macOS this is a
// DIALOG, not a notification banner: `display notification` from a bare
// osascript is permission-gated (and silently no-ops ungranted —
// field-found 2026-07-10: a successful Quick Action showed nothing), while
// `display dialog` needs no permission and is guaranteed visible. Successes
// auto-dismiss ("giving up after"); failures stay until acknowledged. If
// dialogs are refused in some context (-1713 no-user-interaction), the
// banner is still attempted as a fallback.
func notify(goos string, body string, sticky bool, roots hostexec.Roots) {
	switch goos {
	case "darwin":
		esc := func(s string) string { // AppleScript string literal escaping
			s = strings.ReplaceAll(s, `\`, `\\`)
			return strings.ReplaceAll(s, `"`, `\"`)
		}
		icon, dismiss := "note", " giving up after 5"
		bodyEsc := esc(body)
		if sticky {
			icon, dismiss = "caution", ""
		} else {
			// An auto-closing dialog with an OK button reads as haunted
			// unless it SAYS it self-dismisses. Appended AFTER escaping, as
			// AppleScript's own \n escape: a raw line break inside an
			// AppleScript string literal is a syntax error.
			bodyEsc += `\n\n(this window closes itself)`
		}
		script := fmt.Sprintf(`display dialog "%s" with title "%s" buttons {"OK"} default button 1 with icon %s%s`,
			bodyEsc, esc(notifyTitle), icon, dismiss)
		osa, err := clipLookPath("osascript", roots)
		if err != nil {
			return // no osascript to notify with; the terminal path already ran
		}
		if _, err := clipRunOut(osa, "-e", script); err != nil {
			banner := fmt.Sprintf(`display notification "%s" with title "%s"`, esc(body), esc(notifyTitle))
			_, _ = clipRunOut(osa, "-e", banner)
		}
	default:
		if ns, err := clipLookPath("notify-send", roots); err == nil {
			// -u critical keeps a failure on screen until acknowledged --
			// the linux spelling of the macOS sticky dialog.
			urgency := "normal"
			if sticky {
				urgency = "critical"
			}
			_, _ = clipRunOut(ns, "-u", urgency, notifyTitle, body)
		}
	}
}

// deliverNotify reports a deliver outcome on the notification channel when —
// and only when — nothing else reaches the user: no TTY, GUI present.
func deliverNotify(s Streams, landed []string, err error, roots hostexec.Roots) {
	if s.TTY || !guiSession(runtime.GOOS, os.Getenv) {
		return
	}
	switch {
	case err != nil && len(landed) == 0:
		notify(runtime.GOOS, firstNotifyLine(err.Error()), true, roots)
	case err != nil:
		notify(runtime.GOOS, fmt.Sprintf("%s — but %s", notifySummary(landed), firstNotifyLine(err.Error())), true, roots)
	case len(landed) > 0:
		notify(runtime.GOOS, notifySummary(landed)+" — path copied to the clipboard", false, roots)
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
