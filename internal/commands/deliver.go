package commands

import (
	"io"
	"os"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/project"
)

// Deliver gets files from the host into a running box's /inbox — byre's one
// machine-scoped verb (discovery finds a box; every other command derives one
// from cwd). The mechanics live in internal/deliver; this file wires the host
// side in: installed engines, the label vocabulary, workdir ids for the
// cascade's ancestor walk, and the caller's identity.
func Deliver(s Streams, dir string, opts deliver.Options, paths []string) error {
	return deliverWith(s, dir, opts, paths, installedEngines(), os.Getuid(), hostClipboardWriter())
}

func deliverWith(s Streams, dir string, opts deliver.Options, paths []string, engines []sessionRunner, uid int, clip *deliver.Clipboard) error {
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
	}
	for _, r := range engines {
		cfg.Engines = append(cfg.Engines, engineAdapter{r})
	}
	landed, err := deliver.Run(cfg, opts, paths)
	_ = landed // the clipboard round-trip consumes these (next build step)
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
