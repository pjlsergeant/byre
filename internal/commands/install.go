package commands

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/skills"
	"github.com/pjlsergeant/byre/internal/version"
)

func stage2For(kind packages.Kind) func([]byte) error {
	if kind == packages.KindSkill {
		return skills.ValidatePrimaryBytes
	}
	return config.ValidateTemplateBytes
}

func PackageInstall(s Streams, kind packages.Kind, uri, expectDigest string, yes bool) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}

	acq, err := packages.Acquire(&packages.Fetcher{}, uri, kind, version.Semver(), stage2For(kind))
	if err != nil {
		return err
	}
	id := acq.Core.ID
	if expectDigest != "" {
		want := strings.ToLower(strings.TrimPrefix(expectDigest, "sha256:"))
		if want != acq.Digest {
			return fmt.Errorf("digest mismatch: expected sha256:%s, fetched package is sha256:%s -- refusing (the bytes are not what the hint promised)", want, acq.Digest)
		}
	}

	idx, err := packages.ReadIndex(home)
	if err != nil {
		return err
	}
	old, replacing := idx[id]

	// One id, one kind: an installed skill is never silently overwritten by a
	// template (or vice versa) -- every stored reference means the old kind,
	// and the grant review below diffs primaries of ONE kind.
	if replacing && old.Kind != string(kind) {
		return fmt.Errorf("%q is already installed as a %s; refusing to change its kind -- uninstall the %s first, then install this %s", id, old.Kind, old.Kind, kind)
	}

	// A local package already claiming the id is a conflict-to-be: refuse
	// with the remedy rather than manufacturing a conflict row. The one
	// non-installed row allowed through is an INVALID row over an indexed id
	// -- a broken installed package, exactly the state whose printed remedy
	// is this reinstall.
	brokenInstalled := false
	if ent, ok := cat.Lookup(id); ok && ent.Provenance != packages.ProvInstalled {
		if replacing && ent.Provenance == packages.ProvInvalid {
			brokenInstalled = true
		} else {
			return fmt.Errorf("%s %q already exists (%s); rename or remove it before installing this id", ent.Kind, id, ent.ProvenanceLabel())
		}
	}

	// Same ID, same digest: no-op -- unless the installed copy is
	// broken, where re-landing the same verified bytes IS the repair.
	if replacing && old.Digest == acq.Digest && !brokenInstalled {
		dataf(s.Err, "byre: %s %s is already installed (sha256:%s...) -- nothing to do\n", id, old.Version, acq.Digest[:8])
		return nil
	}

	hits := scanReferences(home, cat, id)

	switch {
	case replacing && old.Digest == acq.Digest:
		// Repair: the digest pins these to the exact bytes originally
		// consented to, but boxes referencing the id flip from a resolve
		// error back to running code -- activation-shaped, so it confirms.
		dataf(s.Err, "byre: %s %s is installed but its snapshot is broken -- reinstalling the same verified bytes (sha256:%s...)\n", id, old.Version, short(acq.Digest))
		if len(hits) > 0 {
			dataf(s.Err, "Boxes referencing it (currently failing to resolve) run it again at next launch:\n%s", escaped(renderRefHits(hits)))
		}
		if err := requireConsent(s, yes, "Repair? [y/N]: "); err != nil {
			return err
		}
	case replacing:
		// Same ID, different digest: replacement -- machine-wide scope,
		// affected boxes enumerated, package-level diff, grant declarations
		// called out. TTY confirm or --yes; never a silent default.
		dataf(s.Err, "byre: replacing %s\n", id)
		dataf(s.Err, "  installed: %s (sha256:%s...)\n", old.Version, short(old.Digest))
		dataf(s.Err, "  candidate: %s (sha256:%s...)\n", acq.Core.Version, short(acq.Digest))
		printPayloadDiff(s.Err, home, old, acq)
		printGrantDelta(s.Err, home, old, acq)
		fmt.Fprintln(s.Err, "Replacement is machine-wide: every box referencing this id runs the new code at its next launch.")
		if len(hits) > 0 {
			dataf(s.Err, "Affected boxes:\n%s", escaped(renderRefHits(hits)))
		} else {
			fmt.Fprintln(s.Err, "No stored config currently references it.")
		}
		if err := requireConsent(s, yes, "Replace? [y/N]: "); err != nil {
			return err
		}
	case len(hits) > 0:
		// Install-as-activation: dangling references flip from failing
		// to running new code. Treated like a replacement.
		dataf(s.Err, "byre: %s is not installed, but stored configs already reference it -- installing ACTIVATES it there at next launch:\n%s", id, escaped(renderRefHits(hits)))
		printAcquiredSummary(s.Err, acq)
		if err := requireConsent(s, yes, "Install and activate? [y/N]: "); err != nil {
			return err
		}
	default:
		// Fresh ID, no references: a verified download that grants nothing.
		// TTY sees the summary and confirms; non-TTY proceeds.
		printAcquiredSummary(s.Err, acq)
		if s.TTY && !yes {
			if !confirmed(s.Err, s.In, "Install? [y/N]: ") {
				return fmt.Errorf("install declined")
			}
		}
	}

	snap := packages.Snapshot{
		ID: id, Digest: acq.Digest, Primary: acq.Primary,
		Manifest: acq.Manifest, Files: acq.Payloads, Exec: acq.Exec,
		Entry: packages.IndexEntry{
			Digest: acq.Digest, Version: acq.Core.Version, Kind: string(kind),
			URI: uri, InstalledAt: time.Now().UTC().Format(time.RFC3339),
		},
		// The consent above was given against this prior state; the land
		// step re-checks it under the store lock (TOCTOU guard).
		ExpectPrior: old.Digest,
		// A broken snapshot dir must be rewritten even when content-addressing
		// says it is already present.
		Repair: brokenInstalled,
	}
	if err := packages.WithStoreLock(home, func() error { return packages.LandSnapshot(home, snap) }); err != nil {
		return err
	}
	dataf(s.Err, "byre: installed %s %s (sha256:%s...)\n", id, acq.Core.Version, short(acq.Digest))
	// The closer must not walk back the consent narrative just accepted:
	// only a FRESH, unreferenced install grants nothing; replacement
	// and activation change what referencing boxes run next launch.
	switch {
	case replacing && old.Digest == acq.Digest:
		fmt.Fprintln(s.Err, "      Snapshot repaired; referencing boxes resolve it again at their next launch.")
	case replacing:
		fmt.Fprintln(s.Err, "      Boxes referencing this id run the new code at their next launch.")
	case len(hits) > 0:
		fmt.Fprintln(s.Err, "      The boxes listed above run it at their next launch.")
	default:
		fmt.Fprintln(s.Err, "      Installed -- grants nothing until enabled in a box.")
	}
	return nil
}

// requireConsent enforces the consent rule for state-changing steps: TTY
// asks; a pipe refuses without --yes.
func requireConsent(s Streams, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !s.TTY {
		return fmt.Errorf("state-changing confirmation never defaults: re-run with --yes, or on a TTY")
	}
	if !confirmed(s.Err, s.In, prompt) {
		return fmt.Errorf("declined")
	}
	return nil
}

func short(digest string) string {
	if len(digest) >= 8 {
		return digest[:8]
	}
	return digest
}

// printAcquiredSummary is the install grant summary: the same contribution set
// inspect leads with, rendered from the acquired manifest.
func printAcquiredSummary(w io.Writer, acq *packages.Acquired) {
	dataf(w, "Package: %s %s (%s), sha256:%s...\n", acq.Core.ID, acq.Core.Version, acq.Kind, short(acq.Digest))
	if acq.Core.Description != "" {
		dataf(w, "  %s\n", acq.Core.Description)
	}
	if acq.Kind == packages.KindSkill {
		if f, err := skills.ParsePrimaryBytes(acq.Manifest); err == nil {
			printSkillContributions(w, f)
		}
	} else {
		printTemplateShape(w, acq.Manifest)
	}
	fmt.Fprintf(w, "Payloads: %d file(s), hash-verified.\n", len(acq.Files))
}

// printPayloadDiff shows destination-level payload changes between the
// installed snapshot's manifest and the candidate (package-level diff).
func printPayloadDiff(w io.Writer, home string, old packages.IndexEntry, acq *packages.Acquired) {
	oldEnt, err := readInstalledManifest(home, old, acq.Primary)
	if err != nil {
		dataf(w, "  (old payload list unavailable: %v)\n", err)
		return
	}
	oldByDest := map[string]packages.FileEntry{}
	for _, e := range oldEnt {
		oldByDest[e.Dest] = e
	}
	var added, changed, removed []string
	newDests := map[string]bool{}
	for _, e := range acq.Files {
		newDests[e.Dest] = true
		prev, ok := oldByDest[e.Dest]
		switch {
		case !ok:
			added = append(added, e.Dest)
		case !strings.EqualFold(prev.SHA256, e.SHA256) || prev.Executable != e.Executable:
			changed = append(changed, e.Dest)
		}
	}
	for dest := range oldByDest {
		if !newDests[dest] {
			removed = append(removed, dest)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	for _, d := range added {
		dataf(w, "  payload added:   %s\n", d)
	}
	for _, d := range changed {
		dataf(w, "  payload changed: %s\n", d)
	}
	for _, d := range removed {
		dataf(w, "  payload removed: %s\n", d)
	}
	if len(added)+len(changed)+len(removed) == 0 {
		fmt.Fprintln(w, "  payloads unchanged (manifest text differs)")
	}
}

func readInstalledManifest(home string, e packages.IndexEntry, primary string) ([]packages.FileEntry, error) {
	raw, err := readSnapshotPrimary(home, e.Digest, primary)
	if err != nil {
		return nil, err
	}
	return packages.ParseManifestFiles(raw)
}

// printGrantDelta calls out grant DECLARATIONS present in the candidate but
// not the installed version (declarations, not per-box effective grants
// -- a project layer may override them).
func printGrantDelta(w io.Writer, home string, old packages.IndexEntry, acq *packages.Acquired) {
	oldRaw, err := readSnapshotPrimary(home, old.Digest, acq.Primary)
	if err != nil {
		// The installed side is unreadable (broken snapshot): there is no
		// delta to trust, so show every candidate declaration rather than
		// silently implying "nothing changed".
		lines := make([]string, 0)
		for l := range grantLines(acq.Kind, acq.Manifest) {
			lines = append(lines, l)
		}
		sort.Strings(lines)
		if len(lines) > 0 {
			fmt.Fprintln(w, "Installed version unreadable -- candidate grant declarations (in full, not a diff):")
			for _, l := range lines {
				dataf(w, "  + %s\n", escaped(l))
			}
		}
		return
	}
	oldLines := grantLines(acq.Kind, oldRaw)
	newLines := grantLines(acq.Kind, acq.Manifest)
	var fresh, dropped []string
	for l := range newLines {
		if !oldLines[l] {
			fresh = append(fresh, l)
		}
	}
	for l := range oldLines {
		if !newLines[l] {
			dropped = append(dropped, l)
		}
	}
	sort.Strings(fresh)
	sort.Strings(dropped)
	if len(fresh) > 0 {
		fmt.Fprintln(w, "New or widened grant declarations in the package:")
		for _, l := range fresh {
			dataf(w, "  + %s\n", escaped(l))
		}
	}
	if len(dropped) > 0 {
		// Removals change the trust surface too (they are CHANGED contributions).
		fmt.Fprintln(w, "No longer declared:")
		for _, l := range dropped {
			dataf(w, "  - %s\n", escaped(l))
		}
	}
}

// grantLines renders a manifest's contribution summary and returns its lines
// as a set, for declaration-level diffing.
//
// The depth ruling, in full, because it is cited from here and lives nowhere
// else: a SUMMARY renders build content as counts ("3 dockerfile lines" --
// printSkillContributions), which is the right depth for a reader deciding
// whether to look at a package at all. A DIFF cannot: it is what the reviewer
// consents against on a replacement, and a count is stable under a total
// rewrite of what it counts, so a swapped `RUN` line would land under an
// unchanged summary. Raw Dockerfile commands and run_args are therefore
// included verbatim here (escaped, marked not-introspected). The asymmetry is
// deliberate; do not "align" the two.
func grantLines(kind packages.Kind, raw []byte) map[string]bool {
	var b strings.Builder
	if kind == packages.KindSkill {
		if f, err := skills.ParsePrimaryBytes(raw); err == nil {
			printSkillContributions(&b, f)
			for _, l := range f.Build.Dockerfile {
				dataf(&b, "dockerfile (not introspected): %s\n", l)
			}
		}
	} else {
		printTemplateShape(&b, raw)
		if cfg, err := config.ParseTemplateBody(raw); err == nil {
			for _, l := range append(append([]string{}, cfg.DockerfilePre...), cfg.DockerfilePost...) {
				dataf(&b, "dockerfile (not introspected): %s\n", l)
			}
			for _, a := range cfg.RunArgs {
				dataf(&b, "run_arg: %s\n", a)
			}
		}
	}
	out := map[string]bool{}
	for _, l := range strings.Split(b.String(), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			out[t] = true
		}
	}
	return out
}

func readSnapshotPrimary(home, digest, primary string) ([]byte, error) {
	return hostopen.PlainReadFile(filepath.Join(packages.SnapshotDir(home, digest), primary), hostopen.StoreOwned)
}

func PackageUninstall(s Streams, kind packages.Kind, id string, yes bool) error {
	home, err := project.Home()
	if err != nil {
		return err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return err
	}
	idx, err := packages.ReadIndex(home)
	if err != nil {
		return err
	}
	var ent *packages.Entry
	var contested, shadowedBy *packages.Entry
	if row, present := idx[cat.ExpandAlias(id)]; present {
		// The index is authoritative for what uninstall can remove: the
		// catalog may hold an INVALID row (broken snapshot) or a CONFLICT row
		// (a local dir fighting over the id) in the installed package's
		// place, and both remedies need this removal to work. An index kind
		// that parses as neither skill nor template is itself a problem row;
		// let either verb remove it.
		k := packages.Kind(row.Kind)
		if k != packages.KindSkill && k != packages.KindTemplate {
			k = kind
		}
		ent = &packages.Entry{ID: cat.ExpandAlias(id), Kind: k, Provenance: packages.ProvInstalled}
		// A CONFLICT row means another claimant survives this removal and
		// becomes the id's sole provider -- that is activation, not cleanup,
		// and the consent below must say so. A LOCAL row shadowing this
		// install means the opposite: the local copy is ALREADY the provider,
		// so removal changes nothing at launch.
		if cr, ok := cat.Lookup(id); ok {
			switch {
			case cr.Provenance == packages.ProvConflict:
				contested = cr
			case cr.Provenance == packages.ProvLocal && cr.ShadowsInstalled != "":
				shadowedBy = cr
			}
		}
	} else {
		var ok bool
		ent, ok = cat.Lookup(id)
		if !ok {
			return fmt.Errorf("package %q is not installed", id)
		}
	}
	if ent.Provenance != packages.ProvInstalled {
		switch ent.Provenance {
		case packages.ProvBundled:
			return fmt.Errorf("%q is bundled inside byre and cannot be uninstalled (disable it per box instead)", ent.ID)
		case packages.ProvLocal:
			return fmt.Errorf("%q is a local package: it is a plain directory under ~/.byre -- delete it there if you mean to", ent.ID)
		default:
			return fmt.Errorf("%q is not an installed package (%s)", ent.ID, ent.ProvenanceLabel())
		}
	}
	if ent.Kind != kind {
		return fmt.Errorf("package %q is a %s; use `byre %s uninstall`", ent.ID, ent.Kind, ent.Kind)
	}

	hits := scanReferences(home, cat, ent.ID)
	// A contested id with exactly two claimants, one of them THIS snapshot,
	// means removal ACTIVATES the survivor. Both-local contests (the
	// snapshot got shadowed before a third claimant collided) stay contested
	// whatever happens to the snapshot, and an empty claimant list (a
	// conflict row from an unexpected path) gets the conservative
	// wording -- never promise activation we cannot prove.
	takeover := contested != nil && len(contested.Claimants) == 2 && slices.Contains(contested.Claimants, packages.SnapshotDir(home, idx[ent.ID].Digest))
	switch {
	case contested != nil:
		dataf(s.Err, "byre: %s is contested: %s\n", ent.ID, contested.Reason)
		if takeover {
			fmt.Fprintln(s.Err, "Removing the installed copy leaves the other claimant as this id's SOLE provider -- it loads normally from then on.")
		} else {
			fmt.Fprintln(s.Err, "Removing the installed copy leaves the id contested among the remaining claimants -- it stays unresolvable until one remains.")
		}
	case shadowedBy != nil:
		dataf(s.Err, "byre: the installed %s is shadowed by the local package at %s -- boxes already run the local copy; removing the snapshot changes nothing at launch.\n", ent.ID, shadowedBy.Dir)
	}
	if len(hits) > 0 {
		switch {
		case takeover:
			dataf(s.Err, "byre: these configs reference %s -- their boxes run the surviving claimant at next launch:\n%s", ent.ID, escaped(renderRefHits(hits)))
		case contested != nil:
			dataf(s.Err, "byre: these configs reference %s -- their boxes keep hitting the conflict error at next develop:\n%s", ent.ID, escaped(renderRefHits(hits)))
		case shadowedBy != nil:
			dataf(s.Err, "byre: these configs reference %s -- their boxes keep running the local package:\n%s", ent.ID, escaped(renderRefHits(hits)))
		default:
			dataf(s.Err, "byre: these configs reference %s -- their boxes hit a resolve error at next develop:\n%s", ent.ID, escaped(renderRefHits(hits)))
		}
	}
	if err := requireConsent(s, yes, fmt.Sprintf("Uninstall %s? [y/N]: ", ent.ID)); err != nil {
		return err
	}
	if err := packages.WithStoreLock(home, func() error { return packages.RemoveInstalled(home, ent.ID) }); err != nil {
		return err
	}
	dataf(s.Err, "byre: uninstalled %s\n", ent.ID)
	switch {
	case takeover:
		fmt.Fprintln(s.Err, "      The surviving claimant now provides this id; referencing boxes load it at their next launch.")
	case contested != nil:
		fmt.Fprintln(s.Err, "      The id remains contested among the remaining claimants.")
	case shadowedBy != nil:
		fmt.Fprintln(s.Err, "      The local package now solely provides this id.")
	case len(hits) > 0:
		fmt.Fprintln(s.Err, "      Referencing boxes will print the reinstall remedy when they next resolve.")
	}
	return nil
}
