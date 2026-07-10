package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
)

// storeSnapshot captures the self-edit mount's contents before the session so
// the exit report can say exactly what the agent touched: content signatures
// for every file, plus byre.config's bytes (the one file whose edits grant
// things, so it gets a content diff rather than just a name).
type storeSnapshot struct {
	sigs   map[string]string // rel path -> content signature
	config []byte            // byre.config bytes; nil when absent
}

func snapshotStore(dir string) storeSnapshot {
	s := storeSnapshot{sigs: map[string]string{}}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // an unreadable entry just sits out the comparison
		}
		if rel, rerr := filepath.Rel(dir, p); rerr == nil {
			s.sigs[filepath.ToSlash(rel)] = fileSig(p, d)
		}
		return nil
	})
	s.config, _ = os.ReadFile(filepath.Join(dir, config.ProjectConfigName))
	return s
}

// fileSig is a comparison signature: content hash for regular files, the
// target for symlinks (a retargeted link IS a change), the type otherwise.
func fileSig(path string, d fs.DirEntry) string {
	if d.Type()&fs.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		if err != nil {
			return "link:?"
		}
		return "link:" + t
	}
	if !d.Type().IsRegular() {
		return "special:" + d.Type().String()
	}
	f, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable"
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// reportSelfEditChanges closes a --self-edit session by showing what the agent
// changed in the project store against the pre-session snapshot -- the user's
// one pushed chance to review before the next develop applies it. byre.config
// gets a content diff; every other file is listed by status. Silent when
// nothing changed.
func reportSelfEditChanges(w io.Writer, dir string, before storeSnapshot) {
	after := snapshotStore(dir)
	var added, changed, deleted []string
	for rel, sig := range after.sigs {
		switch bsig, ok := before.sigs[rel]; {
		case !ok:
			added = append(added, rel)
		case bsig != sig:
			changed = append(changed, rel)
		}
	}
	for rel := range before.sigs {
		if _, ok := after.sigs[rel]; !ok {
			deleted = append(deleted, rel)
		}
	}
	if len(added)+len(changed)+len(deleted) == 0 {
		return
	}
	// "changed", not "the agent changed": the store is shared across worktree
	// sessions, so a sibling develop rebuilding context/ mid-session would be
	// misattributed by the stronger claim.
	fmt.Fprintln(w, "🛑 self-edit: the project store changed during this session:")

	// byre.config first, as a content diff (existence flips called out -- a
	// created or deleted EMPTY config is still a change).
	beforeMissing, afterMissing := before.config == nil, after.config == nil
	if beforeMissing != afterMissing || !bytes.Equal(before.config, after.config) {
		fmt.Fprintln(w, "   byre.config (applies on the next develop):")
		if afterMissing {
			fmt.Fprintln(w, "      (deleted)")
		} else if beforeMissing {
			fmt.Fprintln(w, "      (created)")
		}
		// Any byte change yields hunks (a final-newline-only edit shows as a
		// "\ No newline" marker), so unequal content never prints a bare header.
		for _, l := range unifiedDiff("byre.config (session start)", "byre.config (now)", string(before.config), string(after.config)) {
			fmt.Fprintln(w, "      "+l)
		}
	}

	for _, g := range []struct {
		label string
		rels  []string
	}{{"added:  ", added}, {"changed:", changed}, {"deleted:", deleted}} {
		sort.Strings(g.rels)
		for _, rel := range g.rels {
			if rel == config.ProjectConfigName {
				continue // shown as the diff above
			}
			fmt.Fprintf(w, "   %s %s\n", g.label, rel)
		}
	}
}

// splitLines splits file content into lines without a trailing phantom empty
// line ("" means no lines).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
