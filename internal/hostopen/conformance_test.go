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
// filesystem calls are BANNED in non-test production code. A call either
// rides this package's real functions, or says why the plain call is safe at
// the call site -- hostopen.PlainStat(p, hostopen.StoreOwned) -- with a
// Reason the compiler requires and grep can audit. The rule got three
// external reports in one day before it was written down, and its first
// conversion sweep found the config-load hot path unguarded; prose alone
// does not hold this class.
//
// The wrapper REPLACED an allowlist table (2026-07-28). The table was keyed
// file+callee, so a new unreviewed call rode an existing entry silently --
// an arm that weakened with every legitimate exception it granted. Moving
// the justification to the call site kills that: an exemption cannot be
// inherited, and it cannot drift away from the code it describes.
//
// Matching is by resolved import path: an aliased `stdos "os"` is caught,
// and a dot-import of os is refused outright.

// watchedCallees is deliberately DUMB: it names the stdlib calls, not the
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
var watchedCallees = map[string]bool{"ReadFile": true, "Open": true, "OpenFile": true}

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
			osNames := map[string]bool{}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if p != "os" {
					continue
				}
				switch {
				case imp.Name == nil:
					osNames["os"] = true
				case imp.Name.Name == ".":
					violations = append(violations, fmt.Sprintf("%s: dot-imports os — the conformance walk cannot attribute its calls; use a named import", rel))
				default:
					osNames[imp.Name.Name] = true
				}
			}
			if len(osNames) == 0 {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !watchedCallees[sel.Sel.Name] {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || !osNames[ident.Name] {
					return true
				}
				violations = append(violations, fmt.Sprintf(
					"%s calls os.%s at %s — plain os filesystem calls are banned outside internal/hostopen. Either ride this package's real functions (fd-judged, bounded, anchored), or say why the plain call is safe at the CALL SITE: hostopen.Plain%s(path, hostopen.<Reason>). If nobody has checked the three routes for that path, hostopen.Unreviewed is the honest marker.",
					rel, sel.Sel.Name, fset.Position(call.Pos()), sel.Sel.Name))
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
