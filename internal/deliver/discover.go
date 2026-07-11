package deliver

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// pool is the discovered session set plus what discovery couldn't see:
// hidden counts sessions the uid filter excluded (revealable with
// --skip-uid-check), unusable counts sessions whose identity couldn't be
// read at all (no flag reveals those — attach would fail closed anyway),
// and partial notes that at least one engine's query failed (so "exactly
// one" is unknowable).
type pool struct {
	sessions []Session
	hidden   int
	unusable int
	partial  bool
}

// discover enumerates running byre boxes across every configured engine —
// the union IS the machine's session pool; each entry keeps engine affinity.
// The uid filter is an accident guard, not confinement (BYRE_UID is runtime
// env the box's own author can override): with SkipUIDCheck the foreign boxes
// are included and marked, never silently equal.
func discover(cfg Config, opts Options) (pool, error) {
	var p pool
	for _, eng := range cfg.Engines {
		ids, err := eng.Sessions(cfg.ProjectLabel)
		if err != nil {
			// Degrade loudly, never mask a broken engine as "nothing running".
			fmt.Fprintf(cfg.Err, "byre: warning: %s query failed (%v); its sessions are invisible this run\n", eng.Name(), err)
			p.partial = true
			continue
		}
		for _, id := range ids {
			s, verdict := inspect(cfg, opts, eng, id)
			switch verdict {
			case sessionOK:
				p.sessions = append(p.sessions, s)
			case sessionForeign:
				p.hidden++
			case sessionUnusable:
				p.unusable++
			}
		}
	}
	// Deterministic order for listings and tests: engine, then project, then id.
	sort.Slice(p.sessions, func(i, j int) bool {
		a, b := p.sessions[i], p.sessions[j]
		if a.EngineName != b.EngineName {
			return a.EngineName < b.EngineName
		}
		if a.ProjectID != b.ProjectID {
			return a.ProjectID < b.ProjectID
		}
		return a.ID < b.ID
	})
	return p, nil
}

// sessionVerdict is inspect's outcome: usable, foreign (revealable with
// --skip-uid-check), or unusable (identity unreadable — nothing reveals it,
// since attach fails closed without BYRE_UID/BYRE_GID).
type sessionVerdict int

const (
	sessionOK sessionVerdict = iota
	sessionForeign
	sessionUnusable
)

// inspect reads one container's identity.
func inspect(cfg Config, opts Options, eng Engine, id string) (Session, sessionVerdict) {
	s := Session{Engine: eng, EngineName: eng.Name(), ID: id}
	labels, err := eng.Labels(id)
	if err == nil {
		s.ProjectID = labels[cfg.ProjectLabel]
		s.WorkdirID = labels[cfg.WorkdirLabel]
	} else {
		fmt.Fprintf(cfg.Err, "byre: warning: could not read labels of %s (%v)\n", shortID(id), err)
		s.ProjectID = "(unknown)"
	}
	env, err := eng.Env(id)
	if err != nil {
		fmt.Fprintf(cfg.Err, "byre: warning: could not read the identity of %s (%v); it cannot be a target\n", shortID(id), err)
		return s, sessionUnusable
	}
	uid, uerr := strconv.Atoi(strings.TrimSpace(env["BYRE_UID"]))
	gid, gerr := strconv.Atoi(strings.TrimSpace(env["BYRE_GID"]))
	if uerr != nil || gerr != nil || uid < 0 || gid < 0 {
		// Not a box byre can attach to (shell.go's fail-closed rule).
		fmt.Fprintf(cfg.Err, "byre: warning: %s carries no valid BYRE_UID/BYRE_GID; it cannot be a target\n", shortID(id))
		return s, sessionUnusable
	}
	s.UID, s.GID = uid, gid
	s.Foreign = uid != cfg.CallerUID
	if s.Foreign && !opts.SkipUIDCheck {
		return s, sessionForeign
	}
	return s, sessionOK
}

// selectSession runs the target cascade: --box, cwd ancestor walk, sole
// session, else an ambiguity error listing the candidates (the picker's
// no-TTY/no-GUI degradation; interactive pickers slot in above it).
func selectSession(cfg Config, opts Options) (Session, error) {
	p, err := discover(cfg, opts)
	if err != nil {
		return Session{}, err
	}

	// Step 0: explicit --box. Operates even on a partial pool; a prefix must
	// match uniquely (a project prefix legitimately matches several worktree
	// sessions — one container per workdir).
	if opts.Box != "" {
		var matches []Session
		for _, s := range p.sessions {
			if strings.HasPrefix(s.ProjectID, opts.Box) ||
				strings.HasPrefix(s.WorkdirID, opts.Box) ||
				strings.HasPrefix(s.ID, opts.Box) {
				matches = append(matches, s)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			return Session{}, fmt.Errorf("no running box matches --box %q%s%s", opts.Box, hiddenHint(p), unusableNote(p))
		default:
			return Session{}, fmt.Errorf("--box %q is ambiguous:\n%s", opts.Box, sessionList(matches))
		}
	}

	if len(p.sessions) == 0 {
		if p.hidden > 0 {
			return Session{}, fmt.Errorf("no running boxes owned by you (%d hidden; --skip-uid-check to include them)%s", p.hidden, unusableNote(p))
		}
		if p.unusable > 0 {
			return Session{}, fmt.Errorf("no deliverable byre boxes (%d running without a readable dev identity — see the warnings above)", p.unusable)
		}
		if p.partial {
			return Session{}, fmt.Errorf("no running byre boxes found (and an engine query failed, so some may be invisible)")
		}
		return Session{}, fmt.Errorf("no running byre boxes; start one with 'byre develop'")
	}

	// Step 1: cwd match, walking ancestors — `byre deliver` from a
	// subdirectory of the session's workdir is the common case, and the
	// workdir id is derived from the literal directory, so each level gets
	// its own id computed and compared.
	for dir := cfg.Cwd; cfg.WorkdirIDOf != nil && dir != ""; {
		if id, err := cfg.WorkdirIDOf(dir); err == nil {
			for _, s := range p.sessions {
				if s.WorkdirID != "" && s.WorkdirID == id {
					return s, nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Step 2: sole session on the machine — but only when the pool is
	// complete; with an engine unqueried, "exactly one" is a guess.
	if len(p.sessions) == 1 {
		if p.partial {
			return Session{}, fmt.Errorf("one box is visible but an engine query failed — pick explicitly with --box %s", pickArg(p.sessions[0]))
		}
		return p.sessions[0], nil
	}

	// Step 3: ambiguous — the picker's moment. The listing error is the
	// script/no-picker degradation and the always-available floor.
	if cfg.Pick != nil {
		s, ok, err := cfg.Pick(p.sessions)
		if err != nil {
			return Session{}, err
		}
		if !ok {
			return Session{}, errCancelled
		}
		return s, nil
	}
	return Session{}, fmt.Errorf("%d boxes are running — pick one with --box:\n%s", len(p.sessions), sessionList(p.sessions))
}

func hiddenHint(p pool) string {
	if p.hidden > 0 {
		return fmt.Sprintf(" (%d sessions hidden by the uid filter; --skip-uid-check to include them)", p.hidden)
	}
	return ""
}

func unusableNote(p pool) string {
	if p.unusable > 0 {
		return fmt.Sprintf("; %d more lack a readable dev identity", p.unusable)
	}
	return ""
}

// sessionList renders candidates for ambiguity errors (and, later, pickers).
func sessionList(sessions []Session) string {
	var b strings.Builder
	for _, s := range sessions {
		fmt.Fprintf(&b, "  %s  %s (%s)%s\n", pickArg(s), s.ProjectID, s.EngineName, foreignNote(s))
	}
	return strings.TrimRight(b.String(), "\n")
}

// pickArg is the string a user would pass to --box for this session: the
// workdir id when it differs from the project id (worktree sessions), else
// the project id.
func pickArg(s Session) string {
	if s.WorkdirID != "" && s.WorkdirID != s.ProjectID {
		return s.WorkdirID
	}
	if s.ProjectID != "" && s.ProjectID != "(unknown)" {
		return s.ProjectID
	}
	return shortID(s.ID)
}
