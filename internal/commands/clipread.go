package commands

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// Host clipboard READ for deliver's no-arg mode. Import priority (ADR
// 0021): file references → image → text. File references resolve to paths and
// ride path mode; images and text land as clipboard-<timestamp> captures
// whose extension follows the format the pasteboard ACTUALLY held (never
// transcode, never mislabel).
//
// The backend normalizes both platforms to MIME-ish type tags so priority
// and parsing are unit-testable; only the tool invocations are platform code.
// macOS reads ride osascript (file refs via JXA/NSPasteboard — the only
// route that yields MULTIPLE Finder selections; image bytes via pngpaste
// when installed, else AppleScript's hex «data» rendering). Linux rides
// wl-paste / xclip. Shelling out is house style (ADR 0002).

// clipBackend is one clipboard's read surface.
type clipBackend struct {
	listTypes func() ([]string, error)
	fetch     func(typ string) ([]byte, error)
}

const typeFileRefs = "file-refs" // normalized tag for file references

// clipRunOut is the capture-exec seam for read tools. Errors wrap the
// original (%w), so callers that must distinguish exit codes — a dialog's
// cancel-exit vs a broken tool — can errors.As their way to the ExitError.
var clipRunOut = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	// Children leave the tty's foreground process group: Terminal.app's
	// title shows the fg group's active process, and the beat's sampler
	// spawning a child every tick made the title flash byre↔osascript
	// (field-found 2026-07-10). None of these tools read the tty.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s (%s): %w", name, strings.TrimSpace(string(ee.Stderr)), err)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// exitCode digs the child's exit code out of a wrapped clipRunOut error
// (-1 when there is none — a lookup or I/O failure, not a tool exit).
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// readClipboard reads the highest-priority representation into sources. A
// FAILED fetch of a higher tier degrades to the next with a warning (same as
// an empty one) — a compositor advertising a type it can't serve must not
// take working text down with it. Only when no tier delivers does the first
// failure surface.
func readClipboard(cb clipBackend, now func() time.Time, warn io.Writer) ([]deliver.Source, error) {
	types, err := cb.listTypes()
	if err != nil {
		return nil, fmt.Errorf("reading clipboard types: %w", err)
	}
	stamp := now().Format("20060102-150405")
	var firstErr error
	degrade := func(what string, err error) {
		fmt.Fprintf(warn, "byre: clipboard %s read failed (%v); trying the next representation\n", what, err)
		if firstErr == nil {
			firstErr = err
		}
	}

	if hasType(types, typeFileRefs) {
		raw, err := cb.fetch(typeFileRefs)
		if err != nil {
			degrade("file-references", err)
		} else if paths := parseFileRefs(string(raw)); len(paths) > 0 {
			return deliver.PathSources(paths), nil
		}
		// Fall through: a furl/uri-list type with nothing usable behind it.
	}
	if imgType := pickImageType(types); imgType != "" {
		raw, err := cb.fetch(imgType)
		if err != nil {
			degrade("image", err)
		} else if len(raw) > 0 {
			return []deliver.Source{{
				Data: raw,
				Name: "clipboard-" + stamp + extFor(imgType),
				Kind: "clipboard image",
			}}, nil
		}
	}
	if hasType(types, "text/plain") {
		raw, err := cb.fetch("text/plain")
		if err != nil {
			degrade("text", err)
		} else if len(raw) > 0 {
			return []deliver.Source{{
				Data: raw,
				Name: "clipboard-" + stamp + ".txt",
				Kind: "clipboard text",
			}}, nil
		}
	}
	if firstErr != nil {
		return nil, fmt.Errorf("reading the clipboard: %w", firstErr)
	}
	// Name what WAS seen: when a real board lands here, the types list is
	// the diagnostic (a class byre doesn't map yet, an empty representation).
	seen := "no types at all"
	if len(types) > 0 {
		seen = "types seen: " + strings.Join(types, ", ")
	}
	return nil, fmt.Errorf("the clipboard holds nothing deliverable (%s)", seen)
}

func hasType(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

// pickImageType prefers PNG (most portable), then any other image/*.
func pickImageType(types []string) string {
	if hasType(types, "image/png") {
		return "image/png"
	}
	for _, t := range types {
		if strings.HasPrefix(t, "image/") {
			return t
		}
	}
	return ""
}

// extFor names a capture after the format actually read (never mislabel).
func extFor(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpeg"
	case "image/tiff":
		return ".tiff"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	default:
		if i := strings.Index(mime, "/"); i >= 0 && i+1 < len(mime) {
			return "." + mime[i+1:]
		}
		return ""
	}
}

// parseFileRefs accepts both shapes the backends yield: plain absolute paths
// one per line (macOS JXA) and file:// URIs (Linux text/uri-list).
func parseFileRefs(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "file://") {
			u, err := url.Parse(line)
			if err != nil || u.Path == "" {
				continue
			}
			out = append(out, u.Path)
			continue
		}
		if strings.Contains(line, "://") {
			continue // a non-file URI (http copy, etc) is not a file reference
		}
		if strings.HasPrefix(line, "/") {
			out = append(out, line)
		}
	}
	return out
}

// --- platform backends ---

// darwinBackend reads the macOS pasteboard via osascript/pbpaste.
func darwinBackend() clipBackend {
	return clipBackend{
		listTypes: func() ([]string, error) {
			out, err := clipRunOut("osascript", "-e", "clipboard info")
			if err != nil {
				return nil, err
			}
			return parseDarwinClipInfo(string(out)), nil
		},
		fetch: func(typ string) ([]byte, error) {
			switch typ {
			case typeFileRefs:
				// JXA + NSPasteboard: the one route that yields EVERY file of a
				// multi-select Finder copy (AppleScript's furl coercion returns
				// only the first).
				return clipRunOut("osascript", "-l", "JavaScript", "-e", darwinFileRefsJXA)
			case "image/png":
				if _, err := clipLookPath("pngpaste"); err == nil {
					return clipRunOut("pngpaste", "-")
				}
				return darwinClipData("PNGf")
			case "image/jpeg":
				return darwinClipData("JPEG")
			case "image/gif":
				return darwinClipData("GIFf")
			case "image/tiff":
				return darwinClipData("TIFF")
			case "text/plain":
				return clipRunOut("pbpaste")
			}
			return nil, fmt.Errorf("unsupported clipboard type %q", typ)
		},
	}
}

const darwinFileRefsJXA = `ObjC.import("AppKit");
const pb = $.NSPasteboard.generalPasteboard;
const opts = $.NSDictionary.dictionaryWithObjectForKey(true, $.NSPasteboardURLReadingFileURLsOnlyKey);
const urls = pb.readObjectsForClassesOptions($.NSArray.arrayWithObject($.NSURL.class), opts);
const out = [];
if (urls) { for (let i = 0; i < urls.count; i++) out.push(ObjC.unwrap(urls.objectAtIndex(i).path)); }
out.join("\n");`

// parseDarwinClipInfo maps `clipboard info` output to normalized type tags.
// The output is a comma-separated list alternating type tokens and sizes,
// e.g. `«class furl», 57, «class PNGf», 11916, string, 12`.
func parseDarwinClipInfo(info string) []string {
	var types []string
	add := func(t string) {
		if !hasType(types, t) {
			types = append(types, t)
		}
	}
	if strings.Contains(info, "«class furl»") {
		add(typeFileRefs)
	}
	if strings.Contains(info, "«class PNGf»") {
		add("image/png")
	}
	if strings.Contains(info, "«class JPEG»") {
		add("image/jpeg")
	}
	if strings.Contains(info, "«class GIFf»") {
		add("image/gif")
	}
	if strings.Contains(info, "«class TIFF»") {
		add("image/tiff")
	}
	if strings.Contains(info, "string") { // covers string / Unicode text / utf8
		add("text/plain")
	}
	return types
}

// darwinClipData reads binary clipboard data via AppleScript's hex rendering:
// `the clipboard as «class PNGf»` prints `«data PNGf6789...»`.
func darwinClipData(class string) ([]byte, error) {
	out, err := clipRunOut("osascript", "-e", "the clipboard as «class "+class+"»")
	if err != nil {
		return nil, err
	}
	return parseDarwinHexData(string(out), class)
}

func parseDarwinHexData(out, class string) ([]byte, error) {
	s := strings.TrimSpace(out)
	prefix, suffix := "«data "+class, "»"
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return nil, fmt.Errorf("unexpected clipboard data shape for %s", class)
	}
	return hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(s, prefix), suffix))
}

// linuxBackend reads via wl-paste (Wayland) or xclip (X11), whichever the
// session offers.
func linuxBackend(getenv func(string) string) *clipBackend {
	if getenv("WAYLAND_DISPLAY") != "" {
		if _, err := clipLookPath("wl-paste"); err == nil {
			return &clipBackend{
				listTypes: func() ([]string, error) {
					out, err := clipRunOut("wl-paste", "--list-types")
					if err != nil {
						return nil, err
					}
					return normalizeLinuxTypes(string(out)), nil
				},
				fetch: func(typ string) ([]byte, error) {
					if typ == typeFileRefs {
						return clipRunOut("wl-paste", "--type", "text/uri-list")
					}
					return clipRunOut("wl-paste", "--type", typ)
				},
			}
		}
	}
	if getenv("DISPLAY") != "" {
		if _, err := clipLookPath("xclip"); err == nil {
			return &clipBackend{
				listTypes: func() ([]string, error) {
					out, err := clipRunOut("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
					if err != nil {
						return nil, err
					}
					return normalizeLinuxTypes(string(out)), nil
				},
				fetch: func(typ string) ([]byte, error) {
					if typ == typeFileRefs {
						return clipRunOut("xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o")
					}
					if typ == "text/plain" {
						return clipRunOut("xclip", "-selection", "clipboard", "-o")
					}
					return clipRunOut("xclip", "-selection", "clipboard", "-t", typ, "-o")
				},
			}
		}
	}
	return nil
}

// normalizeLinuxTypes maps advertised targets to the normalized tags.
func normalizeLinuxTypes(listing string) []string {
	var types []string
	add := func(t string) {
		if !hasType(types, t) {
			types = append(types, t)
		}
	}
	for _, line := range strings.Split(listing, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "text/uri-list":
			add(typeFileRefs)
		case strings.HasPrefix(t, "image/"):
			add(t)
		case t == "text/plain" || strings.HasPrefix(t, "text/plain;") || t == "UTF8_STRING" || t == "STRING" || t == "TEXT":
			add("text/plain")
		}
	}
	return types
}

// hostClipboardReader probes for a read backend; nil means no read path
// (headless SSH: the paste beat degrades to literal text capture).
func hostClipboardReader() *clipBackend {
	switch runtime.GOOS {
	case "darwin":
		if _, err := clipLookPath("osascript"); err == nil {
			cb := darwinBackend()
			return &cb
		}
		return nil
	default:
		return linuxBackend(os.Getenv)
	}
}
