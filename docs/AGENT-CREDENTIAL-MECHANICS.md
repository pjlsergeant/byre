# Agent-CLI credential and state-dir mechanics

Research notes for the shared-auth skill design: how Claude Code, OpenAI Codex
CLI, Google Gemini CLI, xAI Grok CLI, and OpenCode store identity vs
per-project state, how they write
credential files, and what breaks when one credential is shared across
containers. Gathered 2026-07-06. Empirical facts come from this box (Claude
Code 2.1.201 live session; Codex CLI 0.142.5 logged in via ChatGPT; Gemini CLI
**not installed** -- its section rests on docs and source only). Egress was
open enough for all needed fetches; the only web failures were moved URLs
(404s), each re-located -- no findings below are firewall-degraded. `gh` is not
installed in this box (used the GitHub REST API over WebFetch instead).
The Grok section was added 2026-07-09 (Grok CLI 0.2.93 live in this box,
authenticated with a seeded consumer credential; grok was closed source then,
so the section began as empirical + vendor docs) and upgraded 2026-07-16 from
the published Grok Build source (see the section header).
The OpenCode section was added 2026-07-16 (opencode 1.18.2, the linux-x64
binary from npm, run live in a PROXIED container -- not a byre box; opencode.ai
and models.dev egress were denied there, which itself produced findings.
opencode is open source, but the sst/opencode repo was out of reach in that
session, so "source-verified" in that section means read from the JS embedded
in the shipped binary, plus the vendor's own published auth-plugin package --
weaker than repo source, stronger than docs).

## Summary table

| | Claude Code | Codex CLI | Gemini CLI | Grok CLI | OpenCode |
|---|---|---|---|---|---|
| Identity/credential | `.credentials.json` (Linux; macOS uses Keychain) | `auth.json` | `gemini-credentials.json` (0.49+ FileKeychain, hostname-bound; `oauth_creds.json` is legacy -- see the CORRECTION in the Gemini section), `google_accounts.json` | `auth.json` (keyed by auth-scope URL), 0600, sibling `auth.json.lock` | `auth.json` (provider-keyed multi-credential store), 0600 |
| Per-project state in a ROOT-LEVEL FILE | **YES** -- `.claude.json` `projects` key (trust, allowed tools, MCP local scope) | **YES** -- `config.toml` `[projects."<path>"] trust_level` | **YES** -- `trustedFolders.json` (per-folder trust) | none observed (closed source; `worktrees.db`/`active_sessions.json` are root-level but not trust-shaped) | none observed (no trust dialog; `opencode.db` is root-level but session storage, not trust) |
| Per-project state in subdirectories | `projects/<encoded-cwd>/` (transcripts, auto memory) | none keyed by project (`sessions/` is date-keyed) | `history/<projectId>/`, `tmp/<project_hash>/` | `memory/<project-slug>-<hash8>/`; `sessions/` is date-keyed | none at the file level -- sessions live INSIDE `opencode.db` (sqlite), project identity in-row |
| Machine-wide prefs | `settings.json`, `CLAUDE.md`, `skills/`, `keybindings.json` | `config.toml` (same file as trust), `skills/`, `plugins/` | `settings.json`, `commands/`, `skills/`, `policies/`, `keybindings.json` | `config.toml`, `skills/`, `AGENTS.md` (global rules) | `~/.config/opencode/` (opencode.json config, `AGENTS.md` global rules) -- a SEPARATE XDG dir from the credential |
| Cache/ephemeral | `cache/`, `paste-cache/`, `shell-snapshots/`, `session-env/`, `backups/`, `.last-*` | `tmp/`, `.tmp/`, `cache/`, `models_cache.json`, `*.sqlite`, `log/`, `shell_snapshots/`, `packages/` (binary cache) | `tmp/`, per-project temp trees | `models_cache.json`, `logs/`, `upload_queue/`, `marketplace-cache/`, `downloads/` (binary!) | `~/.cache/opencode/` (incl. `bin/` downloads), `log/`, `snapshot/`, `~/.local/state/opencode/locks` |
| Config-dir relocation env | `CLAUDE_CONFIG_DIR` (moves everything incl. `.claude.json` + credentials) | `CODEX_HOME` | **none** (open feature requests) | `GROK_HOME` (**verified**: moves auth + sessions + config) | XDG vars, per dir (**verified**: `XDG_DATA_HOME` moves auth+db, `XDG_CONFIG_HOME` moves config); no single all-state env |
| Credential write pattern | closed source; temp+rename observed for sibling files (symlink-replacing) | **in-place** truncate+write, 0600, no rename | **in-place** `fs.writeFile`, 0600, no rename | closed source; temp+rename **inferred from the 2026-07-10 field failure** (symlink replaced -- gate 1 FAILED; see §6) | **in-place** write + chmod 0600, no rename (**live-verified through a symlink**) |
| Refresh-token rotation | **rotated server-side, single-use**; concurrent refresh races documented | server MAY return new refresh_token; in-process lock only | Google installed-app flow; rotation not typical (unverified) | ~6h access tokens, silent OIDC refresh; **single-use with chain revocation inferred** (gate 2 FAILED in the field -- see §6) | per PROVIDER: API keys static; Anthropic OAuth rides the same single-use server rotation as Claude Code |
| Plan-scoped env token | `CLAUDE_CODE_OAUTH_TOKEN` via `claude setup-token` (1 year, Pro/Max/Team/Enterprise, inference-only) | `CODEX_ACCESS_TOKEN` (ChatGPT token) + device-code login (beta) | **none** -- API key only (different billing) | **none** -- `XAI_API_KEY` only (different billing); native `grok login --device-auth` | **none** long-lived; `OPENCODE_AUTH_CONTENT` injects the whole store via env (static -- refresh can't write back) |
| Vendor stance on copying creds between machines | supported implicitly (devcontainer volume docs); copied-file refresh is buggy (#21765) | **explicitly endorsed**: copy `auth.json` to headless machines | silent; headless docs say "use cached credential or env vars" | silent; its own installer reads `auth.json` tokens for authenticated downloads (empirically: a copied credential works) | silent |

## Claude Code (2.1.201, empirical + docs)

### 1. State-dir inventory

Empirical, this box (note: this box sets `CLAUDE_CONFIG_DIR=/home/dev/.claude`,
which is also the default location, so layout matches a vanilla install; the
one wrinkle is below under `.claude.json`).

Inside `~/.claude/`:

- `.credentials.json` -- **identity/credential**. Shape (keys only):
  `{claudeAiOauth: {accessToken, refreshToken, expiresAt, scopes,
  subscriptionType, rateLimitTier}}`. Mode 0600. Docs: on Linux credentials
  live at `~/.claude/.credentials.json`; on macOS in the Keychain; with
  `CLAUDE_CONFIG_DIR` set, "the `.credentials.json` file lives under that
  directory instead" (https://code.claude.com/docs/en/authentication,
  Credential management).
- `.claude.json` -- **MIXED, root-level file, the load-bearing problem**. Keys
  observed (53 total) include `oauthAccount` (account/org identity metadata --
  email, org UUID, tiers; no secrets), `projects` (per-project state keyed by
  absolute cwd, e.g. `"/workspace"`: `hasTrustDialogAccepted`, `allowedTools`,
  `mcpServers`, `hasCompletedProjectOnboarding`, last-session metrics),
  machine-wide prefs (`autoUpdates`, `hasCompletedOnboarding`, tips/counters),
  and caches (`cachedGrowthBookFeatures`, `modelAccessCache`, ...). Docs
  confirm: "OAuth session, MCP server configurations for user and local
  scopes, per-project state (allowed tools, trust settings), and various
  caches" (https://code.claude.com/docs/en/settings) and "The `projects` key
  tracks per-project state like trust-dialog acceptance and last-session
  metrics" (https://code.claude.com/docs/en/claude-directory).
  Location wrinkle: default path is `~/.claude.json` (home root, OUTSIDE
  `~/.claude/`); with `CLAUDE_CONFIG_DIR` set it moves to
  `$CLAUDE_CONFIG_DIR/.claude.json` (empirical, this box: live 36 KB copy at
  `~/.claude/.claude.json`, plus a stale 435-byte `~/.claude.json` containing
  only machine-level keys, evidently written before the env var was in
  effect). Docs: "If you set CLAUDE_CONFIG_DIR, every ~/.claude path on this
  page lives under that directory instead"
  (https://code.claude.com/docs/en/claude-directory).
- `projects/<encoded-cwd>/` -- **per-project, directory-keyed** (path with `/`
  -> `-`, e.g. `-workspace`): session transcripts `<session-id>.jsonl`,
  per-session subdirs, and `memory/MEMORY.md` (auto memory). Empirical.
- `settings.json`, `CLAUDE.md`, `skills/`, `plugins/`, `keybindings.json` --
  **machine-wide prefs** (user scope).
- `history.jsonl` -- **machine-wide root-level file** containing prompt
  history across projects (entries carry the project path inline; splitting it
  per-project is not possible by mount).
- Cache/ephemeral: `backups/` (timestamped `.claude.json` backups, written
  every few minutes while a session runs -- empirical), `cache/`,
  `paste-cache/`, `session-env/<session-id>/`, `shell-snapshots/`, `tasks/`,
  `file-history/<session-id>/`, `sessions/`, `.last-update-result.json`,
  `.last-cleanup`, `mcp-needs-auth-cache.json`.

### 2. Credential write patterns

Claude Code is closed source; no direct code evidence. Empirical: cannot
confirm rename vs in-place for `.credentials.json` itself -- the file was
rewritten mid-session today (mtime 17:26, a refresh) but no further refresh
occurred within the observation window, so no before/after inode pair was
captured. Strong adjacent evidence that the config writer uses
**write-temp-then-rename, which replaces symlinks with regular files**:
issue #40857 ("writing to symlinked file replaces symlink with regular file",
observed on `.claude/settings.local.json`;
https://github.com/anthropics/claude-code/issues/40857). Treat every
Claude-managed file -- `.credentials.json`, `.claude.json`, settings -- as
rename-hazardous for file-level symlinks until proven otherwise. Refresh
trigger: access-token expiry (`expiresAt`) and 401 responses; #54443
documents an early-401-before-expiresAt path
(https://github.com/anthropics/claude-code/issues/54443).

### 3. Refresh-token rotation semantics

**Rotation is real and single-use, and it is the documented failure mode.**
Multiple open issues describe exactly the shared-credential scenario:

- #24317 "Frequent re-authentication required with multiple concurrent Claude
  Code sessions (OAuth refresh token race condition)" -- refresh tokens are
  single-use; when one process refreshes, the old refresh token is invalidated
  server-side (https://github.com/anthropics/claude-code/issues/24317).
- #56339 "Multiple CLI sessions race on ~/.claude/.credentials.json token
  refresh" -- no file locking; sessions holding old tokens in memory 401,
  their refresh with the stale token fails, cascading logout
  (https://github.com/anthropics/claude-code/issues/56339). Also #48786.
- Shared-single-copy vs forked-copies: a shared inode is the LESS bad case --
  after one container wins the refresh, the others can re-read the winning
  token from disk (the issues above are about in-memory staleness + races
  within a refresh window). Forked copies are strictly worse: a stale copy's
  refresh token is dead the moment any other copy refreshes. #21765 adds that
  a copied `.credentials.json` on a headless machine may not even attempt the
  refresh path correctly
  (https://github.com/anthropics/claude-code/issues/21765).

### 4. Env overrides

- `CLAUDE_CONFIG_DIR` -- relocates the entire state dir including
  `.claude.json` and `.credentials.json` (docs above; empirical, this box).
- `CLAUDE_CODE_OAUTH_TOKEN` -- long-lived OAuth token from
  `claude setup-token`: "generate a one-year OAuth token"; "authenticates with
  your Claude subscription and requires a Pro, Max, Team, or Enterprise plan.
  It is scoped to inference only"; printed to terminal, never saved; not read
  in `--bare` mode (https://code.claude.com/docs/en/authentication#generate-a-long-lived-token).
  CLI reference: "Generate a long-lived OAuth token for CI and scripts. Prints
  the token to the terminal without saving it. Requires a Claude subscription"
  (https://code.claude.com/docs/en/cli-reference). This is a static token --
  no refresh, no rotation, no file writes: **the rotation-proof alternative**.
- Auth precedence (top wins): cloud-provider vars -> `ANTHROPIC_AUTH_TOKEN` ->
  `ANTHROPIC_API_KEY` -> `apiKeyHelper` -> `CLAUDE_CODE_OAUTH_TOKEN` ->
  `/login` OAuth credentials (https://code.claude.com/docs/en/authentication).
  **Host-falsified for interactive use (2026-07-07, three boxes):** when
  `~/.claude/.credentials.json` holds a **`claudeAiOauth` block** (a stored
  inference login), interactive Claude Code (2.1.202) rides the STORED
  access token for its requests -- `/status` still claims env-token auth --
  and the env token's presence suppresses the refresh cycle, so the box
  401s ~8h after the last `/login`. The documented precedence holds
  headless (and without a stored login). CAUTION (box-verified
  2026-07-15): `.credentials.json` is NOT only the inference login -- MCP
  server OAuth tokens live in the same file under a top-level `mcpOAuth`
  key, and in a shared-token box the file is typically mcpOAuth-ONLY
  (healthy, load-bearing state; the hijack above does not apply to it).
  Check for `"claudeAiOauth"` before treating the file as a stale login.
  Fix per box when it IS one: `mv ~/.claude/.credentials.json{,.bak}` +
  relaunch -- knowing the move also signs the box out of any MCP servers
  riding `mcpOAuth` (re-auth in-session via `/mcp`); the claude-shared-auth
  firstrun hook detects the `claudeAiOauth` case at launch, warns, and on
  an interactive launch offers that move itself (never for mcpOAuth-only).
  `apiKeyHelper` (a settings key, not env) shells out for a credential,
  re-called after 5 min or on 401 (`CLAUDE_CODE_API_KEY_HELPER_TTL_MS`) --
  a viable "fetch token from host/volume" hook.

**Host-verified addendum (2026-07-07, byre box):** `CLAUDE_CODE_OAUTH_TOKEN`
authenticates headless/inference use, but interactive Claude Code's
first-run gate is its ONBOARDING state, not its auth state: with a fresh
`CLAUDE_CONFIG_DIR` (no `.claude.json`) the setup wizard runs -- login step
included -- without consulting the env token. Seeding
`{"hasCompletedOnboarding": true}` into a fresh config dir makes the token
take effect directly (trade: no first-run theme picker; `/config` re-opens
it). The shared-auth skill does this seed on fresh volumes only.

### 5. Vendor guidance on sharing

Devcontainer docs prescribe a **named volume at `~/.claude`** to persist
"authentication token, user settings, and session history" across rebuilds,
and note the reference config uses `source=claude-code-config-${devcontainerId}`
to isolate per project vs sharing one volume across all repositories -- i.e.
both shared and per-project volumes are sanctioned patterns. "If you mount the
volume somewhere other than ~/.claude, set CLAUDE_CONFIG_DIR to the mount
path." For Codespaces they recommend carrying auth as a
`CLAUDE_CODE_OAUTH_TOKEN` (or `ANTHROPIC_API_KEY`) secret instead of files.
Also: "dev containers do not prevent a malicious project from exfiltrating ...
the Claude Code credentials stored in ~/.claude"
(https://code.claude.com/docs/en/devcontainer).

## OpenAI Codex CLI (0.142.5, empirical + source)

### 1. State-dir inventory

Empirical, this box. `CODEX_HOME=/home/dev/.codex-home` is set; `~/.codex`
itself holds only the standalone-binary package cache (`packages/`, `tmp/`) --
evidence that binary cache and state can already cohabit oddly. Inside
`$CODEX_HOME`:

- `auth.json` -- **identity/credential**. Shape (keys only):
  `{OPENAI_API_KEY: null, auth_mode: "chatgpt", last_refresh,
  tokens: {access_token, refresh_token, id_token, account_id}}`. Mode 0600.
- `config.toml` -- **machine-wide prefs + per-project trust in one root-level
  file** (absent on this box; documented). Per-project trust:
  `[projects."/absolute/path"] trust_level = "trusted"`
  (https://developers.openai.com/codex/config-reference). Open issues ask to
  split trust out of it: #15433, #14601
  (https://github.com/openai/codex/issues/15433,
  https://github.com/openai/codex/issues/14601).
- `sessions/YYYY/MM/DD/rollout-*.jsonl` -- session transcripts, **date-keyed,
  not project-keyed** (empirical). Project identity is inside the file, so no
  mount-level per-project split is possible.
- Machine-wide: `skills/`, `plugins/`, `installation_id`.
- Cache/ephemeral: `models_cache.json`, `state_5.sqlite`, `logs_2.sqlite`,
  `memories_1.sqlite`, `goals_1.sqlite` (agent state/memory dbs --
  machine-wide, not project-keyed at the file level), `cache/`, `log/`,
  `shell_snapshots/`, `tmp/`, `.tmp/`.

### 2. Credential write patterns

**In-place write, NOT rename** -- symlink-safe. `FileAuthStorage::save` in
`codex-rs/login/src/auth/storage.rs` (main, 2026-07-06): opens with
`options.truncate(true).write(true).create(true)` (+ `mode(0o600)` on unix),
then `write_all` + `flush` -- no temp file, no rename, and no file locking
(https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/storage.rs,
~lines 187-226). Path is `codex_home.join("auth.json")`. Storage modes: File,
Keyring, Auto (keyring-then-file), Ephemeral.

Refresh triggers (`codex-rs/login/src/auth/manager.rs`): proactive when the
access token expires within 5 minutes
(`CHATGPT_ACCESS_TOKEN_REFRESH_WINDOW_MINUTES = 5`) or `last_refresh` is older
than 8 days (`TOKEN_REFRESH_INTERVAL = 8`); reactive on 401 via an
`UnauthorizedRecovery` state machine. Each refresh rewrites `auth.json`
(`persist_tokens`).

### 3. Refresh-token rotation semantics

`persist_tokens` replaces the stored refresh token only if the server returns
one: `if let Some(refresh_token) = refresh_token { tokens.refresh_token =
refresh_token; }` -- i.e. **rotation is server-driven and code-supported**.
Concurrency control is an in-process `Semaphore::new(1)` plus a
reload-from-disk-before-write check (manager.rs) -- this serializes refreshes
within one process and makes a process pick up another writer's newer token
from the shared file, but there is **no cross-process/file lock**, so two
containers refreshing in the same 5-minute window can still race. With a
shared single inode the loser can re-read the winner's token on the next
reload; with forked copies each copy's refresh token dies when another copy
rotates (if the server invalidates old refresh tokens -- server policy, not
visible in client source). Practical signal: the vendor endorses copying
`auth.json` around (below), and this box's `auth.json` shows
`last_refresh` two days old with heavy daily use, so refresh is infrequent
(8-day interval; ChatGPT access tokens are long-lived) -- the race window is
small in practice.

### 4. Env overrides

- `CODEX_HOME` -- relocates the whole state dir; "credentials are cached at
  ~/.codex/auth.json under the CODEX_HOME directory (which defaults to
  ~/.codex)" (https://developers.openai.com/codex/auth; empirical, this box).
- Plan-scoped env token: **yes** -- `CODEX_ACCESS_TOKEN` carries a ChatGPT
  token (https://developers.openai.com/codex/auth; constant
  `CODEX_ACCESS_TOKEN_ENV_VAR` read in manager.rs, taking precedence over
  stored auth). Being an access token (not refresh), it expires -- unlike
  Claude's one-year setup-token. Device-code login (`codex login
  --device-auth`, beta) covers headless first-login.
- API key: `OPENAI_API_KEY` (and `CODEX_API_KEY_ENV_VAR` in manager.rs) --
  "OpenAI bills API key usage through your OpenAI Platform account at standard
  API rates", vs ChatGPT sign-in which "uses workspace permissions and ChatGPT
  plan credits" (https://developers.openai.com/codex/auth).

### 5. Vendor guidance on sharing

Explicit endorsement: "If you can complete the login flow on a machine with a
browser, you can copy your cached credentials to the headless machine" --
including a Docker-container variant -- with the caveat "Treat
~/.codex/auth.json like a password: it contains access tokens"
(https://developers.openai.com/codex/auth).

## Google Gemini CLI (NOT installed in this box -- docs + source only)

> **CORRECTION, live-verified 2026-07-07 (gemini-cli 0.49.0, host test +
> npm tarball read):** the credential no longer lives in
> `oauth_creds.json`. In containers (no native keytar) 0.49 uses
> **FileKeychain**: `~/.gemini/gemini-credentials.json`, AES-256-GCM,
> key = scrypt("gemini-cli-oauth", salt = `hostname-username-gemini-cli`).
> Consequences: (1) the credential is HOSTNAME-BOUND -- docker's default
> per-container hostname means the login is lost on every rebuild and can
> never be shared across boxes; byre's gemini skill now pins
> `--hostname byre`. (2) Write is `fs.writeFile` in place
> (fileKeychain.js saveData) -- symlink-safe. (3) `oauth_creds.json` is
> legacy; link both. (4) `GEMINI_CLI_TRUST_WORKSPACE=true` is the
> highest-precedence folder-trust override (trust.js checkPathTrust).
> The inventory below predates this and stands for older versions.


### 1. State-dir inventory

From `packages/core/src/config/storage.ts` (main, 2026-07-06;
https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/config/storage.ts).
Global dir is hardcoded `path.join(homedir(), '.gemini')`:

- Identity/credential: `oauth_creds.json` (OAuth tokens),
  `google_accounts.json` (account info), `mcp-oauth-tokens.json`,
  `a2a-oauth-tokens.json`.
- **Per-project state as a root-level file**: `trustedFolders.json` (folder
  trust decisions; relocatable via `GEMINI_CLI_TRUSTED_FOLDERS_PATH`).
- Per-project, directory-keyed under the global dir: `history/<projectId>/`
  (chat history; projectId is a slug, migrated from a path hash) and
  `tmp/<project_hash>/` (shell_history, checkpoints, logs, plans, tasks,
  chats).
- Machine-wide prefs: `settings.json`, `commands/`, `skills/`, `agents/`,
  `policies/`, `keybindings.json`, `installation_id`.
- Project-side (in the repo, not home): `<project>/.gemini/settings.json`,
  policies, extensions.

### 2. Credential write patterns

**In-place write, NOT rename** -- symlink-safe.
`packages/core/src/code_assist/oauth2.ts` writes the cache with
`await fs.writeFile(filePath, credString, { mode: 0o600 });` -- no temp file,
no rename
(https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/code_assist/oauth2.ts,
`cacheCredentials`). Refresh is delegated to the google-auth-library
`OAuth2Client`; a `client.on('tokens', ...)` handler rewrites the cache file
whenever the library refreshes. An env-selected encrypted-keystore mode
bypasses the file entirely.

### 3. Refresh-token rotation semantics

Client code stores whatever the library emits. Google's installed-app OAuth
flow does not normally rotate refresh tokens on each access-token refresh --
the same refresh token stays valid until revoked (from model knowledge,
unverified -- re-check before relying on it; no rotation-race issue was found
in the gemini-cli repo during this pass). If that holds, shared-single-copy
and even forked copies are both tolerant: concurrent refreshes just mint
parallel access tokens.

### 4. Env overrides

- Config-dir relocation: **none**. `storage.ts` hardcodes `~/.gemini`; only
  `GEMINI_CLI_TRUSTED_FOLDERS_PATH` and `GEMINI_CLI_SYSTEM_SETTINGS_PATH` /
  `GEMINI_CLI_SYSTEM_DEFAULTS_PATH` relocate specific files. Open feature
  requests for a home override: #2815, #8440
  (https://github.com/google-gemini/gemini-cli/issues/2815,
  https://github.com/google-gemini/gemini-cli/issues/8440). So the split must
  happen AT `~/.gemini` itself (mounts/symlinks inside it).
- Plan-scoped env token: **none**. Auth methods are Login with Google (OAuth,
  uses Google AI Pro/Ultra subscription quotas), `GEMINI_API_KEY` /
  `GOOGLE_API_KEY` (AI Studio key, pay-per-use billing, separate from
  subscription), and Vertex (`GOOGLE_GENAI_USE_VERTEXAI`,
  `GOOGLE_CLOUD_PROJECT`, `GOOGLE_APPLICATION_CREDENTIALS`) -- "Your
  authentication method affects your quotas, pricing, Terms of Service"
  (https://google-gemini.github.io/gemini-cli/docs/get-started/authentication.html).
  There is no way to pass the subscription OAuth credential through env; only
  the cached `oauth_creds.json` file carries it.

### 5. Vendor guidance on sharing

Effectively silent. Headless docs say non-interactive mode works "if an
existing authentication credential is cached", else use env vars (same URL as
above) -- no endorsement or prohibition of copying `oauth_creds.json`, but
the cached-credential path is the only subscription option headless.

## xAI Grok CLI (0.2.93-0.2.101, empirical + vendor docs + SOURCE)

Added 2026-07-09, from a live authenticated install in this box (the host's
consumer credential, seeded read-only for inspection); at the time the CLI
was closed source and nothing here could be source-verified. **UPGRADED
2026-07-16: xAI published the Grok Build source**, and a full pass over the
auth subsystem (local tree `~/dev/grok-build`; the tree carries all three
log strings the 0.2.93 field failure produced, and the in-box binary is
0.2.101) confirmed, corrected, and extended the record below. Source-derived
claims are marked; the empirical history is kept verbatim — it is what the
gates were run against.

### 1. State-dir inventory

Default home `~/.grok` MIXES the binary with state: the installer downloads
the real binary to `~/.grok/downloads/grok-<platform>` and symlinks
`~/.grok/bin/{grok,agent}` to it (relative links). State observed live:
`auth.json` (0600) + `auth.json.lock`, `config.toml`, `agent_id`, `sessions/`
(date-keyed), `memory/<project-slug>-<hash8>/` (project-keyed, hash from the
git remote so clones/worktrees share), `models_cache.json`, `logs/`,
`skills/`, `upload_queue/`, `marketplace-cache/`, `worktrees.db`,
`active_sessions.json`. Global agent rules: `$GROK_HOME/AGENTS.md` (also
accepts `Claude.md`/`AGENT.md`/`Agents.md`; each rules file capped at 10,000
chars).

`auth.json` shape (observed, and confirmed by the vendor installer's own
parser): `{"<auth-scope-url>": {"key": "<token>", ...}, ...}` with two known
scopes -- `https://auth.x.ai::<client-id>` (OIDC) and
`https://accounts.x.ai/sign-in` (legacy).

### 2. Credential write patterns

**Gate 1 FAILED in the field (2026-07-10; see §6).** Originally unverified
-- closed source, and forcing a rewrite risked the live credential. The
field answered it: refreshed pairs never reached the shared file through
the symlink (its mtime froze at login time while boxes kept refreshing),
which is the temp+rename signature -- the rename replaces the symlink with
a private local file and the credential silently forks.

**SOURCE-CONFIRMED 2026-07-16** (no strace needed): `write_auth_json`
writes a sibling `auth.json.<pid>.tmp` (0600, fsync'd) and `rename(2)`s it
over the path -- which replaces a symlink with a regular file, exactly the
field signature. One nuance: on `ENOSPC` only, it falls back to a
non-atomic in-place truncate+rewrite (with restore-on-failure), and THAT
path follows symlinks -- irrelevant to any design, but it explains why a
fork needs a healthy disk. File-sharing through a symlink stays dead as a
mechanism.

### 3. Refresh-token rotation semantics

**Gate 2 FAILED in the field (2026-07-10; see §6).** Vendor docs: consumer
tokens "expire after 7 days"; OIDC tokens "auto-refresh silently via the
stored `refresh_token`"; `GROK_AUTH_EARLY_INVALIDATION_SECS` (default 300)
controls how early a token is treated as expired. The field corrected two
things. First, the working lifetime is the ACCESS token's **~6h**, not "7
days" -- the shared copy expired the same night it was minted. Second,
rotation is effectively **single-use with permanent chain failure**: the
stale shared pair went "ServerRejected" with `refresh_chain short-circuit
on permanent failure` in the event log, unrecoverable short of a fresh
login. (Strictly, the field evidence could not distinguish "reuse revoked
the chain" from "the pair aged out unrefreshed"; byre's working assumption
was revoke-on-reuse. **SOURCE-CONFIRMED 2026-07-16, replay experiment not
needed**: vendor comments state a doubly-spent refresh token "triggers
invalid_grant + token-family revocation", and grok holds its file lock
across the IdP call specifically so "only one participant ever spends a
given refresh token". `invalid_grant`/`invalid_client` are the ONLY
terminal error codes; `invalid_grant` verdicts are sticky until the
credential itself changes.) Sessions re-read `auth.json` lazily on expiry
("auth recovery: disk token expired") -- and, source-addendum: a config
file-watcher DOES hot-swap a changed `auth.json` into running sessions
(watching `$GROK_HOME` only -- it ignores `GROK_AUTH_PATH`, see §6), plus
a proactive background refresh fires ~300s+jitter before expiry. Either
way rotation does not kill in-flight access tokens, which is why
concurrent sessions on ONE real file coexist. Cross-PROCESS refresh on one
file is properly serialized (flock held across the IdP call + adopt-from-
disk-before-spending); cross-CONTAINER it can never be -- see §6's lock
finding.

### 4. Env overrides

- `GROK_HOME` -- relocates the config dir. **Verified live** (0.2.93): with
  `GROK_HOME` set to a fresh dir containing only a seeded `auth.json`, the
  CLI authenticated from it, read `AGENTS.md` global rules from it (codeword
  probe), and created `sessions/`, `config.toml`, `logs/` etc. under it. This
  gives Grok the codex-shaped binary/state split: binary stays in image
  `~/.grok`, state volume mounts at `$GROK_HOME`. ONE known leak in the
  split (found by Grok itself reviewing byre's grok skill, reproduced
  2026-07-09): the CLI **extracts its bundled product packs into
  `$HOME/.grok/bundled`** while skill discovery reads `$GROK_HOME/bundled`,
  so under the split the bundled review/design/execute-plan/pr-babysit
  skills silently vanish unless `$GROK_HOME/bundled` is symlinked to the
  extraction dir (byre's grok-bundled firstrun hook does exactly that).
- `XAI_API_KEY` -- static API key (console.x.ai, separate API billing);
  rotation-proof by construction. CORRECTED 2026-07-11: the shipped auth
  guide says the key is a **fallback** -- "if you have already signed in
  interactively, the stored session token takes precedence" -- so a stored
  login SHADOWS the key (the reverse of what this doc first recorded). Also
  ruled out as the shared-auth path on cost: xAI API billing is a separate
  pay-per-token track (no subscription credits), ~50x the flat subscription
  at coding-agent volumes.
- `GROK_AUTH_PROVIDER_COMMAND` -- delegate auth to an external command;
  the seam under grok-shared-auth v2 (ADR 0036). PUBLICLY documented as of
  the 0.2.101 user guide (no longer gray). Full contract (source +  guide,
  2026-07-16): run via `sh -c`, stdin null; stdout is either a bare token
  (assumed 30-day lifetime, or `GROK_AUTH_TOKEN_TTL`) or JSON
  `{access_token, refresh_token?, expires_in?, issuer?}`; exit non-zero =
  failure; `GROK_AUTH_EXPIRED=1` is set on refresh re-runs. TWO executors:
  the login/mint path (300s timeout, stderr surfaced) and the refresh path
  (**5s timeout, stderr swallowed, killed on overrun**). With the command
  set, `build_refresher` NEVER constructs the OIDC refresher -- the command
  is the refresh authority for every token type, on expiry and on 401;
  401 recovery never falls back to interactive login.
- `GROK_AUTH_PATH` -- relocates the credential FILE (default
  `$GROK_HOME/auth.json`) for the whole auth-manager: reads, writes, login
  persistence, and the lock (a sibling `auth.json.lock`). Source-verified
  2026-07-16; NOT in the user guide. Two bypasses: the config watcher and
  hot-reloader stay pinned to `$GROK_HOME/auth.json`. byre uses it for
  exactly one thing -- seeding the shared store through grok's own login
  (ADR 0036) -- and never for cross-box file sharing (the lock cannot
  serialize across containers; §6).
- No plan-scoped long-lived token equivalent to `claude setup-token`.

### 5. Vendor guidance on sharing

Silent on copying `auth.json` between machines. Adjacent evidence both ways:
the vendor's own `install.sh` parses `~/.grok/auth.json` and uses the token
to authenticate downloads (the file is treated as the canonical, portable
credential), and a host-minted credential seeded into a fresh `GROK_HOME`
worked for inference in this box; but the ~6h access-token lifetime means
any shared copy goes stale within hours without a shared refresh path --
and the symlink mechanism that was meant to provide one failed its field
gate (§6); the working shared refresh path is the v2 broker (ADR 0036),
which sidesteps sharing grok-written files entirely. Native headless login
exists:
`grok login --device-auth` (aliased `--device-code`) -- documented by
`grok login --help` on 0.2.93 and by the shipped user guide
(`~/.grok/docs/user-guide/02-authentication.md`); the TOP-LEVEL vendor README
lags the binary and does not mention the flag. The in-box side of the flow
uses `accounts.x.ai/oauth2/device` (observed live, unauthenticated probe).

### 6. Field failure record and retirement (2026-07-10/12, ADR 0023)

`grok-shared-auth` v1 (the codex-shaped symlink) shipped 2026-07-09 to run
gates 1 and 2; both failed within a day, twice. Timeline and evidence:
shared `/home/dev/.byre-identity/grok/auth.json` mtime frozen at Jul 9
18:55 against a 00:55 expiry; `unified.jsonl` logged "auth recovery: disk
token expired" then `refresh_chain short-circuit on permanent failure`
("ServerRejected"). Refreshes rename-forked into per-box files (gate 1);
the frozen shared pair died permanently within hours (gate 2); and the
skill's every-launch "shared wins" heal then clobbered working per-box
logins with the corpse. User-facing shape: "grok randomly breaks", and
headless runs (`grok -p`) HANG on an interactive device prompt -- grok's
auth-failure fallback is interactive login, and the device code lands in a
debug file nobody is watching.

Findings from the retirement investigation (2026-07-11, this box):

- `auth.json.lock` content is `PID:timestamp` (`23185:1783753745`), i.e. a
  create-exclusively / steal-if-stale lockfile, and it persists after grok
  exits. PID liveness is meaningless across container PID namespaces, so
  **grok's own lock cannot serialize refreshes between boxes** no matter
  how the files are shared -- this forecloses "share the whole GROK_HOME"
  as a fix, and any lock-symlink scheme besides (`O_EXCL` creation does
  not follow symlinks).
  **SOURCE-CORRECTED 2026-07-16 -- conclusion unchanged, mechanism
  sharper**: the lock is better than the file content suggested (a REAL
  `flock`, held across the whole IdP refresh; `PID:timestamp` is only a
  staleness sentinel; within one PID namespace it is sound) and worse
  across boxes than assumed: the staleness probe is `kill(pid, 0)`, so a
  holder in another PID namespace reads as ESRCH = dead and a contender
  **unlinks a LIVE lock near-instantly** (first non-blocking attempt, the
  60s threshold never consulted), leaving both processes flocked on
  different inodes and both spending the refresh token. Cross-container
  serialization needs a lock grok doesn't interpret -- the v2 broker's.
- `GROK_AUTH_PROVIDER_COMMAND` (then shipped-user-guide only; publicly
  documented by 0.2.101): grok delegates credential acquisition to an
  external command -- stderr surfaces to the user, **stdout is stored as
  the access token**, non-zero exit falls back to interactive login (on
  the login path only; full contract in §4). This inverts the
  acts-first/observed-after problem and became the seam under the v2
  rebuild.
- The "headless runs HANG on a device prompt" failure shape is
  **vendor-fixed** by 0.2.101: `grok -p`'s auth path bails with re-auth
  instructions instead of falling through to interactive login (source:
  the print-mode client rejects interactive-only auth methods; verified
  live in this box against an empty `GROK_HOME` -- exit 1, no hang).
  Boxes pinning older binaries can still hang; `grok update` or a rebuild
  moves them past it.

Resolution history: 2026-07-12, the skill was retired to a resolvable
no-op stub; the grok skill's login hook removes ANY symlinked `auth.json`
(healing damaged boxes at next launch); per-box logins became the
supported shape; two rebuild designs were parked with pre-build gates.
**2026-07-16: rebuilt as the v2 auth broker (ADR 0036)** -- every parked
gate was answered from the published source (this section carries the
upgrades) and the broker design won: no resident process, one flock in
the machine identity volume serializing all refreshes, the shared store
seeded through grok's own login via `GROK_AUTH_PATH`, dead chains moved
aside self-healingly (v1's orphaned volume credential included). The
watcher+jitter design is obsolete (its race now has a confirmed maximal
price -- family revocation) and the no-broker `GROK_AUTH_PATH` file-share
variant is foreclosed by the ESRCH lock-steal above. The skill ships
`companion_for = "grok"`; the `shared_auth_for` vouch waits for the one
gate source cannot answer -- a live ~6h rollover through the broker in a
real box (the v1 lesson: vouch AFTER the field gate). The `XAI_API_KEY`
path stays ruled out on cost (see §4).

## OpenCode (1.18.2, empirical + binary-embedded source)

Added 2026-07-16, from a live install in a proxied container (see the intro
note: opencode.ai and models.dev were DENIED there, npm was open). opencode is
open source (sst/opencode) but the repo itself was out of reach in that
session; "source" below means the JS embedded in the shipped Bun binary
(readable with `strings`, minified) and the vendor's published
`opencode-anthropic-auth` npm package -- the binary may hold additional
bytecode-compiled chunks the strings pass cannot see, which is why some
claims below carry a repo-recheck caveat.

### 1. State-dir inventory

Textbook XDG split (**verified live**, `opencode debug paths` -- the CLI
ships its own path introspection, use it):

- `~/.local/share/opencode/` -- **data: the credential AND the sessions**.
  `auth.json` (0600) -- a **provider-keyed multi-credential store**:
  `{"<providerID>": {"type":"api","key":...} | {"type":"oauth","access":...,
  "refresh":..., "expires":...,"accountId"?} | {"type":"wellknown",...}}`
  (shape from the binary's Auth schema classes and a live API-key login).
  Alongside it: `opencode.db` (+ `-shm`/`-wal`) -- ALL session/message state
  in one machine-global sqlite db (nothing project-keyed at the file level;
  project identity lives in-row), `log/`, `repos/`, `snapshot/`.
- `~/.config/opencode/` -- **machine-wide prefs**: `opencode.json`
  (auto-created with a `$schema` line on first run), global `AGENTS.md`
  (agent rules -- see §4), `plugin/`, `themes/`.
- `~/.cache/opencode/` -- cache, including `bin/` (runtime binary downloads:
  LSP servers, self-update artifacts).
- `~/.local/state/opencode/locks/` -- lock files.
- No trust dialog / per-project trust file was observed (no `projects` key,
  no `trustedFolders.json` equivalent) -- the root-level-mixed-scope-file
  problem the other three agents share does not appear. Per-project config
  is the in-repo `opencode.json` (+ `OPENCODE_DISABLE_PROJECT_CONFIG` to
  refuse it).

### 2. Credential write patterns

**In-place write, NOT rename -- symlink-safe, and live-verified.** The
binary's `Auth.set`/`Auth.remove` route through `FileSystem.writeJson` =
`writeFileString` + `chmod 0600` -- no temp file, no rename (binary source).
Empirical confirmation (2026-07-16): with `auth.json` pre-created as a
symlink to a file in another directory, a real `opencode auth login`
(API-key path) wrote **through the link** -- link intact, target same inode,
target chmod 600. This is the codex shape; grok's rename-fork failure mode
(ADR 0023) does not apply. (The GitLab-specific writer in the same binary is
also in-place `writeFileSync` + chmod.) Repo-recheck caveat: a
bytecode-compiled provider flow could in principle write differently;
re-verify against sst/opencode source when reachable.

### 3. Refresh-token rotation semantics

**Per provider -- auth.json is a store of many credentials, each with its
own rotation policy.**

- API-key entries (`type:"api"`, most providers): static, no rotation --
  sharing-safe by construction.
- Anthropic Claude Pro/Max OAuth (`type:"oauth"`): the token endpoint is
  `console.anthropic.com/v1/oauth/token` and refresh triggers at request
  time when `expires` has passed (vendor auth-plugin source; the flow is
  compiled into 1.18.2, the plugin package is its published ancestor).
  Rotation policy is Anthropic's server side -- the SAME single-use refresh
  tokens documented in the Claude Code section (#24317 etc.), so concurrent
  refresh from two boxes is the same cascade risk. opencode re-reads
  `auth.json` lazily (`Auth.all` reads the file per access, no cache --
  binary source), so a shared single inode gets the codex-style tolerance:
  a race loser picks up the winner's pair on its next read. UNVERIFIED
  live: an actual multi-box refresh race (byre's opencode-shared-auth gate).
- `OPENCODE_AUTH_CONTENT` (env) overrides the file entirely when set --
  static injection; a refresh cannot write back to env, so it only suits
  non-rotating credentials.

### 4. Env overrides

- Relocation is per-XDG-dir, **no single all-state env** (the one agent of
  the five with no `*_HOME`): `XDG_DATA_HOME` moves data incl. `auth.json`
  and `opencode.db` (**verified live**, `debug paths` + the auth path shown
  by `opencode auth list`); `XDG_CONFIG_HOME` moves config (**verified
  live**); `XDG_CACHE_HOME`/`XDG_STATE_HOME` read in source. WRINKLE:
  `OPENCODE_CONFIG_DIR` exists but `debug paths` does NOT reflect it
  (1.18.2) -- one code path substitutes it for the config dir, another
  ignores it; treat the XDG vars as the reliable seam and re-check upstream
  before relying on `OPENCODE_CONFIG_DIR`.
- Global agent rules: `<config-dir>/AGENTS.md` plus project `AGENTS.md`
  files walked up from cwd (binary source: the rules resolver deduplicates
  `[config/AGENTS.md, ...upward hits]`). No size cap observed in the
  resolver (grok's 10k cap has no analogue found -- absence unproven).
- Headless/permissions (byre-relevant, all binary-source-verified):
  `opencode run` is the headless mode; on a permission "ask" it prints
  "permission requested: ...; auto-rejecting" and REPLIES REJECT -- **it
  never hangs** (grok's silent-death lesson answered by design). `--auto`
  (hidden aliases `--yolo`, `--dangerously-skip-permissions`) flips asks to
  approve-once. The default `build` agent's ruleset opens with
  `{permission:"*", action:"allow"}` (bash/edit included; live via
  `opencode debug agent build`) with ask/deny carve-outs (`doom_loop`,
  `external_directory` ask; `question`, `plan_enter` deny), and
  `OPENCODE_PERMISSION` (JSON) merges into the permission config.
- Egress fact (**verified live** against a deny-by-default proxy): opencode
  fetches its provider/model catalog from **models.dev** at startup and
  retries hard. With it blocked, the auth-login picker silently degrades to
  API-key-only (the Claude Pro/Max option never appears) and default-model
  selection falls back to an embedded snapshot. models.dev is FUNCTIONAL
  egress for opencode, not telemetry. Related knobs seen in source:
  `OPENCODE_DISABLE_MODELS_FETCH`, `OPENCODE_MODELS_URL`/`_PATH`,
  `OPENCODE_DISABLE_AUTOUPDATE`, `OPENCODE_DISABLE_DEFAULT_PLUGINS`.
- Other credential-relevant env: `OPENCODE_API_KEY` (OpenCode Zen, the
  vendor gateway at opencode.ai/zen), provider-native keys
  (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GITHUB_TOKEN`, AWS pair, ...)
  are honored directly (`opencode auth list` shows an "Environment"
  section).

### 5. Vendor stance on sharing

Silent -- no endorsement or prohibition of copying `auth.json` found in the
reachable material. The mechanics (in-place writes, lazy re-reads) are the
codex shape, the friendliest to a shared single inode;
the rotation risk is inherited per provider (Anthropic OAuth = Claude-class
single-use refresh). byre's `opencode-shared-auth` ships gate-pending on
exactly that question (its skill.toml is the status record).

## Implications for the shared-auth split

Per agent, what goes in the shared identity volume vs per-project volume:

**Claude Code**
- Shared identity: a `claude setup-token` in the identity volume, exported as
  `CLAUDE_CODE_OAUTH_TOKEN` at launch (decided + shipped, ADR 0017). NOT
  `.credentials.json` -- sharing that file is the path this doc's Claude
  section argues against (temp+rename breaks symlinks, single-use refresh
  cascade), and a leftover copy alongside the token 401s the box ~8h after
  its last login (see the precedence falsification above). Arguably also
  machine-wide prefs (`settings.json`, `CLAUDE.md`, `skills/`) if "one login,
  one persona" is the goal.
- Per-project: `projects/` (transcripts + auto memory), `file-history/`,
  `session-env/`, `tasks/`, `shell-snapshots/`, caches.
- Unsplittable-by-mount: `.claude.json` (identity metadata + per-project trust
  + machine prefs + caches in ONE root-level file) and `history.jsonl`
  (cross-project prompt history in one file).
- `CLAUDE_CONFIG_DIR` moves the whole tree but cannot split it.

**Codex CLI**
- Shared identity: `auth.json`.
- Per-project: nothing is project-keyed on disk; `sessions/` and the sqlite
  state/memory dbs are machine-global. Sharing them across containers means
  sharing session history and agent memory too; keeping them per-project means
  each container evolves its own.
- Unsplittable-by-mount: `config.toml` (machine prefs + per-project trust in
  one root-level file). `CODEX_HOME` moves the tree but cannot split it.

**Gemini CLI**
- Shared identity: `gemini-credentials.json` (the 0.49+ FileKeychain
  credential -- hostname-bound, so sharing also requires pinning the box
  hostname; byre's gemini skill pins `--hostname byre`), legacy
  `oauth_creds.json` (pre-0.49; link both, per the CORRECTION above),
  `google_accounts.json`, `installation_id` (+ `mcp-oauth-tokens.json`
  if MCP auth should be shared).
- Per-project: `history/<projectId>/`, `tmp/<project_hash>/`.
- Unsplittable-by-mount: `trustedFolders.json` (root-level per-folder trust)
  -- though `GEMINI_CLI_TRUSTED_FOLDERS_PATH` can relocate it, which makes it
  the ONE root-level file among all three agents with an official escape
  hatch.
- No home-relocation env var: the shared/per-project seam must be built by
  mounting into `~/.gemini` itself.

**Grok CLI**
- Shared identity: **none, currently** -- the symlinked `auth.json` failed
  both its gates in the field and the skill is retired (§6, ADR 0023);
  per-box logins are the supported shape, rebuild designs parked.
- Per-project: `sessions/`, `memory/<project-slug>-<hash8>/`, `config.toml`
  (holds per-project-ish UI prefs plus `[custom_endpoints]` API keys -- the
  secret-capable field is also why grok has no prefs seeding),
  `agent_id`, `worktrees.db`, caches.
- The lock caveat that was recorded here as a benign residual turned out
  load-bearing: `auth.json.lock` stays per-box while its target is shared,
  and the race it leaves open is NOT codex-benign -- grok's rotation makes
  it fatal (§3, §6).
- `GROK_HOME` moves the tree (verified) but cannot split it -- same as
  `CODEX_HOME`.

**OpenCode**
- Shared identity: `auth.json` via a file-level symlink into the identity
  volume (byre's opencode-shared-auth) -- mechanically sound (in-place
  writes, live-verified write-through), but note it shares the WHOLE
  multi-provider store, and the Anthropic-OAuth rotation gate is pending
  (see §3/§5 above). API-key entries are rotation-immune.
- Per-project: everything else in the data dir -- `opencode.db` (all
  sessions), `log/`, `repos/`, `snapshot/`. Nothing project-keyed at the
  file level, so per-project state volumes give per-project sessions (the
  codex situation).
- Unsplittable-by-mount: nothing observed -- the credential is a standalone
  root-level file in a dir that is otherwise sessions/cache, and trust
  state doesn't exist. The cleanest split of the five agents.
- Relocation: per-XDG-dir envs move data and config independently
  (verified); no single all-state env var.

### Hard blockers and hazards

1. **Root-level mixed-scope files in all three agents.** Claude
   `.claude.json`, Codex `config.toml`, Gemini `trustedFolders.json` each mix
   per-project trust with machine-wide state in a single root-level file. A
   pure nested-mount split (shared volume at the root, per-project volumes at
   subdirs, or vice versa) puts each of these wholly on one side: shared side
   -> project trust decisions leak across projects (trusting a path in one
   container trusts it everywhere -- arguably fine under byre's
   one-path-per-project model, since `/workspace`-style paths collide
   meaningfully); per-project side -> login-adjacent state (`oauthAccount`,
   onboarding flags) fragments and Claude may re-onboard per project.
2. **Rename-over-symlink hazard is Claude-specific.** Codex and Gemini write
   credentials in place (source-verified), so a file-level symlink from the
   per-project volume into the shared volume survives writes. Claude Code
   demonstrably replaces symlinked config files via temp+rename (#40857) --
   file-level symlinks for `.credentials.json` / `.claude.json` must be
   assumed to break on first write, silently forking the credential.
   Directory-level splits (bind mounts, or a symlinked parent dir) are the
   safe shape: rename happens within the same directory and lands on the
   mounted volume.
3. **Claude refresh rotation is the real invalidation risk.** Refresh tokens
   are single-use; concurrent refresh from multiple sessions/containers causes
   cascading logout (#24317, #48786, #56339) even on a shared inode, and
   forked copies are worse. Mitigations: shared single inode (never copies);
   or sidestep files entirely with `CLAUDE_CODE_OAUTH_TOKEN` from
   `claude setup-token` (one-year, static, inference-only, Pro/Max/Team/
   Enterprise) -- the most robust multi-container answer for Claude.
4. **Codex is the easy case.** Vendor-endorsed auth.json copying, in-place
   writes, `CODEX_HOME` relocation, infrequent refresh (5-min-to-expiry or
   8-day interval), reload-before-write. Shared volume + `CODEX_HOME` works;
   residual risk is the unlocked cross-process refresh race.
5. **Gemini has no env relocation and no plan-scoped env token** -- shared
   subscription auth REQUIRES the cached credential file at literally
   `~/.gemini/` (0.49+: `gemini-credentials.json`, hostname-bound, so the
   box hostname must be pinned too; pre-0.49: `oauth_creds.json` -- link
   both), so the design must mount/symlink within the real home dir; and
   per-project trust needs `GEMINI_CLI_TRUSTED_FOLDERS_PATH` pointed at
   the per-project volume.

## Source list

- Empirical, this box: Claude Code 2.1.201 live `~/.claude` (with
  `CLAUDE_CONFIG_DIR` set) and `~/.claude.json`; Codex CLI 0.142.5
  `$CODEX_HOME=~/.codex-home` (ChatGPT auth mode); `gemini` not installed.
- Empirical, this box (2026-07-09): Grok CLI 0.2.93 -- unauthenticated
  install into a clean `$HOME`; `GROK_HOME` relocation + global `AGENTS.md`
  pickup with a seeded credential; `grok login --help` (device-auth flag);
  a read-only mount of the host's live `~/.grok` (dir inventory, `auth.json`
  shape and mode, `auth.json.lock`).
- https://code.claude.com/docs/en/authentication (credential storage,
  precedence, setup-token)
- https://code.claude.com/docs/en/settings (~/.claude.json contents)
- https://code.claude.com/docs/en/claude-directory (full dir inventory,
  CLAUDE_CONFIG_DIR semantics)
- https://code.claude.com/docs/en/devcontainer (volume-mount guidance)
- https://code.claude.com/docs/en/cli-reference (setup-token)
- anthropics/claude-code issues #40857, #24317, #48786, #56339, #54443, #21765
- https://developers.openai.com/codex/auth (methods, billing, headless
  copying, CODEX_HOME, CODEX_ACCESS_TOKEN, device-auth)
- https://developers.openai.com/codex/config-reference (projects trust_level)
- openai/codex source: codex-rs/login/src/auth/storage.rs (in-place save),
  codex-rs/login/src/auth/manager.rs (refresh windows, rotation, semaphore),
  codex-rs/login/src/token_data.rs; issues #15433, #14601
- google-gemini/gemini-cli source: packages/core/src/config/storage.ts (path
  inventory), packages/core/src/code_assist/oauth2.ts (in-place write,
  tokens-event rewrite); issues #2815, #8440
- https://google-gemini.github.io/gemini-cli/docs/get-started/authentication.html
- https://x.ai/cli (Grok CLI product page); https://x.ai/cli/install.sh (the
  installer is also the best public spec of `auth.json`: its `read_grok_token`
  parses the scope-keyed shape, and it falls back to unauthenticated download)
- Grok CLI vendor README (shipped in `~/.grok/README.md`, 0.2.93):
  Authentication, Automatic Credential Refresh, Environment Variables,
  AGENTS.md sections
- Empirical, proxied container (2026-07-16): opencode 1.18.2
  (`opencode-linux-x64` from registry.npmjs.org) -- `debug paths` under
  default/`XDG_DATA_HOME`/`XDG_CONFIG_HOME`/`OPENCODE_CONFIG_DIR`;
  `auth list` (credential path + env section); `debug agent build`
  (default permission ruleset); a real API-key `auth login` through a
  symlinked `auth.json` (inode-preserving in-place write); headless
  `opencode run` error path (exit 1, no hang); models.dev/models.github.ai
  connect attempts observed at the deny-by-default proxy
- opencode binary-embedded JS (1.18.2, via `strings`): `Auth.set`/`Auth.all`
  + `FileSystem.writeJson` (in-place write), the run-command
  permission.asked handler (auto-reject vs `--auto`), the rules resolver
  (`<config>/AGENTS.md` + upward project files), `OPENCODE_*` runtime-flag
  surface, XDG path resolution
- `opencode-anthropic-auth` 0.0.13 (npm; the vendor's published auth
  plugin, ancestor of the compiled-in flow): claude.ai /
  console.anthropic.com OAuth authorize + token/refresh endpoints,
  `api.anthropic.com/api/oauth/claude_cli/create_api_key`
