// Package deliver implements `byre deliver`: getting files from the host into
// a running box's /inbox over an exec stream (no mount, no host-side state).
//
// The package owns machine-scoped session discovery (union across engines,
// filtered to the caller's own boxes), the target-selection cascade, and the
// atomic no-clobber transport. It is deliberately independent of the commands
// package: engines arrive through the small Engine interface, and host-side
// identity (label keys, workdir ids, the caller uid) arrives via Config, so
// the whole flow is unit-testable with a fake engine. ADR 0021 carries the
// rationale; docs/deliver/decisions.md is the full decision record.
package deliver

import (
	"fmt"
	"io"
)

// Engine is the slice of a container engine deliver consumes. *runner.Runner
// satisfies it via a thin adapter in the commands package.
type Engine interface {
	// Name is the engine's user-facing name ("docker", "podman").
	Name() string
	// Sessions returns running container ids carrying the label (a bare key
	// matches presence; "key=value" matches exactly).
	Sessions(label string) ([]string, error)
	// Env returns a running container's configured environment.
	Env(id string) (map[string]string, error)
	// Labels returns a running container's labels.
	Labels(id string) (map[string]string, error)
	// ExecInput runs a command in the container as uid:gid, feeding stdin and
	// returning captured stdout.
	ExecInput(id string, uid, gid int, stdin io.Reader, argv ...string) (string, error)
}

// Session is one running byre box, as discovery sees it.
type Session struct {
	Engine     Engine
	EngineName string
	ID         string // container id
	ProjectID  string // byre.project label value
	WorkdirID  string // byre.workdir label value
	UID, GID   int    // the box's dev identity (BYRE_UID/BYRE_GID)
	Foreign    bool   // owned by a different uid than the caller
}

// Options are the per-invocation knobs.
type Options struct {
	Box          string // explicit target: id or project prefix (cascade step 0)
	SkipUIDCheck bool   // include (and permit) boxes owned by other uids
	NoClip       bool   // skip the clipboard round-trip's return leg
}

// Config is the host-side wiring deliver needs but must not derive itself.
type Config struct {
	Engines      []Engine
	ProjectLabel string // the project label KEY (presence = a byre box)
	WorkdirLabel string // the per-workdir label key
	CallerUID    int
	Cwd          string
	// WorkdirIDOf computes the workdir id a session started in dir would
	// carry — used by the cascade's ancestor walk. Errors mean "no id for
	// this level", not failure.
	WorkdirIDOf func(dir string) (string, error)
	Out         io.Writer  // the contract: delivered in-box paths, one per line
	Err         io.Writer  // byre's voice: target line, notes, degrade claims
	Clip        *Clipboard // host clipboard write path; nil = unavailable
}

// Run delivers each source path into the selected box and returns the landed
// in-box paths (top-level: one per file argument, one per directory argument).
// Failures are per-source: successes stay, the error reports the count.
func Run(cfg Config, opts Options, paths []string) ([]string, error) {
	sess, err := selectSession(cfg, opts)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(cfg.Err, "byre: delivering to %s (%s, %s)%s\n",
		sess.ProjectID, sess.EngineName, shortID(sess.ID), foreignNote(sess))

	var landed []string
	failed := 0
	for _, p := range paths {
		got, err := deliverPath(cfg, sess, p)
		// A partial directory returns BOTH a path and an error: the path is
		// real and still prints; the error (and exit code) carry completeness.
		if got != "" {
			landed = append(landed, got)
			fmt.Fprintln(cfg.Out, got)
		}
		if err != nil {
			fmt.Fprintf(cfg.Err, "byre: %v\n", err)
			failed++
		}
	}
	shipClipboard(cfg, opts, landed)
	if failed > 0 {
		return landed, fmt.Errorf("%d of %d deliveries failed", failed, len(paths))
	}
	return landed, nil
}

func foreignNote(s Session) string {
	if s.Foreign {
		return fmt.Sprintf(" — owned by uid %d, not you", s.UID)
	}
	return ""
}

// shortID abbreviates a container id for display, engine-style.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
