# The project tree is a host-execution channel; byre reports, never gates

Decided 2026-07-26, from an external user report. The project mount is
not only a place the agent can read and leak -- it is a place the agent
can write code that YOUR HOST runs, later, as you. byre states this
plainly and points out the changes it happens to notice when a session
ends. It does not gate, block, or claim coverage.

Principles: P1 (the footgun doctrine -- legibility instead of gates,
degrade the claim rather than refuse the configuration); P4 (a claim
byre cannot stand behind is degraded, never silently asserted).
Related: ADR 0003, ADR 0009, ADR 0026.

## The fact

`~/project -> /workspace (rw)` is the one grant every box has, and the
project directory is a set of files the host executes. An agent that
writes `.git/hooks/pre-commit`, or a git config naming a program
(`core.hooksPath`, `credential.helper`, `core.sshCommand`,
`core.fsmonitor`, a `filter.*` or `diff.*` helper, `init.templateDir`,
a `!`-prefixed alias), gets its code run the next time the human runs a
host-side command that reaches it -- a commit, a checkout, a fetch, an
`init`, depending on what was planted -- as them, with their keys and
their machine. Not necessarily the very next command, and not certain;
what is certain is that ordinary use of the repository is the trigger,
and no container escape is involved. The container's strength never
enters into it.

Three properties make it worth acting on rather than shrugging at:

1. **It is invisible to the review the user actually performs.** The git
   admin directory is outside the working tree and never appears in
   `git status` or `git diff`; `.env*` is conventionally ignored.
   Everything else the agent writes DOES appear in the diff, where
   reviewing it is already the user's job.
2. **Host-side git is the ordinary path.** Host credentials are boxed by
   default and the core `env_from_host` layer passes git identity only
   -- "not your `.gitconfig`, not git credentials" (ARCHITECTURE, ADR
   0026) -- so review, commit and push usually happen host-side. Note
   this is *usual*, not forced: an agent with network plus a minted
   token or `gh auth` can push from the box.
3. **The network is irrelevant to it.** Exfiltration needs the open
   network; this needs only the mount. A user who enabled the firewall
   skill has not addressed it -- and the previous ARCHITECTURE wording
   ("an agent with BOTH can exfiltrate...") actively invited that
   mistake.

`byre worktree` widens it: `runparams.go` binds the common git dir into
every worktree box read-write (a linked worktree has no object store of
its own), so an agent in a throwaway worktree box can write hooks and
config that fire for the human's git in the main tree and every sibling
worktree.

## What had already been decided, and where it stopped

ADR 0009 reached this insight for byre's OWN git: "a git checkout runs
the repository's own code ... That is exactly the code byre's model
keeps inside the box", and moved `byre worktree`'s git into a container.
It then declined to generalise -- CHANGES for v1.1.0 says "a worktree you
make yourself with `git worktree add` is unchanged -- that was always
yours", which is true of a repo you cloned and false of a repo an
unsupervised agent has had rw access to for hours.

ADR 0003 ("the config that defines the sandbox must live outside the
sandbox") is the same insight one step short: git config and git hooks
are also configuration that runs code, and they live inside the sandbox.

## The decision

**Say it, and mention what we happen to notice. Do not gate.**

1. The docs state the channel: ARCHITECTURE's contract bullet no longer
   bounds project-rw to exfiltration, and the security model carries a
   sharp fact ("What comes out of the box may be executable").
2. A session-end **exit report** names changes byre noticed in a small,
   fixed set of places -- git config's exec-relevant keys, the hooks
   directory, `config.worktree`, `.env*` -- in the register of "we
   thought you should know", explicitly not a list of everything.

byre is not a security product (see the threat model): against an
actively malicious agent it takes some simple precautions and claims
nothing more. The exit report is one of those precautions.

## Why a report is not the allowlist ADR 0009 rejected

ADR 0009 rejected "enumerating and disabling every exec-capable git
config key host-side" as "a losing allowlist against a moving target".
That ruling stands and is not reopened -- it is a ruling about a GATE.
An incomplete gate fails silently and leaves the user believing they are
covered. An incomplete report just reports less, and says so in its own
text ("byre checks a handful of places, not everything"). The key list
in the exit report is RANKING, not gating: its job is to keep the
message rare enough to read, not to be exhaustive.

## Rejected: shadow the hooks directory read-only

A read-only bind over the hooks directory was proposed and tested. It is
defeated by one line -- `git config core.hooksPath elsewhere` -- verified
empirically: with writes to `.git/hooks` genuinely denied, a hook in
`elsewhere/` still fired on `git commit`. Closing that too would mean
mounting the git config read-only, which breaks `git remote add`,
git-lfs, and byre's own in-box `worktree add` registration (ADR 0009
deliberately runs it in the box, where it writes the common git dir).

It is also a gate, which P1 forbids where a report will do -- and it
would only stop an agent that wouldn't think to set `core.hooksPath`,
i.e. an accidental one, which is not a failure mode that writes hooks.

## The half byre owns: the tools byre itself runs

This ADR is about the code the USER's host runs later. There is a second
half it did not cover, and the exit report's own probes sat squarely in
it: byre spawns host tools automatically -- the engine CLI, and the
`git` these probes ride -- and it resolved them by bare name, re-reading
PATH on every call. A host PATH listing an absolute directory the box
can write (a direnv `.venv/bin`, a project `.bin`) ahead of the real
binary meant byre ran whatever `git` or `docker` sat there, at a session
end nobody typed a command for.

That is byre's own construction, not the user's arrangement, so it is
byre's to fix (P4: warrant what byre builds). Every host-side spawn now
resolves through one helper (`internal/hostexec`): the absolute path is
pinned once per invocation, and a binary resolved out of a directory
this project's box can write -- the work tree, the main tree, the common
git dir, byre's store for the project -- is declined by name, with the
path and the root, so the fix is reordering PATH. No checksums, no
signature checks, no judgement of binary CONTENT: where the binary was
resolved FROM is the whole test, and byre still passes no judgement on
the user's PATH.

Dispositions follow the caller, not a single rule: `develop` finding its
ENGINE there is a hard refusal (nothing safe can proceed), while the
exit report finding `git` there DEGRADES -- the probes are skipped and
the loss is disclosed once, before the session -- because a session end
must never be blockable by the thing it reports on. That last one is
this ADR's own rule turned on itself: the report is a notice, and a
notice that can block a session is a gate.

The disposition that is easiest to get wrong is a THIRD one, and two
independent reviewers found it in the first cut: a command that
ENUMERATES engines must not treat "declined" as "not installed". An
absent engine holds nothing; a declined one may be holding this
project's live session while byre cannot look. So the enumerators
report the two separately, `develop`'s single-session check takes a
declined engine as uncheckable (a loud note, and a place in the skipped
set so the engine record stays unresolved), and the commands that speak
in TOTALS -- `forget`, `reset`, `rehome` -- refuse rather than quietly
enumerate one fewer. Skipping it there would let `forget` delete the
store while that engine's volumes, images and credentials stayed
behind, under the word "completely".

## Accepted residuals

Stated here so none of them is later "discovered" as a bug:

- **The include graph is not followed.** `include.path` and `includeIf`
  can pull exec-capable settings out of a file byre does not read. The
  sharp fact discloses this; implementing git's config semantics would
  be the losing allowlist again.
- **Ignored trees are not watched** (`node_modules/`, `venv/`, build
  output). Two walks of 100k+ files per session for a marginal gain over
  directories already full of third-party code nobody reads.
- **`.envrc` is not watched.** direnv already gates it: its allow record
  is `sha256(path + "\n" + content)`, so an edited `.envrc` re-blocks
  until `direnv allow` runs again. Duplicating that adds noise, not
  safety.
- **The report is not guaranteed to run.** `SIGKILL`, terminal loss, or
  a host reboot skips the after-snapshot. Nothing may be worded as
  "byre always tells you when a session ends".
- **Concurrent sibling worktree sessions share the common git dir**, so
  one session's report can name another's change. Attribution is
  deliberately passive ("these changed"), the same choice
  `reportSelfEditChanges` already makes for the store.
- **`byre shell` establishes no snapshot of its own**; its writes are
  covered only by the owning `develop` process.
- **Detection, not prevention.** An unread report protects nobody.
- **A project rooted at the filesystem root is outside the check.**
  `hostexec.NewRoots` drops "/" from every root set: as a root it declines
  every binary on the machine, and a Finder launch runs byre from cwd "/"
  (project resolution resolves any directory, so "/" arrived as the work
  tree and the deliver app declined both engines). The cost is a box
  someone deliberately runs on a project AT "/" -- the project-tree
  coverage of the check is gone for it (the store root still guards),
  and not vacuously so: the box writes what the host user writes, so
  root-owned binaries stay beyond it while user-owned PATH entries do
  not. Accepted 2026-08-26, on user responsibility: a project
  at "/" hands the box the user's entire writable surface by explicit
  choice ("anyone running byre at / knows the risks"), byre does not
  gate the user's own configuration (P1), and the pre-fix alternative
  guarded nothing either -- it declined every binary, root-owned ones
  included, refusing the configuration outright.
- **Sibling worktrees are not in the box-writable root set.** The only
  listing of them lives in the common git dir, which the box writes, so
  trusting it would let an agent choose which host directories byre
  refuses to run tools from -- a refusal it can aim is worse than the
  shadow it would close. The work tree, the main tree and the common git
  dir are byre's own resolution and cover the case.
