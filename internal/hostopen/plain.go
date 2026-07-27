package hostopen

import (
	"io/fs"
	"os"
)

// plain.go is the ESCAPE from this package, and it exists so that escaping is
// a visible, categorised act rather than an invisible one.
//
// byre runs on the host, as the user, with the user's privileges, and it
// touches paths a box can shape. Three routes give a box control over what
// byre ends up touching:
//
//   - the STRING -- the agent authored the path (a `gitdir:` pointer read out
//     of a .git file, a name from an enumeration the box produced);
//   - the ROUTE -- byre authored the string, but the agent controls a
//     component it traverses, so the same string resolves somewhere else;
//   - the TARGET -- byre owns every component, but the agent can replace the
//     object sitting there (your file is now a FIFO).
//
// Where any of those is live, the call rides this package's real functions:
// O_NONBLOCK so nothing hangs, the type judged from the descriptor rather
// than the pathname, bounded reads, openat-anchored roots so a swapped
// ancestor cannot redirect.
//
// Where none of them is live, the plain stdlib call is correct -- and the
// PlainX wrappers below are how you say so. The Reason is unused at runtime;
// it is a claim, made at the call site, that the compiler forces you to make
// and grep can audit. That is the whole mechanism: the justification lives
// with the code instead of in a table forty files away, and a new call
// cannot inherit an old call's reasoning.
//
// Raw os.* calls are refused outside this package by
// TestHostOpenConformance.

// Reason is the category under which a plain stdlib call is claimed safe.
// A closed set on purpose: `rg StoreOwned` answers "show me everything
// resting on that argument", and a NEW kind of excuse means adding a
// constant here, which is itself a reviewable event.
type Reason string

const (
	// StoreOwned: a path under byre's host-side store (~/.byre) that no box
	// can reach. NOT automatic -- --self-edit mounts a project's store rw
	// into its box, so a path under ~/.byre/projects/<id> is agent-writable
	// during such a session and does not qualify.
	StoreOwned Reason = "store-owned"

	// ByreCreated: a file byre itself just wrote, in a directory byre owns,
	// re-read or replaced within the same operation.
	ByreCreated Reason = "byre-created"

	// HostUserOwned: a path outside byre entirely, named by the user and
	// owned by them (~/Applications, ~/.local/share, an $EDITOR temp file).
	// The user is not the threat model (P1).
	HostUserOwned Reason = "host-user-owned"

	// SubprocessConsumer: the path is agent-influenced, but what ultimately
	// opens it is a SUBPROCESS that resolves the path itself (git, the
	// engine CLI). Anchoring here would buy nothing -- byre never opens it,
	// so there is no descriptor for byre to judge. The mitigation is the
	// subprocess's own bounds (timeouts, output caps), not this package.
	SubprocessConsumer Reason = "subprocess-consumer"

	// Device: an explicitly named device the code means to open (/dev/tty).
	Device Reason = "device"

	// TestHarness: production code serving the test harness, on files the
	// harness itself owns.
	TestHarness Reason = "test-harness"

	// IdentityChecked: the path IS agent-influenced and following it is
	// DELIBERATE -- the call's answer is never trusted on its own, only as
	// one side of an os.SameFile comparison against an inode byre already
	// obtained safely. Refusing to follow would break legitimate setups (a
	// repo reached through a symlinked path); anchoring would change the
	// semantics rather than harden them, because resolving is the point.
	// The security property is the identity comparison, not the lookup.
	//
	// Added 2026-07-28 after a four-way classification: two independent
	// reviewers marked these sites "no Reason fits" and two others reached
	// for categories that did not describe them. Three wrong-shaped answers
	// to one question is what a missing word looks like.
	IdentityChecked Reason = "identity-checked"

	// Unreviewed means NOBODY HAS CHECKED the three routes for this path.
	// It is not a disposition -- it is a marker for work not yet done, kept
	// so a sweep can be honest instead of dressing unexamined calls up as
	// reviewed ones. `rg hostopen.Unreviewed` is the backlog.
	Unreviewed Reason = "UNREVIEWED"
)

// The wrappers below are signature-identical pass-throughs to their os
// counterparts. They add exactly one thing: the Reason the caller had to
// name. New wrappers are added when a call needs one -- an os function with
// no wrapper here is one nothing has yet claimed a reason to use.

func PlainReadFile(name string, _ Reason) ([]byte, error) { return os.ReadFile(name) }

func PlainOpen(name string, _ Reason) (*os.File, error) { return os.Open(name) }

func PlainOpenFile(name string, flag int, perm fs.FileMode, _ Reason) (*os.File, error) {
	return os.OpenFile(name, flag, perm)
}

func PlainWriteFile(name string, data []byte, perm fs.FileMode, _ Reason) error {
	return os.WriteFile(name, data, perm)
}

func PlainStat(name string, _ Reason) (fs.FileInfo, error) { return os.Stat(name) }

func PlainLstat(name string, _ Reason) (fs.FileInfo, error) { return os.Lstat(name) }

func PlainReadDir(name string, _ Reason) ([]os.DirEntry, error) { return os.ReadDir(name) }

func PlainReadlink(name string, _ Reason) (string, error) { return os.Readlink(name) }

func PlainRemove(name string, _ Reason) error { return os.Remove(name) }

func PlainRemoveAll(path string, _ Reason) error { return os.RemoveAll(path) }

func PlainMkdir(path string, perm fs.FileMode, _ Reason) error { return os.Mkdir(path, perm) }

func PlainMkdirAll(path string, perm fs.FileMode, _ Reason) error { return os.MkdirAll(path, perm) }

// PlainRename takes ONE reason: both paths must qualify under it, which is
// true of every rename byre does (a temp file and its destination in the
// same byre-owned directory).
func PlainRename(oldpath, newpath string, _ Reason) error { return os.Rename(oldpath, newpath) }

func PlainChmod(name string, mode fs.FileMode, _ Reason) error { return os.Chmod(name, mode) }

// The name-minting creators. byre picks the NAME, so the STRING route is
// closed by construction -- but the DIRECTORY the name lands in may still be
// agent-writable (a project store under --self-edit), which leaves the ROUTE
// live. They take a Reason for that reason.

func PlainCreateTemp(dir, pattern string, _ Reason) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}

func PlainMkdirTemp(dir, pattern string, _ Reason) (string, error) {
	return os.MkdirTemp(dir, pattern)
}

// PlainLink hard-links oldname to newname. Both ends must qualify under the
// one Reason; byre only ever links a temp it just created onto a destination
// in the same directory (the atomic no-clobber publish).
func PlainLink(oldname, newname string, _ Reason) error { return os.Link(oldname, newname) }
