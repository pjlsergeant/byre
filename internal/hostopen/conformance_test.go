package hostopen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestHostOpenConformance is the hostopen rule's enforcement arm: plain os
// filesystem calls, and path/filepath.EvalSymlinks/Walk/WalkDir, are BANNED
// in non-test production code. A call either rides this package's real
// functions, or says why the plain call is safe at the call site --
// hostopen.PlainStat(p, hostopen.StoreOwned) -- with a Reason the compiler
// requires and grep can audit. The rule got three external reports in one
// day before it was written down, and its first conversion sweep found the
// config-load hot path unguarded; prose alone does not hold this class.
//
// The wrapper REPLACED an allowlist table (2026-07-28). The table was keyed
// file+callee, so a new unreviewed call rode an existing entry silently --
// an arm that weakened with every legitimate exception it granted. Moving
// the justification to the call site kills that: an exemption cannot be
// inherited, and it cannot drift away from the code it describes.
//
// Matching is by resolved import path: an aliased `stdos "os"` or
// `fpath "path/filepath"` is caught, and a dot-import of a watched
// package is refused outright.

// watchedByImport is deliberately DUMB: it names the stdlib calls, not the
// paths they touch, because the walk cannot see where a path points. A
// cleverer walk that tried to flag only agent-writable targets would fail
// in the one direction that never announces itself -- a miss would look
// exactly like coverage. Noise here is a sentence someone writes; a blind
// spot there is a hole nobody sees.
//
// Reads hang (FIFO) and stream (device). WRITES are the class that produced
// a real bug. PROBES are the first half of every check-to-use race byre has
// had to fix by hand (the worktree back-pointer, checkContainedHostSource's
// window, staging's classify-then-open): "unsolicited probes degrade, never
// block" is about AVAILABILITY and buys nothing for INTEGRITY, which is what
// anchoring gives.
//
// path/filepath.EvalSymlinks is the first half of every classify-then-open
// race: it resolves an agent-plantable symlink chain by pathname.
// Walk/WalkDir traverse by pathname, so a swapped component redirects the
// walk itself.
var watchedByImport = map[string]map[string]bool{
	"os": {
		// reads
		"ReadFile": true, "Open": true, "OpenFile": true,
		// writes
		"WriteFile": true, "Create": true, "Remove": true, "RemoveAll": true,
		"Rename": true, "Mkdir": true, "MkdirAll": true, "Symlink": true,
		"Link": true, "Chmod": true, "Chown": true, "Truncate": true,
		// probes
		"Stat": true, "Lstat": true, "ReadDir": true, "Readlink": true,
		// name-minting creators: the NAME is byre's, but the DIRECTORY it lands
		// in may not be, so they answer the same three questions as the rest.
		"CreateTemp": true, "MkdirTemp": true,
		// directory ENTRY POINTS. Plain os.OpenRoot follows a swapped final
		// component -- OpenDirRootNoFollow exists for exactly that -- and every
		// read through an os.DirFS resolves by pathname underneath it. Anchoring
		// is what these are for, so an unwatched one is the worst kind of gap:
		// it looks like containment.
		"OpenRoot": true, "DirFS": true,
	},
	"path/filepath": {
		"EvalSymlinks": true,
		"Walk":         true,
		"WalkDir":      true,
	},
}

// defaultImportName is the identifier a named import would bind: the last
// path element of the import path (`os`, `filepath`).
func defaultImportName(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

func conformanceViolation(rel, importPath, callee, pos string) string {
	switch importPath {
	case "path/filepath":
		switch callee {
		case "EvalSymlinks":
			return fmt.Sprintf("%s calls filepath.EvalSymlinks at %s — path/filepath.EvalSymlinks is banned outside internal/hostopen: it resolves an agent-plantable symlink chain by pathname. Ride hostopen.RealUnder (resolve then judge containment by identity), or say why the plain call is safe at the CALL SITE: hostopen.PlainEvalSymlinks(path, hostopen.<Reason>).", rel, pos)
		case "Walk", "WalkDir":
			return fmt.Sprintf("%s calls filepath.%s at %s — path/filepath.%s is banned outside internal/hostopen: it traverses by pathname. Ride hostopen.OpenDirRootNoFollow and fs.WalkDir over root.FS().", rel, callee, pos, callee)
		}
	}
	return fmt.Sprintf(
		"%s calls os.%s at %s — plain os filesystem calls are banned outside internal/hostopen. Either ride this package's real functions (fd-judged, bounded, anchored), or say why the plain call is safe at the CALL SITE: hostopen.Plain%s(path, hostopen.<Reason>). If nobody has checked the three routes for that path, hostopen.Unreviewed is the honest marker.",
		rel, callee, pos, callee)
}

func TestHostOpenConformance(t *testing.T) {
	root := "../.."
	var violations []string

	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/hostopen/") {
				return nil // the one place the plain calls belong
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("parsing %s: %w", rel, perr)
			}
			// local name -> resolved import path, for every watched package
			// this file imports (aliased or not).
			pkgNames := map[string]string{}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if watchedByImport[p] == nil {
					continue
				}
				switch {
				case imp.Name == nil:
					pkgNames[defaultImportName(p)] = p
				case imp.Name.Name == ".":
					violations = append(violations, fmt.Sprintf("%s: dot-imports %s — the conformance walk cannot attribute its calls; use a named import", rel, p))
				default:
					pkgNames[imp.Name.Name] = p
				}
			}
			if len(pkgNames) == 0 {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				importPath, ok := pkgNames[ident.Name]
				if !ok || !watchedByImport[importPath][sel.Sel.Name] {
					return true
				}
				violations = append(violations, conformanceViolation(rel, importPath, sel.Sel.Name, fset.Position(call.Pos()).String()))
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}
