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
// a session under, plus any engine a prior session implicated that could not
// be conclusively ruled out since. It exists to SCOPE the ADR 0004
// cross-engine single-session check to actual engine switches -- without it,
// every develop on a machine with a second engine installed but stopped
// (podman on a Mac, typically) printed the "can't be ruled out" disclosure as
// ambient noise (#4 ruling, 2026-07-22). Keyed by WorktreeID because sessions
// are per-worktree (the workdir label), while paths.Dir is shared by every
// worktree of the project.
//
// On disk: one line, space-separated -- the last engine, then zero or more
// "unresolved=<engine>" tokens (legible to a human reading the store).
func engineRecordPath(paths project.Paths) string {
	return filepath.Join(paths.Dir, "engine."+paths.WorktreeID)
}

// engineRecord is the parsed record. The zero value means "unknown" -- no
// record, or one byre couldn't read or believe -- and callers respond by
// checking every other installed engine (the safe, pre-record behavior).
type engineRecord struct {
	last       string   // engine of the last session started here
	unresolved []string // implicated engines never conclusively ruled out since
}

// loadEngineRecord reads the record, degrading to the zero value on ANY
// failure: absent, unreadable, non-regular, oversized, or any token naming no
// known engine. The store is agent-writable under --self-edit, so the read
// rides hostopen (a planted FIFO must not hang develop) and every token is
// validated rather than trusted -- a garbage record must widen the check,
// never suppress it.
func loadEngineRecord(paths project.Paths) engineRecord {
	f, fi, err := hostopen.OpenRegular(engineRecordPath(paths), false)
	if err != nil {
		return engineRecord{}
	}
	defer f.Close()
	if fi.Size() > 256 {
		return engineRecord{} // a couple of engine names; anything bigger is not ours
	}
	b, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return engineRecord{}
	}
	tokens := strings.Fields(string(b))
	if len(tokens) == 0 || !knownEngine(tokens[0]) {
		return engineRecord{}
	}
	rec := engineRecord{last: tokens[0]}
	for _, tok := range tokens[1:] {
		name, ok := strings.CutPrefix(tok, "unresolved=")
		if !ok || !knownEngine(name) {
			return engineRecord{} // unrecognized token: distrust the whole record
		}
		rec.unresolved = append(rec.unresolved, name)
	}
	return rec
}

func knownEngine(name string) bool {
	return name == string(runner.Docker) || name == string(runner.Podman)
}

// recordSessionEngine writes the record for the engine this develop is about
// to start a session under -- called only AFTER the cross-engine check passed,
// so a refusal never advances the record. unresolved carries the implicated
// engines that check could NOT conclusively clear (unreachable at the time):
// they stay in the record, so every later develop re-checks and re-discloses
// them until one finds the engine reachable and empty -- an inconclusive
// check must never launder the record into silence (codex review).
// Temp+rename: rename(2) replaces the destination's final component without
// following it, so a symlink a --self-edit agent planted at the record name
// can't redirect the write onto another host file. Failure degrades loudly,
// never blocks: the next develop simply re-checks every engine.
func recordSessionEngine(w io.Writer, paths project.Paths, eng runner.Engine, unresolved []string) {
	line := string(eng)
	for _, u := range unresolved {
		line += " unresolved=" + u
	}
	err := func() error {
		tmp, err := os.CreateTemp(paths.Dir, ".engine-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(line + "\n"); err != nil {
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
//   - clean record naming the configured engine: steady state, no switch since
//     the last session here -- nothing to check, no ambient note.
//   - record implicating other engines (the last engine after a switch, plus
//     any unresolved leftovers): check exactly those; they are the only places
//     a byre box for this worktree could live.
//   - no/invalid record (older byre, a crash before the first record,
//     tampering): check every other installed engine, the pre-record behavior.
//     tracked=false tells the caller this check carries no prior implication,
//     so engines skipped as unreachable are disclosed once but NOT carried as
//     unresolved -- otherwise a fresh project beside a stopped podman would
//     re-disclose forever, the exact noise the record exists to end.
//
// An implicated engine gone from PATH is disclosed and dropped: byre can never
// query a CLI-less daemon, so carrying it unresolved would nag forever after a
// deliberate uninstall (footgun doctrine -- the user's own host is their
// call). Residuals, disclosed (ADR 0004): a --self-edit agent can forge the
// record, which only downgrades to the pre-record behavior in a box that
// already authors its own next sandbox; and a develop run by an OLDER byre
// doesn't update the record (mixed-version staleness).
func crossEnginesToCheck(w io.Writer, others []sessionRunner, self runner.Engine, paths project.Paths) (toCheck []sessionRunner, tracked bool) {
	rec := loadEngineRecord(paths)
	if rec.last == "" {
		return others, false
	}
	implicated := map[string]bool{}
	for _, u := range rec.unresolved {
		if u != string(self) {
			implicated[u] = true
		}
	}
	if rec.last != string(self) {
		implicated[rec.last] = true
	}
	for _, rr := range others {
		if implicated[string(rr.Engine())] {
			toCheck = append(toCheck, rr)
			delete(implicated, string(rr.Engine()))
		}
	}
	for name := range implicated {
		fmt.Fprintf(w, "byre: the last session here ran under %s, which is no longer installed — a competing session there can't be ruled out.\n", name)
	}
	return toCheck, true
}
