package commands

import (
	"fmt"
	"io"
	"os"
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
	sources, err := deliverSources(s, opts, paths, hostClipboardReader())
	if err != nil || sources == nil { // nil sources = beat cancelled, cleanly
		return err
	}
	return deliverWith(s, dir, opts, sources, installedEngines(), os.Getuid(), hostClipboardWriter(), hostPicker(s))
}

// deliverSources resolves the input mode (decisions D17-D19): path args →
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
		action, text, err := runPasteBeat(s, reader != nil)
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
		}
		return readClipboard(*reader, time.Now)
	case stdinIsPiped():
		return []deliver.Source{stdinSource(s.In)}, nil
	case reader != nil: // graphical / detached launch: read immediately
		return readClipboard(*reader, time.Now)
	default:
		return nil, fmt.Errorf("nothing to deliver: no paths, no piped stdin, and no clipboard access — pass a path or pipe content in")
	}
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

func deliverWith(s Streams, dir string, opts deliver.Options, sources []deliver.Source, engines []sessionRunner, uid int, clip *deliver.Clipboard, pick func([]deliver.Session) (deliver.Session, bool, error)) error {
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
		cfg.Engines = append(cfg.Engines, engineAdapter{r})
	}
	_, err := deliver.RunSources(cfg, opts, sources)
	if deliver.IsCancelled(err) {
		fmt.Fprintln(s.Err, "byre: cancelled — nothing delivered")
		return nil
	}
	return err
}

// engineAdapter narrows a sessionRunner to deliver's Engine interface.
type engineAdapter struct{ r sessionRunner }

func (a engineAdapter) Name() string { return string(a.r.Engine()) }
func (a engineAdapter) Sessions(label string) ([]string, error) {
	return a.r.RunningContainersByLabel(label)
}
func (a engineAdapter) Env(id string) (map[string]string, error)    { return a.r.ContainerEnv(id) }
func (a engineAdapter) Labels(id string) (map[string]string, error) { return a.r.ContainerLabels(id) }
func (a engineAdapter) ExecInput(id string, uid, gid int, stdin io.Reader, argv ...string) (string, error) {
	return a.r.ExecInput(id, uid, gid, stdin, argv...)
}
