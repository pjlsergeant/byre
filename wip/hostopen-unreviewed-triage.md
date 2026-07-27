# The 40 `hostopen.Unreviewed` sites -- triaged by which primitive removes them

**Status: PARKED mid-work, 2026-07-28.** The ban shipped (see ADR-less commits
`c68a2312`, `697e9208`, `1619d5b2`); 226 call sites carry a `Reason`, and 40 of
them carry `Unreviewed`, which means nobody had checked the three routes.
`rg hostopen.Unreviewed` is always the live list -- trust it over this file if
they disagree.

Delete on absorb: when a group below is done, cut it; when all are, delete the
file.

## The reframe that produced this list

I had been about to ask Pete one global question -- *does `--self-edit` make a
project's own store agent-writable?* -- which all four classifying reviewers
had split on. He asked instead whether the lock file ever has a good reason to
be a symlink. It does not. That turns a doctrine ruling into a two-line fix,
and the useful question becomes per-path and usually obvious:

> **Does byre ever need to FOLLOW a symlink at this path?**

Where the answer is no, refusing to follow removes the site from the contested
set without any ruling. Where it is yes (config files people deliberately
symlink into `~/.byre`), following is a real requirement, not an oversight.

Second, larger point, now recorded in `plain.go`: **reach for a primitive
before reaching for a Reason.** A Reason says "this plain call happens to be
fine here"; a primitive makes the safe behaviour unavoidable for every future
caller. Most exemptions are a primitive nobody has written yet.

## The groups

**A -- the lock (2 sites, `internal/lock/lock.go`).** `OpenFile` +
the post-flock re-check `Stat`. Primitive: `OpenLockFile(path)` doing
`O_CREATE|O_RDWR|O_NOFOLLOW`, fd-judged. The re-check gets better than
no-follow: fstat the descriptor already held, so no pathname is resolved
twice at all. This was the headline example in the whole `--self-edit`
argument and it needs no ruling.

**B -- atomic publish (7 sites: `config.go` x2, `enginerecord.go` x2,
`project.go` x3).** All the same shape: `CreateTemp` in a directory, write,
then `Rename`/`Link` onto the destination. These are byre's own records;
nobody symlinks them. One primitive that publishes through a root anchored at
the destination directory removes all seven.

**C -- existence probes on byre-owned records (9 sites: `rehome.go` x4,
`refscan.go` x2, `reset.go`, `onboard.go`, `preset.go`'s applied marker).**
"Does this record exist" never needs to follow. `ExistsNoFollow(path)` takes
them. CAREFUL: this does NOT extend to config-family files -- a user
symlinking `~/.byre/default.config` out of a dotfiles repo is a supported
arrangement, and `ParseFile` already carries an explicit follow flag for it.

**D -- probes on the agent tree (13 sites: `preset.go` x3, `exitreport.go`
x4, `forget.go` x4, `build/context.go` x2).** These are ALREADY no-follow.
The exposure is not the leaf, it is that the PARENT is agent-shapeable, so
the primitive is an anchored probe (`LstatIn(root, rel)`,
`ReadDirIn(root, rel)`), not a no-follow flag. Bigger and more delicate than
A-C; do it deliberately, not as a sweep.

**E -- user-named paths that may be in-tree (9 sites: `grabhost.go` x2,
`configui/listitem.go` x2, `seed.go` x2, `skills/claudeskills.go` x2,
`runparams.go`).** THE ONLY GROUP THAT NEEDS A RULING. Each call handles both
a user's own path (`~/.claude`) and a path inside the project the agent can
shape, because `checkContainedHostSource` deliberately permits in-tree
sources. No primitive fixes that. The choices are a ruling ("in-tree seed
sources are the user's problem"), or a runtime split (anchor when the path
resolves inside an agent-writable root, plain when it does not).

## Order

A, B, C first -- 18 sites, three primitives, no doctrine attached. Then D.
E waits for Pete.
