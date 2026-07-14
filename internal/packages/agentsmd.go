package packages

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// agentsMDTitle is how byre recognizes its own past writes across versions:
// a file that does not start with it is user-placed and gets preserved, not
// clobbered, on takeover. Keep it stable forever.
const agentsMDTitle = "# ~/.byre -- a guide for coding agents\n"

// agentsMD is the byre-owned guide landed at ~/.byre/AGENTS.md for coding
// agents (and humans) operating on the store host-side. Ownership contract
// (stated in its first paragraph): byre regenerates it, edits are
// overwritten. Content is version-independent so the file only rewrites
// when the binary's copy actually changes.
const agentsMD = agentsMDTitle + `
byre generates this file and rewrites it as byre evolves. Do not edit it
-- your edits will be overwritten. Keep notes in a file of your own; byre
ignores anything in this directory it does not own.

This is byre's host-side store: the machine-wide config baseline,
reusable config layers, packages, and each project's live sandbox
config. It deliberately lives outside every project tree: the agent
inside a byre box sees the project, never this directory, so a boxed
agent cannot rewrite its own sandbox. If you are reading this you are
operating on the HOST, where that wall does not protect anyone -- these
conventions are what stands in its place.

## The map, with write rules

- ` + "`default.config`" + ` -- the machine-wide baseline, bottom layer of the
  config cascade (default -> template -> project). Yours to edit; every
  box on this machine feels it.
- ` + "`templates/<name>/template.config`" + ` -- named, reusable config layers
  (` + "`template = \"go\"`" + ` in a project config picks one). Shape only: a
  template cannot enable skills or pick an agent. Yours to edit.
- ` + "`skills/`" + ` -- local packages: editable skill directories, bare
  (` + "`skills/mything/`" + `) or owner-nested (` + "`skills/you/mything/`" + `). The
  directory IS the package -- no install step, no manifest hashing.
  Yours to edit.
- ` + "`packages/`" + ` -- installed packages: content-addressed, hash-verified
  snapshots under ` + "`packages/<digest>/`" + `, plus ` + "`index.toml`" + ` recording
  what was acquired from where. NEVER edit anything here. Snapshots are
  the record of what was installed; an edited snapshot makes every
  digest byre prints a lie. To change an installed package, fork it
  (below).
- ` + "`bundled/`" + ` -- a display mirror of the packages compiled into the
  byre binary, regenerated on every byre version change. byre never
  loads from it; edits are ignored and destroyed.
- ` + "`projects/<id>/`" + ` -- each project's live store: ` + "`byre.config`" + ` (the
  consent document -- see the next section), the recorded project
  ` + "`path`" + `, the generated Dockerfile and build context, locks and
  markers. Machine state, mostly byre-written.
- ` + "`skills.legacy/`" + `, ` + "`templates.legacy/`" + ` -- parked leftovers from old
  byre versions (` + "`byre skill archive-legacy`" + ` puts them there). Never
  loaded.

Anything not listed is byre's plumbing; leave it alone.

## The one rule with teeth: projects/<id>/byre.config

That file is the project's consent document: the human-reviewed record
of what its box is allowed to see and do. It is stored HERE, not in the
project tree, precisely so the agent in the box cannot grant itself
capabilities. A host-side agent quietly editing it defeats the entire
design.

So: do not add or widen grants -- mounts, env, ports, egress, volumes,
skills, raw dockerfile/run_args blocks -- unless the user asked for that
exact change, and say plainly what you wrote. The right route for config
that originates in a repo is a ` + "`byre.preset`" + ` committed there and
applied by the human with ` + "`byre preset apply`" + `; it gets a proper
review, diff, and confirm. Do not bypass that flow by writing the store
file directly.

## Use byre's verbs, not mv/cp

The store has identity rules -- bare vs ` + "`owner/name`" + ` ids, names
retired by old versions, digest-keyed snapshots, an install index.
Hand-moved directories turn into conflict or LEGACY rows, not working
packages.

    byre skill list                       what the catalog sees, and why
    byre skill inspect <id|uri>           a package's full trust surface
    byre skill install <uri> --digest sha256:...    acquire, pinned
    byre skill uninstall <id>
    byre skill fork <id> <new-id>         immutable -> editable copy
    byre skill init <name>                start a fresh local skill
    byre skill validate [name]
    byre skill pack <name>                emit a distributable manifest
    byre skill archive-legacy             park leftover legacy dirs

` + "`byre template`" + ` has the same verbs (except ` + "`archive-legacy`" + `, which
lives under ` + "`byre skill`" + ` and archives both kinds). When byre reports
a missing package it prints the exact install command -- run that, don't
improvise.

## Changing a provided package: layer, don't fork-edit

Never edit ` + "`bundled/`" + ` or ` + "`packages/`" + ` in place (see above). In order
of preference:

1. Config first. Most behavior differences belong in the cascade --
   ` + "`default.config`" + ` for the machine, the project config for one box.
2. Add a local skill alongside. Skills compose; a small skill of your
   own that adds the missing piece keeps the provided package intact
   and upgradable.
3. Fork as the last resort: ` + "`byre skill fork <id> <you>/<name>`" + ` gives
   an editable copy under your id -- and permanently stops tracking
   upstream. Enable the fork instead of the original where you want it.

## Authoring and composing skills

A skill is a portable bundle of opinion: it can contribute Dockerfile
lines and files (build), mounts/env/args (runtime), agent context, and
named volumes (state). One skill, one opinion -- compose small skills
rather than growing a monolith.

- Start with ` + "`byre skill init <name>`" + `; the manifest is the
  ` + "`[package]`" + ` block in ` + "`skill.toml`" + `. Keep ` + "`byre skill validate`" + `
  clean.
- A package's existence changes what is AVAILABLE, never what runs.
  Enabling -- listing its id in a config layer's ` + "`skills = [...]`" + ` --
  is the only grant, and enabling a skill is trusting it: its
  Dockerfile block builds the image and its grants open the box.
- Grant minimally. Open only the skill's own functional endpoints
  (deny-by-default), and declare everything: an undeclared capability
  is the first thing a review of your skill will flag.

## Version control and multiple machines

Do NOT ` + "`git init`" + ` this directory as a whole: the store mixes source
you author (local skills, templates, ` + "`default.config`" + `) with
per-machine state (project stores, the install index, regenerated
mirrors), and a repo spanning both syncs state that must not travel.

Version the source pieces instead:

- Keep your local skills in a git repo cloned AT ` + "`skills/<owner>/`" + `
  (or ` + "`skills/`" + ` itself if everything in it is yours). The catalog
  walker skips dot-directories, so the ` + "`.git/`" + ` inside is invisible to
  byre. The same works under ` + "`templates/`" + `.
- ` + "`packages/`" + ` needs no versioning (it lands its own ` + "`.gitignore`" + `):
  snapshots are re-acquirable byte-for-byte. Reproducibility comes from
  pinned install commands -- a ` + "`[sources]`" + ` hint (uri + digest) in the
  config or preset that references a package makes byre print the exact
  command wherever it turns up missing.
- ` + "`default.config`" + ` is small and machine-specific; if you want it
  versioned, a symlink into a dotfiles repo works -- byre reads through
  it.

## Sharing skills with others

Give the skill a qualified id (` + "`owner/name`" + `) and a version in its
` + "`[package]`" + ` block, then ` + "`byre skill pack <name>`" + ` -- it emits a
manifest with a per-file payload list and prints the package digest
plus a ready ` + "`--digest`" + `-pinned install command. Host the files
anywhere https serves them raw (a git tag on GitHub works); consumers
install by manifest URL. For private code, ` + "`file:`" + ` URLs from a local
clone are the path -- the fetcher does no auth. Put a ` + "`[sources]`" + `
hint in any config or preset that references the package.

## Where the full story lives

` + "`byre --help`" + ` for the CLI; https://github.com/pjlsergeant/byre for
the README, architecture notes, and design records.
`

// ensureAgentsMD lands the byre-owned agent guide at the store root,
// rewriting it whenever the on-disk copy differs from the binary's --
// self-healing by design: this is the file that tells host-side agents
// what not to touch, so a stale or edited copy is the worst one to keep.
//
// Two hazards of "byre owns this path" are handled here, not assumed away:
// a pre-existing file byre never wrote (agents conventionally create
// AGENTS.md) is moved aside to AGENTS.md.bak once, never destroyed; and the
// write is stage+rename, which replaces a symlink at the path itself
// rather than following it into some unrelated target file.
func ensureAgentsMD(home string, out io.Writer) error {
	path := filepath.Join(home, "AGENTS.md")
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() {
		cur, rerr := os.ReadFile(path)
		if rerr == nil && bytes.Equal(cur, []byte(agentsMD)) {
			return nil
		}
		// Past byre writes all start with the stable title line; anything
		// else (or unreadable) is user-placed -- preserve it. Rename, not
		// copy, so an unreadable file is still saved whole. A .bak that
		// already exists keeps the ORIGINAL takeover; later foreign copies
		// are edits to a file whose first line says byre overwrites it.
		if rerr != nil || !bytes.HasPrefix(cur, []byte(agentsMDTitle)) {
			bak := path + ".bak"
			if _, berr := os.Lstat(bak); os.IsNotExist(berr) {
				if os.Rename(path, bak) == nil && out != nil {
					fmt.Fprintln(out, "byre: ~/.byre/AGENTS.md existed but is not byre's -- preserved it as AGENTS.md.bak")
				}
			}
		}
	}
	tmp, err := os.CreateTemp(home, ".agents-md-*")
	if err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(agentsMD); err != nil {
		tmp.Close()
		return fmt.Errorf("agents guide: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("agents guide: %w", err)
	}
	if out != nil {
		fmt.Fprintln(out, "byre: wrote ~/.byre/AGENTS.md (byre-owned agent guide)")
	}
	return nil
}
