package commands

import (
	"fmt"
	"os"

	"github.com/pjlsergeant/byre/internal/deliver"
)

// Grab gets a file or directory out of a running box onto the host — deliver's
// mirror, sharing its machine-scoped discovery. The mechanics live in
// internal/deliver (RunGrab); this file wires the same host side in as
// Deliver, minus the clipboard leg: the landed path prints to stdout, where
// the user's own shell already is.
func Grab(s Streams, dir string, opts deliver.Options, boxPath, hostPath string) error {
	return grabWith(s, dir, opts, boxPath, hostPath, installedEngines(), os.Getuid(), hostPicker(s))
}

func grabWith(s Streams, dir string, opts deliver.Options, boxPath, hostPath string, engines []sessionRunner, uid int, pick func([]deliver.Session) (deliver.Session, bool, error)) error {
	cfg, err := deliverConfig(s, dir, engines, uid, nil, pick)
	if err != nil {
		return err
	}
	if _, err := deliver.RunGrab(cfg, opts, boxPath, hostPath); err != nil {
		if deliver.IsCancelled(err) {
			fmt.Fprintln(s.Err, "byre: cancelled — nothing grabbed")
			return ExitError{Code: 1}
		}
		return err
	}
	return nil
}
