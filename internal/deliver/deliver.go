// Package deliver implements `byre deliver`: getting files from the host into
// a running box's /inbox over an exec stream (no mount, no host-side state).
//
// The package owns machine-scoped session discovery (union across engines,
// filtered to the caller's own boxes), the target-selection cascade, and the
// atomic no-clobber transport. It is deliberately independent of the commands
// package: engines arrive through the small Engine interface, and host-side
// identity (label keys, workdir ids, the caller uid) arrives via Config, so
// the whole flow is unit-testable with a fake engine. ADR 0021 carries the
// rationale; docs/DELIVER.md is the user-facing behavior.
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
	// CallerScoped reports that every session this engine can see was started
	// by the calling user (rootless Podman: per-user storage). The uid
	// accident-guard is then satisfied by construction — and must not compare
	// ids: a keep-id box's BYRE_UID is the in-container generic uid, not the
	// caller's (ADR 0032).
	CallerScoped() bool
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
	Name         string // landing basename for stdin captures (--name)

	// The remote-delivery surface (ADR 0037). Boxes/Tar/Proto are what a
	// LOCAL byre invokes on the remote over ssh; RemoteByre is local-side
	// (the remote binary's path when "byre" isn't on the ssh non-interactive
	// PATH).
	Boxes      bool   // --boxes: list deliverable boxes headlessly and exit
	Tar        bool   // --tar: stdin is a tar archive to unpack into /inbox
	Proto      int    // --proto: protocol handshake (0 = flag not given)
	RemoteByre string // --remote-byre: byre binary path on the ssh remote
}

// Config is the host-side wiring deliver needs but must not derive itself.
type Config struct {
	Engines      []Engine
	ProjectLabel string // the project label KEY (presence = a byre box)
	WorkdirLabel string // the per-workdir label key
	CallerUID    int
	Cwd          string
	// WorkdirIDOf computes the workdir id a session started in dir would
	// carry — used by the cascade's ancestor walk. Return an error wrapping
	// ErrNoWorkdirID to mean "no id for this level, keep walking"; ANY other
	// error aborts selection loudly. The distinction has teeth: an id
	// collision (the recorded canonical path names a different project) must
	// refuse, not skip — a skipped level falls through to the sole-session
	// and picker fallbacks, which would happily select the collided box.
	WorkdirIDOf func(dir string) (string, error)
	Out         io.Writer  // the contract: delivered in-box paths, one per line
	Err         io.Writer  // byre's voice: target line, notes, degrade claims
	Clip        *Clipboard // host clipboard write path; nil = unavailable
	// Pick resolves an ambiguous session set interactively (TTY list or a
	// graphical dialog — the platform adapter is the caller's). nil means no
	// picker exists; the cascade degrades to an error listing the candidates.
	// ok=false is a clean user cancel.
	Pick func(sessions []Session) (s Session, ok bool, err error)
}

// ErrNoWorkdirID is the WorkdirIDOf sentinel for "this directory level has no
// workdir id" — the ancestor walk keeps climbing. Any WorkdirIDOf error NOT
// wrapping it aborts selection (see the Config field's doc).
var ErrNoWorkdirID = fmt.Errorf("no workdir id for this level")

// errCancelled marks a clean user cancel at the picker: not a failure, and
// callers exit quietly (IsCancelled).
var errCancelled = fmt.Errorf("cancelled")

// IsCancelled reports whether err is the user cancelling at the picker.
func IsCancelled(err error) bool { return err == errCancelled }

// Run delivers path arguments — RunSources over PathSources.
func Run(cfg Config, opts Options, paths []string) ([]string, error) {
	return RunSources(cfg, opts, PathSources(paths))
}

// RunSources delivers each source into the selected box and returns the
// landed in-box paths (top-level: one per source; a directory is one path).
// Failures are per-source: successes stay, the error reports the count.
func RunSources(cfg Config, opts Options, sources []Source) ([]string, error) {
	sess, err := selectSession(cfg, opts)
	if err != nil {
		return nil, err
	}
	// pickArg, not ProjectID: a worktree box shares its project's id, and
	// naming that here made main-tree and worktree deliveries print the same
	// line (QA pass-2 finding) — the workdir id is the box's own name.
	fmt.Fprintf(cfg.Err, "byre: delivering to %s (%s, %s)%s\n",
		pickArg(sess), sess.EngineName, shortID(sess.ID), foreignNote(sess))

	var landed []string
	failed := 0
	for _, src := range sources {
		got, err := deliverSource(cfg, sess, src)
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
		return landed, fmt.Errorf("%d of %d deliveries failed", failed, len(sources))
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
