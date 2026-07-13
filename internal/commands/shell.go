package commands

import (
	"fmt"
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
func Shell(s Streams, projectDir string) error {
	return shell(s, projectDir, installedEngines())
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

func shell(s Streams, projectDir string, engines []sessionRunner) error {
	paths, err := project.Resolve(projectDir)
	if err != nil {
		return err
	}
	// Query the worktree label so `byre shell` opens THIS worktree's session, not
	// a sibling worktree's (both carry the project label).
	label := workdirLabel(paths)
	// Find the session in whichever installed engine actually holds it — a session
	// may run under podman even when docker is also installed. Skip engines that
	// can't be queried; the first with a match wins.
	var r sessionRunner
	var ids []string
	var queryErr error
	for _, rr := range engines {
		got, qerr := rr.RunningContainersByLabel(label)
		if qerr != nil {
			queryErr = qerr // remember; another engine may still hold the session
			continue
		}
		if len(got) > 0 {
			r, ids = rr, got
			break
		}
	}
	if r == nil {
		// Don't mask a broken engine as "nothing running".
		if queryErr != nil {
			return fmt.Errorf("no running session found for this project (an engine query failed: %w)", queryErr)
		}
		return fmt.Errorf("no session is running for this project; start one with 'byre develop'")
	}
	if len(ids) > 1 {
		fmt.Fprintf(s.Err, "byre: %d containers match this project; using %s\n", len(ids), shortID(ids[0]))
	}
	cenv, err := r.ContainerEnv(ids[0])
	if err != nil {
		return fmt.Errorf("reading session environment: %w", err)
	}
	// Fail closed: the dev identity must come from the container. Don't fall back
	// to the caller's uid, which could be 0:0 under sudo.
	uid, uerr := strconv.Atoi(strings.TrimSpace(cenv["BYRE_UID"]))
	gid, gerr := strconv.Atoi(strings.TrimSpace(cenv["BYRE_GID"]))
	if uerr != nil || gerr != nil || uid < 0 || gid < 0 {
		return fmt.Errorf("could not determine a valid dev user (BYRE_UID/BYRE_GID) from container %s", shortID(ids[0]))
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
	return r.Exec(ids[0], uid, gid, "/workspace", env, s.TTY, "bash", "-l")
}
