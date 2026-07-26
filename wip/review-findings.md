# First outside review -- findings worth keeping

**Status: PARKED, untriaged.** Companion to `philosophical-problems.md` (same
review, doctrine-level criticism). This file is the concrete half: what four
independent reviewers found, distilled. Nothing here has been actioned, and
some of it is doctrine calls rather than bugs.

Delete-on-absorb: as items are fixed, deferred with a reason, or ruled on, cut
them from here.

**Provenance.** 2026-07-27, four reviewers on the same brief, whole repo, no git
history and no devlog: **codex** and **grok** (via `byre-codereview`, full text in
`.byre-devlog/reviews.md`), **Opus 5** and **Fable 5** (subagents; full text not
retained -- this file is the distillate). "Reproduced" below means the reviewer
built a throwaway test and observed the behavior; Fable did this for several.

---

## Tier 1 -- found independently by more than one reviewer

**`[env]` can switch off the firewall launch gate, invisibly.** *(Opus + Fable,
Fable reproduced.)* `launcher.sh:30` reads `GATE_FILE="${BYRE_LAUNCH_GATE_FILE:-...}"`;
config `[env]` is emitted as image `ENV` (`gen.go:300`); `envKeyRe` accepts
`BYRE_*`. So `env = { BYRE_LAUNCH_GATE_FILE = "/dev/null" }` makes `[ -s ]` false
and the agent runs before/without the firewall, while status still prints
`deny-by-default` unhedged. Four sibling vectors to the same outcome are each
guarded *and* degrade the claim (files clobber, raw dockerfile/run_args, mount
over `/etc/byre`); this one does neither, and `status` never renders `[env]` at
all. Fix precedent is one field over: `runparams.go:43` already re-asserts
`BYRE_EGRESS` as a `-e` flag so no `[env]` key can skew it. Same class:
`BYRE_ENVD_DIR` pointed into `/workspace` sources agent-authored shell at launch.
Note the runtime override *is* documented (`launcher.sh:47`, `dockerfile.go:62`)
as an eject-path hatch -- that rationale covers a `docker run` flag, which
degrades the claim; a typed config key reaching the same seam does not.

*Reachability, verified in-session (not by a reviewer) -- this is what sets the
severity.* Who can put that key in a config: **you**, by hand or via `byre
config` (P1, legitimate, but status still lies afterwards). **A cloned repo's
`byre.preset`, via `byre preset apply`** -- the only hostile path, and it is
gated by human consent: `renderPresetReview` prints the full preset body or a
diff vs your store, terminal-escaped, so the line *is* visible as raw text. What
it does not get is a `⚠` grant line -- `review.go` builds those from mounts,
skills and `env_from_host` additions, and `[env]` is not in that set, so it
renders as one unremarkable TOML line in a body a user may skim. **A skill's
`[runtime].env`** -- inside the documented "enabling a skill is trusting it".
**The boxed agent: no** -- the store is host-side and unmounted, barring
`--self-edit`, which is already documented as transitive host trust. So this is
a legibility defect (P4), not an agent escape: the realistic exposure is a
preset from a repo you cloned, plus status asserting a posture it is not
enforcing afterwards.

**`files` duplicate sources make the Dockerfile nondeterministic.** *(Opus +
Fable, Fable reproduced: 40 renders, 7/33 split.)* `context.go:403` keys `staged`
on `filepath.Clean(src)`, so `{"seed.txt" = "/opt/A", "./seed.txt" = "/opt/B"}`
are two TOML keys collapsing to one map key; survivor is random per process.
Breaks `gen.go`'s byte-identical contract, the golden test's premise, and ADR
0001's cache-sharing rationale, and silently drops a declared build input.
`files` is the only user-facing config key that never reaches `Validate`. Fix:
reject the duplicate in `planFiles`, naming both spellings.

**No downgrade path for the config format.** *(Opus + Fable.)* `Parse` uses
`DisallowUnknownFields`; only *retired* keys are tolerated. A config touched by a
newer byre hard-fails an older binary with `unknown key(s): [context]` -- from
every command including `byre config`, the interface P6 designates for fixing
configuration. `~/.byre` is machine-wide across four documented install routes,
and downgrade-after-a-bad-release is the ordinary recovery move. ADR 0049's
machinery is thorough and points one direction only. Cheapest honest fix: the
error mentions "may have been written by a newer byre". Note package manifests
already get this right (`package_api` + lenient stage-1 parse).

**`env_from_host` reports a grant as delivered when the source resolved empty.**
*(codex headline, grok concurred.)* `addEnvFromHost` only inserts a non-empty
value; `statusInfo.EnvProvided` marks every non-disabled source provided from the
config string alone, so `hostEnvRow` prints `GIT_AUTHOR_EMAIL <- git:user.email`
when nothing reached the box. Already on TODO; codex's argument is that the
acknowledgment doesn't make the shipped contract honest, and asks whether it
blocks the next release.

## Tier 2 -- single reviewer, high confidence

- **Unbounded probe fan-out wedges `develop` before the box starts.** *(Fable,
  measured.)* `exitreport.go:271` enumerates `<gitdir>/worktrees/` with no cap,
  one serial `git config` subprocess per entry (each 5s-capped, count uncapped).
  Three planted FIFO worktree configs = 15.017s, linear; the first `snapshotExit`
  is at `develop.go:277`, before `StartAttach`. The agent creates those entries.
  Violates "unsolicited probes degrade, never block"; the bound exists one field
  over (`maxHooksEntries = 500`). The `.env*` listing (`:405`) is also uncapped.
- **The config editor destroys `agent = "none"` on any save.** *(Fable,
  reproduced -- zero-edit save drops the key.)* `form.go:368` maps through
  `config.OrNone` so literal `"none"` and absence share a picker row;
  `complete.go:194` emits `""`; `reconcile.go:36` removes the key. Since
  `Merge(…{Agent:"none"})` resolves agentless but `{Agent:""}` inherits, an
  agentless project extending an agent-bearing layer silently gains that agent.
  Round-trip destruction (P6) plus a missing off-switch (ADR 0018). Same
  mechanics, lower stakes, for `engine = "auto"`.
- **`configui.Save` takes no lock and no staleness check.** *(Opus.)*
  Read → parse → reconcile-against-open-time-snapshot → write, with the window
  being the whole editor session. With parallel worktree sessions advertised,
  session A's save reverts session B's grant-bearing change. `preset.go:157`
  does re-read-under-lock-and-abort-on-drift correctly, forty lines away. Not
  catchable by `-race` (cross-process); `preset_test.go:397` already has the
  right harness shape.
- **`agent-writable` is WorkDir-only in the build path.** *(Opus.)* `CLAUDE.md`
  says "the project tree *and anything a box can shape*"; `context.go:220,286`
  bind the classifier to one directory, consulting neither `cfg.Mounts` (rw) nor
  `CommonGitDir`. Consequence lands in `[[context]] file`: outside WorkDir,
  `readBoundedHostFile` uses `hostopen.OpenRegular(path, follow=true)` by
  pathname -- a file in an rw mount can be swapped for a symlink to `~/.ssh/id_rsa`
  between develops. `[[claude_skills]] path` is *not* affected (routes through
  `copyPath`, Lstat + no-follow).
- **The only forged-worktree guard is commented as redundant.** *(Fable.)*
  `worktree.go:133` calls the back-pointer check "Belt-and-suspenders". It is
  not: an agent writing `.git` = `gitdir: /home/u/other/.git/worktrees/w` passes
  the structural commondir check (genuine metadata is self-consistent) and only
  this check rejects it; without it `commonGitDir` becomes an rw bind of another
  repo's git dir and `mainDir` becomes that project's identity, config, volumes,
  image. Code is correct and pinned by test; the comment invites a future
  "simplification". Cheapest high-value item in the review -- comment edit only.
  (Also trips the banned-phrase rule.)
- **`internal/deliver` never calls `EscapeTerminal`.** *(Opus.)* Zero uses in the
  one package whose headers declare its input comes from inside the box; `grab.go:218`
  prints box-side filenames straight to the terminal, which can rewrite the
  scrollback that *is* the product. Same package: `grab.go:78` uses `ExecInput`
  for a control reply whose vocabulary is `"f"` or `"d <path>"`, with stdout
  uncapped by design ("stdout is the payload") -- the stderr cap's own rationale
  applies verbatim.
- **Summary counts treat closed rows as effective.** *(Fable, reproduced.)* Rows
  closed by a *lower* layer keep `rowSkill` kind (`effective.go:198`), and
  `rowCounts`/`exposureNow` count every `rowSkill`; rows closed by *this* file
  are `rowRemoved` and correctly excluded -- the asymmetry is the tell.
  Contradicts ADR 0018 and the code's own "must agree with the launch tally".
- **`--self-edit` exit report invents deletions and can be silenced.** *(Fable,
  reproduced.)* `selfedit.go:59` discards `WalkDir`'s error; for an unreadable
  *directory* the children vanish and read as deletions -- and if unreadable at
  both snapshots, changes there go unreported. The mirror code (`exitSnapshot`)
  carries five completeness fields against exactly this class, with named tests.
- **Release chain has no authenticity.** *(Opus, grok concurred.)* `install.sh`
  fetches `checksums.txt` from the same release as the binary (transport
  integrity, not authenticity); no `signs:`/`sboms:` in goreleaser, no
  attestation permission, no `-trimpath` so builds aren't reproducible; the
  Homebrew cask strips `com.apple.quarantine`. Inbound is meticulously pinned --
  the asymmetry is the finding. `install.sh` itself is competent.
- **ADR 0011's "or raw build line" clause may not hold.** *(Fable, unverified --
  needs an engine.)* The security guard re-COPYs the file, but a raw line need
  not *write* the path, it can re-point it: `RUN rm -f /etc/byre/launch-gate &&
  ln -s /dev/null /etc/byre/launch-gate`. Whether `COPY` writes through a
  destination symlink is engine-specific. **One integration test settles it**:
  build with that `dockerfile_post` plus the firewall skill, then
  `stat -c %F /etc/byre/launch-gate` -- `regular file` means the docs are right.
  Cheapest high-value verification on the list. Mitigating: raw blocks degrade
  the claim regardless, so disclosure holds either way.
- **`preset apply` with no argument follows a symlink the drift probe refuses.**
  *(Fable.)* Justified as "the user NAMED this path", but with no argument byre
  derived it from cwd. User-visible perversity: `presetState` refuses to read a
  symlinked `byre.preset` and therefore prints "this repo ships a byre.preset;
  `byre preset apply` to review it" -- steering the user into the flow that does
  follow it.
- **Concurrent-bind residual is in the ADR, not in the user docs.** *(grok.)*
  ADR 0009 accepts that bind sources are pathnames, not inode-pinned, so a
  concurrent rw session can redirect a bind in the detect-to-mount window.
  Worktrees make that concurrency first-class. Absent from
  `site/content/docs/security-model.md`. Relatedly `runparams.go`'s
  `checkContainedHostSource` comment -- "no adversary in the check-to-mount
  window: the prior session has ended" -- is true serially, false with a live
  sibling worktree.
- **Two silent-corruption bugs.** *(Opus.)* `validatePorts` allows a layer to
  hold both `{container:3000, host:3001}` and `{container:3000, remove:true}`,
  but `configui/reconcile.go` keys port blocks on `container` alone -- a save can
  destroy the user's binding. And `ResolveProposed(proj Config)` takes `Config`
  by value while `c.Skills[i] = cat.ExpandAlias(s)` mutates the shared backing
  array, so a resolve-then-save path (`mcp remove`) persists alias expansions
  never asked for.

## The pattern to sweep for

Fable named it: **"the bound exists one field over"** -- the mechanism was built
and applied to the sibling case, not this one. Instances: `maxHooksEntries` but
no worktrees cap; five completeness fields in `exitSnapshot` but one in
`storeSnapshot`; Claude Skills bounds at validate but not at stage; `BYRE_EGRESS`
re-asserted but not the gate pointer; `agent-writable` defined broadly and
implemented narrowly. This is a grep, not a rewrite. Opus proposed the general
fix shape independently: one exported predicate the build path shares.

## Absences (tests and mechanisms)

- **No test pins the launch gate's *ordering* property** -- that it waits above
  the firstrun hooks. ADR 0011's claim is positional; `TestLauncherGateTimesOutClosed`
  asserts exit code and message, and `runLauncher` points `BYRE_FIRSTRUN_DIR` at
  a non-existent dir, so no test plants a hook and asserts it did not run. A
  future edit moving the gate below the loop stays green. ~10 lines. *(Fable.)*
- **`capBuffer`, the anti-OOM stderr cap, has zero tests** (`runner.go:604`),
  despite a comment stating its security purpose. *(Fable.)*
- **`hostopen` is the least-tested package at 57.1%**; `ReadFileBounded` (14
  production call sites across 6 packages, including the whole config cascade and
  the project-identity fence) is at **0.0%** -- its three refusal branches,
  including the "an oversize file is never truncated and parsed" guarantee, have
  no test. `OpenDirRootNoFollow`'s `os.SameFile` identity guard is also 0% and
  deleting it keeps the suite green. *(Opus.)*
- **The hostopen conformance walk covers reads only** -- `watchedCallees` is
  `{ReadFile, Open, OpenFile}`, so writes (the class that produced a real bug)
  and probes (45 `os.Stat`, 11 `os.Lstat`, 14 `os.ReadDir`) are unguarded; and
  the allowlist is keyed `file + callee`, so a new unreviewed call in an
  already-listed file rides the existing entry. *(Opus.)*
- **`writeEnv`'s emit loop never executes in any test** -- no test builds an
  Input with a non-empty `Env` map, so deleting either `sort.Strings` leaves the
  suite green. The golden test uses empty maps and structurally cannot catch it.
  *(Opus.)*
- **No fuzz tests, no `testdata/`.** Obvious targets: `config.Parse`, and a
  `tomldoc` Load→edit→Bytes→Parse round-trip, since every config write rides
  go-toml's *unstable* parser (ADR 0044). *(Opus.)*
- **~2,100 lines of shipped shell with no lint, not even `bash -n`**; 13 of 19
  embedded scripts lack `set -e`/`set -u` with nothing recording which omissions
  are deliberate. `firewall.sh` is disciplined; the rest are unchecked. *(Opus.)*
- **No podman leg in CI** despite README's first-class claim and ADR 0032's
  podman-specific machinery; **no Claude leg in the agent canary** -- the
  flagship agent, installed unpinned via `curl | bash`, is the one whose upstream
  drift nothing catches. *(Opus.)*
- **`firewall.sh`'s deny probe is IPv4-only** (`1.1.1.1 8.8.8.8 9.9.9.9`) while
  `firewall-open.sh` -- the *weaker*, hygiene-only posture -- has an explicit v6
  arm reasoning that "an IPv6-only closure must not go unverified". The
  containment posture is less self-verified than the hygiene one, and ADR 0010
  reads as covering both families. *(Opus.)*
- **No top-level panic recovery in `cmd/byre`** -- a panic reaches the user as a
  raw Go stack, off-brand for a legibility-first tool. *(Opus.)*
- **`--self-edit` in a worktree under-warns**: it mounts the *project* store
  shared by the main tree and every sibling (ADR 0009); the escalation banner
  never says so, though `reportSelfEditChanges` already reasons about the
  sharing. P5 says consent is stated at the scope of its effect, and this is the
  loudest consent moment. Related: `noteSharedVolumes` warns about volumes only,
  but `forget` from a worktree also deletes the shared store and image. *(Fable.)*
- **No editor surface for `shared_auth`, `env_from_host`, `npm_global`, `files`**
  -- P6 says "for every config feature, always", and `env_from_host` is
  grant-class. `listitem.go:336`'s own remedy text instructs a hand-edit ("set
  KEY = \"\" under env_from_host **in this file**"), and the cookbook recipe for
  "use my API key instead of an agent login" is raw TOML. *(Opus + Fable.)*

## Doc drift

| Where | Says | Actually |
|---|---|---|
| ADR 0010 | "Rootful engines only in v1" | ADR 0032 made rootless podman first-class; no supersession note *(grok)* |
| `configuration-reference.md:98` | `[files]` = "host paths" | `planFiles` refuses absolute paths; sources are project-relative *(Opus)* |
| `packaging-reference.md:10` | "full authoring spec … is docs/SKILLS.md" | SKILLS.md omits `[runtime]`, `[agent]`, `companion_for`, `[context]` -- they're in ARCHITECTURE.md, and this misdirects package *authors* *(Fable)* |
| `configuration.md:10` | "`q` to leave" | no `q` binding; on a text row it types a q *(Fable)* |
| `configuration.md:33` | favourites carries "shared-login" | carries only Template and Agent; `shared_auth` has no editor surface *(Fable)* |
| `ARCHITECTURE.md:723,661` | command lists | omit `byre context add/remove/list` and `claude-skill add/remove`, which do mutate config *(Fable)* |
| `MaxConfigBytes` comment | bounds "layer files" | `config/layers.go:133,190,213` use plain unbounded `os.ReadFile` *(Fable)* |
| `install.md` | "checksum-verified" | verifies transport, not authenticity *(Opus)* |
| `/etc/byre/mcp.json` | self-described "quasi-public contract" | not covered by the security guard *or* `warnGuardCollisions`, and emitted before the project block, so a `files` entry wins silently *(Fable -- recommends a warn entry, not a guard entry)* |

## Dependencies

`bubbletea` pinned at v1.1.0, current v1.3.10 (Opus counts `bubbles` 0.20→1.0 and
`lipgloss` 0.13→1.1 as pending majors, in the most test-fragile area). Dependabot
covers Actions only, with a written rationale; govulncheck covers the CVE half,
nothing covers the maintenance half. Both reviewers frame this as P7 ("dependencies
don't make design decisions") wanting the seam owned, replaced, or accepted on the
record. Also: `akedrou/textdiff v0.1.0`, a single-author pre-1.0 module in the
trust path for one call (`textdiff.Unified` in `commands/diff.go`).

## Open questions the reviewers could not answer from the docs

1. Is the config `[env]` → chassis-env seam user-reachable *by design*? If yes,
   does status owe a degraded network claim, and should it render `[env]` at all?
2. Does `byre preset apply` with no argument count as the user "naming" that
   path? The two call sites currently disagree.
3. Is forward-incompatibility of the config format a ruling or an oversight?
   Should an unknown key from a newer byre warn rather than hard-fail?
4. Is the config editor deliberately outside the setup lock?
5. Are `env_from_host` / `npm_global` / `files` / `shared_auth` grandfathered
   under P6? If so it isn't written down.
6. Was release signing considered and declined?
7. Does ADR 0049 intend a mechanical enforcement arm?
8. Should the `--self-edit` banner name the worktree-sharing scope?
9. Is `bubbletea` at v1.1.0 deliberate?
