# Agent-CLI credential and state-dir mechanics

Research notes for the shared-auth skill design: how Claude Code, OpenAI Codex
CLI, Google Gemini CLI, and xAI Grok CLI store identity vs per-project state,
how they write
credential files, and what breaks when one credential is shared across
containers. Gathered 2026-07-06. Empirical facts come from this box (Claude
Code 2.1.201 live session; Codex CLI 0.142.5 logged in via ChatGPT; Gemini CLI
**not installed** -- its section rests on docs and source only). Egress was
open enough for all needed fetches; the only web failures were moved URLs
(404s), each re-located -- no findings below are firewall-degraded. `gh` is not
installed in this box (used the GitHub REST API over WebFetch instead).
The Grok section was added 2026-07-09 (Grok CLI 0.2.93 live in this box,
authenticated with a seeded consumer credential; grok is closed source, so its
section is empirical + vendor docs, with the write/rotation claims explicitly
unverifiable from source).

## Summary table

| | Claude Code | Codex CLI | Gemini CLI | Grok CLI |
|---|---|---|---|---|
| Identity/credential | `.credentials.json` (Linux; macOS uses Keychain) | `auth.json` | `oauth_creds.json`, `google_accounts.json` | `auth.json` (keyed by auth-scope URL), 0600, sibling `auth.json.lock` |
| Per-project state in a ROOT-LEVEL FILE | **YES** -- `.claude.json` `projects` key (trust, allowed tools, MCP local scope) | **YES** -- `config.toml` `[projects."<path>"] trust_level` | **YES** -- `trustedFolders.json` (per-folder trust) | none observed (closed source; `worktrees.db`/`active_sessions.json` are root-level but not trust-shaped) |
| Per-project state in subdirectories | `projects/<encoded-cwd>/` (transcripts, auto memory) | none keyed by project (`sessions/` is date-keyed) | `history/<projectId>/`, `tmp/<project_hash>/` | `memory/<project-slug>-<hash8>/`; `sessions/` is date-keyed |
| Machine-wide prefs | `settings.json`, `CLAUDE.md`, `skills/`, `keybindings.json` | `config.toml` (same file as trust), `skills/`, `plugins/` | `settings.json`, `commands/`, `skills/`, `policies/`, `keybindings.json` | `config.toml`, `skills/`, `AGENTS.md` (global rules) |
| Cache/ephemeral | `cache/`, `paste-cache/`, `shell-snapshots/`, `session-env/`, `backups/`, `.last-*` | `tmp/`, `.tmp/`, `cache/`, `models_cache.json`, `*.sqlite`, `log/`, `shell_snapshots/`, `packages/` (binary cache) | `tmp/`, per-project temp trees | `models_cache.json`, `logs/`, `upload_queue/`, `marketplace-cache/`, `downloads/` (binary!) |
| Config-dir relocation env | `CLAUDE_CONFIG_DIR` (moves everything incl. `.claude.json` + credentials) | `CODEX_HOME` | **none** (open feature requests) | `GROK_HOME` (**verified**: moves auth + sessions + config) |
| Credential write pattern | closed source; temp+rename observed for sibling files (symlink-replacing) | **in-place** truncate+write, 0600, no rename | **in-place** `fs.writeFile`, 0600, no rename | closed source; **unverified** (gate 1, grok-shared-auth) |
| Refresh-token rotation | **rotated server-side, single-use**; concurrent refresh races documented | server MAY return new refresh_token; in-process lock only | Google installed-app flow; rotation not typical (unverified) | ~7-day expiry, silent OIDC refresh; rotation **unverified** (gate 2) |
| Plan-scoped env token | `CLAUDE_CODE_OAUTH_TOKEN` via `claude setup-token` (1 year, Pro/Max/Team/Enterprise, inference-only) | `CODEX_ACCESS_TOKEN` (ChatGPT token) + device-code login (beta) | **none** -- API key only (different billing) | **none** -- `XAI_API_KEY` only (different billing); native `grok login --device-auth` |
| Vendor stance on copying creds between machines | supported implicitly (devcontainer volume docs); copied-file refresh is buggy (#21765) | **explicitly endorsed**: copy `auth.json` to headless machines | silent; headless docs say "use cached credential or env vars" | silent; its own installer reads `auth.json` tokens for authenticated downloads (empirically: a copied credential works) |

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
  `~/.claude/.credentials.json` exists, interactive Claude Code (2.1.202)
  rides the STORED access token for its requests -- `/status` still claims
  env-token auth -- and the env token's presence suppresses the refresh
  cycle, so the box 401s ~8h after the last `/login`. The documented
  precedence holds headless (and when no credentials file exists). Fix per
  box: `mv ~/.claude/.credentials.json{,.bak}` + relaunch; the
  claude-shared-auth env hook warns at launch on this combination and, on
  an interactive launch, offers to make that move itself.
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

## xAI Grok CLI (0.2.93, empirical + vendor docs -- closed source)

Added 2026-07-09, from a live authenticated install in this box (the host's
consumer credential, seeded read-only for inspection). Grok CLI ships as a
closed-source static binary, so unlike Codex/Gemini nothing here is
source-verified; claims are either observed live or quoted from the vendor
README.

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

**Unverified** -- closed source, and forcing a rewrite requires a login or a
refresh (a refresh against a live shared credential risks invalidating the
host's session if rotation turns out single-use, so it was not run
unilaterally). The `auth.json.lock` sibling shows the CLI coordinates writes
across processes on one machine; whether the write is in-place (symlink
survives) or temp+rename (symlink replaced) is **gate 1** for
`grok-shared-auth`, cheaply testable: device-auth login through the asserted
symlink, then check the link survived and the credential landed in the
identity volume.

### 3. Refresh-token rotation semantics

Vendor docs: consumer tokens "expire after 7 days"; OIDC tokens
"auto-refresh silently via the stored `refresh_token`";
`GROK_AUTH_EARLY_INVALIDATION_SECS` (default 300) controls how early a token
is treated as expired -- setting it very high forces refresh-on-next-use,
which is the lever for **gate 2** (two concurrent boxes, forced refresh,
neither session dies -- same gate as Gemini OAuth). Whether refresh tokens
are single-use is undocumented and unverified.

### 4. Env overrides

- `GROK_HOME` -- relocates the config dir. **Verified live** (0.2.93): with
  `GROK_HOME` set to a fresh dir containing only a seeded `auth.json`, the
  CLI authenticated from it, read `AGENTS.md` global rules from it (codeword
  probe), and created `sessions/`, `config.toml`, `logs/` etc. under it --
  nothing written back to `~/.grok`. This gives Grok the codex-shaped
  binary/state split: binary stays in image `~/.grok`, state volume mounts at
  `$GROK_HOME`.
- `XAI_API_KEY` -- static API key (console.x.ai, separate API billing), takes
  precedence over the file credential; rotation-proof by construction, same
  status as Gemini's API-key path.
- `GROK_AUTH_PROVIDER_COMMAND` -- delegate auth to an external binary whose
  stdout becomes the stored token; the enterprise/SSO escape hatch.
- No plan-scoped long-lived token equivalent to `claude setup-token`.

### 5. Vendor guidance on sharing

Silent on copying `auth.json` between machines. Adjacent evidence both ways:
the vendor's own `install.sh` parses `~/.grok/auth.json` and uses the token
to authenticate downloads (the file is treated as the canonical, portable
credential), and a host-minted credential seeded into a fresh `GROK_HOME`
worked for inference in this box; but the 7-day expiry means any shared copy
goes stale fast without a shared refresh path -- which is exactly what the
gate-pending symlink mechanism would provide. Native headless login exists:
`grok login --device-auth` (aliased `--device-code`) -- documented by
`grok login --help` on 0.2.93; the vendor README lags the binary and does not
mention the flag.

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
- Shared identity: `oauth_creds.json`, `google_accounts.json`,
  `installation_id` (+ `mcp-oauth-tokens.json` if MCP auth should be shared).
- Per-project: `history/<projectId>/`, `tmp/<project_hash>/`.
- Unsplittable-by-mount: `trustedFolders.json` (root-level per-folder trust)
  -- though `GEMINI_CLI_TRUSTED_FOLDERS_PATH` can relocate it, which makes it
  the ONE root-level file among all three agents with an official escape
  hatch.
- No home-relocation env var: the shared/per-project seam must be built by
  mounting into `~/.gemini` itself.

**Grok CLI**
- Shared identity: `auth.json` (both write claims gate-pending -- see the
  Grok section; the symlink mechanism ships to run the gates, mirroring the
  Gemini-OAuth stance).
- Per-project: `sessions/`, `memory/<project-slug>-<hash8>/`, `config.toml`
  (holds per-project-ish UI prefs plus `[custom_endpoints]` API keys -- the
  secret-capable field is also why grok has no prefs seeding),
  `agent_id`, `worktrees.db`, caches.
- Unsplittable-by-mount: none known root-level; but `auth.json.lock` stays
  per-project while its target is shared, so lock-based write coordination
  does not extend across boxes (codex-shaped benign-race residual at worst,
  pending gate 2).
- `GROK_HOME` moves the tree (verified) but cannot split it -- same as
  `CODEX_HOME`.

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
   subscription auth REQUIRES the `oauth_creds.json` file at literally
   `~/.gemini/oauth_creds.json`, so the design must mount/symlink within the
   real home dir; and per-project trust needs `GEMINI_CLI_TRUSTED_FOLDERS_PATH`
   pointed at the per-project volume.

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
