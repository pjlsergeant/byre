package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
)

// maxStoreFileBytes bounds every host-side read the self-edit report makes of
// the agent-writable store: a planted device node or a symlink to /dev/zero
// cannot stream unbounded into the host byre process, and an oversized regular
// file is recorded by size rather than hashed. The store's real files
// (byre.config, skill context) are kilobytes; this is pure slack.
const maxStoreFileBytes = 8 << 20 // 8 MiB

// storeSnapshot captures the self-edit mount's contents before the session so
// the exit report can say exactly what the agent touched: content signatures
// for every file, plus byre.config's bytes (the one file whose edits grant
// things, so it gets a content diff rather than just a name).
type storeSnapshot struct {
	sigs   map[string]string // rel path -> content signature
	config []byte            // byre.config bytes; set only when configReadable
	// configReadable is true when byre.config was captured as a present, regular,
	// within-cap file (so config holds its bytes for a diff). It is false when the
	// config is absent, oversized, or a non-regular file -- change detection then
	// rides the signature map and the diff falls back to a status line.
	configReadable bool
	// unreadable is set when the store directory itself could not be opened as
	// a contained root -- e.g. a --self-edit agent swapped it for a symlink.
	// The diff against such a snapshot is meaningless, so the report degrades to
	// a notice rather than inventing mass additions or deletions.
	unreadable bool
}

// snapshotStore reads the store through a no-follow contained root
// (hostopen.OpenDirRootNoFollow + a rooted walk), so a --self-edit agent that
// planted a FIFO, a device node, or a swapped symlink in the mount can neither
// hang nor OOM the host byre process taking this snapshot. Every read is
// bounded; a refusal degrades that one entry, never the whole report.
func snapshotStore(dir string) storeSnapshot {
	s := storeSnapshot{sigs: map[string]string{}}
	root, err := hostopen.OpenDirRootNoFollow(dir)
	if err != nil {
		s.unreadable = true
		return s
	}
	defer root.Close()
	// Enumerate THROUGH the root (never by re-walking the pathname), so the
	// opens below and this walk cannot observe two different directories.
	fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // an unreadable entry just sits out the comparison
		}
		s.sigs[p] = fileSig(root, p, d)
		return nil
	})
	// byre.config, bounded and rooted like every other read: oversize or
	// non-regular leaves config nil (reported as absent), never an unbounded
	// slurp of a device or a hung open of a FIFO.
	if f, fi, ferr := hostopen.OpenRegularIn(root, config.ProjectConfigName); ferr == nil {
		if fi.Size() <= maxStoreFileBytes {
			s.config, _ = io.ReadAll(io.LimitReader(f, maxStoreFileBytes))
			s.configReadable = true
		}
		f.Close()
	}
	return s
}

// fileSig is a comparison signature: content hash for regular files, the target
// for symlinks (a retargeted link IS a change), a size + capped-prefix hash for
// a file too large to hash whole (so a same-size rewrite within the prefix is
// still caught), and the type otherwise. Reads ride the contained root and are
// bounded -- a device or FIFO can neither hang nor stream unbounded.
func fileSig(root *os.Root, rel string, d fs.DirEntry) string {
	if d.Type()&fs.ModeSymlink != 0 {
		t, err := root.Readlink(rel)
		if err != nil {
			return "link:?"
		}
		return "link:" + t
	}
	f, fi, err := hostopen.OpenRegularIn(root, rel)
	if err != nil {
		if errors.Is(err, hostopen.ErrNotRegular) {
			return "special:" + d.Type().String()
		}
		return "unreadable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxStoreFileBytes)); err != nil {
		return "unreadable"
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if fi.Size() > maxStoreFileBytes {
		// Too large to hash whole under the time bound: sign by size plus a hash
		// of the capped prefix, so a same-size content rewrite within the prefix
		// is still caught. Residual: a change confined to bytes BEYOND the cap
		// that also preserves the exact size and prefix goes unreported -- an
		// extreme corner at an 8 MiB cap, accepted over hashing unbounded.
		return fmt.Sprintf("large:%d:%s", fi.Size(), sum)
	}
	return "sha256:" + sum
}

// reportSelfEditChanges closes a --self-edit session by showing what the agent
// changed in the project store against the pre-session snapshot -- the user's
// one pushed chance to review before the next develop applies it. byre.config
// gets a content diff; every other file is listed by status. Silent when
// nothing changed.
func reportSelfEditChanges(w io.Writer, dir string, before storeSnapshot) {
	after := snapshotStore(dir)
	if before.unreadable || after.unreadable {
		// The store couldn't be opened as a contained directory on at least one
		// side -- a --self-edit agent may have swapped it for a symlink. Say so
		// rather than build a diff from an empty side, which would misreport
		// every file as added or deleted.
		fmt.Fprintln(w, "🛑 self-edit: the project store could not be read to report changes (it may have been replaced during the session)")
		return
	}
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

	// byre.config gets special handling: a content diff when both snapshots
	// captured it as a readable regular file, else an honest status line.
	// Change detection rides the signature map (which encodes absent /
	// regular-hash / oversize / non-regular uniformly), so a config swapped for a
	// FIFO or device reads as a change, never a false "(deleted)".
	bSig, bHas := before.sigs[config.ProjectConfigName]
	aSig, aHas := after.sigs[config.ProjectConfigName]
	if bHas != aHas || bSig != aSig {
		fmt.Fprintln(w, "   byre.config (applies on the next develop):")
		switch {
		case !aHas:
			fmt.Fprintln(w, "      (deleted)")
		case !bHas:
			fmt.Fprintln(w, "      (created)")
		}
		// A present-but-unreadable side (device/FIFO/oversize) has no captured
		// bytes to diff; an absent side is just the empty string. Name which side
		// lacked bytes so the notice can't contradict the (deleted)/(created)
		// line above (e.g. a config oversized at session start, then deleted).
		beforeUnreadable := bHas && !before.configReadable
		afterUnreadable := aHas && !after.configReadable
		switch {
		case !beforeUnreadable && !afterUnreadable:
			// Both sides diffable. Any byte change yields hunks (a final-newline-only
			// edit shows a "\ No newline" marker), so unequal content never prints a
			// bare header.
			for _, l := range unifiedDiff("byre.config (session start)", "byre.config (now)", string(before.config), string(after.config)) {
				fmt.Fprintln(w, "      "+l)
			}
		case beforeUnreadable && afterUnreadable:
			fmt.Fprintln(w, "      (not a readable regular file before or after — cannot show a diff)")
		case afterUnreadable:
			fmt.Fprintln(w, "      (now present but not a readable regular file — cannot show a diff)")
		default: // beforeUnreadable only
			fmt.Fprintln(w, "      (was not a readable regular file at session start — cannot show a diff)")
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
