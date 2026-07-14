# Skills and templates as packages

How to discover, install, author, and publish byre packages. Mechanics
reference: `ARCHITECTURE.md`; decision record: `adr/0029`; vocabulary:
`GLOSSARY.md` (Packages).

A **package** is a skill or a template. Where it came from is its
provenance, and `byre skill list` / `byre template list` always show it:

```text
claude (byre/claude)          bundled v0.2.0     The Claude Code agent...
pjlsergeant/codereview        installed 1.0.0 (sha256:36609376...)  byre-codereview...
pete/my-linter                local              my WIP linter skill
```

- **bundled** -- shipped inside your byre binary, immutable. Display
  copies live at `~/.byre/bundled/` for reading; edits there are ignored
  (the loader reads the binary). A bundled package owns its bare name:
  `claude` always means `byre/claude`.
- **local** -- plain directories under `~/.byre/skills/` and
  `~/.byre/templates/`, yours to edit. Nest them (`pete/my-linter`) or
  keep them bare.
- **installed** -- immutable snapshots fetched from a URL, hash-verified
  file by file, stored under `~/.byre/packages/`.

Whatever the provenance, **nothing runs until a box's config enables
it**. Installing is acquisition, like cloning a repo; the grant is the
`skills = [...]` / `agent = ...` / `template = ...` line in a config,
per box, as ever.

## Installing

```sh
byre skill inspect https://raw.githubusercontent.com/owner/repo/v1.0.0/skills/thing/skill.toml
byre skill install https://raw.githubusercontent.com/owner/repo/v1.0.0/skills/thing/skill.toml --digest sha256:...
```

`inspect` on a URL fetches and verifies the package and renders its full
trust surface -- contributions, grants, payload hashes, digest -- without
installing anything. `install` does the same and then snapshots it; the
review leads with the grant summary. `--digest` pins the whole package
to the bytes the person who handed you the command reviewed (a git tag
can be moved; the digest can't lie) -- published commands should carry
it, and installs record the digest either way, so a later change at the
same URL surfaces as a replacement review rather than sliding in.

Reinstalling the same id is a no-op on an identical digest, and a
**replacement review** otherwise: version and digest before/after,
payload adds/changes/removals, new or dropped grant declarations (raw
Dockerfile lines shown verbatim), and the boxes whose configs reference
the id -- replacement is machine-wide, and those boxes run the new code
at their next launch, so it always confirms (`--yes` in scripts).
Installing an id that configs already reference gets the same treatment:
that install is activation.

`byre skill uninstall <id>` lists the referencing boxes first, confirms,
and removes the snapshot; a box left referencing a missing package fails
loudly at develop with the exact reinstall command.

Sources are `https://` URLs or local paths (`file:` works too). Private
https (auth) is not supported yet -- clone privately and install from
the path.

## Authoring and publishing

A local skill is a directory with a `skill.toml`; `byre skill init
<name>` scaffolds one, and `byre skill fork <id> <new-id>` copies any
immutable package into a local editable one (the only way to modify
bundled/installed content). `byre skill validate` runs the full strict
parse; broken local packages also show as INVALID rows in `list` with
the reason.

To publish, declare identity in `[package]` -- a qualified id
(`owner/name`), a `version`, and a `requires_byre` constraint -- then:

```sh
byre skill pack owner/name > skill.toml   # the distribution manifest
```

`pack` enumerates every file in the package directory (the manifest
itself excepted -- it cannot contain its own hash), computes sha256s and
exec bits, emits the manifest, and prints the package digest plus a
ready `install --digest` command. Host the manifest with its payload
files beside it (payload paths resolve relative to the manifest, same
origin only) -- a raw GitHub URL at a tag works as-is. Re-publishing is
edit, bump `version`, re-pack, re-tag: installers see a replacement
review of exactly what changed.

Templates get the identical verb set (`byre template ...`). Templates
are *shape* (base image, packages, egress offers); they never reference
other packages -- composition belongs in a preset.

## Presets

A preset is a complete config proposal -- a saved answer to the setup
questions -- conventionally shipped as **`byre.preset`** in a repo.
Cloning gives you a file, not a prompt:

```sh
byre preset inspect          # the review, read-only
byre preset apply            # review + install walk-through + write
```

`apply` validates the preset, then walks you through installing any
referenced packages you don't have (each install gets its own grant
summary and confirm -- declining any is fine), shows the composed box's
full grant review with a diff against your current config, and on your
confirm writes it as this project's `byre.config`. A preset's
`[sources]` table supplies the pinned URLs and digests for that
walk-through, and for the error remedies if you decline and develop
later. Both verbs also take an explicit path or https URL.

After applying, develop and status stay quiet while the repo's preset
matches what you applied, and note when it changes ("differs from the
version you applied -- `byre preset apply` to review the changes") or
was never applied. Notes, never prompts.
