package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// The per-worktree engine record: which engine the last develop here started
// (or was cleanly about to start) a session under. It exists to SCOPE the
// ADR 0004 cross-engine single-session check to actual engine switches --
// without it, every develop on a machine with a second engine installed but
// stopped (podman on a Mac, typically) printed the "can't be ruled out"
// disclosure as ambient noise (#4 ruling, 2026-07-22). Keyed by WorktreeID
// because sessions are per-worktree (the workdir label), while paths.Dir is
// shared by every worktree of the project.
func engineRecordPath(paths project.Paths) string {
	return filepath.Join(paths.Dir, "engine."+paths.WorktreeID)
}

// lastSessionEngine reads the record, degrading to "" (= unknown, so the
// caller checks every other engine -- the safe direction) on ANY failure:
// absent, unreadable, non-regular, oversized, or a value naming no known
// engine. The store is agent-writable under --self-edit, so the read rides
// hostopen (a planted FIFO must not hang develop) and the value is validated
// against the known engines rather than trusted -- a garbage record must
// widen the check, never suppress it.
func lastSessionEngine(paths project.Paths) string {
	f, fi, err := hostopen.OpenRegular(engineRecordPath(paths), false)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi.Size() > 64 {
		return "" // an engine name is a short word; anything bigger is not ours
	}
	b, err := io.ReadAll(io.LimitReader(f, 64))
	if err != nil {
		return ""
	}
	switch name := strings.TrimSpace(string(b)); name {
	case string(runner.Docker), string(runner.Podman):
		return name
	default:
		return ""
	}
}

// recordSessionEngine writes the record for the engine this develop is about
// to start a session under -- called only AFTER sole-session is established,
// so a refusal never advances the record. Temp+rename: rename(2) replaces the
// destination's final component without following it, so a symlink a
// --self-edit agent planted at the record name can't redirect the write onto
// another host file. Failure degrades loudly, never blocks: the next develop
// simply re-checks every engine.
func recordSessionEngine(w io.Writer, paths project.Paths, eng runner.Engine) {
	err := func() error {
		tmp, err := os.CreateTemp(paths.Dir, ".engine-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(string(eng) + "\n"); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), engineRecordPath(paths))
	}()
	if err != nil {
		fmt.Fprintf(w, "byre: couldn't record the session engine (%v) — the next develop will re-check every installed engine\n", err)
	}
}

// crossEnginesToCheck scopes the ADR 0004 cross-engine check by the record:
//
//   - record == the configured engine: steady state, no switch since the last
//     session here -- nothing to check, and no ambient unreachable-engine note.
//   - record names the OTHER engine: a real switch -- check that engine alone;
//     it is the only place a byre box for this worktree could live.
//   - no/invalid record (older byre, a crash before recording, tampering):
//     check every other installed engine, the pre-record behavior.
//
// Residuals, disclosed (ADR 0004): a --self-edit agent can forge the record
// to suppress the check -- in a box that already authors its own next
// sandbox, that only downgrades to the pre-check behavior. And the record
// trusts that every session-starter updates it, so a develop run by an OLDER
// byre alongside this one can leave it stale (mixed-version residual).
func crossEnginesToCheck(w io.Writer, others []sessionRunner, self runner.Engine, paths project.Paths) []sessionRunner {
	last := lastSessionEngine(paths)
	switch last {
	case "":
		return others
	case string(self):
		return nil
	}
	var scoped []sessionRunner
	for _, rr := range others {
		if string(rr.Engine()) == last {
			scoped = append(scoped, rr)
		}
	}
	if len(scoped) == 0 {
		// The recorded engine is gone from PATH. Its daemon can't be queried, so
		// sole-session against it can't be established -- same disclosure shape
		// as an unreachable engine (skip-and-disclose, never block).
		fmt.Fprintf(w, "byre: the last session here ran under %s, which is no longer installed — a competing session there can't be ruled out.\n", last)
	}
	return scoped
}
