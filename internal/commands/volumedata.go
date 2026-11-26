package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pjlsergeant/byre/internal/configui"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/runner"
)

// The two things a person can do to a volume's DATA from the editor, shared
// by the project editor and the --global one:
//
//   - delete: remove the volume. The engine will not remove a volume a
//     container still mounts, so byre asks the engine which containers those
//     are and names them, instead of refusing over every byre box on the
//     machine whether or not it uses this volume.
//   - clear: empty the volume and keep it. This is the one that works while
//     boxes are running -- the way to force a fresh shared login without
//     closing every window. The emptying runs in a one-shot helper from the
//     image of a box that already mounts the volume (so nothing is pulled,
//     and the image is one byre built). With no box mounting it, clear
//     removes it and creates it again empty: the volume still stays, which
//     is what separates clear from delete for a seeded volume (a seed fires
//     only into a volume being created, so a delete re-seeds on the next
//     launch and a clear does not).

// holderList names the containers holding a volume, for the refusal.
func holderList(hs []runner.VolumeHolder) string {
	parts := make([]string, len(hs))
	for i, h := range hs {
		name := h.Name
		if name == "" {
			name = shortID(h.ID)
		}
		state := "stopped"
		if h.Running {
			state = "running"
		}
		parts[i] = fmt.Sprintf("%s (%s)", name, state)
	}
	return strings.Join(parts, ", ")
}

// refuseMachineHelper refuses while a clear helper of a machine volume is
// still on the engine: one that outlived its byre may still be emptying it.
func refuseMachineHelper(r engineRunner, physical string) error {
	ids, err := r.ContainersByLabel(helperMachineLabel(physical))
	if err != nil {
		return fmt.Errorf("checking for leftover helper containers (%s): %w", r.Engine(), err)
	}
	if len(ids) > 0 {
		short := make([]string, len(ids))
		for i, id := range ids {
			short[i] = shortID(id)
		}
		return fmt.Errorf("a byre helper container from an earlier clear is still present on %s (%s); it may still be emptying this volume — remove it, then try again: %s rm -f %s",
			r.Engine(), strings.Join(short, ", "), r.Engine(), strings.Join(short, " "))
	}
	return nil
}

// deleteVolumeData removes physical from r, refusing by name while any
// container mounts it.
func deleteVolumeData(r engineRunner, physical string) error {
	hs, err := r.VolumeHolders(physical)
	if err != nil {
		return fmt.Errorf("checking what uses the volume (%s): %w", r.Engine(), err)
	}
	if len(hs) > 0 {
		return fmt.Errorf("in use on %s by %s — exit those boxes first, or use Clear to empty it while they run", r.Engine(), holderList(hs))
	}
	return r.VolumeRemove(physical)
}

// emptyScript deletes everything under mnt without following a symlink out
// of it (rm removes the link, not its target), then proves nothing is left.
func emptyScript(mnt string) string {
	return "find " + mnt + " -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + && [ -z \"$(ls -A " + mnt + ")\" ]"
}

// emptyVolumeData empties physical on r and keeps it. The returned note says
// how.
func emptyVolumeData(r engineRunner, physical string, labels []string) (string, error) {
	hs, err := r.VolumeHolders(physical)
	if err != nil {
		return "", fmt.Errorf("checking what uses the volume (%s): %w", r.Engine(), err)
	}
	if len(hs) == 0 {
		// Already gone (removed since the screen listed it, or an earlier
		// clear whose re-create failed) is finished by the create alone.
		exists, err := r.VolumeExists(physical)
		if err != nil {
			return "", fmt.Errorf("checking the volume on %s: %w", r.Engine(), err)
		}
		if exists {
			if err := r.VolumeRemove(physical); err != nil {
				return "", fmt.Errorf("removing the volume on %s: %w", r.Engine(), err)
			}
		}
		if err := r.VolumeCreate(physical); err != nil {
			return "", fmt.Errorf("its data is gone, but creating it again empty failed on %s, so it is absent now: the next launch creates it, seeding it again if it declares a seed: %w", r.Engine(), err)
		}
		return "cleared — it is empty now", nil
	}
	image := hs[0].ImageID
	for _, h := range hs {
		if h.Running {
			image = h.ImageID
			break
		}
	}
	runID, err := hostopen.NewRunID()
	if err != nil {
		return "", err
	}
	mnt := "/.byre-helper-" + runID
	h := runner.Helper{
		Image:     image,
		Identity:  engineIdentity(r, os.Getuid(), os.Getgid()),
		Labels:    append(append([]string{}, labels...), helperRunLabel(runID)),
		Volume:    physical,
		MountPath: mnt,
		Script:    emptyScript(mnt),
	}
	// RunHelper folds the helper's stderr into a nonzero exit's error. A
	// failed run may be the CLIENT losing the engine while the helper keeps
	// deleting, so it is taken away (bounded) before the error is reported,
	// and one that cannot be is named.
	if _, err := r.RunHelper(h, nil, io.Discard); err != nil {
		if cerr := removeRunHelpers(io.Discard, r, runID); cerr != nil {
			return "", fmt.Errorf("emptying the volume on %s: %w; its helper may still be running — find it with: %s ps -a --filter label=%s, and remove it with: %s rm -f <id>",
				r.Engine(), err, r.Engine(), helperRunLabel(runID), r.Engine())
		}
		return "", fmt.Errorf("emptying the volume on %s: %w", r.Engine(), err)
	}
	// A running agent keeps what it read in memory (a login especially) and
	// can write it back, refilling the volume for every box sharing it; a
	// shell opened before the clear also still exports a login (Claude's
	// shared token), so restarting the agent from it is not enough.
	return fmt.Sprintf("emptied in place — still mounted by %s; exit those boxes and start them again with byre develop, or an agent still running may write back what it holds (a login, say)", holderList(hs)), nil
}

// machineVolumeAdmin is the --global editor's VolumeAdmin: the volumes that
// belong to no project, which is why the global editor is where they live.
// It lists every machine volume of this user on every installed engine and
// deletes or clears them. There is no setup lock to take: none guards a
// machine volume, and the engine's own in-use refusal is what stops a delete
// racing a box that starts using it.
type machineVolumeAdmin struct {
	rs []engineRunner
}

// newMachineVolumeAdmin returns nil (no Volume data row) when no engine can
// be used, saying why on w when byre found one and declined it. dir is where
// the editor was opened: an engine binary resolved inside a tree a box can
// write is declined, as everywhere else byre runs one (ADR 0047).
func newMachineVolumeAdmin(w io.Writer, dir string) configui.VolumeAdmin {
	rs, err := lifecycleEngines(boxWritableRootsFor(dir))
	if err != nil {
		var declined declinedEngine
		if errors.As(err, &declined) {
			fmt.Fprintf(w, "byre: %v The editor's Volume data row is hidden — byre can't list or clear volumes it can't reach.\n", declined)
		}
		return nil
	}
	if len(rs) == 0 {
		return nil
	}
	return &machineVolumeAdmin{rs: rs}
}

func (a *machineVolumeAdmin) SharedNote() string { return "" }

func (a *machineVolumeAdmin) List() ([]configui.VolumeStatus, []string, error) {
	prefix := machineVolumePrefix(os.Getuid())
	var out []configui.VolumeStatus
	var notes []string
	for _, r := range a.rs {
		names, err := r.VolumesByPrefix(prefix)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s could not be queried (%s) — its copies aren't shown", r.Engine(), firstLine(err.Error())))
			continue
		}
		for _, n := range names {
			out = append(out, configui.VolumeStatus{Name: strings.TrimPrefix(n, prefix), Exists: true, Machine: true, Engine: string(r.Engine())})
		}
	}
	return out, notes, nil
}

func (a *machineVolumeAdmin) runnerFor(engine string) engineRunner {
	for _, r := range a.rs {
		if string(r.Engine()) == engine {
			return r
		}
	}
	return a.rs[0]
}

func (a *machineVolumeAdmin) Delete(v configui.VolumeStatus) error {
	if !v.Machine {
		return fmt.Errorf("%s belongs to a project — delete it from that project's editor", v.Name)
	}
	r, physical := a.runnerFor(v.Engine), machineVolumeName(os.Getuid(), v.Name)
	if err := refuseMachineHelper(r, physical); err != nil {
		return err
	}
	return deleteVolumeData(r, physical)
}

func (a *machineVolumeAdmin) Empty(v configui.VolumeStatus) (string, error) {
	if !v.Machine {
		return "", fmt.Errorf("%s belongs to a project — clear it from that project's editor", v.Name)
	}
	r, physical := a.runnerFor(v.Engine), machineVolumeName(os.Getuid(), v.Name)
	if err := refuseMachineHelper(r, physical); err != nil {
		return "", err
	}
	return emptyVolumeData(r, physical, []string{helperMachineLabel(physical)})
}
