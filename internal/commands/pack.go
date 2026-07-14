package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/version"
)

// SkillPack / TemplatePack implement `byre skill|template pack <name>`:
// the distribution manifest on stdout, the digest and a ready install hint on
// stderr.
func SkillPack(s Streams, name string) error {
	return pkgPack(s, packages.KindSkill, name)
}

func TemplatePack(s Streams, name string) error {
	return pkgPack(s, packages.KindTemplate, name)
}

func pkgPack(s Streams, kind packages.Kind, name string) error {
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
	ent, err := cat.ResolveName(name)
	if err != nil {
		return err
	}
	if ent.Kind != kind {
		return fmt.Errorf("package %q is a %s; use `byre %s pack`", ent.ID, ent.Kind, ent.Kind)
	}
	manifest, digest, err := packages.Pack(ent)
	if err != nil {
		return err
	}
	if _, err := s.Out.Write(manifest); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: packed %s (sha256:%s)\n", ent.ID, digest)
	fmt.Fprintf(s.Err, "      Publish the manifest with its payload files beside it, then hand out:\n")
	fmt.Fprintf(s.Err, "      byre %s install <manifest-url> --digest sha256:%s\n", kind, digest)
	return nil
}

// inspectURI handles `byre skill|template inspect <uri>` (phase 2):
// fetch, verify, render the trust surface -- installing nothing.
func inspectURI(s Streams, kind packages.Kind, uri string) error {
	acq, err := packages.Acquire(&packages.Fetcher{}, uri, kind, version.Semver(), stage2For(kind))
	if err != nil {
		return err
	}
	printAcquiredSummary(s.Out, acq)
	for _, e := range acq.Files {
		exec := ""
		if e.Executable {
			exec = "  (executable)"
		}
		fmt.Fprintf(s.Out, "  payload: %s  sha256:%s...%s\n",
			packages.EscapeTerminal(e.Dest), strings.ToLower(e.SHA256[:12]), exec)
	}
	fmt.Fprintf(s.Out, "\nDigest: sha256:%s\n", acq.Digest)
	fmt.Fprintf(s.Out, "Not installed. To install:\n  byre %s install %s --digest sha256:%s\n",
		kind, packages.EscapeTerminal(uri), acq.Digest)
	return nil
}

// looksLikeURI decides whether an inspect argument is a URI/path rather than
// a catalog ID. IDs win: callers try the catalog FIRST (qualified ids contain
// '/' too); this is only consulted for names the catalog does not know.
func looksLikeURI(arg string) bool {
	if strings.Contains(arg, "://") {
		return true
	}
	if strings.HasSuffix(arg, "skill.toml") || strings.HasSuffix(arg, "template.config") {
		return true
	}
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		return true
	}
	return false
}
