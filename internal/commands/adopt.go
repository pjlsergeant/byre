package commands

import (
	"fmt"
	"path/filepath"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// PackageAdopt establishes a directory as the local source for the package
// id it declares: a symlink at the store path the id names. This is the
// round trip back from a distribution repo — on any machine that isn't the
// original authoring one, the repo checkout is the source and the catalog
// knows the id only as installed; adopt says "the source is here" so pack
// and editing work again under the same id. The identity checks mirror
// ingestLocal's, and the catalog's own reload is the final gate: an entry
// that lands as anything but LOCAL rolls the link back out.
func PackageAdopt(s Streams, kind packages.Kind, dir string) error {
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
	// The link must survive a cwd change, so it carries the absolute path.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	prim := packages.PrimaryName(kind)
	raw, err := packages.ReadPrimaryBounded(filepath.Join(abs, prim))
	if err != nil {
		return fmt.Errorf("adopt: %s is not a readable %s package dir (%v)", abs, kind, err)
	}
	m, hasPkg, err := packages.ParseManifestCore(raw)
	if err != nil {
		return fmt.Errorf("adopt: %s: %w", prim, err)
	}
	if !hasPkg || m.ID == "" {
		return fmt.Errorf("adopt needs %s to declare an id in [package] (id = \"owner/name\") — the id names the store path the link goes to", prim)
	}
	id := m.ID
	if err := packages.ValidateID(id, true); err != nil {
		return err
	}
	if m.Kind != "" && m.Kind != string(kind) {
		return fmt.Errorf("%s declares kind %q; use `byre %s adopt`", prim, m.Kind, m.Kind)
	}
	if packages.IsBare(id) && cat.IsProtected(id) {
		return fmt.Errorf("%q is protected; the package needs a qualified id (owner/name)", id)
	}
	if packages.Owner(id) == "byre" {
		return fmt.Errorf("byre/* is reserved for bundled packages")
	}
	if prev, ok := cat.Lookup(id); ok {
		switch prev.Provenance {
		case packages.ProvLocal:
			return fmt.Errorf("%q is already a local package at %s; remove that first", id, prev.Dir)
		case packages.ProvInstalled:
			// The authoring case: the local entry will shadow the snapshot,
			// announced below once the reload confirms it.
		default:
			return fmt.Errorf("%q already occupies the catalog (%s); resolve that first: %s", id, prev.Provenance, prev.Reason)
		}
	}
	dest := filepath.Join(home, packages.StoreSubdir(kind), filepath.FromSlash(id))
	// Lstat, not Stat: a dangling link at the store path is still an
	// occupant to refuse, not an invisible slot.
	if _, err := hostopen.PlainLstat(dest, hostopen.StoreOwned); err == nil {
		return fmt.Errorf("%s already exists; remove it first", dest)
	}
	if err := hostopen.PlainMkdirAll(filepath.Dir(dest), 0o755, hostopen.StoreOwned); err != nil {
		return err
	}
	if err := hostopen.PlainSymlink(abs, dest, hostopen.StoreOwned); err != nil {
		return err
	}
	// The reload is the real validation: the linked dir goes through the
	// exact ingest path (strict parse, stage-2, compat, id-vs-path) every
	// future load will use. Anything but a LOCAL entry means adopt would
	// leave a broken or conflicted store — take the link back out and say
	// why.
	cat2, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		_ = hostopen.PlainRemove(dest, hostopen.StoreOwned)
		return err
	}
	ent, ok := cat2.Lookup(id)
	if !ok || ent.Provenance != packages.ProvLocal {
		_ = hostopen.PlainRemove(dest, hostopen.StoreOwned)
		reason := "the catalog did not load it"
		if ok && ent.Reason != "" {
			reason = ent.Reason
		}
		return fmt.Errorf("adopt rolled back: %s", reason)
	}
	dataf(s.Err, "byre: adopted %s -> %s\n", id, dest)
	if ent.ShadowsInstalled != "" {
		dataf(s.Err, "      shadows installed %s; remove the link to fall back to the snapshot\n", ent.ShadowsInstalled)
	}
	return nil
}
