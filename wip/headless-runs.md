# Headless runs -- byre in CI, end to end

Status: v5 -- Pete's rulings folded (grilling 2026-11-02); no open
decisions; ready for build. Blinded rounds 1-3 (codex/grok/zai, fresh
each round) folded; the design loop is CAPPED, and the remaining
finding class (races/TOCTOU edges, test-inventory completeness)
transfers to the build's tests and the per-phase code review. Rulings
R1-R10 (section H) are Pete's, settled: do not reopen them, or the two
pieces CUT during review (the persistent codex env-key login, 2.3;
`--ephemeral`, 7), to close a finding. wip/ lifecycle: absorbed into
ADRs 0061-0063 (+0065 if adopted) + docs on ship, then deleted.

Provenance: [CODE] = verified against the tree at `v1.13.0-18-gbc549d33`
(file:line, re-derived with `nl -ba`). External-repo citations are
`pjlsergeant-byre-skills/...` at its checkout `a3189a3`
(`/home/dev/pjlsergeant-byre-skills`). [PROBED] = run live in the
planning box (claude 2.x, codex-cli 0.166.0, grok 1.0.70, no engine
present). [PROBE] = open, each with a disposition. [R n] = Pete's
ruling n (section H).

Audience: a different box/session builds this with no memory of the
planning conversation. Every claim is cited; where the tree is silent
the gap is named.

## H. Handoff to the build session

**Read first, in this order**: `CLAUDE.md`; `docs/PRINCIPLES.md`;
`docs/adr/README.md` (the doctrine index every review cites); ADRs 0006,
0040, 0047, 0050, 0053, 0057, 0058; `docs/GLOSSARY.md`; then this
document end to end; then the files 3.4 and 3.5 cite (`internal/gen/
launcher.sh`, `internal/commands/develop.go`, `internal/commands/
statusdata.go`, `internal/commands/escape.go`, `internal/hostopen/
publishstream.go`, `internal/skills/reservedenv.go`).

**Phase order**: A (hook guards) -> B (contract + `byre run`) -> C
(`requires`, grok/zai headless) and D (`preset apply --yes`) in either
order -> E (`byre build`; then, LAST, the E1 investigation). B is the
bulk; A is independent and ships first. Section 10 has the table.

**Per-phase discipline** (CLAUDE.md): commits per unit; after each
phase run `byre-codereview` AND the other reviewer (`--reviewer grok`
when codex is the default, and the reverse), independently, each given
the doctrine-index instruction verbatim; fix or consciously defer every
finding; `gofmt` + `go vet` + `go test ./...` green before every commit;
docs sweep in the same unit (ARCHITECTURE / GLOSSARY / README /
commands.md must not keep describing the pre-change behaviour).

**PROBEs to run FIRST, in a box with the CLIs and an engine** (each
names its fallback in the text): B1 (claude `stream-json`/`--verbose`,
`result` event fields, `auth status` with an env token), B3 (codex
0.166 `exec --json` event names), B5 (codex `exec` under a
per-invocation `CODEX_HOME`), B4 (grok `--prompt-file`, json shape,
adapter in headless mode), the `/dev/fd/N` reopen semantics for
`--output-last-message` (section 12), B2 (`--permission-prompts none`),
then -- after A-D ship -- E1 on the inttest VM.

**Open decisions: none.** Pete's rulings (grilling, 2026-11-02),
recorded here as settled; each is applied in the section named:

| R | Ruling (Pete, 2026-11-02, grilling) | Applied in |
|---|---|---|
| R1 | Core feature, no `pjlsergeant/task` wrapper skill | 1.2 |
| R2 | Task input: `--task <file>`, `--task -` (stdin to EOF; the never-closing-pipe hang is documented), `BYRE_TASK` in byre's own env; flag beats env with a one-line note; neither = usage error; 1 MiB cap; NO implicit `.byre-task.md` | 3.1, 3.5 |
| R3 | Verb `byre run`; result dir `.byre-run/` | 3.1 |
| R4 | Output model: no `--outcome` file; `byre run --data` prints byre's versioned JSON outcome on stdout (the `status --data` convention); without `--data` stdout carries the agent's final answer once; stderr carries everything else (agent live stream, agent stderr, byre's banners, the one-line summary); only the exit code and the `--data` JSON are authoritative, the summary line is informational and forgeable; stdout escaped on a TTY, raw when redirected; `--quiet` suppresses the live stream and byre's notices, never errors, refusals, the summary or the exit report; default is loud, noisy logs accepted for v1 | 3.1, 3.3-3.5, 3.7-3.8, 4, 9 |
| R5 | NO default timeout, ever; `--timeout` is opt-in; when set, chassis-owned timer, exit 12; docs name grok's dead-refresh hang as the case to use it | 3.4, 3.5, 3.6 |
| R6 | codex with `OPENAI_API_KEY`/`CODEX_ACCESS_TOKEN`: per-invocation temporary `CODEX_HOME` (config.toml copied, never auth.json; login via `printf '%s' ... \| codex login --with-api-key\|--with-access-token`; removed on exit); a present key beats an existing login; no env-key inference path exists (`codex exec --help`: "auth still uses `CODEX_HOME`" [PROBED]); MUST be well documented (why, when it matters, that the key wins) | 2.1, 2.3, 3.6, 3.8 |
| R7 | `preset apply --yes` REFUSES when either side carries encrypted credential rows; the error points at `env_from_host`; an extra switch is a future ADR if ever needed | 6 |
| R8 | Chassis JSON: printf + strict escaper, no jq in core; `--build-cache` stays a flag, and only exists if E1 is adopted | 3.4, 8 |
| R9 | Phase E: keep `byre build`; E1 is investigation-only and runs LAST after A-D ship; v1 promises no hosted-runner speed-up and the docs say hosted runners rebuild per job | 8, 10 |
| R10 | `--ephemeral` stays cut | 7 |

Settled earlier, not open: `byre run` needs no implicit
`--credentials skip` (zero rows is already a no-op, credentials.go:
156-158); no persistent codex env login (2.3).

## 0. What this is, in one paragraph

Today `byre develop` is a foreground session for a human at a terminal.
This design makes a byre box runnable from any script -- CI or not,
no vendor-specific anything -- for ONE non-interactive agent task, with
an exit status a script can trust, the agent's answer on stdout (or
byre's JSON verdict with `--data`), and a result directory holding what
the agent produced. Five phases: (A) login-hook guards so a
terminal-less or headless box never stalls, plus a byre-owned headless
signal; (B) a core **headless contract** -- agent skills declare a
headless command, and a new verb `byre run` drives it, writes
`.byre-run/`, owns the closed exit-code set and the opt-in timeout, and
reports byre's own outcome; (C) skill `requires` dependencies and the
zai/grok headless entries; (D) non-interactive config onboarding
(`preset apply --yes`); (E) `byre build`, then the engine cache
import/export investigation, last. The earlier external wrapper skill
`pjlsergeant/task` is **superseded** by (B) [R1].

## 1. Rulings this design assumes, and why

### 1.1 Already settled in the tree (not reopened)

- `develop --agent` on a configured project is a RUN-SCOPED override
  with REPLACE semantics [CODE: docs/adr/0058;
  internal/commands/resolve.go:230-243; internal/skills/skills.go:
  1355-1366 `enabledSkillNames`].
- byre never hashes content to decide whether to rebuild; Docker owns
  caching [CODE: docs/adr/0001; internal/project/project.go:3-4].
- Context, MCP and Claude Skills reach the agent by INJECTION, vouched
  per agent skill; the vouch is a closed-set string validated at load
  [CODE: skills.go:996-1011] and read by status's delivery lines
  [CODE: internal/commands/status.go:1389-1471].
- `BYRE_*` is byre's reserved vocabulary: refused in `[env]` and
  `env_from_host` [CODE: internal/config/config.go:1137-1138,
  1982-1983]; a skill's `[runtime].env` may set one -- accepted,
  rendered, and the claims it can skew stop asserting [CODE:
  internal/skills/reservedenv.go:1-14, 68-105 `reservedEnvKnobs`,
  111-116 unknown-key default]. `run_args` is last-wins over every byre
  flag, `--entrypoint` included; only the label set is re-asserted, and
  byre never parses the block -- its presence degrades claims [CODE:
  docs/adr/0006; internal/runner/runargs.go:177-185].
- `develop` passes the box's exit status 0-127 through unbannered as
  `ExitError`; 128+ is decoded into a plain error (banner, exit 1);
  byre's own codes are 1, 2, 3, 4, 70. Exit 2 is produced ONLY by
  `cmd/byre`'s unexported `usageError`; a plain error from
  `internal/commands` is exit 1 [CODE: internal/commands/develop.go:
  656-680; cmd/byre/main.go:81-85, 1366-1376, 1400-1411;
  site/content/docs/commands.md:108-161].
- `byre status --data` is the precedent for a versioned JSON document
  on stdout: its own version constant with a dated changelog in the
  comment, bumped when a consumer would have to change [CODE:
  internal/commands/statusdata.go:31-50; flag at cmd/byre/main.go:356].
- External text byre prints is DATA: `packages.EscapeTerminal` per line
  (`EscapeMultiline` for blocks), `dataf` as the funnel; the preset
  review is the one surface that styles for a TTY [CODE: internal/
  commands/escape.go:12-48; PRINCIPLES P4 "reported strings are data"].
- The launch record holds what byre TOLD THE ENGINE, pre-start, keys
  not values, byte-pinned; scalars must precede the first sub-table,
  which is `image` [CODE: docs/adr/0053; internal/commands/
  launchrecord.go:84-89 (the comment's history), 123-124 (the fact)].
- Host writes into an agent-writable tree ride
  `hostopen.OpenDirRootNoFollow` and NEVER overwrite: O_EXCL temp +
  link(2), fail on EEXIST [CODE: docs/adr/0040; internal/hostopen/
  hostopen.go:89; the EXPORTED helper is `PublishStreamExclusive`
  [CODE: internal/hostopen/publishstream.go:52-111]]. Host READS of a
  path the agent can route: `OpenRegular(path, follow)` guards only the
  final component; `OpenRegularIn(root, rel)` contains the whole walk
  [CODE: hostopen.go:41-52, 60]. `hostopen.NewRunID` mints
  per-invocation ids [CODE: publishstream.go:117-123]. Plain
  `os.Root`/`os.OpenRoot` follows a symlinked directory [CODE:
  hostopen_test.go:148-150 comment; `PublishFile*` use `os.OpenRoot`,
  publish.go:52, so they are NOT the primitive for agent-writable dirs].
- The agent-writable HOST surface is wider than the project tree:
  `boxWritableRoots` is the work tree, the main tree, the common git
  dir and the store -- deliberately NOT configured mounts [CODE:
  internal/commands/hostroots.go:10-50]; rw binds come from the
  resolved mounts [CODE: internal/commands/runparams.go:50-66; mode
  at runargs.go:147-153]; raw `run_args` can expose anything and is
  not parsed.
- Setup (generate, build, seed, record, create) is serialized under the
  project setup lock; the created container -- in ANY state -- is the
  session-ownership marker, and `ContainersByLabel` (not the
  running-only query) is how lifecycle code sees it [CODE: develop.go:
  269-288, 439-446, 627; internal/runner/runner.go:329-342].
  `StartAttach` runs after the lock is released; attach can fail with
  the box still running, which develop reports as `ExitRefused` [CODE:
  develop.go:363, 377-388, 402-413]. The attach hands the engine
  byre's own stdin/stdout/stderr [CODE: runner.go:146, 325-327,
  808-813].

### 1.2 Decision: core headless contract, not a wrapper agent skill [R1]

The first plan built `pjlsergeant/task`: an agent skill whose
`[agent].command` was a bash wrapper that picked a real backend by env
var and exec'd its headless mode. Four structural costs, each a
consequence of putting the opinion in a wrapper:

1. Under `-a pjlsergeant/task` the configured agent is REPLACED (ADR
   0058), so the backend had to be hand-listed in `skills` or its
   binary, volume and hooks vanished -- the zai precedent, expressed
   only in prose [CODE: pjlsergeant-byre-skills/skills/zai/skill.toml:7;
   skills/zai/zai:16-20 exits 127 when codex is absent].
2. The wrapper's `[agent]` vouches could only be true for SOME backends
   -- status would over-claim [CODE: skills.go:996-1011 judges the vouch
   per manifest]. Pete's stated preference: accurate per-agent
   reporting over over-claiming.
3. The in-box names could not be `BYRE_TASK` (config.go:1982-1983).
4. The builtin login hooks run BEFORE any agent command [CODE:
   internal/gen/launcher.sh:404-423 firstrun loop, 460-471 exec], so a
   wrapper cannot prevent the 600 s device-auth stall (section 2).

With the headless command declared by each REAL agent skill and driven
by core, none of these arise. **Consequence for item 5 of the brief**
("should `-a` keep the configured agent enabled as a ride-along?"):
**no change to ADR 0058.** Cross-skill dependency (zai needs codex's
binary) is the `requires` field (section 5).

### 1.3 Principles this answers to

P2: core learns a CONTRACT (task bytes in, a result out, a closed set
of exit codes), never an agent's flags or JSON shape. P4: byre's
verdict is the exit code and the `--data` JSON byre itself prints;
where byre cannot verify what ran (raw `run_args`) the JSON SAYS so
rather than asserting; everything inside `.byre-run/`, everything on
stderr, and the answer text on stdout are agent-trust and the docs say
so; status names whether an agent has a headless command and hedges it
when a skill holds the knobs. P1: nothing new refuses a deliberate
choice; the refusals this adds (a non-directory at `.byre-run`, a
credential-row preset under `--yes`) each name the remedy, and the
unverifiable case degrades instead of refusing. P5: no credential a
project supplies ever becomes machine-wide -- why the persistent
env-key login was cut (2.3, R6) -- and `--yes` never stands in for ADR
0057's credential confirm (6, R7). P0/P6: no new config KEY; the two
new skill fields get read-only annotations where skills already render
(section 9) -- never in a slot that disables a row.

## 2. Phase A -- login hooks: never stall, and honour a headless signal

### 2.1 Facts

- `claude-shared-auth/firstrun.sh:85` guards `[ -t 0 ] || exit 0` before
  its paste prompt; its stale-login prompt at :63 is guarded the same way
  behind the `BYRE_ASSUME_TTY` test seam [CODE]. Four hooks do NOT guard
  and run `timeout --foreground 600 <cli> login ...` whenever the
  credential is absent: `codex/codex-login.sh:400-402` (device auth),
  `grok/grok-login.sh:68-71` (device auth), `opencode/opencode-login.sh:
  132-135` (`opencode auth login`, an interactive picker),
  `grok-shared-auth/00-grok-shared-auth.sh:95-107` (device auth into
  the shared store) [CODE]. gemini has no login hook. With no terminal
  each is a 10-minute stall per launch, then the box proceeds
  unauthenticated.
- codex-login's `codex login status` at :287 has NO timeout and runs
  before any of the above; its live probe is bounded (~8 s, :157) [CODE].
  The stale-credential path DETACHES the shared link at :354 before
  falling through to the device login at :375-377, so any exit added
  after :354 owes `restore_shared_link` (:198-205), as the failed-login
  path (:415) and the INT trap (:385) do [CODE].
- Terminal-ness is byre's to decide, not the caller's: `-t` is added
  only when `RunParams.TTY` is set [CODE: runargs.go:103, 129-133],
  which develop takes from `s.TTY` [CODE: develop.go:483]. Section 3.5
  forces it off for `run`, which is also what lets the chassis route
  the agent's stream to stderr and the answer to stdout as two
  separate pipes (R4).
- grok short-circuits on the broker env and on `XAI_API_KEY` [CODE:
  grok-login.sh:41, 44]; opencode on `ANTHROPIC_API_KEY`/
  `OPENCODE_API_KEY` [CODE: opencode-login.sh:97-98]. codex has no env
  short-circuit and NO env-key inference path: `codex login status`
  with `OPENAI_API_KEY` set and an empty `CODEX_HOME` prints "Not
  logged in", rc=1 [PROBED]; `codex exec --help` says "auth still uses
  `CODEX_HOME`" [PROBED, confirmed in the grilling session] -- the only
  way a key reaches codex is a login written into SOME `CODEX_HOME`.
- **A codex key file in the state volume becomes machine-wide on the
  next launch when codex-shared-auth is enabled.** Its firstrun hook
  sorts before codex-login and runs `reconcile.sh startup` every launch
  [CODE: codex-shared-auth/firstrun.sh:19; skill.toml:46]; startup
  publishes a valid local regular file when the shared one is missing/
  invalid [CODE: reconcile.sh:347-350] and, when both are valid,
  prefers the local one by mtime when it carries no `last_refresh` --
  which an API-key file never does, deliberately [CODE: reconcile.sh:
  114-127, 361-366]; `auth_valid` accepts the `OPENAI_API_KEY` form
  [CODE: reconcile.sh:63-81, :70]. No per-file scope marker exists. So
  any login that PERSISTS a project-supplied key into the real
  `CODEX_HOME` is a P5 violation one launch later: billing lands on the
  wrong key, every other project's box gains a credential nobody
  granted it, and a shared ChatGPT login is displaced -- all invisibly.
- claude's shared-auth hook seeds the onboarding marker via `[ ! -e ]`
  and a plain `>` [CODE: claude-shared-auth/firstrun.sh:19-25] -- a
  dangling symlink passes `! -e` and `>` writes through it. Launcher
  ordering: a pending worktree checkout (repo hooks and filters, i.e.
  REPO code) runs first [CODE: launcher.sh:73-82], then credential
  exports [:393], firstrun hooks [:404-423], env.d [:437-447], the
  re-apply [:458], then the exec [:460-471]. Hooks print their banners
  to STDOUT (`echo` without `>&2`, e.g. codex-login.sh:387-394).

### 2.2 The headless signal

Hooks learn "this is a headless launch" from ONE byre-owned variable,
`BYRE_HEADLESS=1`, which the launcher exports from its own ARGV before
the firstrun loop (3.4), and UNSETS otherwise. Why argv: a skill's
`[runtime].env` can set any `BYRE_*` name (tier 2, ADR 0050) and raw
`run_args` can override any `-e` (ADR 0006), but neither reaches the
command argv, which the runner places after the image [CODE:
runargs.go:187-188] -- a stray positional in `run_args` is taken as the
image and breaks the launch loudly, and `--entrypoint` is the raw tier,
already degraded (3.5 says how `run` reports it). A hook tests
`[ -n "${BYRE_HEADLESS:-}" ] || [ ! -t 0 ]` and never prompts when
either holds; the `BYRE_HEADLESS` test comes FIRST so the
`BYRE_ASSUME_TTY` seam cannot beat it. `BYRE_HEADLESS` joins
`reservedEnvKnobs` (3.3). The export is Phase B's launcher change, so
Phase A's own acceptance is the no-terminal (pipe) case; the pty case
is Phase B's.

### 2.3 Changes

A1. **codex-login.sh -- three edits; the env-key login is CUT [R6].**
(i) Immediately after the library is loaded (:42), before any
credential probe: `[ -n "${BYRE_HEADLESS:-}" ] && exit 0` -- the
headless codex script (3.6) owns a BOUNDED preflight, so this generic
hook has nothing to do in a headless launch and must not run the
unbounded status call. (ii) `:287` becomes `timeout 30 codex login
status` (a hang there is a planted-state class, not work; the codex-
shared-auth firstrun bounds its reconciler the same way, :14-18), so a
non-TTY `byre develop` from a pipe is bounded too. (iii) After the
detach block (:377-381) and before the INT trap (:383):
```
if [ ! -t 0 ]; then
  restore_shared_link || echo "byre: the shared Codex credential link could not be restored; it will retry next launch." >&2
  echo "byre: codex is not logged in and there is no terminal for the device login -- set OPENAI_API_KEY for a headless run (env_from_host, or byre credentials set), or log in from byre shell" >&2
  diag_event device_login_skipped_no_terminal
  exit 0
fi
```
placed AFTER the detach so the stale-credential path (:354) and the
not-logged-in path (:372) both reach it with the link state they left,
and the restore runs either way. Why no `--with-api-key` login here:
2.1's fifth bullet -- a persistent key file is published machine-wide
on the next launch, and ADR 0057 already names a credential copy that
outlives its unlock window as a hole. Interactive boxes keep today's
device auth on a terminal; the headless path logs the key into a
per-invocation `CODEX_HOME` instead (3.6).

A2. **TTY-less guard sweep** (ADR README rule 4: cover the class). Add
`if [ -n "${BYRE_HEADLESS:-}" ] || [ ! -t 0 ]; then echo "<remedy>" >&2;
exit 0; fi` immediately before the banner in `grok-login.sh` (after :53),
`opencode-login.sh` (after :113), `grok-shared-auth/00-grok-shared-auth.sh`
(after :95). Remedies name the env key (`XAI_API_KEY`, the opencode
provider keys) and `byre shell`. `claude-shared-auth/firstrun.sh:85` and
:63 gain the `BYRE_HEADLESS` test ahead of their existing TTY tests.
Not in scope, recorded: grok's dead-refresh-chain hang [CODE:
grok-login.sh:50-52] is a USE-time hang inside the agent, not a hook
stall; under `run` it is the documented reason to pass `--timeout`
(R5), under `develop` it remains what it is today.

A3. **claude onboarding seed, both copies.** New firstrun hook
`claude/claude-firstrun.sh` -> `/etc/byre/firstrun.d/claude-onboarding`:
when neither `$CLAUDE_CONFIG_DIR/.claude.json` nor a symlink at that
name exists (`[ ! -e ] && [ ! -L ]`) and any of `CLAUDE_CODE_OAUTH_TOKEN`,
`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` is non-empty, seed the
marker under `set -C` (noclobber = O_EXCL; refuses a dangling symlink
[PROBED by round-3 grok on this shell]). Never prompts, exit 0. The
SAME `-L` + `set -C` fix lands in `claude-shared-auth/firstrun.sh:19-25`
(`seed_onboarding`). The claude headless script (3.6) re-checks after
env.d as a belt.

### 2.4 Tests / acceptance

- `internal/builtins/*_test.go` drive the REAL hook scripts against stub
  binaries on PATH [CODE: wrapper_test.go:35-82 pattern;
  hookrun_test.go:14-29 `physTempDir`]. New: codex hook exits 0 at once
  under `BYRE_HEADLESS=1` with no `codex` invocation at all; a stub
  `login status` that hangs is cut off at the bound and the no-TTY
  guard then exits 0; the no-TTY guard restores the shared link on BOTH
  the not-logged-in and the stale-credential (live probe -> 10) paths;
  grok/opencode/grok-shared-auth hooks exit 0 without running their
  login when stdin is not a TTY, and when `BYRE_HEADLESS=1` with a TTY
  (`BYRE_ASSUME_TTY` where the hook has the seam, a pty helper
  otherwise); both claude seeds refuse a dangling symlink at
  `.claude.json`, leave an existing file alone, seed on the env
  credential / token file respectively.
- Agent-contract tier (`BYRE_AGENT_TESTS=1`, no credentials needed)
  [CODE: internal/commands/agent_contract_test.go:3-16]: a real codex
  box with no credential launched from a PIPE reaches its agent in
  < 30 s. (The pty case needs the Phase B launcher export.)
- Docs: `docs/AGENT-CREDENTIAL-MECHANICS.md` gains the headless signal
  and the "no persistent key file" rule (the full write-up is 3.8's
  R6 item); CHANGES entries. No ADR (bug-class fixes under ADR
  0007/0017 doctrine); the `BYRE_HEADLESS` signal is recorded in ADR
  0061.

## 3. Phase B -- the headless contract and `byre run`

### 3.1 Vocabulary (GLOSSARY additions) [R2, R3, R4]

- **Headless run**: a session whose agent executes ONE task
  non-interactively and exits; started by `byre run`. Still a session
  (one container, single-session rule applies).
- **Task**: the text the agent is given; from `--task <file>`, `--task -`
  (stdin, read to EOF), or `BYRE_TASK` in byre's own environment.
  Bounded at `MaxTaskBytes` = 1 MiB. No implicit repo file.
- **Result dir**: `<workspace>/.byre-run/`, self-ignoring (`.gitignore`
  = `*`, the devlog-dir pattern [CODE: pjlsergeant-byre-skills/skills/
  devlog/devlog-lib.sh:24-53]); what the agent produced and the
  chassis's transcript of it. **Agent-trust content, all of it**: byre
  reads nothing back from it, and a script must not gate on it.
  Emptied by byre at every invocation. _Avoid_: "artifact", "output".
- **Answer**: the agent's final message, the one thing `byre run`
  prints on stdout without `--data`. Agent-trust text.
- **Outcome**: byre's own verdict on a run -- the exit code, and the
  versioned JSON `byre run --data` prints on stdout (`RunDataVersion`,
  the `status --data` convention). Carries an `authority` field that is
  `verified` unless raw `run_args` were in force. **Only these two are
  authoritative.** A best-effort copy lands in the result dir for
  humans.
- **Summary line**: the one line byre prints on stderr at the end
  (`byre: run <run_id>: ran, exit 0 (verified)`). Informational and
  FORGEABLE: stderr is shared with the agent for the whole run, so the
  docs say in so many words that a script must gate on the exit code
  or `--data`, never on this line.
- **Headless command**: the `[agent] headless = ...` entry an agent skill
  declares; the in-box program that honours the contract in 3.3.
- **Run id**: the per-invocation nonce `byre run` mints
  (`hostopen.NewRunID`) and stamps into the outcome, `result.json` and
  the launch record. Distinct from the `byre.run=<nonce>` container
  label (ADR 0006), the netns ownership proof.

Verb and dir name: `byre run` and `.byre-run/` [R3]; the glossary's
**Session** is "one foreground run of a project's box" [CODE:
docs/GLOSSARY.md:23-25], so the word is already in that register.

### 3.2 Skill side: `[agent] headless`

```toml
[agent]
command  = "byre-claude-launch --dangerously-skip-permissions --mcp-config /etc/byre/mcp.json --add-dir /etc/byre/claude-skills"
headless = "byre-claude-headless"
```
`headless` is a shell command string like `command` (a new field on
`AgentContrib` [CODE: skills.go:55-60]; word-split by `sh` through a
baked script, the agent-cmd pattern [CODE: internal/build/context.go:
282, 410-418 `agentScript`; internal/gen/gen.go:124, 327-332]).
Typed-field justification (accretion guardrail): a second consumer
exists from day one (four agents), status must understand it (P4), and
raw fields cannot carry it. Absent = the agent has no headless mode;
`byre run` refuses before building, naming the skill. Validation
(beside the vouch checks, skills.go:996-1011): non-empty when present;
may not equal `command`. Both new fields are skill-manifest text:
rendered through `packages.EscapeTerminal` on the Skills screen and
`dataf` in `printSkillContributions` (P4).

### 3.3 The contract (core <-> headless command), all byre vocabulary

The chassis execs the headless command with:
- `BYRE_RUN_TASK_FILE` -- absolute in-box path of the task text (read
  it; stdin is `/dev/null`).
- `BYRE_RUN_RESULT_FILE` -- where to write the agent's answer. This is
  `/dev/fd/5`: a descriptor the chassis created O_EXCL and holds; a
  script that needs a real path writes its own temp file and `cat`s it
  there. The chassis prints this descriptor's content to the box's
  stdout at the end (3.4), which is how the answer reaches `byre run`'s
  stdout without byre ever reading the agent-writable dir.
- `BYRE_RUN_STATUS_FILE` -- `/dev/fd/6`, same shape; the script appends
  `key=value` lines: `backend_exit=<n>` (mandatory before exit 11),
  `agent_version=<text>` (optional). Nothing else is read.
- `BYRE_RUN_MODEL` -- optional model override (empty = agent default).
- `BYRE_RUN_DIR` -- the result dir, for files the script keeps for
  humans (the chassis never reads them).
- `BYRE_RUN_AGENT`, `BYRE_RUN_ID` -- informational.

The script's STDOUT is the agent's live event stream; the chassis
records it to `transcript.jsonl` and, unless quiet, relays it to the
box's stderr. The script's STDERR is recorded to `stderr.log` and
relayed the same way. The script never writes to the box's real stdout
(the chassis has moved fd 1 to stderr before the script starts).

**Exit status: a CLOSED set, never the agent's raw status.** The script
exits exactly one of: `0` success (and fd 5 is non-empty); `10` the
agent REPORTED failure (an error event / `is_error`) with the process
otherwise orderly; `11` the agent process failed -- `backend_exit=<n>`
written first; `13` credential preflight failed (the script decides how
to tell, 3.6); `15` the backend is not present (zai without codex).
Anything else the chassis treats as the SCRIPT failing (14): a bash
death, a `set -e` exit, a signal, a raw status accidentally passed up,
or `0` with an empty result. A raw agent exit of 10 can only reach the
host as 11 + `backend_exit=10`. The script MUST NOT read a TTY and MUST
give the agent `</dev/null` or the task file on stdin (codex appends
piped stdin to the prompt and reads to EOF [PROBED: `codex exec
--help`]; claude `-p` reads piped stdin as prompt; an open pipe = hang).
Every credential probe the script runs is bounded (`timeout 30`) and a
timeout is a 13. The chassis owns: its descriptors, `result.json`, the
timeout when one was asked for, the exit map, signal forwarding, the
stdout/stderr routing. The script owns every agent flag.

**Inventory.** Every name below joins `reservedEnvKnobs` [CODE:
reservedenv.go:68-105] under a new claim `ClaimHeadless` (= "headless
run") PLUS `ClaimLaunch`: `BYRE_HEADLESS`, `BYRE_RUN_TASK_FILE`,
`BYRE_RUN_TASK_SHA256`, `BYRE_RUN_RESULT_FILE`, `BYRE_RUN_STATUS_FILE`,
`BYRE_RUN_MODEL`, `BYRE_RUN_DIR`, `BYRE_RUN_AGENT`, `BYRE_RUN_ID`,
`BYRE_RUN_TIMEOUT`, `BYRE_RUN_QUIET`. The host re-asserts its
`BYRE_RUN_*` set AFTER skill env, the `BYRE_EGRESS` precedent [CODE:
runparams.go:36-39, 46-48]; the chassis re-exports `RESULT_FILE` and
`STATUS_FILE` itself. A raw `run_args = ["-e","BYRE_RUN_TASK_FILE="]`
still wins (ADR 0006 is not reopened); its only effect is a chassis
that cannot find its task and exits 16 -- the MODE is argv-keyed (2.2).
There is NO env seam for the chassis command (3.7): the test passes the
stub as a positional argument the launcher never supplies.

**The existing test does not check the map.**
`TestChassisScriptKnobsRideReservedPrefix` only asserts the `BYRE_`
prefix [CODE: internal/gen/reserved_vars_test.go:24-61]; `fetch-run.sh`
already reads `BYRE_FETCH_RUN_RETRIES`, which the map lacks. New
`TestReservedEnvKnobsCoverChassisReads` (in `internal/skills`, reading
`../gen/*.sh` with the same regexes): every `BYRE_*` name a chassis
script reads is a key of `reservedEnvKnobs`; add `BYRE_FETCH_RUN_RETRIES`
(ClaimLaunch). `ClaimLaunch` itself has no claim-surface consumer today
(`rg ClaimLaunch` hits reservedenv.go and its test alone) -- recorded,
not this plan's to fix. `ClaimHeadless` ships WITH consumers: status's
Agent row, the Skills screen suffix and `run`'s launch line hedge the
headless annotation ("headless: <cmd> -- not warranted: a skill sets
BYRE_RUN_*", the delivery-line shape [CODE: status.go:1402-1403]).

### 3.4 Chassis: `byre-headless` (core, gen-embedded) and the launcher

New `internal/gen/headless.sh`, embedded like `launcher.sh` [CODE:
gen.go:20-25] and staged to `/usr/local/bin/byre-headless` in the core
block (golden test moves: `TestDockerfileGolden` [CODE: gen_test.go:10]).
Launcher changes, two lines plus one:
1. Before the firstrun loop (after :393):
   `if [ "${1:-}" = byre-headless ]; then export BYRE_HEADLESS=1; exec 9>&1 >&2; else unset BYRE_HEADLESS; fi`
   -- under headless, the launcher's and every hook's stdout goes to
   stderr; the box's real stdout is saved on fd 9 for the chassis's one
   deliberate write (R4: stdout is the product). Hooks print banners to
   stdout today (2.1), so this is what keeps stdout clean.
2. No branch in the command choice: `byre run` passes `byre-headless`
   as the container command (`RunParams.Command`; production
   `runParams` never sets it), so the existing explicit-argv arm [CODE:
   launcher.sh:463-464] execs it. The `agent-cmd` arm is untouched.

`byre-headless` (bash, PID 1 after the exec; `byre-headless [script]`,
the optional positional is the test seam):
1. Validate inputs, else exit 16 with the reason: `BYRE_RUN_DIR` is a
   directory and not a symlink; `BYRE_RUN_TASK_FILE` is a regular
   non-symlink file inside it; **its sha256 equals
   `BYRE_RUN_TASK_SHA256`** (one bounded `head -c` read -- the only
   pathname READ the chassis ever makes). Why the hash check: REPO code
   runs before the chassis -- a pending worktree checkout's hooks and
   filters [CODE: launcher.sh:73-82] -- and can rewrite `task.md`; by
   the time the chassis runs those steps have finished and nothing
   untrusted is concurrent, so a match here means the agent gets the
   bytes the host hashed, and a mismatch is a loud 16, never a silent
   swap. The chassis creates NOTHING -- the host made and emptied the
   dir (3.5).
2. Open byre's own files EXCLUSIVELY, once, and hold the descriptors
   under `set -C` (noclobber = `O_CREAT|O_EXCL`, failing on any
   pre-existing node, a dangling symlink included -- the deliver
   transport's precedent [CODE: internal/deliver/transport.go:21]):
   fd 3 `transcript.jsonl`, fd 4 `stderr.log`, fd 5 `result.md`, fd 6
   `status`, and `result.json` (written once as `started`). EEXIST ->
   exit 16 naming the node. **After this step the chassis never opens a
   pathname inside the dir again**: every later write goes to a held
   fd, every later read is a bounded read of a held fd (`head -c 4096
   /dev/fd/6`, `[ -s /dev/fd/5 ]`, `cat /dev/fd/5`) [PROBED by round-3
   zai/grok: an inherited write-only fd reopens readable via
   `/proc/self/fd`; a child's append is visible to the parent's fd], so
   no FIFO or symlink the agent plants can block or redirect PID 1. The
   one exception is the final `result.json` publish in step 5, which is
   best-effort and non-authoritative.
3. Resolve the headless command: `$1` if given, else
   `/etc/byre/agent-headless` (baked by gen beside `agent-cmd`; absent
   -> exit 15, a belt).
4. Supervise: start the script in its own process group (`setsid`),
   stdin `</dev/null`; its stdout -> `tee -a /dev/fd/3` and, unless
   `BYRE_RUN_QUIET` is set, the box's stderr; its stderr -> `tee -a
   /dev/fd/4` and the box's stderr under the same rule. With
   `BYRE_RUN_QUIET` both go to the files only [R4]. The chassis runs
   ITS OWN timer ONLY when `BYRE_RUN_TIMEOUT` is set and non-zero [R5]:
   on expiry it sets `timed_out=1`, sends TERM to the group, KILL 15 s
   later. TERM/INT to the chassis are forwarded to the group.
   `timeout(1)` is not used for the verdict: a script that exits 124 or
   137 on its own is NOT a timeout [CODE: codereview.sh:508-509
   documents that ambiguity]. No timer means no bound: that is the
   ruling, and the docs name grok's dead-refresh hang as the case to
   pass `--timeout` for.
5. Map, in this order: `timed_out` -> 12; else script 0 with fd 5
   non-empty -> 0 (empty -> 14 "exited 0 without a result"); 10 -> 10;
   11 -> 11 with `backend_exit_code` parsed from fd 6
   (`^backend_exit=[0-9]{1,3}$`, else `null`); 13 -> 13; 15 -> 15;
   anything else -> 14 with `script_exit_code`. Then publish the final
   `result.json` by temp + rename: `mktemp` in the dir, `[ ! -d
   result.json ]`, `mv -f`, then `[ -f result.json ]` -- the
   claude-launch precedent with its post-move probe [CODE:
   claude-launch.sh:17-51, probe at :50]. rename(2) replaces a planted
   symlink rather than following it; a planted DIRECTORY wins the
   squat, the probe notices, and the chassis says so on stderr. Either
   way the verdict a script may trust is the exit code, never this
   file (3.1).
6. **The one stdout write**: `cat /dev/fd/5 >&9` -- the answer, from
   the held descriptor, to the box's real stdout (saved by the launcher
   in step 1 above), whatever the exit code (a 10 still has a message
   worth reading; an empty fd prints nothing). Then one chassis summary
   line to stderr, and exit with the code.

`result.json` (schema 1):
```json
{"schema":1,"run_id":"<hex>",
 "status":"started|success|agent_error|agent_exit|timeout|no_credentials|script_failed|backend_unavailable|result_dir",
 "exit_code":0,"agent":"claude","agent_version":"...","model":null,
 "backend_exit_code":null,"script_exit_code":null,
 "task_sha256":"<from BYRE_RUN_TASK_SHA256>","started_at":"RFC3339","finished_at":"RFC3339","duration_s":0,"timeout":"30m|null",
 "result_file":"result.md","transcript_file":"transcript.jsonl","stderr_file":"stderr.log","message":"one line"}
```
Written with a printf JSON writer and a strict escaper (`"`, `\`,
control characters, non-ASCII passed through as UTF-8); no jq in the
core block [R8].

### 3.5 Host side: `byre run`

```
byre run [--task <file>|-] [--data] [--agent <skill>|-a] [--model M]
         [--timeout D] [--quiet] [--credentials ask|skip|stdin]
         [--template <t>]   # first-run only, as develop
```
`run` calls `developCommand` [CODE: develop.go:62] with a `headless`
option struct; onboarding rules, lock, resolve, build, record, attach
and exit report are unchanged in substance. The option struct carries
three things develop does not have today: a **stage** callback develop
invokes under the lock, an **attach I/O** choice, and a **result**
pointer develop fills as it goes -- `developResult{Phase: prelaunch|
created|attached, Live bool, LaunchHash, ContainerStatus int|-1,
ExitReport []string, RawRunArgs bool}`. `Phase` is set at `Create`
(develop.go:627) and when `StartAttach` returns (:363); `Live` is
develop's own liveness answer after a failed attach (:377-388);
`ContainerStatus` is the raw `ExitCode()` captured before
`decodeAgentExit` (:656-680); `ExitReport` is what `reportExit` prints
(:395; it gains a slice return beside its stderr write); `RawRunArgs`
is whether the resolved `run_args` were non-empty. Without this, a
builder would scrape stderr or scan sibling launch records; neither is
acceptable.

- **Exit 2 is `cmd/byre`'s.** Only `usageError` exits 2 [CODE:
  main.go:81-85, 1366-1373]; a plain error from `internal/commands` is
  exit 1 with a banner. So the FLAG-SHAPE checks live in the cobra
  `RunE` for `run` and return `usageError`: no task source at all;
  `--task -` together with `--credentials stdin`; unknown
  `--credentials` value. Everything that needs the filesystem or the
  config (oversize/unreadable task, no agent, no headless command) is
  `internal/commands`'s and exits 1 -- refusals print their report and
  return `ExitError{1}` (no banner; the refusal register of
  commands.md:138-143).
- **Non-interactive, whatever the caller's terminal.** `run` hands
  develop a `Streams` copy with `TTY: false` [CODE: internal/commands/
  commands.go:35-40]: `RunParams.TTY` is false, so no `-t`, and the
  box's stdout and stderr are two pipes byre can treat differently
  (R4). The attach uses a new `Runner.StartAttachIO(stdin, stdout
  io.Writer)` (the `streamIn` seam exists [CODE: runner.go:148]; nil
  stdin = `/dev/null`), so the box sees EOF at once; `-i` stays
  (runargs.go:130). Credential rows need `--credentials stdin|skip`
  [CODE: credentials.go:178-179; zero rows is a no-op, :156-158].
  **Onboarding**: because `run` forces non-TTY, the existing non-TTY
  arms apply [CODE: onboard.go:110-115, 211-213, 271] -- but their
  remedy text ("run on a TTY") can never be followed inside `run`, so
  `run` pre-checks an UNCONFIGURED project itself before calling
  develop: both `--template` and `--agent` given -> develop writes the
  config (onboard.go:271) and proceeds; otherwise refuse with run's own
  remedy: "this project is not configured here: pass --template <t>
  and --agent <a> (use \"none\" for no template), or configure it
  first with byre develop / byre preset apply". On a CONFIGURED
  project a resolved agent of `""`/`none` is refused: "no agent
  selected: set agent in byre config, or pass -a <skill>" -- an
  agentless box has nothing to run headlessly.
- **Task resolution, bounded, before develop [R2].** Precedence:
  `--task` flag > `BYRE_TASK` in byre's own env (never `env_from_host`).
  Both set -> flag wins, one stderr note. Neither -> usage error (exit
  2). `--task -` reads byre's stdin to EOF through
  `io.LimitReader(MaxTaskBytes+1)`; that read blocks until the writer
  closes -- BY DEFINITION, and the docs say so: a CI step that cannot
  guarantee EOF uses `--task <file>` or `BYRE_TASK`. The no-hang
  promise (section 12) is about the BOX's stdin, which is `/dev/null`
  in every case. `--task <file>`: clean and absolutize against the
  cwd; if the path is under any agent-writable root (`boxWritableRoots`
  [CODE: hostroots.go:49-50] or an rw bind source from the resolved
  mounts), open it through `OpenDirRootNoFollow(root)` +
  `OpenRegularIn(root, rel)` so an agent-swapped INTERIOR component
  cannot route the read outside that root [CODE: hostopen.go:41-52,
  60] (a symlink that stays inside the root reads another in-tree file,
  which is agent-trust content either way); otherwise
  `ReadFileBounded(path, false, MaxTaskBytes)` [CODE: hostopen.go:186]
  (a symlinked task file is refused: pass the resolved path). Over the
  bound or blank -> exit 1. The bytes are held in memory and hashed
  once; that hash is `BYRE_RUN_TASK_SHA256`. Because the writable-root
  set needs the resolved mounts, this read happens after `run`'s
  pre-lock resolve and is re-validated (same root set from `prep.rv`)
  under the lock. No implicit `.byre-task.md`: the project tree does
  not pick the prompt silently (ADR 0047 spirit).
- **Refusals before build**: unconfigured without both flags; no
  agent; selected agent has no `headless` (exit 1: "agent X declares
  no headless command; agents that do: ...").
- **Staging: after `Create`, under the lock, before the lock is
  released.** The stage callback runs inside `prepareLaunchLocked`
  immediately AFTER `r.Create` succeeds (develop.go:627-634) and before
  the lock is released and `StartAttach` runs (:363). The created
  container IS the ownership token -- in any state (ADR 0004;
  `ContainersByLabel`, runner.go:340-342) -- so no second `run`, in
  the created-but-not-started window or later, can reach the stage
  step: it loses the name at `Create` first. Staging: open the
  workspace with `OpenDirRootNoFollow`; `.byre-run` absent -> `Mkdir`;
  present and not a plain directory -> refuse, naming it (the devlog
  stand-down); open `.byre-run` itself as the root (final component
  guarded; ADR 0040's ancestor residual accepted) and EMPTY it with
  `Root.RemoveAll` per entry (unlinks a contained symlink rather than
  following it -- Go 1.27 `os/removeall_at.go`); publish `.gitignore`
  (`*`) and `task.md` with `PublishStreamExclusive` (O_EXCL temp +
  link(2), fails on EEXIST). A stage failure removes the just-created
  container (`r.ContainerRemove`, the develop.go:411 pattern) and
  returns `ExitError{1}` with the report -- outcome `refused`. Every
  invocation therefore starts from an empty dir; "archive `.byre-run/`
  before re-running" is the documented rule. A refusal before the lock
  touches the tree not at all. The launch record (written before
  `Create`, :617-620) already carries `task_sha256`: the bytes were in
  memory before the lock.
- **Launch parameters**: `BYRE_RUN_TASK_FILE=/workspace/.byre-run/
  task.md`, `BYRE_RUN_TASK_SHA256`, `BYRE_RUN_DIR`, `BYRE_RUN_MODEL`,
  `BYRE_RUN_TIMEOUT` (only when `--timeout` was given), `BYRE_RUN_QUIET`
  (only under `--quiet`), `BYRE_RUN_AGENT`, `BYRE_RUN_ID` as `-e` env
  (`RunParams.Env` [CODE: runargs.go:138-140]), re-asserted after skill
  env (3.3); `Command: ["byre-headless"]`.
- **Timeout [R5].** `--timeout D` (coreutils syntax, validated host-side
  as codereview does [CODE: codereview.sh:141-157]); opt-in only, NO
  default, ever; `0` is a usage error (say "omit the flag"). When set,
  the chassis owns the timer (3.4) and the run exits 12. The timer
  starts with the chassis; stalls before it are bounded by their own
  paths' timeouts (the gate's, the hooks' now-guarded and now-bounded
  probes). The docs' worked example is grok's dead-refresh hang
  [CODE: grok-login.sh:50-52].
- **The two output streams [R4].** `run` relays the box's STDERR to
  its own stderr as-is: that is the agent's live event stream and its
  own stderr (unless `--quiet`), hook output, and the chassis summary.
  The box's STDOUT -- the answer, and nothing else once the launcher
  and chassis have moved everything else off it (3.4) -- is relayed to
  `run`'s stdout WITHOUT `--data`, through a pipe byre owns: when
  `run`'s stdout is a TTY each line passes through
  `packages.EscapeTerminal` (external text is data, escape.go:12-29);
  when redirected it is copied raw, byte for byte. Recorded as a
  deliberate choice: a redirected stdout is a file or a program, and a
  program wants the bytes; a terminal obeys what it is sent. WITH
  `--data` the box's stdout is discarded and `run` prints ONE JSON
  document (below) on stdout at the end, and nothing else, ever, on
  stdout. byre never READS `result.json` or anything else in the dir;
  the answer reaches stdout through the chassis's held descriptor
  (3.3, 3.4). Loud by default: the raw agent event JSON on stderr is
  accepted for v1; "readable per-agent progress rendering" is recorded
  as future work, not owed here.
- **`--quiet` [R4].** Passes `BYRE_RUN_QUIET=1` so the chassis keeps
  the agent's stream and stderr in the files only, and suppresses
  byre's own exposure/network banner lines and the "agent for this
  run" / worktree notices. It NEVER suppresses errors, refusals, the
  summary line, or the ⚠ exit report [CODE: exitreport.go:529-546
  prints only when something changed].
- **Outcome classification, from the typed result.** After develop
  returns `(result, err)`:
  - `err == nil` -> `ran`, exit 0.
  - `ExitError{3}` with `Phase == attached && Live` -> `refused`
    (attach failed, the box is still running -- develop.go:402-405).
  - `ExitError{c}` with `Phase < created` -> `refused` (develop's own
    3, or run's 1).
  - `ExitError{c}` with `Phase >= created` otherwise: `c` in
    {0, 10..16} -> `ran` with `agent_exit_code = c`; any other `c` ->
    `box_exited`: the box returned a status outside the chassis
    contract (a launcher death, a gate timeout, a box that never
    reached the chassis) -- NOT `refused`/`usage`/`crashed`, whatever
    the number. The outcome says so.
  - a plain error with `Phase >= created` -> `byre_failed`, exit 1:
    a credential delivery failure after create (:397-400), a create
    failure (:633), or the 128+ band after attach (e.g. a SIGKILLed box
    decodes to text today [CODE: develop.go:673-677]) --
    `agent_exit_code = ContainerStatus` when known.
  - a plain error with `Phase < created` -> `byre_failed`, exit 1.
  - `usageError` -> `usage`, exit 2 (never reaches `run`'s body; no
    JSON, the summary line still prints).
  - `crashed`: `run` registers, once staging has happened, a deferred
    `recover()` that prints the `--data` document (when asked) and the
    summary line best-effort and RE-PANICS, so main's recovery still
    prints the report and exits 70 [CODE: main.go:1350-1356, 1339].
  `authority` is `"verified"` unless `RawRunArgs`, then `"unverified:
  raw run_args"` on every class including `ran` -- with raw `run_args`
  the box may have been handed any mount or a different PID 1
  (`--entrypoint`), and byre does not parse the block (ADR 0006); P4's
  degrade-never-assert, P1's never-refuse-the-raw-tier. Then `run`
  returns the SAME error so main maps it as today [CODE: main.go:
  1361-1376, 1400-1411].
- **The `--data` document [R4].** `RunDataVersion = 1` (its own
  constant beside `StatusDataVersion`, same comment convention and
  CHANGES rule [CODE: statusdata.go:31-47]). Printed on stdout, once,
  after the session, by byre on the host -- the box's stdout is
  discarded, so nothing in the box can write into this document:
  `{"version":1,"run_id","outcome":"ran|box_exited|byre_failed|refused|
  usage|crashed","authority":"verified|unverified: raw run_args",
  "exit_code":N,"agent_exit_code":N|null,"phase":"prelaunch|created|
  attached","agent":"<skill id>","launch_record":"<sha256>|",
  "started_at","finished_at","message","exit_report":[...]}`.
  `encoding/json` escapes control characters in strings, so the
  agent-chosen paths in `exit_report` are inert on a terminal. A COPY
  of the same document goes to `<workspace>/.byre-run/outcome.json`
  with `PublishStreamExclusive` through `OpenDirRootNoFollow` on
  `.byre-run` (a replaced dir or a planted name -> not written, said
  so) -- for humans, non-authoritative, documented as such. **The
  summary line** always prints on stderr, last: `byre: run <run_id>:
  ran, exit 0 (verified)`. It is informational; stderr was the agent's
  for the whole run, so an agent can print an identical line before
  it. The docs say so loudly: gate on the exit code or `--data`.
- **Exit-code contract** (`commands.md`'s `byre run` table; generated
  page: add `run` to `commandsPageAreas` [CODE: cmd/byre/commandspage.go:
  40-50] or `TestCommandsPageCoversTree`/`TestCommandsPagePinsSiteFile`
  fail [CODE: cmd/byre/commandspage_test.go:33, 15]):

  | Code | Owner | Meaning |
  |---|---|---|
  | 0 | script | success; `result.md` non-empty (the answer is on stdout) |
  | 1 | byre | byre failed (banner) -- before launch, or after create (credential delivery, a 128+ box status); or refused (report, no banner): not configured, no agent, no headless command, `.byre-run` not a directory, task unreadable/over 1 MiB |
  | 2 | byre | usage (`cmd/byre`): no task source, `--task -` with `--credentials stdin`, `--timeout 0`, bad `--credentials` |
  | 3 | byre | session already live -- before create, or attach failed with the box running |
  | 10 | script | agent reported failure |
  | 11 | script | agent process failed; raw status in `backend_exit_code` |
  | 12 | chassis | `--timeout` expired (the chassis's own timer) |
  | 13 | script | no usable credential; remedy on stderr |
  | 14 | chassis | the headless script itself failed, or exited 0 without a result; `script_exit_code` |
  | 15 | script/chassis | backend binary absent / no baked headless command |
  | 16 | chassis | result dir, task hash, or chassis files could not be set up |
  | 70 | byre | crash |

  Caveat the table states: a box that launched and exited 1, 2, 3 or
  70 reaches the host as that number too (the 0-127 passthrough,
  commands.md:128-133), and byre itself can exit 1 or 3 after the box
  was created; `--data`'s `outcome`/`phase` fields are what tell these
  apart, so a script that needs certainty uses `--data` (and reads its
  `authority`). 10-16 ride the same passthrough [CODE: develop.go:
  656-662].
- **Launch record** gains `headless = true`, `run_id`, `task_sha256`,
  `run_timeout` (empty when none) -- scalars inserted AFTER
  `credential_unlock` and BEFORE `image`, the first sub-table [CODE:
  launchrecord.go:123-124]; the hash, never the text.
  `LaunchRecordVersion` 1 -> 2 [CODE: launchrecord.go:57];
  `TestLaunchRecordSerializationContract` re-pinned [CODE:
  launchrecord_test.go:122]. `StatusDataVersion` 4 -> 5 [CODE:
  statusdata.go:47] with `agent.headless`; the Agent row [CODE:
  status.go:727-731, `AgentNote` :28-31] shows `headless: <cmd>` / `no
  headless command`, hedged under `ClaimHeadless` (3.3).

### 3.6 Per-agent headless scripts (skill payloads)

Each honours 3.3; each does its own bounded credential preflight and
exits 13 with the remedy naming `env_from_host`, `byre credentials set`,
and the agent's shared-auth companion; each wraps the agent so its exit
is translated (`rc=$?; case $rc in 0) ...;; *) printf 'backend_exit=%s\n'
"$rc" >>"$BYRE_RUN_STATUS_FILE"; exit 11;; esac`). All run through the
SAME byre launch adapter the interactive command uses, so context/MCP
injection is preserved by construction and the skill's existing vouches
stay true (the three adapters exec the CLI with `"$@"` [CODE:
codex-mcp-launch.sh:121; grok-launch.sh:47-49; claude-launch.sh:57]).

**claude** (`claude/claude-headless.sh` -> `byre-claude-headless`):
```
byre-claude-launch -p --dangerously-skip-permissions --mcp-config /etc/byre/mcp.json \
  --add-dir /etc/byre/claude-skills --output-format stream-json --verbose \
  ${BYRE_RUN_MODEL:+--model "$BYRE_RUN_MODEL"} < "$BYRE_RUN_TASK_FILE"
```
Flags [PROBED: `claude --help`]; the adapter adds
`--append-system-prompt-file` [CODE: claude-launch.sh:56-62]. Result =
last `{"type":"result"}` line: `.result` -> `$BYRE_RUN_RESULT_FILE`,
`.is_error`/`.subtype!="success"` -> exit 10. Preflight: pass if
`CLAUDE_CODE_OAUTH_TOKEN`/`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN`
non-empty, or `.credentials.json` holds `"claudeAiOauth"` [CODE:
claude-shared-auth/firstrun.sh:50], or `timeout 30 claude auth status`
JSON `.loggedIn==true` [PROBED: subcommand exists]; also seed the
onboarding marker (A3 belt, same guards). [PROBE B1: `stream-json`
requires `--verbose`; the `result` event's field names; `claude auth
status` with an env token -- disposition: smoke-run; fall back to
`--output-format json`.] [PROBE B2: `--permission-prompts none` is
harmless under bypass mode.]

**codex** (`codex/codex-headless.sh` -> `byre-codex-headless`) [R6]:
```
byre-codex-mcp-launch exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check \
  --json --output-last-message "$out" ${BYRE_RUN_MODEL:+-m "$BYRE_RUN_MODEL"} - < "$BYRE_RUN_TASK_FILE"
```
with `$out` a script-owned `mktemp` that is `cat`ed into
`$BYRE_RUN_RESULT_FILE` afterwards. Flags [PROBED: `codex exec --help`];
the adapter's `-c` flags before `exec` are accepted [PROBED]; the bypass
flag matches the interactive codex skill's own posture (codereview.sh:
648-663 explains why codex's own sandbox is off in a box). Exit 10 on a
`turn.failed`/`error` event with rc 0 [PROBE B3: event names on 0.166].
**Credential, in this order**: (a) `OPENAI_API_KEY` or
`CODEX_ACCESS_TOKEN` non-empty -> **a present key wins over any
existing login**: `CODEX_HOME=$(mktemp -d)` for this invocation,
removed by an EXIT trap; copy `config.toml` from the real `CODEX_HOME`
if it is a regular file (user prefs, seed_prefs), never `auth.json`;
`printf '%s' "$KEY" | timeout 30 codex login --with-api-key` (or
`--with-access-token`); run `exec` under that `CODEX_HOME`. Nothing
lands in the state volume, so the shared-auth reconciler never sees a
key file (2.1). (b) otherwise `timeout 30 codex login status` in the
real `CODEX_HOME` rc 0 (a human's device login on a persistent runner)
-> run there. (c) else exit 13. There is no env-key inference path to
prefer over (a): auth always comes from `CODEX_HOME` [PROBED: `codex
exec --help`, "auth still uses `CODEX_HOME`"]. [PROBE B5: `codex exec`
under a fresh `CODEX_HOME` holding only `auth.json` + `config.toml`
works (sessions dir auto-created) -- disposition: smoke-run; fallback
is copying the real home's non-credential files wholesale.]

**grok** (`grok/grok-headless.sh` -> `byre-grok-headless`, phase C):
```
byre-grok-launch --prompt-file "$BYRE_RUN_TASK_FILE" --always-approve --output-format json \
  ${BYRE_RUN_MODEL:+-m "$BYRE_RUN_MODEL"} </dev/null
```
Flags [PROBED: `grok --help`]; precedent `grok -p "$PROMPT"
--always-approve < /dev/null` [CODE: codereview.sh:947-949]. Preflight:
`XAI_API_KEY`, or `GROK_AUTH_PROVIDER_COMMAND`, or a regular
`$GROK_HOME/auth.json` containing `"key"` [CODE: grok-login.sh:41-53].
Known: headless grok hangs on a dead refresh chain [CODE: grok-login.sh:
50-52] -- there is no default timeout (R5), so the grok docs say: pass
`--timeout`. [PROBE B4: json output shape; `--prompt-file` implies
single-turn; `--append-system-prompt` applies in headless mode --
disposition: smoke-run; fall back to `--output-format plain`.]

**zai** (in pjlsergeant-byre-skills, phase C): headless =
`env ZAI_CODEX_BIN=/usr/local/bin/byre-codex-mcp-launch byre-zai-headless`
running `zai exec ...` with the codex flags (zai-through-adapter `exec
--help` rc 0 [PROBED]). Preflight: `ZAI_API_KEY` non-empty [CODE:
zai-shared-auth/env.sh exports it; zai/skill.toml:16 env_docs]; the
codex adapter absent -> exit 15 (today's launcher exits 127 [CODE:
zai/zai:16-20], which the chassis would call 14). Plus `requires =
["codex"]` (section 5). **Package compatibility**: byre strict-decodes
skill.toml [CODE: skills.go:792-811], so a zai manifest carrying
`headless` and `requires` is REJECTED by every byre before the release
that ships them; zai's `requires_byre` (today `">=1.3.0"` [CODE:
zai/skill.toml:6]) must be bumped to that release, with a package
version bump and new payload digests. This repo's `byre.preset` does
not pin zai [CODE: byre.preset:40-46], so nothing here changes.

### 3.7 Tests

- gen: golden Dockerfile (new baked `agent-headless`, `byre-headless`);
  `launcher_test.go`: `BYRE_HEADLESS` exported iff `$1 = byre-headless`
  and UNSET otherwise even when the environment carried it; under
  headless the launcher's own stdout and a hook's stdout land on stderr
  and fd 9 is the original stdout; the explicit-argv arm still execs;
  `TestReservedEnvKnobsCoverChassisReads` (3.3).
- `internal/gen/headless_test.go` drives the REAL `headless.sh` with a
  stub script passed as `$1`: the mapping matrix -- 0 (fd 5 filled) ->
  0; 0 with fd 5 empty -> 14; 10; 11 + `backend_exit=N` (including
  N=10, 13, 124, 137); 13; 15; script 1/124/137/signal -> 14 with
  `script_exit_code`; a sleep past `BYRE_RUN_TIMEOUT` -> 12 and the
  child group gone; NO `BYRE_RUN_TIMEOUT` -> a sleeping stub is not
  killed (test with a short stub and assert no timer process exists);
  `started` then final `result.json`; exit 16 on a symlinked dir, a
  symlinked `task.md`, a task whose hash mismatches
  `BYRE_RUN_TASK_SHA256`, a pre-planted node at any chassis name; a
  FIFO planted at `status`/`result.md`/`result.json` by the stub DURING
  the run (after unlinking the chassis's file) never blocks the chassis
  (test under `timeout 10`); a directory planted at `result.json` is
  reported, exit code unchanged; stdin never read; TERM forwarded;
  **stream routing**: the stub's stdout appears in `transcript.jsonl`
  and on the chassis's stderr, never on fd 9; with `BYRE_RUN_QUIET`
  only in the file; the answer (fd 5) is the ONLY bytes on fd 9, also
  on exit 10, and nothing on fd 9 when fd 5 is empty.
- builtins: each headless script against stub `claude`/`codex`/`grok`
  binaries recording argv, env and stdin: argv pinned, model flag,
  result written through `$BYRE_RUN_RESULT_FILE`, exit 10/13 paths,
  raw agent exit N -> 11 + `backend_exit=N`, task delivered byte-exact;
  codex: with `OPENAI_API_KEY` the stub sees a `CODEX_HOME` that is not
  the state volume, `config.toml` copied, `auth.json` never written to
  the real home, the temp home gone on exit, and the key path taken
  EVEN WHEN the stub's real-home `login status` would pass (the key
  wins); a stub `login status` that hangs -> 13 within the bound.
- `cmd/byre`: `TestRunUsageErrors` (no task source, `-` with
  `--credentials stdin`, `--timeout 0`, bad `--credentials` ->
  `usageError`, exit 2, nothing dispatched; `--data` parses).
- commands: `TestRunTaskPrecedence` (flag > env with the note, `-`
  reads stdin, over-size = 1), `TestRunTaskFileContained` (a task
  under the work tree whose interior dir is a symlink out of the tree
  is refused; one pointing inside is read; a symlinked leaf refused; an
  external path read with the leaf guard), `TestRunStagesAfterCreate`
  (fake runner: stage callback runs after `Create` and before the lock
  releases; a `Create` failure never stages; a stage failure removes
  the container and the outcome is `refused`; symlinked/file
  `.byre-run` refused with the victim unchanged; a populated dir
  emptied; a symlinked `task.md` from a prior run removed not
  followed), `TestRunRefusesUnconfiguredWithoutFlags`,
  `TestRunRefusesAgentWithoutHeadless`, `TestRunRefusesAgentless`,
  `TestRunForcesNonInteractive` (no `-t`, nil stdin, `Command =
  [byre-headless]`, `BYRE_RUN_*` after skill env, `BYRE_RUN_TIMEOUT`
  absent without the flag, `BYRE_RUN_QUIET` only with `--quiet`),
  `TestRunPassesChassisExitThrough` (fake runner returns 12 -> process
  12, no banner), `TestRunOutcomeClassification` (nil; 3 pre-create;
  3 post-attach with Live = `refused`; 11 post-create = `ran`; 1
  post-create = `box_exited`; 137 decode = `byre_failed` with status
  137; cred error post-create = `byre_failed`; `authority` degraded
  with `run_args` set), `TestRunStdoutIsTheAnswer` (fake attach writes
  bytes with control characters to the stdout pipe: a TTY stdout gets
  them escaped per line, a redirected stdout gets them raw; stderr gets
  the relay; nothing else on stdout), `TestRunDataDocument` (with
  `--data` the box's stdout is discarded and stdout is exactly one JSON
  document with `version: RunDataVersion`, `authority`, `exit_report`;
  byte-pinned contract test per the status-data precedent), `TestRunQuiet`
  (banners gone; errors, refusals, summary line and exit report
  present; `BYRE_RUN_QUIET` passed), `TestRunSummaryLineAlwaysPrints`,
  `TestRunInTreeCopySkippedOnPlant`, `TestRunCrashWritesSummary`, the
  launch-record contract update, status Agent row + hedge,
  commands-page pin.
- configui (P0, same unit as the field): every agent row carries the
  `headless` / `no headless command` suffix, appended AFTER the
  mutually exclusive state switch [CODE: configui/skills.go:322-335]
  and before the description [:336-340]; a control character in the
  command renders escaped; the suffix never sets `e.disabled` and
  toggling still works; the `ClaimHeadless` hedge appears when a skill
  sets a `BYRE_RUN_*` key. `skill inspect`: `headless:` line rendered
  via `dataf`, escaped [CODE: skill.go:160-168].
- Gated `BYRE_DOCKER_TESTS=1` integration: a LOCAL test skill
  `headless-echo` (`command = "bash -l"`, `headless = "byre-echo-headless"`
  which copies the task to the result fd, prints a marker line to its
  stdout, and exits per a marker in the task text) proves the whole
  path end to end, no agent, no credentials: `byre run --task f` exits
  0/10/11/14 as the task dictates with the task text as the ONLY stdout
  bytes and the marker line on stderr; `--timeout 2s` on a sleeping
  task -> 12; `--data` -> one JSON document, `ran`/`verified`;
  `.byre-run/` holds exactly the expected files; a run from a pty
  (`script -q`) never allocates `-t`, its hooks never prompt, and its
  stdout is escaped; a config with `run_args = ["--entrypoint",
  "/bin/true"]` exits 0 with `authority: unverified`.
- Agent-contract tier: `byre run` against a real claude/codex box with
  NO credential exits 13 within seconds.

### 3.8 Docs / surfaces

- ADR 0061 "Headless runs: the agent declares a headless command; `byre
  run` drives it" (contract, env names, closed exit set, the fd-based
  script channel, the two-stream output model and what is
  authoritative, the `authority` degradation, task precedence, the
  argv-keyed mode and `BYRE_HEADLESS`, opt-in timeout, why not a
  wrapper skill, why workspace writes ride OpenDirRootNoFollow +
  exclusive publish, why staging follows `Create`, why no persistent
  codex key). Index line + arms.
- **R6 documentation work item (explicit, owed in Phase B)**:
  `docs/AGENT-CREDENTIAL-MECHANICS.md` and `site/content/docs/how-do-i/
  workflow.md` (the CI page) explain, in plain words: WHY the headless
  codex script logs an env key into a per-invocation `CODEX_HOME` and
  never into the state volume -- with codex-shared-auth enabled, the
  reconciler publishes any valid persisted key machine-wide on the next
  launch (2.1), so a key left behind would bill another project's work
  to this key, hand every other project's box a credential nobody
  granted it, and displace a shared ChatGPT login, all without a
  prompt; WHEN it matters -- persistent machines (a developer laptop, a
  self-hosted runner) with codex-shared-auth, never a discarded hosted
  runner; and THAT a present `OPENAI_API_KEY`/`CODEX_ACCESS_TOKEN` wins
  over an existing login for the headless run. CHANGES names it.
- `docs/ARCHITECTURE.md` Skills (`[agent]` block gains `headless`, the
  block is at :474-491), Chassis (the launcher's headless export and
  stdout move, `byre-headless`), Commands. `docs/GLOSSARY.md` (3.1).
  `docs/SKILLS.md` authoring note. `site/content/docs/commands.md`
  (generated; the exit table and the `--data` document),
  `how-do-i/workflow.md` new "Run one task headlessly (CI)?" (gate on
  the exit code or `--data`; the summary line is forgeable; `--task -`
  blocks until EOF; `--timeout` for grok; hosted runners rebuild per
  job), `security-model.md` (what a headless run writes into the
  workspace; that byre reads none of it back; stdout and stderr are
  agent-trust; repo code runs before the chassis), `whats-boxed.md` if
  it lists chassis files, `packaging-reference.md` (the fields). README
  "Commands". CHANGES (incl. `RunDataVersion` 1 and `StatusDataVersion`
  5). Future work, recorded: readable per-agent progress rendering on
  stderr.
- TUI: no new config key. Skills screen: EVERY agent row [CODE:
  internal/configui/skills.go:139-144] gains a `headless` / `no headless
  command` suffix after the state switch [CODE: :322-335], never the
  `disabled` slot. `byre skill inspect` renders `headless: <cmd>` under
  the agent command [CODE: skill.go:160-168].

## 4. The CI recipe (what the docs will say; engine- and CI-agnostic)

1. Install byre (install.sh or release binary). `byre skill install
   <url> --digest sha256:... --yes` for every non-bundled package
   (`--yes` is pipe-required for activation/replacement [CODE:
   cmd/byre/main.go:1049-1050; internal/commands/install.go:124-126,
   172-180]).
2. Config, one of: (a) commit `byre.preset` carrying `agent`, `skills`
   (incl. the firewall), `egress`, `env_from_host = { ANTHROPIC_API_KEY
   = "env:ANTHROPIC_API_KEY" }`, `[sources]` with digests -- and NO
   encrypted credential rows (section 6, R7); on the runner `byre
   preset apply --yes`; or (b) `byre run --template <t> --agent <a> ...`
   on a never-configured checkout, which writes the config from the two
   flags [CODE: onboard.go:211-213, 271] -- the minimal route.
   `default.config` cannot supply the agent: `StripFavourites` blanks
   `template` and `agent` before they cascade [CODE: config.go:547-559;
   config_test.go:474 `TestLoadIgnoresDefaultTemplateAndAgent`].
   Credentials reach the box through `env_from_host` from the CI
   secret store.
3. `out=$(mktemp -d); byre run --task path/to/task.md --data
   > "$out/outcome.json"; rc=$?` (or `BYRE_TASK="..."`; `--task -` only
   when the writer is guaranteed to close; add `--timeout 30m` when the
   agent can hang, grok in particular). Gate on `$rc` or the JSON --
   never on the stderr summary line, which the agent could have
   imitated. Without `--data`, stdout is the agent's answer, ready to
   post somewhere. Archive `.byre-run/` as the agent's output.
4. Push tokens stay on the host: the job pushes after byre exits.
   Hosted runners: nothing to clean -- the machine is discarded -- and
   nothing to speed up in v1: every job rebuilds the image (section 8).
   Self-hosted runners: `byre reset -y` after the job removes this
   project's volumes (agent state) and keeps the image for the next
   build [CODE: main.go:1124 `-y`; reset.go:101-193]; `byre forget -y`
   is the full wipe [CODE: main.go:1186; forget.go:15-25]. Both are
   no-ops on a never-developed checkout [CODE: reset.go:44-59]. A
   persistent machine with codex-shared-auth: read the R6 note (3.8)
   before putting `OPENAI_API_KEY` in `env_from_host`.
Note for self-hosted runners today: without phase A, a box missing a
codex/grok/opencode credential stalls 10 minutes in the login hook, then
fails at preflight.

## 5. Phase C -- skill `requires` (ADR 0062)

Field: top-level `requires = ["codex"]` in skill.toml (a `skills.File`
field beside `companion_for` [CODE: skills.go:113-140]; names expand
through the catalog like everything else). Semantics in
`skills.Resolve` after the load loop ends [CODE: skills.go:1269; the
`agentFound` check at 1271-1273 is the shape]: every required id must be
in the ENABLED set (`enabledSkillNames`); otherwise `skill
"pjlsergeant/zai" requires "codex", which is not enabled -- add it to
skills = [...]`. **Never auto-enabled** (P5). Applies identically under
`-a` (same Resolve). A skill may not require itself (validate); unknown
names are not a validate error (the set only exists at resolve, as for
`agent` [CODE: docs/SKILLS.md:75-84]). Surfaces: `skill inspect`/install
grant summary print `requires: codex` (a dependency line, not a grant).
Skills screen: an ENABLED row whose requirement is unmet is annotated
`(requires codex -- not enabled)` after the state switch [CODE:
configui/skills.go:322-335], recomputed on every `skillEntries()` call
(built per update and per view [CODE: :64, :221, :288]) so toggling
codex on clears it. NOT the `disabled` slot: that slot blocks the toggle
[CODE: :237-239] and renders `[-]` [CODE: :308-309], so a user who
removed codex could not remove zai either -- P0 ranks an editor that
cannot express an off-switch with an engine bug. Preset/restore reviews
surface the Resolve error as they do any other. Typed-field
justification: second consumer (zai now; opencode/mimo/vibe wrappers in
the published repo are candidates), legibility. zai's skill.toml gains
`requires = ["codex"]`, its headless entry, and the `requires_byre` bump
(3.6). Tests: `TestResolveRefusesUnmetRequires`, `TestRequiresSelfRefused`,
inspect render (escaped), skills-screen annotation + toggle still live,
ADR index line.

## 6. Phase D -- non-interactive config onboarding (ADR 0063) [R7]

Facts: `preset apply` refuses without a TTY ("the review is the point")
[CODE: internal/commands/preset.go:62-65]; its confirm is the ONLY gate
for the credential review lines, which exist because a swapped
ciphertext is invisible in the diff and the repo is agent-writable --
the lines do not gate, the human's confirm does [CODE: internal/
commands/review.go:312-324; preset.go:156-158]; those lines are
highlighted only on a TTY [CODE: preset.go:517-518]; its chauffeured
installs pass `yes=false` [CODE: preset.go:470-475], which a pipe
refuses [CODE: install.go:172-180]; it enrolls the project
(`Bootstrap`) before taking the lock and writing the config + applied
marker [CODE: preset.go:165-194]; `--template` on a configured project
errors [CODE: onboard.go:59-71].

Design (the one option kept): `byre preset apply --yes`: allowed
without a TTY; prints the full review to stderr regardless
(un-highlighted off a TTY, the `⚠` prefix carries the weight); passes
`yes` to the chauffeured installs; REQUIRES every `[sources]` hint it
would install from to carry a digest (an https source without one
refuses, naming the row -- in a pipe nobody can inspect the fetched
bytes); path sources allowed. **Credential rows [R7]**: when the preset
OR the stored config it would replace carries encrypted credential
rows, `--yes` REFUSES, naming the rows: ADR 0057's answer is a human's
confirm against the credential lines, and `--yes` is not that human.
The error's remedy: apply on a TTY, or keep credentials out of the
preset and deliver them with `env_from_host`. An extra switch is a
future ADR if ever needed; none is built now.

Rejected: `byre develop --config <file>` applying a repo file for one
run -- it lets the project tree define its own sandbox, which the store
exists to prevent [CODE: onboard.go:48-50 comment; ADR 0003]. With
`--yes` the consent act is the human reviewing and committing the
preset plus the explicit `--yes` on a pinned install set (P5: consent
to applied content is consent to its rendered grants; the review still
renders, into the CI log). On a TTY `--yes` also skips the confirm --
except for credential rows. ADR 0063 records the ADR 0057 residual: a
`--yes` apply is consent to grants, never to a credential swap. Tests:
non-TTY without `--yes` still refuses with the new remedy; with `--yes`
writes config + applied marker and installs from digested hints;
digest-less https hint refuses under `--yes`; credential rows on either
side refuse under `--yes` naming `env_from_host`; the review text
appears on stderr. TUI: `[sources]` stays the existing read-only row.
Docs: SKILLS.md Presets, configuration-reference Presets, how-do-i,
security-model residual.

## 7. `--ephemeral` -- CUT [R10], with the reason

Two review rounds each found a fresh hazard class in it: the
worktree-shared blast radius (ADR 0009), forget's lock/engine protocol,
the cleanup-after-outcome finalization problem, `neverEnrolled`'s real
semantics (no record AND no dir [CODE: reset.go:44-59]), and finally
that `preset apply --yes` itself enrolls the project [CODE: preset.go:
165] so an earlier precondition refused the recipe it was written for.
Stepping back: a hosted runner is discarded, so it needs no cleanup; a
persistent runner has state by definition, and `byre reset -y` (volumes
only, image kept) and `byre forget -y` (everything) already exist, ask
no questions under `-y`, refuse a live session, and are no-ops on a
never-developed checkout. A flag that re-implements them on the way out
of a session buys one line in a job file and keeps costing hazards.
Cut; the recipe (section 4) uses the existing verbs. If it is ever
revisited, it starts from "reset -y after the session" (volumes only,
no store removal) and not from "nothing left". No ADR 0064 is
allocated.

## 8. Phase E -- `byre build`, and the hosted-runner reuse question [R8, R9]

Facts [CODE]: the image tag is `byre-<projectID>-u<uid>-g<gid>`
(naming.go:131-133); `projectID` is `<slug>-<6 hex of sha256(canonical
path)>` (project.go:91), so the tag differs per runner path and uid;
develop ALWAYS runs `docker build` with the engine cache (develop.go:
591 -> 764-768; runner.go:294-303) and never skips on an existing tag;
ADR 0001 forbids a byre-side content hash as a skip key. A hosted
runner therefore rebuilds from scratch every job; a loaded `docker
save` archive restores the IMAGE but not BuildKit's build cache.

**v1 promises no hosted-runner speed-up, and the docs say so**: a
hosted runner rebuilds the image every job; a persistent runner keeps
the engine's own cache. `byre build` does not change that and is not
sold as reuse. What it IS: develop's resolve + assemble + build without
a session -- useful to warm a persistent runner, to inspect the tag, to
fail a job early on a broken config, and as the step E1 measures. It is
shipped on those merits.

`byre build` mechanics: runs UNDER `setupLockedProject` exactly as
develop runs the same steps [CODE: develop.go:283-285, 442, 591] -- a
concurrent develop/worktree build shares the context dir and the tag --
with no seed, no record, no container (ADR 0053: records are for
session containers), printing the tag on stdout. Steps the verb owes:
`commandsPageAreas` entry under "Lifecycle & recovery" [CODE:
commandspage.go:40-50] + regenerate `commands.md`, README "Commands",
`how-do-i/workflow.md`, CHANGES, `app` seam wiring + test, fake-runner
tests (builds under the lock, prints tag, exits 0; no `Create`; refused
while the lock is held).

**E1 -- investigation only, run LAST, after A-D ship [R9]**: engine
cache import/export -- docker BuildKit `--cache-from type=registry,
ref=R --cache-to type=registry,ref=R,mode=max` (or `type=local,
dest=DIR`), podman `--cache-from R --cache-to R --layers` -- measured
on the inttest VM for docker AND podman (does `--cache-to` need
`--layers`/BuildKit, does `--pull`/`rebuild` compose, does a registry
need auth plumbing byre must not own; second-job build time with
import vs without). If the measurement holds, it lands as ADR 0065 and
a `--build-cache <ref|dir>` FLAG on `run|develop|build` (engine-shaped
rendering in `runner.Build`, the `TmpfsMount` precedent [CODE:
runargs.go:35-84]) -- a flag, not a config key, so no TUI row is owed
[R8]; it becomes a key only by a later ADR of its own. Rejected
outright: superseding ADR 0001 with a byre-computed context digest +
skip.

## 9. P0/P6 inventory

New config keys: **none**. New skill.toml fields: `[agent].headless`,
`requires` -- both rendered read-only on the Skills screen after the
state switch (never `disabled`) and in `skill inspect`; neither is
user-authored config. New flags: `run --task/--data/--model/--timeout/
--quiet`, `preset apply --yes`, `build`, and -- only if E1 is adopted --
`--build-cache`. Flags are not keys and owe no row.

## 10. Phasing, ordering, acceptance

| Phase | Units | Depends on | Done when |
|---|---|---|---|
| A | A1-A3 guards + seeds | -- | builtins tests green; agent-contract no-credential launch < 30 s on codex/grok/opencode boxes from a pipe |
| B | gen chassis + launcher export + stdout move; `headless` field; `byre run` (+ `developResult`, stage callback, `StartAttachIO`, `--data`, `RunDataVersion`); claude + codex headless scripts; `--quiet`; record/status; Skills screen + inspect; docs incl. the R6 write-up; ADR 0061 | -- (A improves it) | gated echo-skill e2e green incl. the pty, `--data`, `--timeout` and `--entrypoint` cases; a real `byre run -a claude --task f` prints the answer on stdout and returns 0, and 13 with no credential; configui tests green |
| C | `requires` (ADR 0062); grok headless; zai `requires` + headless + `requires_byre` bump | B | zai refuses without codex at resolve; grok smoke green |
| D | `preset apply --yes` (ADR 0063, R7); CI how-do-i | B | a fresh VM goes from clone to a `--data` document with no TTY; credential-row preset refuses under `--yes` |
| E | `byre build`; then, last of all, the E1 investigation; ADR 0065 + `--build-cache` only if adopted | A-D shipped (for E1) | `byre build` tests green; E1: VM measurement written up either way |

Commits per unit; two independent reviewers per phase (CLAUDE.md);
every review states its Doctrine line.

## 11. Open questions

None. Section H holds the rulings.

## 12. Host e2e checklist (cannot be verified in the planning box)

- [PROBE B1-B5] agent CLI shapes; codex under a per-invocation
  `CODEX_HOME`.
- `byre run` under docker AND podman: from a pty, from `/dev/null`, from
  a never-closing pipe WITH `--task <file>` -- no `-t`, no hang, exit as
  mapped; in-box `[ -t 0 ]` false in a firstrun hook in all three; the
  answer is the only stdout bytes, escaped on the pty and raw into a
  file. (`--task -` on a never-closing pipe blocks by definition.)
- `--output-last-message` and `cat >/dev/fd/5` write into the chassis's
  held descriptor (Linux `/proc/self/fd` reopen semantics).
- Firewall + egress with each agent's headless path.
- Fresh VM: `preset apply --yes` + `run --data` with no TTY; then
  `byre reset -y`; volumes gone, image kept.
- `byre status` during a headless session: Agent row `headless`,
  delivery lines unchanged per agent.
- [PROBE E1] cache import/export on both engines -- after A-D.

## 13. What the earlier wrapper plan got right (carried) and wrong (dropped)

Carried: task env/file precedence; self-ignoring dir; result.json
schema; exit band 10-16; per-agent credential preflight with remedies;
stdin-hang avoidance; opt-in timeout; the claude onboarding seed (now in
the builtins). Dropped: the wrapper agent skill itself; `AGENT_TASK_*`
names; autodetecting a backend; per-backend vouch over-claims; from v1
of THIS plan, the env-keyed launcher branch, raw-status passthrough,
`--result-dir`; from v2, the persistent codex env-key login, the
pre-lock staging, the in-tree outcome file as the gate, the env test
seam, and `--ephemeral`; from v3, pre-`Create` staging and `byre build`
sold as reuse; from v4, the host-path outcome flag and its root
machinery (replaced by `--data` on stdout), and the default timeout.

## 14. Review log

### Round 1 (codex / zai / grok) -- folded in v2

**codex** 1 terminal-independent headless -> fixed (TTY/stdin forced,
`BYRE_HEADLESS` from argv). 2 result lifecycle -> fixed (emptied per
invocation, run id). 3 `disabled` slot -> fixed (annotation slot). 4
citations -> fixed. 5 knob inventory / `ClaimLaunch` -> fixed (argv-keyed
mode; full inventory; `ClaimHeadless` consumers). 6 124/137 inference ->
fixed (chassis-owned timer).
**zai** 1 chassis symlink writes -> fixed (held fds, temp+rename). 2
env trigger / run_args -> fixed; rejected in part: re-asserting env
after `run_args` (ADR 0006's exception is the label set). 3 exit 15
unreachable -> fixed (closed set). 4 citations -> fixed. 5 `crashed`
mechanism -> fixed (deferred recover + re-panic). 6 default timeout ->
later ruled R5 (none). 7 annotate every agent row -> fixed. 8 ephemeral
lock/exit -> superseded (cut, R10). 9 `byre build` docs steps -> fixed.
10 137 as timeout -> fixed.
**grok** 1 host writes via plain `os.Root` -> fixed. 2 ephemeral blast
radius -> superseded (cut, R10). 3 terminal still waits 600 s ->
fixed. 4 exit map collisions -> fixed. 5 codex detach ordering ->
fixed in v2, then the whole env login cut in v3. 6 `disabled` slot ->
fixed. 7 citation table -> fixed. 8 `--result-dir` dropped; stdin
conflict -> usage; launch-record ordering; `printenv` -> moot in v3.

### Round 2 (codex / zai / grok) -- folded in v3

**codex** 1 key machine-wide on the NEXT launch -> fixed by CUTTING the
persistent env login; per-invocation `CODEX_HOME` (R6). 2 refused second
run destroys the active run's dir -> fixed (staging under the lock;
re-done in v4). 3 recipe self-refuses on `--ephemeral` -> fixed by the
cut (7). 4 cleanup finalization -> moot.
**zai** 1 self-refusing recipe / `neverEnrolled` -> the cut. 2 key
published next launch; ADR 0057 persistence -> the cut. 3 in-tree
outcome gate squattable; exit 0 without result -> host-authoritative
verdict (now `--data`, R4), in-tree copy non-authoritative, result via
held fd, empty-on-0 is 14. 4 destructive staging before refusals ->
fixed (v4 moves it after `Create`). 5 pre-chassis failures /
`ExitError{3}` ambiguity -> typed `developResult`, `box_exited`. 6
chassis pathname TOCTOU -> fd channel. 7 unbounded login calls in
scripts -> `timeout 30`. 8 `byre build` lock -> fixed. 9 develop
returns no data -> `developResult`. 10 zai `requires_byre` -> fixed. 11
task unbounded -> `MaxTaskBytes`.
**grok** 1 skipped reconciler does not scope the key -> the cut. 2
`.byre-run` emptied before the lock; unexported writers -> fixed;
`PublishStreamExclusive`. 3 outcome map vs process status -> fixed. 4
`result.json` publish leaves the squat; result path -> rename replaces
a symlink, post-move probe, result via fd. 5 onboarding arms
misdescribed -> fixed (re-done in v4). 6 `BYRE_HEADLESS_CMD` ->
positional seam. 7 post-exit reads can block -> fd channel. 8 ephemeral
semantics -> the cut. 9 `seed_onboarding` dangling symlink -> both
copies fixed. 10 `byre build` lock -> fixed. 11 first sub-table is
`image` -> fixed. Lower corrections -> fixed.

### Round 3 (codex / grok / zai) -- folded in v4; loop capped

**codex**
1. Staging can erase a run that already owns a created-but-not-running
   container -> fixed: staging runs AFTER `Create` succeeds, under the
   lock; a stage failure removes the container (3.5).
2. A host outcome path outside the project can still be agent-writable
   -> fixed in v4 by widening the root set; superseded in v5 by R4
   (no host path at all; the verdict is stdout JSON byre prints).
**grok**
1. Staging deletes another run's dir; `Create` failure after staging
   misclassified -> fixed (as codex 1).
2. No-terminal guard before the detach drops the shared link -> fixed:
   the no-TTY guard sits AFTER the detach block and restores; the
   headless early exit sits before any probe (A1).
3. Exit 0 under `run_args --entrypoint` classified as success -> fixed:
   `developResult.RawRunArgs` -> `authority: unverified` (3.5).
4. `preset apply --yes` skips ADR 0057's consent -> fixed; ruled R7.
5. `--task -` hangs on a never-closing pipe -> fixed by stating it;
   ruled R2.
6. Exit 2 is `cmd/byre`'s `usageError` only -> fixed (3.5, 3.7).
7. Unbounded `codex login status` before the guard; grok dead-refresh
   -> fixed: `timeout 30` on :287, headless early exit; the grok hang
   is use-time and is R5's documented reason for `--timeout`.
8. Phase A pty acceptance; onboarding remedy unreachable inside `run`
   -> fixed (2.2, 2.4, 3.5).
9. Repo code runs before the chassis -> fixed: task hash verified
   before exec (3.4).
10. Relative outcome path; wrong citation -> superseded by R4;
    `hostroots.go:10-50` cited in 1.1 for the task-file read.
**zai**
1. `--task <file>` interior symlink routes out -> fixed:
   `OpenDirRootNoFollow` + `OpenRegularIn` under agent-writable roots.
2. Post-create attach failure with a live box misclassified -> fixed:
   `developResult.Live`; table reworded.
3. Phase E recommendation internally contradictory -> fixed; ruled R9.
4. Outcome path vs configured mounts -> superseded by R4.
5. Codex guard after the unbounded status call -> fixed (as grok 7).
6. No TUI/inspect tests for `headless` -> fixed (3.7, 10).
7. `forget.go:42` cites a consumer -> fixed.

### Round R (Pete's rulings, grilling 2026-11-02) -- folded in v5

R1 core feature -> 1.2 unchanged, marked. R2 task input -> 3.1/3.5:
`--task -` to EOF with the hang documented; 1 MiB; no implicit file.
R3 names -> 3.1. R4 output model -> the design change of this version:
`--outcome` and its agent-writable-root machinery REMOVED; `--data`
JSON on stdout with `RunDataVersion`; answer on stdout otherwise,
escaped on a TTY; everything else on stderr via the launcher's and
chassis's stdout move; authority = exit code + `--data`; the summary
line declared forgeable; `--quiet` scoped; loud default accepted;
progress rendering recorded as future work (3.3-3.5, 3.7-3.8, 4, 9,
10). R5 no default timeout -> 3.4/3.5/3.6; `--timeout 0` is a usage
error; grok's hang is the documented use. R6 codex per-invocation
`CODEX_HOME`, key wins, PROBE B5's direct-env half closed [PROBED] ->
2.1/2.3/3.6; explicit docs work item in 3.8. R7 `--yes` refuses
credential rows -> 6. R8 printf writer; `--build-cache` a flag only if
E1 adopted -> 3.4/8/9. R9 Phase E: `build` kept, E1 last, no v1
speed-up promised -> 8/10/4. R10 `--ephemeral` stays cut -> 7.

Transfers to the build (recorded, not re-designed): the residual
race classes ADR 0040 already accepts (ancestor symlinks on
agent-writable destination paths; a `Create`-after-fast-path name race
that now only costs a refused second run), test-inventory completeness
beyond 3.7, the exact wording of every refusal (tests assert the rule
fired, CLAUDE.md), and the stdout-escape helper's exact line handling
for partial lines at EOF.
