package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
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
// helperKey, helperSrcKey and helperRunKey label the one-shot HELPER
// containers byre runs against a project's volumes (seeding, rehome's copy,
// backup's capture, restore's pour and both verbs' preflight):
// byre.helper=<project id> says which project's volumes the helper may be
// holding, and byre.helper.run=<random per invocation> says which byre
// invocation started it, so a verb cleaning up force-removes exactly ITS
// helpers. byre.helper.src=<project id> is the SECOND project a helper can be
// holding: rehome's copy mounts the old id's volume as well as the new id's,
// and a container label key holds one value, so the old id needs a key of its
// own or a sweep under it (a develop in a recreated old path, a reset or
// forget there) could not see the helper at all. Deliberately
// NOT the project label: a helper is not a session (ADR 0053, as amended),
// and the session sweeps must never read one as a box. They query the helper
// label separately and refuse while one is alive -- a helper that outlived
// the byre that started it is holding a volume the sweep is about to touch.
const (
	labelKey     = "byre.project"
	workdirKey   = "byre.workdir"
	runKey       = "byre.run"
	clientKey    = "byre.client"
	helperKey    = "byre.helper"
	helperSrcKey = "byre.helper.src"
	helperRunKey = "byre.helper.run"
)

// helperLabel, helperSrcLabel and helperRunLabel are the "key=value" forms of
// the labels above: every query, every --label and every remedy line goes
// through them.
func helperLabel(id string) string       { return helperKey + "=" + id }
func helperSrcLabel(id string) string    { return helperSrcKey + "=" + id }
func helperRunLabel(runID string) string { return helperRunKey + "=" + runID }

// helperProjectLabels are the two keys under which a helper can be holding
// project id's volumes: its own and, for rehome's copy, the id it is copying
// FROM. Every leftover-helper sweep queries both.
func helperProjectLabels(id string) []string { return []string{helperLabel(id), helperSrcLabel(id)} }

// containerName is the engine container name — keyed on the worktree id so two
// worktrees of one repo get distinct containers (and distinct single-session
// locks). For a plain project WorktreeID == ID, so this is the historical
// byre-<id>.
func containerName(p project.Paths) string { return "byre-" + p.WorktreeID }

// worktreeCreateName names the one-shot worktree-creation container
// (runner.WorktreeAdd): distinct from every session name (those are byre-<worktree-id>,
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
// volumes by the byre-<id>- prefix. (Worktree volume INHERITANCE works by
// resolving <id> from the main worktree's path — not by a separate volume
// scope — see docs/adr/0009-worktrees-inherit-project-identity.md.)
func volumeName(projectID, name string) string {
	return volumePrefix(projectID) + name
}

// volumePrefix is the one spelling of a project's volume-name prefix. Three
// things key off it -- the name a volume gets, the listing that finds a
// project's volumes, and the longest-id ownership rule -- and a second
// spelling of it would be a silent mismatch between them.
func volumePrefix(projectID string) string { return "byre-" + projectID + "-" }

// machineVolumeName is the Docker name for a machine-scoped volume:
// byre-machine-u<uid>-<name>. No project id — every project of this user
// resolves the same name, which is the point (ADR 0017). The uid qualifier
// matches imageTag's precedent: on a shared daemon two users must not silently
// share one volume (it cannot stop a daemon user mounting another's volume
// deliberately — daemon access is root-equivalent; see docs/SECURITY.md).
func machineVolumeName(uid int, name string) string {
	return machineVolumePrefix(uid) + name
}

// machineVolumePrefix is the physical prefix machineVolumeName builds on, and
// the one backup trims a listed machine volume's logical name off.
func machineVolumePrefix(uid int) string { return fmt.Sprintf("byre-machine-u%d-", uid) }

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
	vols, err := r.VolumesByPrefix(volumePrefix(id))
	if err != nil {
		return nil, err
	}
	// A failure here must NOT come back as "no other projects": that is the
	// answer that makes claimedByLongerID stop excluding anyone, so every
	// prefix-matching volume reads as this project's and forget deletes
	// another project's state. Ownership is a claim byre either establishes
	// or declines to make.
	others, err := knownProjectIDs(home)
	if err != nil {
		return nil, err
	}
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
// No store at all is an empty list; anything else is an error, because the
// caller uses this to decide what NOT to delete.
func knownProjectIDs(home string) ([]string, error) {
	entries, err := hostopen.PlainReadDir(filepath.Join(home, "projects"), hostopen.StoreOwned)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// claimedByLongerID reports whether vol belongs to a different, more-specific
// project id (a longer `byre-<oid>-` prefix) than id.
func claimedByLongerID(vol, id string, others []string) bool {
	return claimingLongerID(vol, id, others) != ""
}

// claimingLongerID is disputingID's longer-id half: the project that owns vol
// INSTEAD of id, "" when none does. Backup prints it ("not carried: owned by
// project <id>"), because "this volume is not yours" is an answer the user can
// only act on if it says whose it is.
func claimingLongerID(vol, id string, others []string) string {
	if oid := disputingID(vol, id, others); len(oid) > len(id) {
		return oid
	}
	return ""
}

// disputingID names the OTHER known project whose own volume prefix also
// spells phys -- the longest such id, which is the one projectVolumes' rule
// hands the name to. "" means no other project's listing can claim it.
//
// ignore is the id whose enrollment must not be consulted: restore asks this
// question about a project that is not enrolled yet (and, on a retry, about
// one that is). Once this id has a store directory it is the LONGEST id for
// its own names, so a listing that counted it would stop reporting the
// shorter project's claim -- the very collision the caller has to refuse.
func disputingID(phys, ignore string, known []string) string {
	winner := ""
	for _, oid := range known {
		if oid == ignore || !strings.HasPrefix(phys, volumePrefix(oid)) {
			continue
		}
		if len(oid) > len(winner) {
			winner = oid
		}
	}
	return winner
}

// refuseVolumeNameOwnership refuses when a physical volume name this project
// is about to create or adopt is a name another enrolled project's own
// listing would claim, in EITHER direction (ids and logical names both use
// "-", so one physical name can spell two projects):
//
//   - a LONGER id claims it outright: projectVolumes hands the name to that
//     project, so a volume byre poured here would be reset and forget's to
//     delete over there. Refused whether or not it exists yet.
//   - a SHORTER id -- this project's id extends an existing one -- claims it
//     only while this project is not enrolled, which is exactly restore's
//     window. Refused when the name ALREADY exists, because then the volume
//     on the engine is the other project's state and byre must not keep it as
//     this project's nor pour over it. A name that does not exist yet is this
//     project's to create: once it is enrolled it is the longer id and wins.
//
// Both refusals name both projects: "this name is not yours" is only
// actionable if it says whose it is.
func refuseVolumeNameOwnership(r volumeRunner, home, id string, logical []string) error {
	known, err := knownProjectIDs(home)
	if err != nil {
		return err
	}
	for _, name := range logical {
		phys := volumeName(id, name)
		oid := disputingID(phys, id, known)
		if oid == "" {
			continue
		}
		if len(oid) > len(id) {
			return fmt.Errorf("volume %s would be project %s's, not this project's (%s) — the two ids spell the same physical name; restore into a directory whose project id does not collide", phys, oid, id)
		}
		exists, verr := r.VolumeExists(phys)
		if verr != nil {
			return fmt.Errorf("checking volume %s: %w", phys, verr)
		}
		if exists {
			return fmt.Errorf("volume %s already exists and project %s lists it as its own (this project's id, %s, extends %s) — remove or rename it there before restoring here", phys, oid, id, oid)
		}
	}
	return nil
}
