package commands

import (
	"io"

	"github.com/pjlsergeant/byre/internal/project"
)

// headlessOptions is how `byre run` drives develop's session without a human
// on the other end. nil is `byre develop` exactly: every field below is read
// only through a non-nil *headlessOptions.
type headlessOptions struct {
	// Command is the container command (RunParams.Command): the chassis.
	Command []string
	// Env is the BYRE_RUN_* set, applied after skill env; raw run_args win
	// (ADR 0006).
	Env map[string]string
	// Stage runs under the setup lock right after Create, with the under-lock
	// resolution; its error comes back as-is, so it reports its own refusal.
	Stage func(paths project.Paths, rv resolved) error
	// AttachStdout, when set, takes the box's stdout, attached with no stdin;
	// unset, the attach is develop's.
	AttachStdout io.Writer
	// RunID, TaskSHA256 and RunTimeout go into the launch record (the task's
	// hash, never its text); RunTimeout is "" for none.
	RunID      string
	TaskSHA256 string
	RunTimeout string
	// Quiet drops develop's banner-class notices; never an error, a refusal,
	// a warning, or the exit report.
	Quiet bool
	// StreamStdout takes the stdout of the pre-attach engine calls (build,
	// seeds); a headless run routes them to stderr, off the answer.
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
