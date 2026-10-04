package commands

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/pjlsergeant/byre/internal/deliver"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
)

// sessionLocation is where this worktree's running session is, as far as byre
// can tell.
type sessionLocation struct {
	r   sessionRunner // the engine holding the session; nil when none was found
	ids []string      // the caller's running boxes for this worktree on r
	// answered are the engines whose own-session query succeeded, in shell's
	// order: the ones a project-wide sibling query can be asked.
	answered []engineAnswer
	// failed are the engines that could not be asked, kept even when the
	// session was found elsewhere: a box on one engine says nothing about
	// what an unasked engine holds for the rest of the project.
	failed []engineFailure
}

// engineAnswer is one engine's own-session answer. ids is every box carrying
// this worktree's label there, the caller's or not: none of them is a
// sibling, so the sibling query subtracts them all.
type engineAnswer struct {
	r   sessionRunner
	ids []string
}

// engineFailure is one engine byre could not ask.
type engineFailure struct {
	engine     string
	err        error
	configured bool // the configured engine, which the Engine row already names
	// identity marks a failure to settle a candidate box rather than to query
	// the engine: the session is unknown, but the engine still answers a
	// project-wide query.
	identity bool
}

// String is the failure as a row fragment, naming the engine unless it is
// the configured one: the reader would otherwise pin the error on the engine
// the Engine row shows.
func (f engineFailure) String() string {
	if f.configured {
		return firstLine(f.err.Error())
	}
	return f.engine + ": " + firstLine(f.err.Error())
}

// locateSession finds this worktree's running session on whichever engine
// holds it. A box keeps running on the engine it was launched under after
// `engine` is flipped in config, and status must always be able to find the
// session (ADR 0004), so the pool is shell's: every installed engine in
// shell's order, plus the engines byre declined to run. Ownership is
// deliver.JudgeBox, the predicate shell and deliver use, so the session
// described is the one `byre shell` would enter; when two engines both hold
// it, the first is described and the other is named on w.
//
// The pool is not scoped by the engine record the way develop's cross-engine
// check is: that check asks "may a second session start", this asks "where
// is the session", and a missing, stale or forged record must not make a
// running box invisible.
//
// An engine that cannot be asked goes in failed, so the caller reports
// unknown rather than "not running" -- except an UNREACHABLE engine, other
// than the configured one, that the engine record does not name (as its last
// engine or an unresolved one). That one is skipped silently, per ADR 0004's
// ambient-noise ruling: an installed-but-stopped podman beside docker is the
// common Mac case, and a down daemon holds no box short of the live-restore
// residual ADR 0004 accepts. A missing or invalid record names nothing.
//
// Disclosed on w: a declined engine other than the configured one; an
// unreachable engine that is the configured one or that the record names; an
// engine the record names that is neither installed nor declined; and, when
// no session is found, the boxes passed over. Failed without a note, because
// the caller's rows carry it: any other query failure, and the configured
// engine's own refusal.
//
// self is the configured engine's name, resolved or not; others never holds
// it. callerUID is the uid ownership is judged against.
func locateSession(w io.Writer, configured sessionRunner, self runner.Engine, others []sessionRunner, declined []declinedEngine, paths project.Paths, callerUID int) sessionLocation {
	var loc sessionLocation
	fail := func(engine string, err error, identity bool) {
		loc.failed = append(loc.failed, engineFailure{engine: engine, err: err, configured: engine == string(self), identity: identity})
	}
	known := map[string]bool{}
	var declinedOthers []declinedEngine
	for _, d := range declined {
		known[d.Engine] = true
		if d.Engine != string(self) {
			declinedOthers = append(declinedOthers, d)
		}
		fail(d.Engine, d, false)
	}
	noteDeclinedEngines(w, declinedOthers, "byre can't look there for this project's session.")
	var engines []sessionRunner
	if configured != nil {
		engines = append(engines, configured)
	}
	engines = append(engines, others...)
	slices.SortStableFunc(engines, func(a, b sessionRunner) int {
		return engineOrder(a.Engine()) - engineOrder(b.Engine())
	})
	for _, rr := range engines {
		known[string(rr.Engine())] = true
	}
	implicated := map[string]bool{}
	if rec := loadEngineRecord(paths); rec.last != "" {
		implicated[rec.last] = true
		for _, u := range rec.unresolved {
			implicated[u] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(implicated)) {
		if !known[name] {
			dataf(w, "byre: %s is no longer installed; a session there can't be ruled out.\n", name)
			fail(name, errors.New("not installed, but this worktree's engine record names it"), false)
		}
	}
	label := workdirLabel(paths)
	hidden, unreadable := 0, 0
	var uncertain []uncertainHide
	for _, rr := range engines {
		name := string(rr.Engine())
		ids, err := rr.RunningContainersByLabel(label)
		if err != nil {
			if deliver.IsUnreachable(err) {
				if name != string(self) && !implicated[name] {
					continue
				}
				dataf(w, "byre: %s isn't reachable, so a session there can't be ruled out (start %s to check).\n", name, name)
			}
			fail(name, err, false)
			continue
		}
		loc.answered = append(loc.answered, engineAnswer{r: rr, ids: ids})
		j := callersBoxes(rr, ids, callerUID)
		hidden += j.hidden
		unreadable += j.unreadable
		if j.uncertain > 0 {
			uncertain = append(uncertain, uncertainHide{engine: name, n: j.uncertain, err: j.modeErr})
		}
		if len(j.mine) == 0 {
			// A candidate byre could not settle may be the session, so this
			// engine has not answered "not here": an env read that failed, a
			// box hidden only because the rootless probe failed (on rootless
			// podman the uid check does not apply, so it is likely the
			// caller's), or one with no readable identity (shell refuses to
			// enter it). A box hidden with the probe answering is confirmed
			// foreign and settles nothing.
			switch {
			case j.envErr != nil:
				fail(name, j.envErr, true)
			case j.uncertain > 0:
				fail(name, fmt.Errorf("a box here is hidden by the identity check, but whether this engine is rootless podman couldn't be determined (%v)", j.modeErr), true)
			case j.unreadable > 0:
				fail(name, errors.New("a box here carries no readable dev identity (BYRE_UID/BYRE_GID)"), true)
			}
			continue
		}
		if loc.r == nil {
			loc.r, loc.ids = rr, j.mine
			continue
		}
		// One worktree's session on two engines is the single-session breach
		// ADR 0004 guards develop against: named, not dropped.
		dataf(w, "byre: this worktree's session is running under both %s (%s) and %s (%s); status and byre shell describe the %s one.\n",
			loc.r.Engine(), shortID(loc.ids[0]), name, shortID(j.mine[0]), loc.r.Engine())
	}
	if loc.r == nil {
		if hidden > 0 {
			dataf(w, "byre: %d box(es) for this worktree run under another user's identity (BYRE_UID); they are not this session.\n", hidden)
		}
		for _, u := range uncertain {
			dataf(w, "byre: %d box(es) for this worktree on %s are hidden by the identity check, but whether %s is rootless podman couldn't be determined (%v); on rootless podman the check doesn't apply and the session is likely yours.\n", u.n, u.engine, u.engine, u.err)
		}
		if unreadable > 0 {
			dataf(w, "byre: %d box(es) for this worktree carry no readable dev identity (BYRE_UID/BYRE_GID); they are not described here.\n", unreadable)
		}
	}
	return loc
}

// engineOrder is shell's probe order (installedEngines): docker, then podman.
func engineOrder(e runner.Engine) int {
	if e == runner.Docker {
		return 0
	}
	return 1
}

// boxJudgement is one engine's candidates sorted by deliver.JudgeBox.
type boxJudgement struct {
	mine       []string
	envErr     error // the first env read that failed
	hidden     int   // confirmed foreign: the rootless probe answered
	uncertain  int   // hidden while the rootless probe failed; likely the caller's
	modeErr    error // the probe's failure, when uncertain > 0
	unreadable int
}

// uncertainHide is one engine's uncertain count, for the stderr note.
type uncertainHide struct {
	engine string
	n      int
	err    error
}

// callersBoxes keeps the candidates that are the caller's by deliver.JudgeBox.
// The rootless probe runs only when there is a candidate, and a probe failure
// leaves the engine treated as shared -- fail-closed, as shell does -- with
// what that hid counted as uncertain.
func callersBoxes(rr sessionRunner, ids []string, callerUID int) boxJudgement {
	var j boxJudgement
	if len(ids) == 0 {
		return j
	}
	rootless, rerr := rr.IsRootlessPodman()
	callerScoped := rerr == nil && rootless
	for _, id := range ids {
		env, err := rr.ContainerEnv(id)
		if err != nil {
			if j.envErr == nil {
				j.envErr = err
			}
			continue
		}
		ident := deliver.JudgeBox(env, callerScoped, callerUID)
		switch {
		case !ident.Valid:
			j.unreadable++
		case ident.Foreign && rerr != nil:
			j.uncertain++
			j.modeErr = rerr
		case ident.Foreign:
			j.hidden++
		default:
			j.mine = append(j.mine, id)
		}
	}
	return j
}
