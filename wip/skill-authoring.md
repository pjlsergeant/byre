# Brief: the skill-publishing round trip has no supported path

Written 2026-07-29 from a real failed release of `pjlsergeant/codereview`
(the pjlsergeant-byre-skills repo). All file:line references are byre @
`95abf4f3`. Everything below was hit live or verified in source; nothing is
hypothesised.

## The story, quickly

Byre's authoring model: local packages live in `$BYRE_HOME/skills/<owner>/<name>`,
`byre skill pack` emits the manifest from there, and the author publishes
manifest + payloads (pack's own stderr says so). The distribution git repo is
the *output* of that flow.

The gap is the round trip **back**. On any machine other than the original
authoring one — new devbox, post-wipe, collaborator — the git repo is all you
have, and the catalog knows the package only as *installed* (immutable,
digest-pinned). There is no verb that says "the source for `pjlsergeant/codereview`
is this directory". The release attempt went:

```
$ byre skill pack pjlsergeant/codereview > skills/codereview/skill.toml
byre: pack works on local packages; "pjlsergeant/codereview" is installed (fork it first)
```

- `fork` is the suggested remedy but is wrong for this case: it copies the
  **old installed payload** to a **new id**. The new payload is in the repo,
  and the id must stay the same.
- `init` makes a blank skeleton.
- The obvious escape hatch — symlink the repo's skill dir into the store —
  fails **silently** (bug 1 below).
- The working answer turned out to be hand-copying the dirs into a hidden
  directory, which nothing documents.

## Bug 1: `loadLocal` silently skips symlinked package dirs

Repro (live, byre on the host, 2026-07-29):

```
$ mkdir -p ~/.byre/skills/pjlsergeant
$ ln -s ~/pjlsergeant-byre-skills/skills/codereview ~/.byre/skills/pjlsergeant/codereview
$ byre skill list          # package absent entirely — no entry, no problem row
$ byre skill pack pjlsergeant/codereview
byre: package "pjlsergeant/codereview" not found
```

Cause: `internal/packages/catalog.go` `loadLocal` (~:324) walks the store with
`PlainReadDir` + `e.IsDir()` / `s.IsDir()` checks at both the owner and the
package level. `PlainReadDir` is bare `os.ReadDir`
(`internal/hostopen/plain.go:156`), whose `DirEntry` for a symlink reports
type symlink, not dir — so the entry is skipped with no record.

Two reasons this is a bug and not a policy:

1. **It contradicts the file's own stated policy.** `catalog.go:749-751`:
   "Symlinks are followed (follow=true): the store is the user's own tree and
   a symlinked primary is their choice". That holds for the primary-read path
   and not for the directory walk that decides existence.
2. **The skip is silent**, which is against the legibility doctrine everywhere
   else in this file: every other declined load gets `addProblem` and shows up
   as a problem row. A package that vanishes from `list` with no explanation
   is the worst version of unsupported.

Fix shape (either is fine): resolve the entry with a stat on the joined path
instead of trusting `DirEntry` type, or keep the restriction and add a problem
row ("symlinked package dir; not followed") so the user learns in `list`
rather than from `not found`.

## Gap 2: no repo→store adoption verb

Ask: a first-class way to (re)establish a local package from a directory.
Possible shapes, no strong preference:

- `byre skill adopt <dir>` — reads the manifest's id, links or copies into the
  store at the matching path; refuses on id/store-path mismatch (the existing
  `ingestLocal` rule).
- or `byre skill pack --source <dir>` — pack straight from a directory,
  sidestepping the store for the one command that needs it.

Either turns "hand-copy into `~/.byre/skills/<owner>/<name>` after reading the
catalog source" into a supported, documented step. Anyone who distributes
skills from more than one machine needs this; today's `fork it first` error
actively points at the wrong tool for the most likely reason a publisher hits
it.

Related nit while in there: if symlinked stores become supported, note that
`pack <id> > <same file in the linked dir>` self-truncates the manifest before
byre reads it (shell truncates at exec). A `pack -o <file>` that writes after
reading would close that; a docs warning also suffices.

## Finding 3 (separate, source-verified): cross-skill destination collisions are unchecked

While answering an unrelated question in byre's source: `skills.go:874-887`
refuses two `[build] files` sources for one destination and the comment calls
it "an authoring mistake with a silent consequence" — but `destBy` is built
per-manifest, so the check is **intra-skill only**. Two *different* skills
declaring the same absolute dest pass resolve; `internal/build/context.go`
(~:485-508) emits one COPY block per skill, stable-ordered by
`provenanceRank`, so the collision resolves by build order, last writer wins,
silently.

This is live in the field today: `pjlsergeant/devlog` and
`pjlsergeant/codereview` both ship `/usr/local/lib/byre-devlog-lib.sh`
(deliberately, so each works alone). They are safe only because the copies are
byte-identical — a property enforced by a test in *their* repo, not by byre at
compose time. If the copies ever diverge (e.g. one package's lib gains a
function the other's lacks), the loser's consumer sources a lib missing the
symbol it calls, under `set -e`, decided by build order.

Ask: extend the one-dest-one-source rule across the composed skill set at
resolve time — or, if cross-skill same-dest shipping is meant to be supported
(the dual-ship pattern above is real), require the colliding sources to be
byte-identical and refuse otherwise. Either way the current silent
last-writer-wins is exactly the consequence the intra-skill check exists to
prevent.

## What the skills repo did in the meantime

Documented the store-copy workaround in its README's publishing section, and
releases with `cp -r` into the store. No action needed on that side; this
brief exists so byre can make the workaround unnecessary.
