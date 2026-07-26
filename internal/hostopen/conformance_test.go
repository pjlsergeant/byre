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

// TestHostOpenConformance is the hostopen rule's enforcement arm: host-side
// reads of agent-writable paths ride this package (CLAUDE.md's coding
// convention). Every call to os.ReadFile / os.Open / os.OpenFile in non-test
// production code must either live in internal/hostopen or carry a reviewed
// allowlist entry stating WHY the plain call is sound. The rule got three
// external reports in one day before it was written down, and its first
// conversion sweep found the config-load hot path unguarded -- prose alone
// does not hold this class.
//
// Matching is by resolved import path (an aliased `stdos "os"` is caught;
// a dot-import of os is refused outright), keyed file+callee so line churn
// never invalidates entries.

// plainOpenAllowlist holds every sanctioned plain call as "file callee":
// disposition -- one of host-owned-read (a ~/.byre path outside the
// --self-edit mount), byre-created (a temp file byre itself just wrote),
// write-or-create (not a read; FIFO-hang and device-read classes don't
// apply the same way), device (an explicitly named device), test-harness
// (files the harness owns). Removing a call removes its entry; adding a
// call means converting it to hostopen or arguing its disposition here, in
// review.
var plainOpenAllowlist = map[string]string{
	"internal/commands/context.go ReadFile":            "byre-created: re-reads the $EDITOR temp file byre just created",
	"internal/commands/install.go ReadFile":            "host-owned-read: installed-package snapshots under ~/.byre, outside the self-edit mount",
	"internal/commands/installapp_install.go ReadFile": "host-owned-read: ~/Applications / ~/.local/share launcher artifacts",
	"internal/commands/pick.go OpenFile":               "device: /dev/tty, named deliberately for the interactive picker",
	"internal/commands/skill.go ReadFile":              "host-owned-read: local skill dirs and snapshots under ~/.byre, outside the self-edit mount",
	"internal/config/layers.go ReadFile":               "host-owned-read: ~/.byre/layers is outside the self-edit mount",
	"internal/configui/listitem.go ReadFile":           "byre-created: re-reads the prose $EDITOR temp file byre just created",
	"internal/lock/lock.go OpenFile":                   "write-or-create: the store lock file (O_CREATE|O_RDWR)",
	"internal/onboard/config.go ReadFile":              "host-owned-read: ~/.byre/default.config, outside the self-edit mount",
	"internal/packages/agentsmd.go ReadFile":           "host-owned-read: ~/.byre/AGENTS.md, store root, outside the self-edit mount",
	"internal/packages/installstore.go ReadFile":       "host-owned-read: the installed-package index under ~/.byre",
	"internal/packages/store.go ReadFile":              "host-owned-read: the bundled-mirror stamp under ~/.byre",
	"internal/tuitest/demo.go ReadFile":                "test-harness: cast files the recording harness owns",
	"internal/tuitest/tuitest.go ReadFile":             "test-harness: the exit-status file the harness owns",
}

var watchedCallees = map[string]bool{"ReadFile": true, "Open": true, "OpenFile": true}

func TestHostOpenConformance(t *testing.T) {
	root := "../.."
	found := map[string]bool{} // "file callee" actually seen
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
				key := rel + " " + sel.Sel.Name
				found[key] = true
				if _, ok := plainOpenAllowlist[key]; !ok {
					violations = append(violations, fmt.Sprintf(
						"%s calls os.%s at %s — host-side reads of agent-writable paths ride internal/hostopen (fd-judged, bounded). Convert it, or add a reviewed plainOpenAllowlist entry with a disposition.",
						rel, sel.Sel.Name, fset.Position(call.Pos())))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Stale entries rot into false confidence: an allowlist row whose call is
	// gone must go with it.
	var stale []string
	for key := range plainOpenAllowlist {
		if !found[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		violations = append(violations, fmt.Sprintf("allowlist entry %q matches no call — remove it", key))
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}
