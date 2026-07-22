package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/runner"
	"github.com/pjlsergeant/byre/internal/skills"
)

// Shell opens an interactive shell in this project's running container as the
// dev user — for `codex login`, running tests, poking around. Everything is
// derived from the RUNNING container, not re-resolved from the current config:
// the session is found in whichever installed engine holds it, and the dev
// identity comes from the container's own BYRE_UID/BYRE_GID (so it's right even
// under sudo, or if the config changed since the session started). `docker exec`
// already inherits the container's env (CLAUDE_CONFIG_DIR/CODEX_HOME/git identity
// were set as run-time -e vars), so we only add HOME, which the launcher sets at
// runtime and isn't in the container's configured env.
func Shell(s Streams, projectDir string, skipUIDCheck bool) error {
	return shell(s, projectDir, installedEngines(), os.Getuid(), skipUIDCheck)
}

// installedEngines returns a sessionRunner per installed engine, in shell's
// probe order (docker, then podman). Engines not on PATH are skipped.
func installedEngines() []sessionRunner {
	var out []sessionRunner
	for _, e := range []string{"docker", "podman"} {
		eng, err := runner.Detect(e, nil)
		if err != nil {
			continue // engine not installed
		}
		out = append(out, runner.New(eng))
	}
	return out
}

// installedEnginesExcept is installedEngines minus the given engine — the OTHER
// engines develop must check for a competing session after an engine switch.
func installedEnginesExcept(self runner.Engine) []sessionRunner {
	var out []sessionRunner
	for _, rr := range installedEngines() {
		if rr.Engine() != self {
			out = append(out, rr)
		}
	}
	return out
}

func shell(s Streams, projectDir string, engines []sessionRunner, callerUID int, skipUIDCheck bool) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Same loud id-collision check as the read-only views, with higher stakes:
	// the session lookup below keys on the id label, so on a collision shell
	// would exec into ANOTHER project's container — refuse instead.
	if err := paths.ValidateExisting(); err != nil {
		return err
	}
	// Query the worktree label so `byre shell` opens THIS worktree's session, not
	// a sibling worktree's (both carry the project label).
	label := workdirLabel(paths)
	// Find the session in whichever installed engine holds it — a session may run
	// under podman even when docker is also installed. On a shared ROOTFUL daemon
	// a box owned by ANOTHER Unix user carries their BYRE_UID; entering it would
	// hand this caller that user's mounted project state and agent credentials.
	// So mirror the deliver/grab accident guard: skip a container whose BYRE_UID
	// differs from the caller's (unless --skip-uid-check), and KEEP scanning — a
	// foreign box on one engine must not shadow this caller's own box on another.
	// Rootless podman is caller-scoped: everything it sees is the caller's, and a
	// keep-id box's BYRE_UID is the generic in-container uid, so the filter must
	// not run there.
	var (
		r           sessionRunner
		targetID    string
		cenv        map[string]string
		chosen      []string
		queryErr    error
		hidden      int
		unreadable  int
		modeUnknown error // the rootless probe failed on an engine that then hid a box
	)
	for _, rr := range engines {
		got, qerr := rr.RunningContainersByLabel(label)
		if qerr != nil {
			queryErr = qerr // remember; another engine may still hold the session
			continue
		}
		if len(got) == 0 {
			continue
		}
		// A probe FAILURE (rerr != nil) leaves callerScoped false, so the filter
		// still runs -- fail-closed, matching deliver's wiring: entering a
		// possibly-foreign box is the worse accident. But on rootless podman the
		// filter can only ever false-hide the caller's own keep-id box (generic
		// in-container uid), so a hide under an UNDETERMINED mode must say so
		// below instead of asserting "another user's identity".
		callerScoped := false
		rootless, rerr := rr.IsRootlessPodman()
		if rerr == nil {
			callerScoped = rootless
		}
		for _, id := range got {
			env, eerr := rr.ContainerEnv(id)
			if eerr != nil {
				queryErr = eerr
				continue
			}
			uid, uerr := strconv.Atoi(strings.TrimSpace(env["BYRE_UID"]))
			gid, gerr := strconv.Atoi(strings.TrimSpace(env["BYRE_GID"]))
			if uerr != nil || gerr != nil || uid < 0 || gid < 0 {
				unreadable++ // a box whose dev identity we can't read -- can't safely enter it, and it must not shadow a valid candidate on another engine
				continue
			}
			if !callerScoped && !skipUIDCheck && uid != callerUID {
				hidden++
				if rerr != nil {
					modeUnknown = rerr
				}
				continue // foreign box — skip and keep scanning
			}
			r, targetID, cenv, chosen = rr, id, env, got
			break
		}
		if r != nil {
			break
		}
	}
	if r == nil {
		if hidden > 0 {
			if modeUnknown != nil {
				return fmt.Errorf("a session for this project is hidden by the identity check (%d), but whether the engine is rootless podman couldn't be determined (%v) — on rootless podman the check doesn't apply and the session is likely yours; pass --skip-uid-check to enter it", hidden, modeUnknown)
			}
			return fmt.Errorf("a session for this project is running under another user's identity (%d hidden); it isn't yours to enter — pass --skip-uid-check to enter it anyway", hidden)
		}
		if unreadable > 0 {
			return fmt.Errorf("a session is running for this project but its dev identity (BYRE_UID/BYRE_GID) couldn't be read — refusing to enter it")
		}
		// Don't mask a broken engine as "nothing running".
		if queryErr != nil {
			return fmt.Errorf("no running session found for this project (an engine query failed: %w)", queryErr)
		}
		return fmt.Errorf("no session is running for this project; start one with 'byre develop'")
	}
	if len(chosen) > 1 {
		fmt.Fprintf(s.Err, "byre: %d containers match this project; using %s\n", len(chosen), shortID(targetID))
	}
	// Fail closed: the dev identity must come from the container. Don't fall back
	// to the caller's uid, which could be 0:0 under sudo.
	uid, uerr := strconv.Atoi(strings.TrimSpace(cenv["BYRE_UID"]))
	gid, gerr := strconv.Atoi(strings.TrimSpace(cenv["BYRE_GID"]))
	if uerr != nil || gerr != nil || uid < 0 || gid < 0 {
		return fmt.Errorf("could not determine a valid dev user (BYRE_UID/BYRE_GID) from container %s", shortID(targetID))
	}
	// Pass the container's BYRE_* plumbing through so the /etc/profile.d shim's
	// env.d hooks have their inputs (e.g. docker-host's COMPOSE_PROJECT_NAME
	// reads BYRE_WORKTREE) -- otherwise a `byre shell` session's environment
	// diverges from the agent's. `bash -l` then sources profile.d -> env.d.
	env := map[string]string{"HOME": skills.DevHome}
	for k, v := range cenv {
		if strings.HasPrefix(k, "BYRE_") {
			env[k] = v
		}
	}
	return r.Exec(targetID, uid, gid, "/workspace", env, s.TTY, "bash", "-l")
}
