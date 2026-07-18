package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/project"
)

// labelKey is the PROJECT label: byre.project=<project-id>. Every container of
// the project (all its worktrees) carries it, so blast-radius lifecycle queries (reset/forget/
// rehome/status) find all worktrees' sessions. workdirKey is the per-worktree
// label: byre.workdir=<worktree-id>, used to find a SINGLE worktree's session
// (develop's fast path, shell) so two worktrees can run at once without one
// seeing the other's container. For a plain project the two values are equal.
// runKey is a transient per-invocation label: byre.run=<random nonce>. Added
// only when netns-init hooks will run, as the OWNERSHIP PROOF for their
// target: the project and workdir label values are derivable from the project
// path, so a container planted with them could otherwise capture the
// root+NET_ADMIN helper — the nonce is fresh randomness that exists only in
// this invocation's run argv (asserted last, so run_args can't override it)
// and cannot be known in advance. (Reading it back post-start requires
// docker-socket access, which is host-root-equivalent already.)
// clientKey records the host byre process that started the session:
// byre.client=<pid>. Status uses it to tell a session whose terminal is gone
// (client hangup orphans the box — the container survives, deliberately)
// from one with a live byre attached. Liveness-by-pid is a heuristic: a
// recycled pid can mask an orphan, which degrades the label back to plain
// "running", never the other way around.
const (
	labelKey   = "byre.project"
	workdirKey = "byre.workdir"
	runKey     = "byre.run"
	clientKey  = "byre.client"
)

// containerName is the engine container name — keyed on the worktree id so two
// worktrees of one repo get distinct containers (and distinct single-session
// locks). For a plain project WorktreeID == ID, so this is the historical
// byre-<id>.
func containerName(p project.Paths) string { return "byre-" + p.WorktreeID }

// worktreeCreateName names the one-shot worktree-creation container (runner.
// WorktreeAdd): distinct from every session name (those are byre-<worktree-id>,
// and the create step carries no session labels), keyed on the target path so
// two concurrent creates of one target collide loudly at the engine while
// creates of different targets proceed. project.ID can only fail on an
// unresolvable absolute path — unreachable for the already-absolute target —
// so the bare prefix fallback is a formality.
func worktreeCreateName(target string) string {
	id, err := project.ID(target)
	if err != nil {
		return "byre-wtadd"
	}
	return "byre-wtadd-" + id
}

// clientGone reports whether a session's recorded byre client (the
// byre.client pid label) is dead — the box survived a client hangup and no
// terminal can reach it. Unknown states (no label: a box from an older byre;
// unparseable pid; liveness unprobeable) report false: the heuristic only
// ever upgrades "running" to "running, orphaned", never invents liveness.
func clientGone(labels map[string]string) bool {
	v, ok := labels[clientKey]
	if !ok {
		return false
	}
	pid, err := strconv.Atoi(v)
	if err != nil || pid <= 0 {
		return false
	}
	// Raw kill(2) with signal 0 probes existence: ESRCH = gone; EPERM =
	// alive as another user (not ours, but somebody's) — treat alive.
	// Deliberately not os.FindProcess/Signal: its pidfd fast path reports
	// a vanished pid as os.ErrProcessDone, an extra encoding of the same
	// fact this raw probe answers directly.
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// projectLabel selects every container of the project (all its worktrees).
func projectLabel(p project.Paths) string { return labelKey + "=" + p.ID }

// workdirLabel selects a single worktree's container.
func workdirLabel(p project.Paths) string { return workdirKey + "=" + p.WorktreeID }

// imageTag is the local image tag for a project's build, qualified by the host
// UID/GID baked into the image. The UID/GID are part of the image's identity (the
// dev user, /home/dev, and the volume mount points are all chowned to them at
// build time), so on a shared daemon two users building the same project path get
// distinct images instead of one reusing the other's wrong-owned build. The
// container NAME stays byre-<id> (it makes a single session atomic, and volume
// names are unchanged); only the image tag carries the uid/gid.
func imageTag(projectID string, uid, gid int) string {
	return fmt.Sprintf("byre-%s-u%d-g%d", projectID, uid, gid)
}

// volumeName is the Docker name for a project's named volume: byre-<id>-<name>.
// The project id namespaces it, so reset/forget/rehome can filter a project's
// volumes by the byre-<id>- prefix. (Worktree volume INHERITANCE, when built,
// works by resolving <id> from the main worktree's path — not by a separate
// volume scope — see docs/adr/0009-worktrees-inherit-project-identity.md.)
func volumeName(projectID, name string) string {
	return "byre-" + projectID + "-" + name
}

// machineVolumeName is the Docker name for a machine-scoped volume:
// byre-machine-u<uid>-<name>. No project id — every project of this user
// resolves the same name, which is the point (ADR 0017). The uid qualifier
// matches imageTag's precedent: on a shared daemon two users must not silently
// share one volume (it cannot stop a daemon user mounting another's volume
// deliberately — daemon access is root-equivalent; see docs/SECURITY.md).
func machineVolumeName(uid int, name string) string {
	return fmt.Sprintf("byre-machine-u%d-%s", uid, name)
}

// machineVolumeRe matches any user's machine-scoped volume names, so project-
// volume listings can exclude them even when a project id happens to begin
// with "machine" (e.g. a repo directory literally named "machine").
var machineVolumeRe = regexp.MustCompile(`^byre-machine-u[0-9]+-`)

// scopedVolumeName picks the Docker name for a resolved volume by its scope.
func scopedVolumeName(projectID string, uid int, v config.Volume) string {
	if v.MachineScoped() {
		return machineVolumeName(uid, v.Name)
	}
	return volumeName(projectID, v.Name)
}

// projectVolumes lists the volumes owned by id. Because project ids may contain
// hyphens, a bare `byre-<id>-` prefix can also match another project's volumes
// (when that project's id begins with this id). Each volume is assigned to the
// LONGEST known id whose prefix it carries, so one project never captures
// another's volumes.
func projectVolumes(r volumeRunner, home, id string) ([]string, error) {
	vols, err := r.VolumesByPrefix("byre-" + id + "-")
	if err != nil {
		return nil, err
	}
	others := knownProjectIDs(home)
	var owned []string
	for _, v := range vols {
		// Machine-scoped volumes (byre-machine-u<uid>-...) are never a
		// project's, even when this project's id begins with "machine" (a
		// repo directory literally named that) -- reset/forget must not
		// capture them (ADR 0017).
		if machineVolumeRe.MatchString(v) {
			continue
		}
		if !claimedByLongerID(v, id, others) {
			owned = append(owned, v)
		}
	}
	return owned, nil
}

// knownProjectIDs lists the ids byre has a ~/.byre/projects/<id>/ dir for.
func knownProjectIDs(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, "projects"))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

// claimedByLongerID reports whether vol belongs to a different, more-specific
// project id (a longer `byre-<oid>-` prefix) than id.
func claimedByLongerID(vol, id string, others []string) bool {
	p := "byre-" + id + "-"
	for _, oid := range others {
		op := "byre-" + oid + "-"
		if oid != id && len(op) > len(p) && strings.HasPrefix(vol, op) {
			return true
		}
	}
	return false
}
