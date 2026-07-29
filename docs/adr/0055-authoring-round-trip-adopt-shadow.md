# The authoring round trip: adopt links a directory in, local shadows installed

Decided 2026-07-29, from a real failed release of `pjlsergeant/codereview`:
on any machine but the original authoring one, the distribution repo is all
a publisher has, the catalog knows the id only as *installed*, and `pack`'s
remedy pointed at `fork` — the wrong tool (it copies the OLD payload to a
NEW id). The store-copy workaround lived in a skills repo README; the
symlink escape hatch failed silently. Four coupled decisions close the loop.

Principles: P1 (the store is the user's own tree; the threat model is the
agent, which cannot write `~/.byre` — so byre honors the user's arrangement
of it rather than policing it); P2 (legibility: the shadow is announced on
every provenance surface, never silent). Related: ADR 0029 (packages),
ADR 0041 (provenance order).

## The decisions

**1. The store walk follows symlinked package dirs.** `loadLocal` judged
entries by `DirEntry` type, so a symlinked package dir vanished with no
entry and no problem row — while the same file's documented policy for the
primary FILE ("the store is the user's own tree and a symlinked primary is
their choice") said follow. Walk entries are now judged by what they
resolve to, at both the owner and the package level; a dangling or non-dir
link is skipped like any stray file. "Link the git repo into the store" is
a working authoring arrangement.

**2. Local shadows installed, announced.** One id both installed and local
(same kind) was a duplicate-id CONFLICT row — the package died on exactly
the machine a publisher needs it, where their own preset installed it. Now
the local entry displaces the installed one: the working copy outranks the
immutable snapshot on the machine that has both, `pip -e`/`npm link`
semantics. The label says so — `local (shadows installed <version>)` — on
every surface that renders provenance, and the snapshot stays on disk,
loading again the moment the local entry goes. Any other same-id pairing
(kind mismatch, double local) stays a conflict. Uninstall's disclosures
follow: removing a shadowed snapshot is cleanup ("boxes already run the
local copy"), and the takeover promise requires the snapshot to actually be
a conflict claimant.

**3. `byre skill|template adopt <dir>`** is the round trip as a verb: read
the id the directory's manifest declares, apply ingest's identity rules,
symlink the directory to the store path the id names — then let a catalog
reload be the real gate (strict parse, stage-2, compat, id-vs-path), rolling
the link back out if the entry lands as anything but LOCAL. Refusals name
their rule: no declared id, kind mismatch, occupied store path (a dangling
link is an occupant), an existing local package elsewhere. `pack`'s
provenance refusal now names adopt for same-id publishing and fork for
new-id copies.

**4. `pack -o <file>` writes after every read.** The blessed workflow makes
`pack <id> > <manifest inside the linked dir>` the obvious release command,
and the shell truncates the target at exec — before byre reads it. `-o`
opens the output only once the whole package is in hand, so the
file-inside-the-packed-dir spelling is safe; adopt's success hint prints
it. Inside the package directory, only the primary is a valid target: a
`-o` naming a packed PAYLOAD would emit a manifest recording that file's
pre-overwrite hash and then replace the file — a distribution failing its
own install verification — so it refuses, with the path fully
symlink-resolved on both sides (the repo checkout, the store link, and a
leaf symlink from outside are all the same file).

## What this does not decide

Project-config `files` semantics are untouched (guard re-assertion, ADR
0039's territory). Cross-skill destination collisions are ADR 0056.
