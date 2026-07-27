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

**A -- the lock (2 sites, `internal/lock/lock.go`). DONE (`49f533b7`).**
`hostopen.OpenLockFile` (`O_CREATE|O_RDWR|O_NOFOLLOW|O_NONBLOCK`, fd-judged)
and `hostopen.SameFileAt` for the post-flock re-check. The re-check turned out
to be the sharper bug: it resolved the pathname a DIFFERENT way than the open
did (`os.Stat` where the open now refuses to follow), so a symlink planted in
the window read as "still mine".

**B -- atomic publish (7 sites: `config.go` x2, `enginerecord.go` x2,
`project.go` x2). DONE (`d8fe8045`).** `hostopen.PublishFile` /
`PublishFileExclusive`. The window each site left open was that `CreateTemp`
and `Rename`/`Link` resolved the destination directory SEPARATELY; the
primitive opens it once and does both through that descriptor. (The
`project.go` third site was `MkdirAll`, not a publish -- it belongs to D.)

**C -- existence probes (9 sites: `rehome.go` x3, `refscan.go`, `reset.go`,
`onboard.go`, `preset.go` x4). DONE (`80c4ecf7`).** `hostopen.ExistsNoFollow`
/ `StatNoFollow`. Most were `os.Stat`, which resolves the leaf -- byre asking
about its own record and being answered about the link's target.
`ExistsNoFollow` keeps "provably nothing here" and "could not look" apart,
because two callers act on absence. NOT swept, as warned: the config family
(following is `ParseFile`'s explicit trust argument), and `rehome.go`'s probe
of a recorded project path, where following is the whole question -- a project
reached through a symlinked path still lives there.

**D -- probes on the agent tree (13 sites: `exitreport.go` x4, `forget.go`
x4, `build/context.go` x2, `refscan.go`'s ReadDir, `rehome.go`'s and
`project.go`'s `MkdirAll`).** The Lstat ones are ALREADY no-follow: the
exposure is not the leaf, it is that the PARENT is agent-shapeable, so the
primitive is an anchored probe (`LstatIn(root, rel)`, `ReadDirIn(root, rel)`),
not a no-follow flag. The two `MkdirAll`s want a third shape: create byre's
own tail of a path without following a component byre created, while the
user-owned head (a symlinked `~/.byre` is a legitimate arrangement) still
resolves. Bigger and more delicate than A-C; do it deliberately, not as a
sweep.

**E -- user-named paths that may be in-tree (9 sites: `grabhost.go` x2,
`configui/listitem.go` x2, `seed.go` x2, `skills/claudeskills.go` x2,
`runparams.go`).** THE ONLY GROUP THAT NEEDS A RULING. Each call handles both
a user's own path (`~/.claude`) and a path inside the project the agent can
shape, because `checkContainedHostSource` deliberately permits in-tree
sources. No primitive fixes that. The choices are a ruling ("in-tree seed
sources are the user's problem"), or a runtime split (anchor when the path
resolves inside an agent-writable root, plain when it does not).

## Order

A, B, C are done: 18 sites, five primitives, no doctrine attached -- exactly
as predicted, none of them needed a ruling. 22 remain. Next is D. E waits for
Pete.
