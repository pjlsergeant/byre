package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/hostexec"
)

// gitProbeMaxOutput bounds a probe's stdout: generous for any legitimate
// answer (a ref listing on a huge repo is hundreds of KB), fatal to a
// hostile repo minting output faster than a timeout alone would stop.
const gitProbeMaxOutput = 1 << 20

const gitProbeTimeout = 5 * time.Second

// gitProbeWaitDelay is how long a killed probe's output pipes may stay open
// before the wait gives up on them. It is not clipWaitDelay: the probe's
// whole budget is 5s and a wedged descendant must not turn it into 10s.
const gitProbeWaitDelay = time.Second

// errNoHostGit is what a probe returns when byre has no host git to run. The
// callers that care are the ones that report a repo fact to the user
// (worktree registration); the unsolicited ones treat it like any other probe
// refusal and degrade.
var errNoHostGit = errors.New("no host git available for this project")

// hostGit resolves the git byre runs on the host for this project, pinned for
// the invocation. It returns "" with the reason when there is no git to run:
// not installed, or resolved out of a directory this project's box can write
// (hostexec declines that one, and says so).
//
// Every git byre runs here is a passive probe of agent-shaped state, so ""
// degrades each caller exactly the way an absent git already does. Only
// develop says anything about it, once, because its session-end probes fire
// with nobody watching for a command to fail.
func hostGit(roots hostexec.Roots) (string, error) {
	p, err := hostexec.Look("git", roots)
	if err != nil {
		return "", err
	}
	return p, nil
}

// hostGitForSession is hostGit for a session: the git to run, plus the one
// line to print when byre DECLINED the git PATH offered.
//
// The session-end report (ADR 0047) runs host git automatically, at every
// exit, with nobody watching for a command to fail. So a declined git degrades
// rather than refusing -- a session end must never be blockable by the thing
// it reports on, which is this ADR's own no-gates rule turned on itself. The
// line is returned rather than printed so the caller can put it BEFORE the
// session: a disclosure in the middle of an exit report is one nobody reads.
//
// Absence returns ("", "") and stays silent, as it already did: a host with no
// git is not a disclosure, it is a host with no git.
func hostGitForSession(roots hostexec.Roots) (exe, disclosure string) {
	p, err := hostGit(roots)
	if err == nil {
		return p, ""
	}
	var shadow *hostexec.ShadowError
	if errors.As(err, &shadow) {
		return "", fmt.Sprintf("byre: %v The session-end report's git probes and any `git:` env sources are skipped for this session.", shadow)
	}
	return "", ""
}

// gitProbe runs a read-only git query against agent-shaped state (the
// project tree) under the standing bounds — CLAUDE.md's rule: a passive
// probe of what the agent can shape must degrade, never wedge (5s wall
// clock) and never balloon (stdout cap; Output() would buffer a hostile
// repo's unbounded emission into host memory). Mutating git commands
// (worktree add) are deliberately NOT probes: they stream to the user,
// take legitimate time, and ctrl-C is theirs.
//
// exe is the git resolved by hostGit — an absolute path pinned for the
// invocation, never a bare name PATH would answer afresh on each probe. An
// empty exe means there is no git byre will run (absent, or resolved out of a
// directory the box writes) and every probe fails immediately: that is the
// same shape callers already handle for a host with no git at all.
func gitProbe(exe string, args ...string) ([]byte, error) {
	return gitProbeBounded(gitProbeTimeout, exe, args...)
}

// gitProbeBounded is gitProbe with the wall-clock bound as a parameter, so a
// test can prove the group-kill deadline fires without waiting 5s.
func gitProbeBounded(timeout time.Duration, exe string, args ...string) ([]byte, error) {
	if exe == "" {
		return nil, errNoHostGit
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := processGroupCmd(ctx, gitProbeWaitDelay, exe, args...)
	// Discard stderr: a probe of an agent-shaped repo must not let git spray
	// the user's terminal (a hostile repo could emit for the full 5s window).
	cmd.Stderr = io.Discard
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := boundPipe(ctx, gitProbeWaitDelay, pipe)
	defer stop()
	out, rerr := io.ReadAll(io.LimitReader(pipe, gitProbeMaxOutput+1))
	if len(out) > gitProbeMaxOutput || rerr != nil {
		cancel() // kill the writer; a capped or broken read never waits it out
		_ = cmd.Wait()
		if rerr != nil {
			return nil, rerr
		}
		return nil, fmt.Errorf("git %s: output exceeds %d bytes", strings.Join(args, " "), gitProbeMaxOutput)
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}
