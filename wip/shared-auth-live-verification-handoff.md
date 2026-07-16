# Shared-auth fix-up -- live-verification handoff

**Status: IN PROGRESS -- all buildable work committed (branch
`fix-up-other-agents`, commits `31f5305`..`219dd6a`); four LIVE-BOX checks
remain, each of which flips a vouch. Delete this file once all four vouches
are flipped and the results are folded into `docs/AGENT-CREDENTIAL-MECHANICS.md`
+ the relevant ADR.**

Written 2026-07-16 handing off from the session that did the source pass +
grilling + build. Full narrative in `.byre-devlog/DIARY.md` (container-local,
won't travel -- everything load-bearing is repeated here).

## Why these are all that's left

The session ran a source pass over the codex/gemini/opencode trees in
`/home/dev`, grilled the fix list with Pete, built every code/doc change, and
got a clean codex review. What could NOT be done from the dev container: run
live boxes (only codex+grok are installed here; opencode+gemini are source-only;
no docker, no `~/.ssh`, no `byre-inttest`). Every remaining item needs a real
box, host-side, which is the next agent's job WITH Pete driving the host.

**Discipline (do not shortcut): a vouch follows its field gate, never source
alone (the grok-v1 lesson). Do NOT flip any `companion_for` -> `shared_auth_for`
(or set `mcp = "inject"`) until its live check below has actually passed.**

## The four remaining checks

### 1. opencode MCP live-load  (fastest; no rebuild, no login)
- **Goal:** confirm opencode loads MCP servers from `OPENCODE_CONFIG_CONTENT`
  (the adapter's whole mechanism). Source says `opencode mcp list` reads the
  merged config (`packages/opencode/src/cli/cmd/mcp.ts`, `McpListCommand` ->
  `configuredServers(config)`), so injected servers appear there.
- **Command (any opencode box, or opencode on the host -- current byre is fine,
  this doesn't need the new code):**
  ```sh
  OPENCODE_CONFIG_CONTENT='{"mcp":{"byre-probe":{"type":"local","command":["echo","hi"]}}}' opencode mcp list
  ```
- **Pass:** `byre-probe` appears in the listed servers.
- **On pass:** in `internal/builtins/skills/opencode/skill.toml` --
  (a) add `jq` to `[build] apt`; (b) add the wrapper to `[build] files`:
  `"opencode-mcp-launch.sh" = "/usr/local/bin/byre-opencode-mcp-launch"` and a
  `RUN chmod +x /usr/local/bin/byre-opencode-mcp-launch` dockerfile line
  (mirror the codex skill exactly); (c) change `[agent] command` to
  `byre-opencode-mcp-launch --auto`; (d) set `mcp = "inject"`; (e) delete the
  "NOT yet wired" paragraph in the MCP seam note. The adapter
  (`opencode-mcp-launch.sh`) and its 3 unit tests (`wrapper_test.go`) are
  already committed (`82ec10c`).

### 2. opencode two-box API-key sharing
- **Goal:** the field gate for `opencode-shared-auth` (scoped to API-key logins
  only, `13c206f`). Confirm a key stored in box A is used by box B via the
  shared inode.
- **Setup:** two byre boxes with `opencode` + `opencode-shared-auth`, both built
  from THIS branch (so the new hook + API-key-only scope are baked in -- see
  "Rebuilding boxes" below).
- **Steps:** box A `opencode auth login` -> pick an API-key provider, store a
  key. Box B (launched after): `opencode auth list` shows the same entry riding
  the shared volume; a trivial `opencode run` works without a second login.
  A dummy key is fine -- this proves the sharing mechanism, not the key's
  validity.
- **On pass:** swap `companion_for = "opencode"` -> `shared_auth_for = "opencode"`
  in `opencode-shared-auth/skill.toml`, and update
  `TestOpencodeSharedAuthCompositionResolves` (it currently asserts
  `companion_for` + no `shared_auth_for`) + the
  `TestBuiltinSharedAuthDeclarations` table in the skills package.

### 3. gemini two-box OAuth sharing
- **Context:** rotation is already PROVEN SAFE -- Google installed-app refresh
  tokens are NON-rotating (primary docs, cited in the mechanics doc Gemini §3),
  so there is NO cascade risk and NO ~1h-expiry vigil needed. The field failure
  was diagnosed as the auth-DIALOG's `clearCachedCredentialFile()` rm'ing the
  symlinked `oauth_creds.json` before login (dialog-only; verified in the 0.51
  bundle) -- FIXED by seeding `selectedType=oauth-personal` (`74e2e49`,
  hardened `219dd6a`). This check confirms the seed actually prevents the fork
  and the shared login works cross-box.
- **Setup:** two byre boxes with `gemini` + `gemini-shared-auth`, both from THIS
  branch.
- **Steps:** box A: launch `gemini`, do the Google "Login with Google"
  paste-code flow (should NOT show the auth-method picker -- the seed skipped
  it). Verify `~/.gemini/oauth_creds.json` is a symlink into
  `~/.byre-identity/gemini/` (NOT a local regular file -- that would mean the
  fork still happens). Box B (after): `gemini -p 'say ok'` works with no login
  prompt. GOTCHA the previous session hit: do NOT open gemini's `/auth` dialog
  after login -- it rm's the symlink and re-forks; the seed is meant to prevent
  the dialog opening at all, so if it still appears, that's a finding.
- **On pass:** swap `companion_for = "gemini"` -> `shared_auth_for = "gemini"`;
  update the same test + declarations table as above.

### 4. grok ~6h broker rollover  (the slow one)
- **Goal:** the pre-existing FIELD gate for `grok-shared-auth` v2 (ADR 0036):
  watch a real box refresh through the broker across the ~6h access-token
  lifetime. Unchanged by this session. Either wait it out, or force a broker
  refresh (the broker honors `GROK_AUTH_EXPIRED=1`; see
  `grok-shared-auth/grok-auth-broker.sh`) and confirm the backend accepts the
  refreshed pair end-to-end.
- **On pass:** swap `companion_for = "grok"` -> `shared_auth_for = "grok"`.

## Rebuilding boxes on this branch (prereq for checks 2 & 3)

Skills are `go:embed`-ed into the byre binary, so the new hook/skill code only
reaches a box via a rebuilt binary. Host-side, in this repo on branch
`fix-up-other-agents`: `go build -o /tmp/byre-branch ./cmd/byre` (or
`go install`), then use THAT binary to `byre develop` the test projects (which
rebuilds their images with the new hooks). Check 1 does NOT need this; checks
2 & 3 do.

## Non-obvious context the next agent needs

- **opencode is API-key ONLY, by ruling.** OAuth entries race (single-use
  Anthropic refresh) and are WARNED, not shared/blocked -- Pete's footgun-
  doctrine call (warn, never quarantine; moving a live credential aside is the
  grok-v1 clobber mistake). codex review pushed back on "warn not enforce"; it
  was consciously deferred as doctrine, do not re-open without Pete.
- **gemini stays per-file symlinks, NOT whole-tree `GEMINI_CLI_HOME`.** Pete
  vetoed sharing history across boxes; context is 100%-per-box (only creds
  shared) and must stay that way. codex + opencode are already correctly
  per-box (state volumes default to `scope = "project"`; only `auth.json` is
  machine-scoped).
- **`codex logout` is a shared-auth footgun** -- it server-revokes the refresh
  token for EVERY box; documented (codex-shared-auth skill.toml + mechanics
  doc), not gated (byre can't intercept it).
- **Two deferred hardening items (Pete's ruling, noted in TODO):** the `$SHARED`
  symlink-target check (assert hooks' lexical equality is sound because they
  overwrite; the deeper "is $SHARED itself an escaping link" is accepted
  residual) and opencode's upstream torn-read/`{}`-store-destruction hazard
  (API-key-only shrinks it; upstream's bug to fix).
- **Green-before-handoff still owed:** the production changes (hooks, adapter)
  have passed `go test ./...` + `go vet` in-container, but the gated
  `BYRE_DOCKER_TESTS=1` engine-side suite (`byre-inttest`) has NOT run -- it
  needs the host/VM. Run it before calling any of this shipped
  (memory: inttest-vm-before-handoff).

## Commits this session (branch fix-up-other-agents)
```
219dd6a gemini-shared-auth: harden selectedType seed against odd settings shapes (codereview)
944c160 TODO: shared-auth pass 2026-07-16 -- buildable work done, three VM checks remain
29e944b docs: source-pass corrections for codex/gemini + codex logout hazard
13c206f opencode-shared-auth: scope to API-key logins, warn on OAuth entries
74e2e49 gemini-shared-auth: seed selectedType=oauth-personal to kill the login-fork
f26447f opencode-shared-auth: record torn-read/{}-store-destruction as deferred upstream residual
82ec10c opencode: build the MCP inject adapter (ADR 0033), pending live-verify vouch
ad15bfc shared-auth: record $SHARED target-check deferral; note why lexical assert is sound
026944c codex-login: trust only codex's own identity dir, not a wildcard
31f5305 gemini skill: install ripgrep (gemini falls back to GrepTool without it)
```
