package commands

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// gitProbeMaxOutput bounds a probe's stdout: generous for any legitimate
// answer (a ref listing on a huge repo is hundreds of KB), fatal to a
// hostile repo minting output faster than a timeout alone would stop.
const gitProbeMaxOutput = 1 << 20

// gitProbe runs a read-only git query against agent-shaped state (the
// project tree) under the standing bounds — CLAUDE.md's rule: a passive
// probe of what the agent can shape must degrade, never wedge (5s wall
// clock) and never balloon (stdout cap; Output() would buffer a hostile
// repo's unbounded emission into host memory). Mutating git commands
// (worktree add) are deliberately NOT probes: they stream to the user,
// take legitimate time, and ctrl-C is theirs.
func gitProbe(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
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
