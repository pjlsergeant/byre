package commands

import (
	"io"

	"github.com/pjlsergeant/byre/internal/project"
)

// headlessOptions is how `byre run` drives develop's session without a human
// on the other end. nil is `byre develop` exactly: every field below is read
// only through a non-nil *headlessOptions.
type headlessOptions struct {
	// Command is the container command, placed last on the create argv
	// (RunParams.Command); `byre run` passes the baked headless chassis.
	Command []string
	// Env is the BYRE_RUN_* set, applied AFTER the skill runtime env, the
	// env_from_host passthrough and BYRE_EGRESS, so no skill's [runtime].env
	// can skew what the run was told. Raw run_args still follow it on the argv
	// and win (ADR 0006): byre does not parse that block.
	Env map[string]string
	// Stage runs under the setup lock immediately after Create succeeds and
	// before the lock is released -- the created container is the session's
	// ownership token (ADR 0004), so nothing else can reach the workspace
	// staging step for this worktree while Stage runs. rv is the under-lock
	// re-read, the resolution the created box was built from. A Stage error
	// removes the just-created container and comes back from develop as-is:
	// develop prints nothing for it, so the callback reports its own refusal.
	Stage func(paths project.Paths, rv resolved) error
	// AttachStdout, when set, is where the box's stdout goes, through
	// StartAttachIO with NO stdin (the box sees EOF at once). Unset, the
	// attach hands the box byre's own stdin/stdout as develop does.
	AttachStdout io.Writer
	// RunID, TaskSHA256 and RunTimeout are written into the launch record so
	// status can say what this box was launched to do -- the task's hash,
	// never its text. RunTimeout is "" when the run has none.
	RunID      string
	TaskSHA256 string
	RunTimeout string
	// Quiet drops develop's banner-class notices -- the worktree and preset
	// notes, the run-scoped agent line, the exposure/network summary. It
	// never drops an error, a refusal, a warning, or the exit report: those
	// are what a quiet run's reader still needs.
	Quiet bool
	// StreamStdout is where the engine calls develop streams before the
	// attach (the build, the volume seeds) send their stdout. Unset, they
	// write to byre's own stdout as develop always has; a headless run's
	// stdout is the answer and nothing else, so it routes them to stderr.
	StreamStdout io.Writer
	// Result, when set, is filled as develop goes (see developResult).
	Result *developResult
}

// launchPhase is how far a session got before develop returned. The order is
// meaningful: a caller asks "did the box exist" as Phase >= phaseCreated.
type launchPhase int

const (
	// phasePrelaunch: no container of this launch exists -- develop failed or
	// refused before Create succeeded, or a Stage failure removed it again.
	phasePrelaunch launchPhase = iota
	// phaseCreated: Create succeeded; the attach has not returned.
	phaseCreated
	// phaseAttached: StartAttach/StartAttachIO returned.
	phaseAttached
)

func (p launchPhase) String() string {
	switch p {
	case phaseCreated:
		return "created"
	case phaseAttached:
		return "attached"
	default:
		return "prelaunch"
	}
}

// developResult is develop's own account of the launch, typed, so a caller
// classifies the outcome without scraping stderr or scanning launch records.
type developResult struct {
	Phase launchPhase
	// Live is develop's liveness answer after a failed attach: the box was
	// still running when develop looked (develop then refuses with
	// ExitRefused). False when the attach succeeded or the query failed.
	Live bool
	// LaunchHash is the launch record's address ("" when the record could not
	// be written; the box launches regardless).
	LaunchHash string
	// ContainerStatus is the box's raw exit status as the engine reported it,
	// captured before decodeAgentExit turns 128+ into prose: 0 for a clean
	// attach, -1 when unknown (no attach yet, a signal-terminated client, or a
	// failure that was not an exit status at all).
	ContainerStatus int
	// ExitReport is the exit report's lines exactly as develop printed them,
	// nil when it printed none.
	ExitReport []string
	// RawRunArgs is whether the resolved run_args (skill and project, as the
	// engine got them) were non-empty: the box may then have been handed
	// anything, and byre does not parse the block (ADR 0006).
	RawRunArgs bool
}

// quiet reports whether develop's banner-class notices are suppressed.
func (h *headlessOptions) quiet() bool { return h != nil && h.Quiet }

// result is the record develop writes into: the caller's when it asked for
// one, a discarded scratch value otherwise, so develop writes unconditionally.
func (h *headlessOptions) result() *developResult {
	if h == nil || h.Result == nil {
		return &developResult{ContainerStatus: -1}
	}
	return h.Result
}

// reset starts the caller's result from "nothing happened yet".
func (h *headlessOptions) reset() {
	if h != nil && h.Result != nil {
		*h.Result = developResult{ContainerStatus: -1}
	}
}
