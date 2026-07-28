// Package hostexec resolves the binaries byre spawns on the HOST -- the
// container engine CLI, git, ssh, the shell that carries $EDITOR, the
// clipboard and notification helpers.
//
// Two properties, and every host-side spawn gets both by coming through here.
//
// PINNED. A name is resolved on PATH once per process and the absolute path
// is reused for the rest of the invocation. Re-resolving per call means the
// binary byre checked and the binary byre ran are two lookups with a window
// between them; it also means PATH is read twenty times to answer one
// question.
//
// NOT OUT OF A DIRECTORY THE BOX WRITES. byre asks the host for `docker`; if
// PATH answers with a file sitting in the project tree, byre would be running
// the agent's own binary, at moments nobody typed a command for (the session
// -end probes fire on every exit). So a resolved path that lands under one of
// the caller's roots is declined, by name, with the remedy. Go's own ErrDot
// already declines RELATIVE PATH entries; the absolute case is this package's.
//
// This is not a judgement of the user's PATH, and there is deliberately no
// judgement of binary CONTENT -- no checksums, no signatures. Where the
// binary was resolved FROM is the whole test. A caller with no project in
// hand passes an empty root set and gets a silent pin, which is the right
// answer: with nothing mounted rw there is no box-writable directory to fall
// under.
package hostexec

import (
	"fmt"
	"os/exec"
	"sync"

	"github.com/pjlsergeant/byre/internal/hostopen"
)

// Roots is the set of directories a project's box can write. Callers build it
// from what they already know (the work tree, the main tree, the common git
// dir, byre's store for the project); this package never derives it, so a
// root is always a directory some caller can point at and name.
type Roots struct{ dirs []string }

// NewRoots returns the root set for dirs, dropping empty entries (Paths
// carries "" for the fields that don't apply to a plain project).
func NewRoots(dirs ...string) Roots {
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		out = append(out, d)
	}
	return Roots{dirs: out}
}

// under returns the root p falls inside, and whether it fell inside one.
//
// Judged by identity, not spelling, and in BOTH directions an agent can
// spell: a shadowing entry written as <tree>/.bin/docker -> /usr/bin/docker
// is still under the tree (the agent can re-point the link at any time), and
// a /usr/local/bin/docker that resolves INTO the tree is under it too.
// hostopen.InTreeByIdentity covers both because it walks the spelled chain
// and the resolved chain.
//
// Can't-tell resolves to "not under": an unreadable ancestor or a dangling
// component is not evidence of shadowing, and byre must not become
// unrunnable because a directory on the user's PATH refused a stat. The
// residual is narrow -- for an unresolvable path to be agent content, the
// user would have had to point a PATH entry into their own project tree
// themselves, which is their arrangement to make (P1) and not something the
// box can arrange.
func (r Roots) under(p string) (string, bool) {
	for _, root := range r.dirs {
		if hostopen.InTreeByIdentity(root, p) {
			return root, true
		}
	}
	return "", false
}

// ShadowError is the refusal: PATH answered with a binary sitting under a
// directory this project's box can write.
type ShadowError struct {
	Name string // what byre asked for ("docker", "git")
	Path string // what PATH answered with
	Root string // the box-writable directory it fell under
}

func (e *ShadowError) Error() string {
	return fmt.Sprintf("byre declines to run %s: PATH resolves it to %s, inside %s — a directory this project's box can write. "+
		"Remove that file, or put the real %s earlier on PATH.", e.Name, e.Path, e.Root, e.Name)
}

// Resolver pins lookups for its lifetime. The package-level functions use
// one that lives as long as the process (byre is a single-shot CLI, so that
// is the invocation); tests make their own with a fake lookup.
type Resolver struct {
	mu     sync.Mutex
	look   func(string) (string, error)
	pinned map[string]pin
}

type pin struct {
	path string
	err  error
}

// NewResolver returns a Resolver over look (exec.LookPath in production).
func NewResolver(look func(string) (string, error)) *Resolver {
	return &Resolver{look: look, pinned: map[string]pin{}}
}

// Look returns the absolute path to run for name, or an error: *ShadowError
// when PATH answered with a binary under one of roots, otherwise whatever the
// lookup gave (exec.ErrNotFound for a tool that isn't installed).
//
// The PIN is the resolution; the containment test is applied per call,
// because two callers can hold different root sets and a name pinned by the
// one with none must not exempt itself from the other's check.
func (r *Resolver) Look(name string, roots Roots) (string, error) {
	p, err := r.resolve(name)
	if err != nil {
		return "", err
	}
	if root, shadowed := roots.under(p); shadowed {
		return "", &ShadowError{Name: name, Path: p, Root: root}
	}
	return p, nil
}

func (r *Resolver) resolve(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if got, ok := r.pinned[name]; ok {
		return got.path, got.err
	}
	// The failure is pinned alongside the success: `installedEngines` asks
	// about both engines several times per invocation, and a not-installed
	// answer is as stable within one run as an installed one.
	p, err := r.look(name)
	r.pinned[name] = pin{path: p, err: err}
	return p, err
}

// process is the pin set for this byre invocation.
var process = NewResolver(exec.LookPath)

// Look resolves name against the process-wide pin set. See Resolver.Look.
func Look(name string, roots Roots) (string, error) { return process.Look(name, roots) }

// Looker adapts Look to the bare lookup signature the injectable seams take
// (runner.LookPath, the clipboard probe), binding roots once.
func Looker(roots Roots) func(string) (string, error) {
	return func(name string) (string, error) { return Look(name, roots) }
}
