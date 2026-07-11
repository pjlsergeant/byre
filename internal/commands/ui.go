package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
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

// rootlessPodmanWarning explains why rootless Podman is unsupported in v0: byre
// bakes the host UID/GID into the image so the in-container uid matches the uid
// on disk, which assumes a ROOTFUL daemon; rootless Podman's user-namespace remap
// breaks that. A generic-uid image + --userns=keep-id is the planned fix.
const rootlessPodmanWarning = "rootless Podman detected — byre bakes the host UID/GID into the image so files it writes land owned by you, which assumes a ROOTFUL daemon. Under rootless Podman the userns remap makes that wrong, so files in the project and volumes can end up owned by the wrong id. v0 supports rootful Docker/Podman only — use one of those for correct ownership."

// warnRootlessPodman prints the rootless-Podman warning to w when r drives it. A
// detection error is ignored — better silent than warning on a guess. Used by
// commands acting on an EXISTING session (deliver); develop refuses instead
// (requireRootfulEngine).
func warnRootlessPodman(w io.Writer, r sessionRunner) {
	if rootless, err := r.IsRootlessPodman(); err == nil && rootless {
		fmt.Fprintln(w, "byre: warning: "+rootlessPodmanWarning)
	}
}

// requireRootfulEngine refuses to start a session under rootless Podman: the
// launch is KNOWN to produce wrong-owned files in the project and volumes
// (see rootlessPodmanWarning), so completing it silently corrupts ownership
// on byre's primary path. BYRE_ALLOW_ROOTLESS_PODMAN=1 overrides with the
// warning retained — the same shape as the root-host refusal
// (requireNonRootHost / BYRE_ALLOW_ROOT). A detection error stays a quiet
// proceed: better to run than to refuse on a guess.
func requireRootfulEngine(warn io.Writer, r sessionRunner) error {
	rootless, err := r.IsRootlessPodman()
	if err != nil || !rootless {
		return nil
	}
	if os.Getenv("BYRE_ALLOW_ROOTLESS_PODMAN") == "1" {
		fmt.Fprintln(warn, "byre: WARNING: running anyway (BYRE_ALLOW_ROOTLESS_PODMAN=1). "+rootlessPodmanWarning)
		return nil
	}
	return errors.New("refusing to run under rootless Podman: " + rootlessPodmanWarning + " Set BYRE_ALLOW_ROOTLESS_PODMAN=1 to proceed anyway.")
}

// confirmed reads a line and returns true only for an affirmative answer.
func confirmed(stdin io.Reader) bool {
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
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
