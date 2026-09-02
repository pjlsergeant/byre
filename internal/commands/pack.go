package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/hostopen"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
	"github.com/pjlsergeant/byre/internal/version"
)

// PackagePack emits a local package's distribution manifest — to Out, or to
// outPath when set. The file write happens strictly AFTER Pack has read the
// whole package, so outPath may name a file inside the packed directory
// (the adopted-repo layout); the shell-redirect spelling truncates that
// same file before byre reads it.
func PackagePack(s Streams, kind packages.Kind, name, outPath string) error {
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
	if outPath != "" {
		if err := packOutGuard(ent.Dir, ent.Primary, outPath); err != nil {
			return err
		}
		if err := hostopen.PlainWriteFile(outPath, manifest, 0o644, hostopen.UserNamed); err != nil {
			return err
		}
		fmt.Fprintf(s.Err, "byre: packed %s -> %s (sha256:%s)\n", ent.ID, outPath, digest)
	} else {
		if _, err := s.Out.Write(manifest); err != nil {
			return err
		}
		fmt.Fprintf(s.Err, "byre: packed %s (sha256:%s)\n", ent.ID, digest)
	}
	fmt.Fprintf(s.Err, "      Publish the manifest with its payload files beside it, then hand out:\n")
	fmt.Fprintf(s.Err, "      byre %s install <manifest-url> --digest sha256:%s\n", kind, digest)
	return nil
}

// packOutGuard refuses a -o target that names a packed PAYLOAD: the manifest
// already records that file's hash, so writing over it ships a distribution
// that fails its own integrity check at install. Inside the package
// directory only the primary is a valid target (the one file pack excludes
// from the payload list). Both sides resolve through symlinks — the adopted
// store path and the repo checkout are the same directory under different
// names. Probe failures degrade to allowing the write (an unreadable dir
// already failed Pack; a nonexistent parent fails at the write, attributed).
func packOutGuard(dir, primary, outPath string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	rdir, err := hostopen.PlainEvalSymlinks(absDir, hostopen.UserNamed)
	if err != nil {
		return nil
	}
	// Absolute FIRST, then resolve the WHOLE path: a relative input stays
	// relative through the resolve, and filepath.Rel(absolute, relative)
	// below errors — which the degrade arm would read as "outside the
	// package", failing open for exactly the in-package spelling
	// (`cd repo && pack id -o README.md`). The final component matters too:
	// a symlink to an in-package payload (following it at the write would
	// overwrite the payload from a name outside the package), or an
	// in-package link pointing out (harmless — the bytes land elsewhere).
	// Only a nonexistent leaf falls back to parent+base: a name that does
	// not exist is not a packed payload (pack enumerated existing files).
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return nil
	}
	out, err := hostopen.PlainEvalSymlinks(absOut, hostopen.UserNamed)
	if err != nil {
		rparent, perr := hostopen.PlainEvalSymlinks(filepath.Dir(absOut), hostopen.UserNamed)
		if perr != nil {
			return nil
		}
		out = filepath.Join(rparent, filepath.Base(absOut))
	}
	if out == filepath.Join(rdir, primary) {
		return nil
	}
	rel, err := filepath.Rel(rdir, out)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	return fmt.Errorf("-o %s would overwrite a packed payload: the manifest already records this file's hash, so the output would fail its own install verification — inside the package dir, only %s is a valid target", outPath, primary)
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
	// Printed to be pasted, so the URI is terminal-escaped AND shell-quoted
	// (ADR 0029): a path or URI carrying shell metacharacters must run as the
	// one argument this line names, not expand into different argv.
	fmt.Fprintf(s.Out, "Not installed. To install:\n  byre %s install %s --digest sha256:%s\n",
		kind, packages.ShellArg(packages.EscapeTerminal(uri)), acq.Digest)
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
	if st, err := hostopen.PlainStat(arg, hostopen.HostUserOwned); err == nil && !st.IsDir() {
		return true
	}
	return false
}
