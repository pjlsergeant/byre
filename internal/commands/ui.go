package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/pjlsergeant/byre/internal/project"
)

// noteSharedVolumes warns, before a destructive lifecycle action, that these
// volumes are shared across the whole project (all its worktrees) — so wiping them from one
// worktree affects them all. No-op for a plain project.
func noteSharedVolumes(w io.Writer, paths project.Paths) {
	if paths.IsWorktree {
		fmt.Fprintf(w, "byre: this is a worktree of %s; its volumes are SHARED — this affects ALL worktrees of this repo.\n", paths.Canonical)
	}
}

// noteMachineVolumes tells the user, during a destructive lifecycle action,
// which machine-scoped volumes were deliberately NOT touched and how to delete
// one on purpose (ADR 0017: reset/forget are project-scoped by contract; the
// machine-wide agent login must never die as a side effect of resetting one
// project). Checked against the engine, not the config: a volume can outlive
// the skill that declared it and still deserves the note.
func noteMachineVolumes(w io.Writer, r volumeRunner, uid int) {
	vols, err := r.VolumesByPrefix(fmt.Sprintf("byre-machine-u%d-", uid))
	if err != nil || len(vols) == 0 {
		return
	}
	fmt.Fprintf(w, "byre: NOT touched (machine-wide, shared by all your projects): %s\n", strings.Join(vols, ", "))
	fmt.Fprintln(w, "byre: to delete one deliberately: byre config -> Volumes -> clear.")
}

// warnRootlessPodman warns, on w, when r drives rootless Podman WITHOUT the
// keep-id mapping byre's rootless path needs — the remaining unsupported
// case (supported rootless Podman is silent here: it's a first-class path,
// ADR 0032). A detection error is ignored — better silent than warning on a
// guess. Used by commands acting on an EXISTING session (deliver); develop
// mode-selects and refuses instead (resolveIdentity).
func warnRootlessPodman(w io.Writer, r sessionRunner) {
	if rootless, err := r.IsRootlessPodman(); err == nil && rootless {
		if ok, kerr := r.SupportsKeepIDMapping(); kerr != nil || !ok {
			fmt.Fprintln(w, "byre: warning: "+rootlessPodmanUnsupported)
		}
	}
}

// confirmed reads a line and returns true only for an affirmative answer.
// It reads BYTE-AT-A-TIME, never buffering ahead: flows that chain several
// confirms over one stdin (preset apply's chauffeur, then its own confirm)
// would otherwise lose every answer after the first inside a discarded
// bufio buffer — the same trap onboarding's shared-reader rule guards.
func confirmed(stdin io.Reader) bool {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			break
		}
	}
	switch strings.ToLower(strings.TrimSpace(string(line))) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
