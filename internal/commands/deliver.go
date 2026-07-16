package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/project"
)

// Deliver gets files from the host into a running box's /inbox — byre's one
// machine-scoped verb (discovery finds a box; every other command derives one
// from cwd). The mechanics live in internal/deliver; this file wires the host
// side in: installed engines, the label vocabulary, workdir ids for the
// cascade's ancestor walk, the caller's identity, and the input-source modes.
func Deliver(s Streams, dir string, opts deliver.Options, paths []string) error {
	// The protocol handshake runs before ANYTHING else — a skewed remote
	// invocation must fail before discovery, listings, or payload (ADR 0035).
	if opts.Proto != 0 {
		if err := deliver.CheckProto(opts.Proto); err != nil {
			return err
		}
	}
	if opts.Boxes {
		return deliverBoxes(s, dir, opts)
	}
	if opts.Tar {
		return deliverTar(s, dir, opts)
	}
	sources, err := deliverSources(s, opts, paths, hostClipboardReader())
	if err != nil || sources == nil { // nil sources = beat cancelled, cleanly
		deliverNotify(s, nil, err)
		return err
	}
	landed, err := deliverWith(s, dir, opts, sources, installedEngines(), os.Getuid(), hostClipboardWriter(), hostPicker(s))
	// Graphical launches (the deliver app, a .desktop entry) have no terminal
	// to read: the outcome ALSO goes to the notification center.
	deliverNotify(s, landed, err)
	return err
}

// deliverSources resolves the input mode (ADR 0021): path args →
// files; `-` → stdin stream; no args on a TTY → the paste beat, then the
// pasteboard; no args with piped stdin → stream it; no args, no TTY, no pipe
// (a graphical launch) → read the pasteboard immediately. A nil, nil return
// means the user cancelled at the beat.
func deliverSources(s Streams, opts deliver.Options, paths []string, reader *clipBackend) ([]deliver.Source, error) {
	stamp := time.Now().Format("20060102-150405")
	stdinSource := func(r io.Reader) deliver.Source {
		name := opts.Name
		if name == "" {
			name = "stdin-" + stamp
		}
		return deliver.Source{Reader: r, Name: name, Kind: "stdin"}
	}
	switch {
	case len(paths) == 1 && paths[0] == "-":
		return []deliver.Source{stdinSource(s.In)}, nil
	case len(paths) > 0:
		return deliver.PathSources(paths), nil
	case s.TTY:
		action, text, err := runPasteBeat(s, reader)
		switch {
		case err != nil:
			return nil, err
		case action == beatCancelled:
			fmt.Fprintln(s.Err, "byre: cancelled — nothing delivered")
			return nil, nil
		case action == beatText:
			if len(text) == 0 {
				fmt.Fprintln(s.Err, "byre: nothing pasted — nothing delivered")
				return nil, nil
			}
			return []deliver.Source{{Data: text, Name: "clipboard-" + stamp + ".txt", Kind: "pasted text"}}, nil
		case action == beatPaste:
			return importFromPaste(s, reader, text, stamp)
		}
		fmt.Fprintln(s.Err, "byre: reading the clipboard…")
		return readClipboard(*reader, time.Now, s.Err)
	case stdinIsPiped():
		return []deliver.Source{stdinSource(s.In)}, nil
	case reader != nil: // graphical / detached launch: read immediately
		return readClipboard(*reader, time.Now, s.Err)
	default:
		return nil, fmt.Errorf("nothing to deliver: no paths, no piped stdin, and no clipboard access — pass a path or pipe content in")
	}
}

// importFromPaste decides what a bracketed paste MEANS (field-found
// 2026-07-10: a file dragged onto the window pastes its path — text that was
// never on the pasteboard, so blindly reading the clipboard delivers stale
// content, in the worst case byre's own previous output). The streamed text
// is evidence:
//
//   - mirrors the pasteboard's text → a real Cmd-V: do the full priority
//     read (a Finder copy's file refs beat its text representation);
//   - parses as existing absolute host path(s) → a drag: deliver the FILES;
//   - anything else → literal pasted content.
//
// Every branch says immediately what was received — the beat must never
// look hung after a gesture.
func importFromPaste(s Streams, reader *clipBackend, text []byte, stamp string) ([]deliver.Source, error) {
	fmt.Fprintf(s.Err, "byre: paste received (%d bytes)\n", len(text))
	trimmed := strings.TrimSpace(string(text))
	if trimmed == "" {
		fmt.Fprintln(s.Err, "byre: empty paste — reading the clipboard instead…")
		return readClipboard(*reader, time.Now, s.Err)
	}
	if pb, err := reader.fetch("text/plain"); err == nil && strings.TrimSpace(string(pb)) == trimmed {
		fmt.Fprintln(s.Err, "byre: reading the clipboard…")
		return readClipboard(*reader, time.Now, s.Err)
	}
	if paths := draggedPaths(trimmed); len(paths) > 0 {
		fmt.Fprintf(s.Err, "byre: delivering the dragged %s\n", plural(len(paths), "file", "files"))
		return deliver.PathSources(paths), nil
	}
	return []deliver.Source{{Data: text, Name: "clipboard-" + stamp + ".txt", Kind: "pasted text"}}, nil
}

// draggedPaths recognizes a terminal drag: absolute path(s), shell-escaped
// (terminals backslash-escape ANY special character in a dragged name —
// spaces, `&`, parens, quotes — so `\X` unescapes to X generally), space-
// separated when multiple. ABSOLUTE is required — pasted prose that happens
// to name a relative file must stay text, never surprise-deliver a file —
// and every token must exist; a miss means the whole paste stays text.
func draggedPaths(s string) []string {
	// One path first (spaces might be separators OR escaped content).
	if p := shellUnescape(s); pathExists(p) {
		return []string{p}
	}
	var toks []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			cur.WriteByte(s[i+1]) // escaped char: literal, never a separator
			i++
		case s[i] == ' ':
			if cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(s[i])
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	if len(toks) == 0 {
		return nil
	}
	for _, t := range toks {
		if !pathExists(t) {
			return nil
		}
	}
	return toks
}

// shellUnescape resolves unquoted backslash escapes (`\X` → X).
func shellUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func pathExists(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && (st.Mode().IsRegular() || st.IsDir())
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// stdinIsPiped distinguishes `... | byre deliver` (a pipe or file on stdin —
// stream it) from a detached launch (stdin is /dev/null, a character device —
// clipboard mode). Overridable seam for tests.
var stdinIsPiped = func() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice == 0
}

// deliverBoxes is `byre deliver --boxes`: the headless enumeration leg of
// remote delivery (ADR 0035). Stdout carries the line grammar, stderr the
// notes, and a partial pool exits ExitPartialPool so the caller knows not to
// auto-pick — the list itself still printed and stays usable.
func deliverBoxes(s Streams, dir string, opts deliver.Options) error {
	cfg, err := deliverConfig(s, dir, installedEngines(), os.Getuid(), nil, nil)
	if err != nil {
		return err
	}
	partial, err := deliver.Boxes(cfg, opts)
	if err != nil {
		return err
	}
	if partial {
		return ExitError{Code: deliver.ExitPartialPool}
	}
	return nil
}

// deliverTar is `byre deliver --tar -`: the delivery leg — unpack the archive
// on stdin into the selected box. Normally invoked over ssh by a local byre
// (which passes --box and --no-clip), but a hand-run works identically:
// picker, clipboard garnish, and cancel behave as in a plain delivery.
func deliverTar(s Streams, dir string, opts deliver.Options) error {
	cfg, err := deliverConfig(s, dir, installedEngines(), os.Getuid(), hostClipboardWriter(), hostPicker(s))
	if err != nil {
		return err
	}
	if _, err := deliver.RunTar(cfg, opts, s.In); err != nil {
		if deliver.IsCancelled(err) {
			fmt.Fprintln(s.Err, "byre: cancelled — nothing delivered")
			return nil
		}
		return err
	}
	return nil
}

func deliverWith(s Streams, dir string, opts deliver.Options, sources []deliver.Source, engines []sessionRunner, uid int, clip *deliver.Clipboard, pick func([]deliver.Session) (deliver.Session, bool, error)) ([]string, error) {
	cfg, err := deliverConfig(s, dir, engines, uid, clip, pick)
	if err != nil {
		return nil, err
	}
	landed, err := deliver.RunSources(cfg, opts, sources)
	if deliver.IsCancelled(err) {
		fmt.Fprintln(s.Err, "byre: cancelled — nothing delivered")
		return landed, nil
	}
	return landed, err
}

// deliverConfig wires the host side into a deliver.Config: installed engines
// (adapted, with the rootless-podman warn and caller-scoping probe), the
// label vocabulary, workdir ids for the cascade, and the caller's identity.
func deliverConfig(s Streams, dir string, engines []sessionRunner, uid int, clip *deliver.Clipboard, pick func([]deliver.Session) (deliver.Session, bool, error)) (deliver.Config, error) {
	if len(engines) == 0 {
		// Zero ENGINES must not masquerade as zero boxes (field-found
		// 2026-07-10: a Finder-launched byre couldn't see Docker Desktop on
		// its sparse PATH and claimed "no running byre boxes").
		return deliver.Config{}, fmt.Errorf("no container engine (docker or podman) found on PATH — if this ran from the Dock or Finder, the environment's PATH may be too sparse to see it")
	}
	cfg := deliver.Config{
		ProjectLabel: labelKey,
		WorkdirLabel: workdirKey,
		CallerUID:    uid,
		Cwd:          dir,
		WorkdirIDOf: func(d string) (string, error) {
			p, err := project.Resolve(d)
			if err != nil {
				return "", err
			}
			return p.WorktreeID, nil
		},
		Out:  s.Out,
		Err:  s.Err,
		Clip: clip,
		Pick: pick,
	}
	for _, r := range engines {
		// Rootless Podman WITHOUT keep-id support inherits develop's
		// detect-and-warn (exec-stream ownership can be wrong there); the
		// claim degrades up front, delivery itself is not blocked. Supported
		// rootless Podman is a first-class path (ADR 0032) — and its per-user
		// storage makes the engine caller-scoped, which discovery's uid
		// accident-guard must know: a keep-id box's BYRE_UID is the generic
		// in-container uid, not the caller's.
		warnRootlessPodman(s.Err, r)
		callerScoped := false
		if rootless, rerr := r.IsRootlessPodman(); rerr == nil && rootless {
			callerScoped = true
		}
		cfg.Engines = append(cfg.Engines, engineAdapter{r: r, callerScoped: callerScoped})
	}
	return cfg, nil
}

// engineAdapter narrows a sessionRunner to deliver's Engine interface.
// callerScoped is decided at wiring time (one rootless probe per engine, not
// per session).
type engineAdapter struct {
	r            sessionRunner
	callerScoped bool
}

func (a engineAdapter) Name() string       { return string(a.r.Engine()) }
func (a engineAdapter) CallerScoped() bool { return a.callerScoped }
func (a engineAdapter) Sessions(label string) ([]string, error) {
	return a.r.RunningContainersByLabel(label)
}
func (a engineAdapter) Env(id string) (map[string]string, error)    { return a.r.ContainerEnv(id) }
func (a engineAdapter) Labels(id string) (map[string]string, error) { return a.r.ContainerLabels(id) }
func (a engineAdapter) ExecInput(id string, uid, gid int, stdin io.Reader, argv ...string) (string, error) {
	return a.r.ExecInput(id, uid, gid, stdin, argv...)
}
