package commands

import (
	"bytes"
	"context"
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
	"github.com/pjlsergeant/byre/internal/hostexec"
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

// clipRunOut is the capture-exec seam for read tools. name is the ABSOLUTE
// path clipLookPath already resolved, not the tool's name -- probing a name
// and then executing the name would be two PATH reads with a window between
// them. Errors wrap the original (%w), so callers that must distinguish exit
// codes — a dialog's cancel-exit vs a broken tool — can errors.As their way
// to the ExitError.
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

// A clipboard READ is bounded on both axes; the shared clipRunOut seam above
// is not, because it also drives interactive dialogs (the session picker's
// chooser), where the child is legitimately waiting on a human. A read is
// never waiting on anyone: no pasteboard tool takes minutes to answer, and one
// that never answers -- a compositor that stopped serving its own advertised
// type -- would wedge `byre deliver` with nothing but ctrl-C to end it. The
// size cap is the same shape: the payload is whatever is on the pasteboard,
// and a runaway one must fail rather than become host memory.
const (
	clipReadTimeout = 2 * time.Minute
	clipMaxOutput   = 64 << 20 // a large screenshot (hex-rendered on macOS, so ~2x the image)
	// clipWaitDelay is how long a killed read's output pipes may stay open
	// before the wait gives up on them.
	clipWaitDelay = 5 * time.Second
)

// clipReadOut runs one clipboard-read tool under those bounds. Errors keep
// clipRunOut's %w wrapping so exitCode can still reach the ExitError.
var clipReadOut = func(name string, args ...string) ([]byte, error) {
	return clipReadBounded(clipReadTimeout, clipMaxOutput, name, args...)
}

// clipReadBounded is clipReadOut's body with the bounds as parameters, so a
// test can prove the deadline and the cap fire without waiting two minutes or
// allocating 64 MiB.
func clipReadBounded(timeout time.Duration, max int, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// Same reason clipRunOut does it: none of these tools read the tty, and a
	// child in the foreground process group makes the terminal title flap.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The group exists, so cancel it as a GROUP. CommandContext's default kills
	// the direct child only, which leaves the descendants these tools spawn
	// (osascript's helpers) alive and holding the stdout pipe -- the read below
	// then blocks past the deadline that was supposed to end it. Negative pid
	// is the whole process group.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// And a second bound on the wait itself, for a descendant that outlives
	// even the group kill (one that changed its own group).
	cmd.WaitDelay = clipWaitDelay
	// Stderr is capped, not saved by exec: Output() populates ExitError.Stderr
	// but this reads stdout through a pipe, so the diagnostic has to be kept
	// here (runner.capBuffer's shape -- bounded, never blocking the child).
	var stderr capBuffer
	stderr.max = 64 << 10
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	out, rerr := io.ReadAll(io.LimitReader(pipe, int64(max)+1))
	over := len(out) > max
	if over {
		cancel() // stop the writer (the whole group); a capped read never waits it out
	}
	werr := cmd.Wait()
	if over {
		return nil, fmt.Errorf("%s: clipboard content exceeds %d bytes", name, max)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: no answer within %s (gave up)", name, timeout)
	}
	if rerr != nil {
		return nil, fmt.Errorf("%s: %w", name, rerr)
	}
	if werr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s (%s): %w", name, msg, werr)
		}
		return nil, fmt.Errorf("%s: %w", name, werr)
	}
	return out, nil
}

// capBuffer keeps at most max bytes but always reports a full write, so a
// child writing past the cap is never blocked on its stderr pipe (it just
// stops being recorded). The runner has the same type for the same reason;
// duplicated rather than exported, since it is four lines and the two packages
// share no other plumbing.
type capBuffer struct {
	b   bytes.Buffer
	max int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.b.Len(); room > 0 {
		if len(p) > room {
			c.b.Write(p[:room])
		} else {
			c.b.Write(p)
		}
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return c.b.String() }

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

// darwinBackend reads the macOS pasteboard via osascript/pbpaste. osa is the
// resolved osascript the caller already probed for; the other two tools are
// resolved here, and a tool that won't resolve simply isn't offered.
func darwinBackend(osa string, roots hostexec.Roots) clipBackend {
	return clipBackend{
		listTypes: func() ([]string, error) {
			out, err := clipReadOut(osa, "-e", "clipboard info")
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
				return clipReadOut(osa, "-l", "JavaScript", "-e", darwinFileRefsJXA)
			case "image/png":
				if exe, err := clipLookPath("pngpaste", roots); err == nil {
					return clipReadOut(exe, "-")
				}
				return darwinClipData(osa, "PNGf")
			case "image/jpeg":
				return darwinClipData(osa, "JPEG")
			case "image/gif":
				return darwinClipData(osa, "GIFf")
			case "image/tiff":
				return darwinClipData(osa, "TIFF")
			case "text/plain":
				exe, err := clipLookPath("pbpaste", roots)
				if err != nil {
					return nil, err
				}
				return clipReadOut(exe)
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
func darwinClipData(osa, class string) ([]byte, error) {
	out, err := clipReadOut(osa, "-e", "the clipboard as «class "+class+"»")
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
func linuxBackend(getenv func(string) string, roots hostexec.Roots) *clipBackend {
	if getenv("WAYLAND_DISPLAY") != "" {
		if exe, err := clipLookPath("wl-paste", roots); err == nil {
			return &clipBackend{
				listTypes: func() ([]string, error) {
					out, err := clipReadOut(exe, "--list-types")
					if err != nil {
						return nil, err
					}
					return normalizeLinuxTypes(string(out)), nil
				},
				fetch: func(typ string) ([]byte, error) {
					if typ == typeFileRefs {
						return clipReadOut(exe, "--type", "text/uri-list")
					}
					return clipReadOut(exe, "--type", typ)
				},
			}
		}
	}
	if getenv("DISPLAY") != "" {
		if exe, err := clipLookPath("xclip", roots); err == nil {
			return &clipBackend{
				listTypes: func() ([]string, error) {
					out, err := clipReadOut(exe, "-selection", "clipboard", "-t", "TARGETS", "-o")
					if err != nil {
						return nil, err
					}
					return normalizeLinuxTypes(string(out)), nil
				},
				fetch: func(typ string) ([]byte, error) {
					if typ == typeFileRefs {
						return clipReadOut(exe, "-selection", "clipboard", "-t", "text/uri-list", "-o")
					}
					if typ == "text/plain" {
						return clipReadOut(exe, "-selection", "clipboard", "-o")
					}
					return clipReadOut(exe, "-selection", "clipboard", "-t", typ, "-o")
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
func hostClipboardReader(roots hostexec.Roots) *clipBackend {
	switch runtime.GOOS {
	case "darwin":
		if exe, err := clipLookPath("osascript", roots); err == nil {
			cb := darwinBackend(exe, roots)
			return &cb
		}
		return nil
	default:
		return linuxBackend(os.Getenv, roots)
	}
}
