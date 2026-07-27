# First outside review -- what is left

**Status: the review is CLOSED except for the items below, which are
deferred with reasons rather than untriaged.** Four reviewers, one brief,
whole repo, 2026-07-27 (codex and grok via `byre-codereview`, Opus 5 and
Fable 5 as subagents; the brief is `review-brief.md`, re-runnable). Every
finding either shipped over 2026-07-27/28 or appears below.

**Shipped**, each with tests and a codereview round: the `[env]` launch-gate
vector and the reserved `BYRE_` namespace; the empty-`env_from_host` delivery
lie and its exposure/annotation siblings; closed rows counting as effective
grants; the unbounded probe fan-out; self-edit invented deletions; the Claude
Skills staging bound; `agent-writable` as one predicate; the forged-worktree
comment; `agent = "none"` destruction; the editor's missing save lock;
cwd-derived preset symlink following; `files` duplicate sources; port-block
identity; `ResolveProposed` slice aliasing; deliver's missing terminal
escaping and its unbounded control reply; the TUI dependency upgrade; four
absences (gate ordering, `writeEnv`, `ReadFileBounded` refusals + the
`SameFile` guard, `bash -n` in CI); and eight doc-drift rows. See ADR 0050,
ADR 0051, and the commits of those two days.

**Two findings changed shape on contact.** ADR 0011's "or raw build line"
clause was MEASURED against a real engine and is false -- `COPY` writes
through a re-pointed symlink -- so the ADR narrowed to `files` clobbers
(the raw tier is covered by claim degradation, which is what the new
integration test pins). And the config-format downgrade gap resolved to
error wording alone: a `config_api` stamp was specced and consciously not
built, because the case already fails loudly.

**What follows** is deferred work, not a backlog of unexamined findings. Each
line says what it is; the judgment calls (podman CI money, fuzzing, the
conformance-walk widening) were made deliberately.

Delete-on-absorb: cut a line when it ships or is ruled out for good.

## Absences (tests and mechanisms)

- **`capBuffer`, the anti-OOM stderr cap, has zero tests** (`runner.go:604`),
  despite a comment stating its security purpose. *(Fable.)*
- **The hostopen conformance walk covers reads only** -- `watchedCallees` is
  `{ReadFile, Open, OpenFile}`, so writes (the class that produced a real bug)
  and probes (45 `os.Stat`, 11 `os.Lstat`, 14 `os.ReadDir`) are unguarded; and
  the allowlist is keyed `file + callee`, so a new unreviewed call in an
  already-listed file rides the existing entry. *(Opus.)*
- **No fuzz tests, no `testdata/`.** Obvious targets: `config.Parse`, and a
  `tomldoc` Load→edit→Bytes→Parse round-trip, since every config write rides
  go-toml's *unstable* parser (ADR 0044). *(Opus.)*
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

## Dependencies -- DONE 2026-07-28

bubbletea/bubbles/lipgloss upgraded (two majors), verified by pane-text
diff on the inttest runner; Dependabot widened to grouped monthly gomod;
`textdiff` kept, with the reasoning at its one call site.

## The reviewers' nine questions -- all answered

Kept because each answer is now a decision somebody could otherwise
re-litigate; the ones that became doctrine cite where.

1. **Is the `[env]` -> chassis-env seam user-reachable by design?** No. `BYRE_`
   is reserved VOCABULARY, never reserved capability: refused in `[env]` with
   the exact `run_args` remedy, accepted-but-rendered from a skill, raw tier
   untouched (ADR 0050).
2. **Does `preset apply` with no argument count as "naming" the path?** No --
   byre derived it from the cwd. The no-argument form now refuses a symlink
   like the drift probe does; an explicit path still follows.
3. **Is config forward-incompatibility a ruling or an oversight?** A ruling
   now: downgrade is not supported, and the error says so. A `config_api`
   stamp was designed and consciously not built.
4. **Is the editor deliberately outside the setup lock?** It was an oversight.
   Project-target writes ride the lock and refuse on drift, with a wholesale
   overwrite prompt.
5. **Are `env_from_host` / `npm_global` / `files` / `shared_auth`
   grandfathered under P6?** STILL OPEN -- the only question with no answer.
   P6 says the editor reaches every config feature "always"; these four have
   no editor surface, and `env_from_host` is grant-class. Either they get
   screens or P6 gets a stated exception.
6. **Was release signing considered and declined?** Declined on the record now
   (ADR 0051), with attestation deferred and its trigger written down.
7. **Does ADR 0049 intend a mechanical enforcement arm?** It has none, and
   that is now VISIBLE rather than assumed: the doctrine index marks it
   `[no arm]`, and every review checks the index.
8. **Should the `--self-edit` banner name the worktree-sharing scope?** Not
   yet ruled -- see the absences list above, where it sits with P5's
   consent-at-the-scope-of-effect argument attached.
9. **Is `bubbletea` at v1.1.0 deliberate?** It was drift. Upgraded, and
   Dependabot now watches the maintenance half.
