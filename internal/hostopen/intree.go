package hostopen

import (
	"os"
	"path/filepath"
	"strings"
)

// intree.go answers "does this path denote something inside that tree?" for
// callers that must judge an agent-shaped spelling. It lives here because the
// answer rests on IdentityChecked lookups -- the one Reason whose whole point
// is that following a symlink is deliberate and the security property is the
// os.SameFile comparison, not the lookup.

// RealUnder resolves base, then base/rel, and returns the resolved path only
// if that result is under the resolved base by file identity -- never by
// lexical Rel, which misclassifies on a case-insensitive filesystem. The
// resolve-then-containment check is the function, so a caller cannot forget
// the second half or judge it by spelling.
//
// Containment is judged on the pathname returned, from that one lookup: a
// second resolve between the two would let a swapped component make the
// check pass on a different object than the name handed back. The returned
// pathname is still a name, not a handle -- the same residual every byre
// path-based open carries.
//
// A missing path returns an error for which errors.Is(err, fs.ErrNotExist)
// holds. A resolved path that is not under the base returns ErrEscapes.
func RealUnder(base, rel string) (string, error) {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Join(realBase, rel))
	if err != nil {
		return "", err
	}
	wd, err := PlainStat(realBase, IdentityChecked)
	if err != nil {
		return "", err
	}
	if !identityUnderResolved(wd, real) {
		return "", ErrEscapes
	}
	return real, nil
}

// InTreeByIdentity reports whether p denotes tree or a descendant of it,
// judged by FILE IDENTITY -- os.SameFile against tree over real ancestor
// chains -- never by spelling. A lexical comparison misclassifies on a
// case-insensitive filesystem (macOS APFS): a case-variant spelling of an
// in-tree path reads as "outside", skipping whatever check the caller gates
// on containment.
//
// Each spelled ancestor of p (deepest first) is resolved and its OWN real
// ancestor chain is compared against tree (identityUnder). Two chains are
// required, not one: a lexically-in-tree spelling whose interior component
// escapes (<tree>/via/x with via -> /outside) only meets the tree at the
// spelled ancestor <tree> itself, while an alias into a SUBDIRECTORY
// (/tmp/link/data with /tmp/link -> <tree>/subdir) never spells the tree and
// only meets it on the resolved chain above subdir (comparing identity with
// the root alone missed exactly this alias). Missing or unresolvable
// components (ENOENT, ELOOP) just walk upward; identityUnder stats but never
// opens, so an agent-planted FIFO can't hang the walk. A final-component
// escape is the caller's to judge, on the EvalSymlinks'd path. If tree itself
// can't be stat'd, degrade to the lexical judgment.
func InTreeByIdentity(tree, p string) bool {
	wd, err := PlainStat(tree, IdentityChecked)
	if err != nil {
		return underTree(tree, p)
	}
	for q := filepath.Clean(p); ; {
		if identityUnder(wd, q) {
			return true
		}
		parent := filepath.Dir(q)
		if parent == q {
			return false // no spelled ancestor resolves into the tree
		}
		q = parent
	}
}

// identityUnder reports whether q resolves to wd or to a descendant of it: q
// is canonicalized (so its lexical ancestors ARE its real ones -- ".." after a
// symlink component would otherwise be resolved against the wrong parent) and
// each ancestor is identity-compared to wd. A q that doesn't exist or can't
// resolve is simply not evidence of containment (false); the caller keeps
// walking its spelled ancestors.
func identityUnder(wd os.FileInfo, q string) bool {
	resolved, err := filepath.EvalSymlinks(q)
	if err != nil {
		return false
	}
	return identityUnderResolved(wd, resolved)
}

// identityUnderResolved is the stat-chain half of identityUnder: resolved is
// already canonical (EvalSymlinks'd), so walking filepath.Dir is walking the
// real ancestor chain. RealUnder calls this on the pathname it returns, so
// the containment judgment and the returned name are the same lookup.
func identityUnderResolved(wd os.FileInfo, resolved string) bool {
	for cur := resolved; ; {
		if fi, ferr := PlainStat(cur, IdentityChecked); ferr == nil && os.SameFile(wd, fi) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// underTree reports whether p is tree itself or a descendant of it. Both are
// cleaned by filepath.Rel; a p outside tree yields a rel path that is ".." or
// begins "../".
func underTree(tree, p string) bool {
	rel, err := filepath.Rel(tree, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
