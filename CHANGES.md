# Changes

## v1.1.0 -- 2026-07-19

- **`byre worktree` now runs the repository's git entirely inside the
  box.** Creating a worktree used to run `git worktree add` on the host,
  and git runs the repository's own machinery -- hooks, filters -- as it
  works; code a repo brings belongs inside the box, not on your machine.
  Both halves of creation moved: the registration runs in a short-lived
  container on the project image (with no network -- it needs none), and
  the files are checked out inside the box at first launch. No host-side
  mutating git remains; the host's role is reduced to read-only probes,
  making the mount point, and starting containers. Two things you'll
  notice: the new worktree's files appear at first launch (not before),
  and `byre worktree` needs a container engine -- it says so and stops if
  there isn't one, rather than leave you a half-made worktree. A worktree
  you make yourself with `git worktree add` is unchanged -- that was
  always yours.

- **The generated Dockerfile re-asserts byre's security files at the
  tail.** Docker's rule is last-write-wins, and the project's `files`
  entries and raw build lines are emitted after byre's own blocks -- so a
  project could silently COPY over the launcher, the launch gate, or the
  firewall's enforcement script, leaving `byre status` claiming
  deny-by-default that the image no longer delivered. byre already forced
  USER/ENTRYPOINT/HEALTHCHECK to the tail; the same treatment now covers
  the security-critical file *contents*, so the claim is true rather than
  clobberable. Legibility over refusal: a `files` entry targeting one of
  these paths still builds, and `byre develop` / `byre dockerfile` print a
  note that byre's copy wins there.

- **Host-side reads of agent-writable paths can no longer hang, stall, or
  mislead byre.** A wave of hardening from external reports and paid
  review rounds, closed as a class rather than per-instance: a FIFO,
  device node, symlink swap, or mid-read growing file planted in the
  project tree (or a hand-dropped skill dir) could previously wedge
  develop/status indefinitely, buffer unbounded data into host memory, or
  race a delivery into reading a directory the user never named. Every
  such read now goes through one shared choke point that judges the
  opened descriptor (never the pathname), bounds what it reads, and
  degrades to a scoped, legible failure instead of blocking -- covering
  build staging, `byre deliver` (including a directory source-selection
  race on both engines), package load and fetch, the preset drift check,
  and the worktree's git probes.

- **Shared-auth onboarding works for installed third-party agents.**
  Accepting shared auth with save-as-default for a qualified agent id
  (`owner/name`) wrote an unparseable config line and aborted onboarding;
  pick keys are now quoted.

- **Sharper conflict and uninstall messages.** Cross-source mount/volume
  collisions name both sides with provenance ("skill byre/docker-host's
  mount ... collides with config's mount ...") instead of restating one
  path twice, and the uninstall/replace reference scan now covers named
  layers and follows symlinked config dirs, so an in-use package can't
  dodge the warning.

- **The Volumes screen groups by scope.** Project-scoped and
  machine-scoped volumes render as separate groups in the config editor,
  matching how their lifecycles differ.

- **State-integrity fixes from an external review.** One wave of smaller
  repairs: a corrupted installed-package index can no longer point package
  removal outside the store (digests are validated at read, and every
  deletion site re-checks); `byre status` now says "unknown" with the
  engine's error when a found docker/podman binary won't answer, instead
  of the confident lie "not running"; a failed `skill fork`/`template
  fork` no longer leaves a partial package poisoning the retry (the fork
  stages beside its destination and publishes with one rename); the temp
  directories bundled packages extract into are cleaned up at exit rather
  than accumulating until the OS sweeps /tmp; and the macOS deliver-app
  installer checks both destinations before touching either, so a foreign
  Quick Action refuses the install up front instead of after the app half
  was already committed.

- **First compatibility sunset.** Several transition aids for behavior
  that changed before v1.0 have reached the end of their support window
  and are removed together:
  - `shared_auth_declined` in `default.config` (v0.1.7's decline record;
    nothing has read it since the offer's default became No). The stale
    key still parses and is ignored -- delete the line at your leisure.
  - The adoption-record migration: pre-preset `adopted`/`declined` files
    in project stores no longer auto-migrate to the `applied` marker on
    store setup. Old records are inert; if you jump many versions and
    your repo preset shows "unapplied", run `byre preset apply` once to
    re-record it.
  - `byre skill update`, the transitional no-op from the packages
    migration (bundled packages update with byre itself; legacy dirs are
    reported by store setup and `byre skill archive-legacy`).
  - The `devloop` compat stub (the skill was renamed `devlog` 2026-07-12,
    then moved out of byre). A config naming `devloop` now fails loudly
    with the pinned `pjlsergeant/devlog` install remedy instead of
    silently contributing nothing.

## v1.0.0 -- 2026-07-18

- **Groundwork: self-recording site demos (not yet published).** A
  recording tier now rides the tmux TUI test harness: gated tests drive
  real byre flows with an asciinema spectator attached, assert every
  screen, and emit playable casts — so a published demo could never show
  yesterday's screens. The player (self-hosted asciinema-player), the
  Hugo embed, and four recorded scenarios are in-tree but parked: no
  casts ship until the demos look the part.

- **The site's docs became the real documentation.** getbyre.com/docs
  now carries the canonical operational reference -- the full
  configuration vocabulary and merge rules, the volumes model with a
  reset/forget/rebuild comparison, the skills packaging system, a "How
  it works" pipeline walk, the security model (moved from repo
  docs/SECURITY.md), and a thirty-recipe cookbook grouped by situation
  (dotfiles, review loops, API keys, remote SSH, full uninstall...).
  The README trimmed to the conversion story plus a verbatim tldr
  index; the commands page is now generated from the binary's own
  command tree (a hidden `byre commands-page`), pinned by test so it
  can't go stale. CONTRIBUTING.md ships. Demo-cast slots are marked
  across the site, pipeline prototyped.

- **The Volumes screen survives an unreachable engine.** With podman
  installed but its machine stopped (macOS's usual state), the config
  editor's Volumes section died wholesale on the first failed engine
  query — including Docker's perfectly listable rows. An unreachable
  engine now narrows the view instead: its copies become a loud
  "podman unreachable — its copies aren't shown and can't be cleared
  here" note (first line of the engine error only) while reachable
  engines list normally — deliver's partial-pool posture, applied.

- **Config editor: resizing no longer litters the screen, and the
  Volumes table aligns.** Shrinking the terminal left stale fragments
  of the previous frame above the editor (the inline renderer can't
  clear old lines that wrapped at the new width) — a resize now forces
  a full repaint. The Volumes screen sizes its columns from the actual
  rows instead of fixed widths, so long identity names and target-less
  orphan rows no longer shatter the table.

- **Config editor polish** (live field reports): section-rule headers
  align with the field labels below (one-dash prefix); the bundled
  provenance suffix ("bundled (devel)") is gone from the picker lines —
  bundled is the unmarked default, only exceptional provenance (fork,
  local, installed) earns a label; and empty list screens no longer
  float their "+ add" row on a phantom indent (a newline inside a
  styled render — lipgloss pads multi-line renders — also fixed in the
  volumes and skills empty states).

- **Config editor design accents.** Section headers render as dim rules
  with the name in a single accent color (4-bit cyan — the terminal
  theme picks the shade), shared by the cursor and focused picker
  selection; errors are bold red, "Saved" green, and yellow stays
  reserved for cross-project-reach warnings. Long lines truncate with a
  visible ellipsis instead of a silent cut, sub-screens carry a
  breadcrumb, and monochrome terminals degrade to exactly the old
  rendering.

- **A store write can no longer resurrect a forgotten store.** Config
  writes (`AtomicWrite`) used to create their parent directory, so a
  write racing a concurrent `byre forget` could re-create the project
  store WITHOUT its path record — a half-enrollment invisible to the
  id-collision check. Writers no longer create directories: store dirs
  come only from Bootstrap (dir + record together), and a store that
  vanished mid-flow fails the write loudly. The `mcp`/`claude-skill`
  layer verbs also validate before enrolling, matching the config
  editor's ordering, and the global/layer editors now create their
  (store-free) target dirs only when a write actually lands. `byre
  reset` and `byre forget` join the contract from the teardown side: on
  a never-enrolled project they say so and touch nothing, instead of
  enrolling a store just to have something to tear down (or leaving one
  behind after a declined confirm). And `byre deliver` closes the last
  id-keyed surface without the loud collision check: a directory whose
  recorded canonical path names a DIFFERENT project now aborts box
  selection outright — previously the collided id could match (or fall
  through to the sole-session fallback and still select) another
  project's box, silently delivering files into a stranger's /inbox.

- **Orphaned boxes are labeled, not hidden.** A box deliberately
  survives its byre client dying (terminal killed, ssh dropped) — but
  nothing said so: `status` reported a plain "running" and the
  reset/forget refusal pointed at a session no terminal could reach.
  Sessions now carry a `byre.client=<pid>` label; `status` reports
  "running — orphaned" with the ways out (`byre shell`, or
  `<engine> stop <id>`), and the reset/forget refusals name the stop
  command for the unreachable case.

- **Field-QA fixes from the grok explore pass** (`docs/qa/PLAYBOOK.md`):
  the firewall and firewall-open skills' apt lists gain
  `ca-certificates` — Debian's curl doesn't pull the trust store, so the
  skills' own diagnostic curl arrived broken on bare bases
  (`template = "none"`), failing TLS (77) against allowlisted/reachable
  hosts; the "already configured" refusal
  of `--template`/`--agent`/`--shared-auth` now points at `byre config`
  as the way to reconfigure; and `byre mcp add --help` shows an argv
  example so TOML's `command = [...]` key name doesn't tempt typing the
  word `command` into the argv.

- **No write, no enrollment.** Commands that only show or review now leave a
  never-seen project un-enrolled: `byre dockerfile`, `byre dockerrun`, and
  `byre ejectfirewall` are documented side-effect-free but bootstrapped the
  project's host-side store (`~/.byre/projects/<id>/` + path record) before
  rendering, leaving a durable identity record behind — they now run a
  read-only check (`ValidateExisting`) that keeps the loud id-collision
  error without creating anything. The same deferral applies wherever a
  flow can end without a write: `byre config` opened-and-quit-unsaved (a
  save the validator refuses also enrolls nothing; the one exception is
  the ctrl+e $EDITOR hand-off, which must create the store up front so
  the editor has a directory to write into), `byre mcp remove` with
  nothing to remove, and a declined `byre preset apply` no longer create
  the store — enrollment now rides the first landed write. Read-only views
  (`status`, `mcp list`, `claude-skill list`, `preset inspect`) gained the
  same loud collision check — as did `byre shell`, which keys its session
  lookup on the project id and on a collision would exec into another
  project's container — and after a ctrl+e round-trip that wrote the file
  the editor now reports "wrote", not "config unchanged".

- **Box login shells keep the image's PATH.** Debian's `/etc/profile`
  resets PATH in login shells, so a `template = "go"` box had no `go` on
  PATH in `byre shell` or the agent-less foreground shell (the agent
  itself was unaffected — the launcher execs with the container env).
  The image's effective PATH is now baked at build time
  (`/etc/byre/image-path`) and byre's profile shim restores whatever a
  login shell is missing — additive, image order first.

- **Prompts never take garbage silently.** Every interactive y/N
  question (the wizard, the shared-credentials offer, `reset`/`forget`
  confirms, preset/install consents) accepts y/Y/n/N (and i/I where
  offered), Enter keeps the default, and anything else reprompts — a
  typo'd `i` at the shared-auth offer used to read as a silent decline.

- **Field-QA legibility fixes** (from the pass-2 report,
  `docs/qa/PLAYBOOK.md`): a box killed out from under a session says so
  (`exit status 137 (SIGKILL — the box was killed …)`) instead of the
  bare number; `byre deliver` names a worktree box by its own workdir id
  instead of the shared project id; `byre status` lists sibling worktree
  sessions as `workdir-id (container-id)` instead of a bare container
  id; and the config UI no longer renders `[none]` twice for
  agent-less projects.

- **Field-QA fixes across onboarding, deliver, and the config UI** (from
  byre's first agent-driven exploratory QA pass). The shared-auth offer
  is one plain question — "Use machine-wide credentials to log in to
  <agent>?" — with the mechanism, volume, and write-scopes detailed under
  `i`; a third-party companion's provenance rides the question line,
  loud. `byre deliver` exit codes are script-trustworthy: cancelling the
  picker or paste prompt (or an empty paste) now exits 1 like every other
  nothing-was-delivered outcome. The config UI wraps long validation
  errors instead of truncating them at the pane edge, and the Claude
  Skills editor warns — without blocking — when a declared directory
  would fail the build (missing, not a dir, no SKILL.md). Store notices
  name the real byre home instead of a hardcoded `~/.byre`.

- **MCP servers now reach OpenCode boxes (`mcp = "inject"`).** The
  opencode skill launches through `byre-opencode-mcp-launch`, which
  builds an `OPENCODE_CONFIG_CONTENT` from the baked `/etc/byre/mcp.json`
  (deep-merged by opencode over the user's own config — byre's servers
  compose, never replace) and execs opencode. Live-verified 2026-07-17
  on opencode 1.18.3; previously declared servers showed
  declared-but-not-delivered.

- **OpenCode shared auth is now vouched (`shared_auth_for`).** The
  two-box field gate passed live: a real `opencode auth login` in one
  box stores its API key through the shared-auth symlink into the
  machine-wide identity volume, and a second project's box reads it —
  so onboarding now offers the share to opencode users. API-key logins
  only; OAuth entries (Claude Pro/Max etc.) race across boxes and draw
  a launch warning instead.

- **Agent-contract canary tier.** `BYRE_AGENT_TESTS=1` gates new
  integration tests (`internal/commands/agent_contract_test.go`) that
  build each agent's real box — live installers, the same unpinned
  versions a user's rebuild pulls — and probe the agent-side assumptions
  byre's skills depend on (opencode's config-injection seam and
  credential path, codex's `-c mcp_servers` overrides, gemini's
  settings/store tokens, grok's broker env seams). They run every two
  days and on push via `.github/workflows/agents.yml`, so an agent
  release that breaks a contract surfaces within days instead of at a
  user's next rebuild.

- **Shared-auth hardening for Gemini, OpenCode, and Codex** (source pass
  over all three trees; at landing time no `shared_auth_for` vouches were
  flipped — opencode's gate later passed and flipped, see the entry above;
  gemini's and grok's live checks are still pending, see each skill.toml).
  `gemini-shared-auth` now seeds `security.auth.selectedType =
  "oauth-personal"` so Gemini's auth-method dialog — which deletes the
  symlinked credential and silently forks the login off the shared
  volume — never opens; Google OAuth rotation was confirmed safe from
  primary docs (non-rotating refresh tokens, no cross-box cascade).
  `opencode-shared-auth` is scoped to API-key logins: OAuth entries
  (e.g. Claude Pro/Max) race across boxes and now draw a launch warning
  (the credential itself is never touched). The codex and opencode
  login hooks trust only a symlink to the exact shared credential path,
  not anything under the identity volume. `codex logout` is documented
  as a shared-auth hazard (it revokes the refresh token server-side for
  every box). Gemini boxes also install ripgrep (its native search
  tool; it warned and fell back to a slower one every session).

- **macOS binaries run again on current macOS; Go floor is now 1.25.**
  The v0.1.1 darwin release binaries were linked without an `LC_UUID`
  load command (Go 1.22's linker omits it under `CGO_ENABLED=0`), which
  modern macOS dyld aborts on sight ("missing LC_UUID load command")
  and macOS 15's local-network permission system can't attribute. Found
  by the new macOS CI leg on its first run. Go 1.24+ emits the UUID by
  default; the module now requires Go 1.25 (the oldest supported
  release), so `go install` and release builds both link runnable
  darwin binaries.

- **Claude Skills delivery (ADR 0039).** `[[claude_skills]]` blocks
  declare Claude Skills (Anthropic's agent-skill format: a directory
  whose root holds a `SKILL.md`) for the box — wiring, not a grant,
  with the same merge taxonomy as `[[mcp]]`: config layers replace by
  name, skill.toml contributions union after, `!name` closes even a
  skill-declared entry. Config declares a host `path`; a byre skill
  contributes a package-relative `from`. The merged set is validated
  and baked to `/etc/byre/claude-skills` in every image, and the claude
  skill injects it via `--add-dir` (`claude_skills = "inject"`, the
  author's vouch); an agent without the vouch shows
  declared-but-NOT-delivered in the new status section. Manage from the
  CLI (`byre claude-skill add/remove/list`) or the config UI's new
  editor screen.

- **The deliver picker survives a busy stdin.** `cmd | byre deliver`
  with several boxes running now opens the interactive picker on your
  controlling terminal (`/dev/tty` -- the same contract ssh's own
  prompts use) instead of erroring with a candidates list; the piped
  bytes stay the payload. Scripts and truly detached runs (no terminal
  at all) keep the legible `--box`-or-error degradation.

- **Remote delivery over ssh (ADR 0037).** `byre deliver
  ssh://[user@]host[:port] ...` delivers through another machine running
  byre: two headless ssh invocations — enumerate the remote's boxes
  (`--boxes`, a frozen tab-separated line grammar; skipped when `--box`
  is given), pick locally, then stream every source as ONE tar archive
  into a single targeted remote deliver (`--tar -`). No staging, no
  remote temp files — the archive feeds the existing per-file transport
  entry by entry, claims uniquify exactly as local delivery does, and
  the landed paths come back to YOUR stdout and clipboard. Every local
  input mode works unchanged (paths, `-`, the paste beat, clipboard).
  `--proto` pins the whole ssh-facing surface and fails legibly on
  version skew; `--remote-byre` names the remote binary when sshd's
  sparse non-interactive PATH hides it; a partial remote pool (exit 4)
  is never auto-picked. Authentication is your own ssh — keys, agents,
  and prompts behave exactly as `ssh host` would. Supersedes ADR 0021's
  unbuilt scp/`--porcelain`/`--consume` shape.

- **grok-shared-auth v2: the auth broker (ADR 0036).** One Grok
  subscription login shared across boxes again — rebuilt on
  `GROK_AUTH_PROVIDER_COMMAND`, grok's own (now publicly documented)
  external-auth seam, replacing the retired v1 symlink (ADR 0023). A
  small broker script answers grok's credential requests under one
  machine-wide flock, so exactly one process ever spends the single-use
  refresh token; seeding logs in through grok itself
  (`GROK_AUTH_PATH` → the shared store), dead chains self-heal by
  move-aside + re-seed (v1's orphaned credential included), and a
  transient refresh failure degrades to the cached token instead of
  breaking the session. Every pre-build gate from the parked designs
  was answered against the published Grok Build source (the tree also
  upgraded/corrected the AGENT-CREDENTIAL-MECHANICS Grok record:
  temp+rename and reuse-revocation confirmed; the headless auth hang is
  vendor-fixed by 0.2.101; grok's own lock is a real flock that still
  cannot serialize across containers). Hand-enable alongside grok
  (`skills = ["grok-shared-auth"]`); the onboarding offer
  (`shared_auth_for`) waits for the live field gate.

- **Named layers and the extends chain (ADR 0035).** Shared,
  user-authored config baselines at `~/.byre/layers/<name>/layer.config`:
  a project's `byre.config` (or another layer) names at most one parent
  via `extends = "<name>"`, and the cascade becomes
  `default ⊕ template ⊕ chain(root … parent) ⊕ project`. Chains are
  linear and walked to the root; cycles and dangling parents fail loudly
  (the dangling error names the exact path to create). Layers carry the
  full config vocabulary except `template`, are plain files rather than
  packages (send the file to share it), and resolve LIVE at every
  develop -- edit the employer layer once, every extending project's
  next box picks it up. New verbs: `byre layer new|list|validate`;
  `byre config` gains an EXTENDS section and attributes inherited rows
  `layer:<name>`; `byre config --layer <name>` edits a layer with the
  same effective-state editor (ancestor attribution, no template picker,
  cycle-safe extends options). `byre status` prints the chain; a preset
  may carry `extends`, its review resolves the chain and shows
  layer-contributed grants, and apply hard-fails on a layer the machine
  doesn't have. Layer files sit outside the `--self-edit` writable set.

- **MCP provisioning (ADR 0033).** `[[mcp]]` blocks in config layers and
  skill.toml declare MCP servers for the box -- local (`command` argv) or
  remote (`url`), with env var NAMES (never values) and optional extra
  egress. Wiring, not grants: declarations list as configuration in
  `byre status`; the egress a remote url implies (plus declared extras)
  joins the firewall allowlist and status attributed `mcp:<name>`, and
  each consumed env name gets a provided / NOT-provided verdict. Config
  layers replace by name; skill declarations union after the merge;
  `!name` closures subtract LAST (ADR 0030 semantics), so one server can
  be dropped out of a toolkit skill. The effective set bakes
  deterministically to `/etc/byre/mcp.json` in every image (empty set
  included -- the path is a stable contract). Delivery is injection,
  per-agent: the claude skill's command carries `--mcp-config` (an
  injected server shadows a same-name in-box addition, others union in),
  and the codex skill ships a launch wrapper deriving per-invocation `-c`
  overrides from the same file (codex scrubs its servers' env, so
  declared env NAMES ride the file's `x_byre_env` key into codex's
  by-name `env_vars` passthrough). byre never writes an agent's MCP
  state -- a designed state-writing registrar was deliberately walked
  back (ADR 0033 records why). The claude skill also sets
  `ENABLE_CLAUDEAI_MCP_SERVERS=false`, keeping claude.ai account
  connectors out of the box -- ambient host-account authority is not
  inherited just because the login is. Agents without an MCP adapter
  (gemini, grok) degrade honestly: status shows declared-but-NOT-
  delivered plus the baked path. Remote OAuth stays agent-owned on the
  project volume (`claude mcp login --no-browser` works headless via URL
  paste-back).
- **MCP header auth (`headers` on remote declarations).** Static-token
  remote servers -- `Authorization: Bearer`, API keys behind a reverse
  proxy -- are now declarable: `headers = { Authorization = "Bearer
  ${TOKEN}" }`, where `${NAME}` refs expand from the box env AT LAUNCH
  (claude expands natively inside --mcp-config; the codex wrapper maps a
  pure bearer to codex's native bearer_token_env_var, pure `${VAR}` values
  to env_http_headers, and expands the rest itself), so the baked
  mcp.json carries only template text -- token values never enter config
  or image. `byre mcp add` grows `--header "Name: value"` and `--bearer
  TOKEN_ENV_NAME`; the config UI's MCP editor gains a Headers input
  (quoted "Name: value" tokens); header env refs join the status rows'
  provided/NOT-provided verdicts. Literal header fragments are allowed
  and documented as baking into the image, like [env] values.
- **The config UI's MCP editor got rebuilt** around a Kind (local|remote)
  picker driving a single Endpoint input -- requiredness on the labels,
  the name auto-lowercased, and the url's implied egress shown live
  ("opened automatically under a firewall") instead of left to guesswork.
- **claude-shared-auth no longer offers to sign you out of MCP servers.**
  The firstrun hook's stale-login remediation keyed off `.credentials.json`
  EXISTING; MCP server OAuth tokens live in the same file (`mcpOAuth`,
  verified live against a real OAuth MCP), and in a shared-token box the
  file is typically MCP-only -- so one MCP login made every launch falsely
  warn and offer (default Y) a move-aside that nuked the MCP tokens. The
  hook now detects the actual hijacker (a `claudeAiOauth` block); an
  mcpOAuth-only file triggers nothing, and a both-keys move discloses the
  MCP sign-out collateral. Same verification confirmed the good news: MCP
  OAuth persists per-project on the `.claude` volume, injected servers
  pick tokens up by URL, and creds are keyed by name+URL-hash (no stale
  inheritance behind a reused name).
- **IPv6 egress entries (bracketed).** The egress grammar accepts
  `[2001:db8::1]` / `[addr]:port` -- the RFC 3986 convention, parsed with
  the stdlib and canonicalized -- everywhere egress is spoken: the `egress`
  config key, `!` closures (portless still closes every port), skill
  declarations, and MCP remote urls (whose IPv6 endpoints previously drew
  a validation refusal). Both firewall helpers program the literals
  directly via ip6tables -- no resolution step, so a v6-less network can't
  misread a literal as unresolvable. Bare (bracketless) IPv6 is rejected
  with a pointer at the brackets. Hostname AAAA resolution already worked
  (getent ahosts); this closes the literal-endpoint gap.
- **`byre mcp add|remove|list`.** CLI sugar over the `[[mcp]]` vocabulary:
  `add` is add-or-update into the project's host-side config (`--global`
  for default.config), `remove` is closure-smart (deletes the layer's own
  block and/or writes `!name` when a lower layer or skill still declares
  it -- saying which), `list` renders the effective attributed set through
  status's own renderer. The interactive `byre config` editor gains a full
  MCP screen: this layer's declarations editable, inherited ones
  override-by-name, and skill-declared servers closable per entry.

## v0.4.0 -- 2026-07-14

- **Rootless Podman is supported (ADR 0032).** `byre develop` now
  mode-selects per engine: under rootless Podman (4.3+) it builds a
  generic-uid image and runs the box -- and every volume-filling helper --
  with `--userns=keep-id:uid=1000,gid=1000`, so files land owned by you
  exactly like the rootful bake. The firewall sidecar joins the box's own
  user namespace; deliver knows rootless engines only show your own boxes.
  Rootless Podman older than 4.3 keeps the previous refusal
  (`BYRE_ALLOW_ROOTLESS_PODMAN=1` override unchanged), and rootful
  Docker/Podman behavior is untouched. The whole gated integration suite
  (firewall included) runs and passes on rootless Podman;
  `BYRE_TEST_ENGINE` pins the suite to one engine on hosts with both.
- **Boxes now inherit the host's TERM and timezone.** `TERM` and `TZ`
  join byre's shipped `env_from_host` layer alongside git identity (`TZ`
  from the host's TZ var, else the `/etc/localtime` symlink's IANA
  name), so the box renders and timestamps like the terminal it was
  launched from. Same rails as before: visible in `byre status`, counted
  in exposure, disable-able per layer (`TERM = ""`).
- **`env_from_host` accepts `env:` and `tz:` sources (ADR 0031).**
  `KEY = "env:HOST_VAR"` passes a named host env var into the box (the
  reservation from ADR 0026, now deliberately designed); an absent host
  var sets nothing. Sources stay a closed scheme set -- a literal value
  belongs in `[env]`, and the validation error says so.
- **Skills can document the env vars they consume.**
  `[runtime.env_docs]` (`NAME = "one-line guidance"`) declares vars a
  skill reads but does not set -- an API key, a feature toggle. Pure
  documentation: nothing validates or warns, but the config editor's env
  screen shows each unprovided var as a dim suggestion row attributed to
  the skill, and enter prefills the add editor. `skill inspect` lists
  them as `env consumed`.

## v0.3.0 -- 2026-07-14

- **Egress closures and the `firewall-open` skill (ADR 0030).** `!host[:port]`
  entries in the `egress` key are now closures that survive the config
  cascade and subtract from the *derived* allowlist -- after skill egress
  unions in, so a config can finally say "claude minus statsig". Portless
  `!host` closes every port; `!host:port` just that one. The new
  `firewall-open` builtin enforces closures on an otherwise-open network
  (posture `open-denylist`: best-effort per-IP drops aimed at well-behaved
  telemetry clients -- the deny-by-default firewall remains the containment
  posture). Closures are never invisible: `byre status` prints them under
  every posture, and a subtracted allowlist entry shows as closed-by, not
  vanished. An unresolvable closure fails the launch (under an open network
  it would stay silently reachable beneath an "N hosts blocked" claim).
- **The firewall now fails closed on a broken IPv6 stack.** A netns with
  real (non-loopback) IPv6 interfaces whose `ip6tables` is unavailable used
  to skip the v6 rules -- leaving that entire side policy-ACCEPT under a
  deny-by-default claim. The launch gate now stays shut instead; the skip
  remains only for a truly v6-less netns, where there is nothing to leak.
- **The gated integration suite is agent-runnable and gates CI.** New
  coverage: the real launch path (ownership round-trip, fresh-volume uid),
  machine-volume sharing across projects (ADR 0017), concurrent worktree
  sessions (ADR 0009), fail-closed across `docker restart`, and the
  firewall-open pair. The suite runs in CI (and so in the release gate) on
  every push.
- **The firewall's IP-snapshot limitation is documented honestly**
  (SECURITY.md, README pointer, the skill's own docs): hostname grants pin
  the IPs resolved at launch, and on per-query-rotating resolvers a granted
  host can start failing -- closed, never open -- seconds after launch.
- **`skill inspect` shows a digest for bundled packages too** (computed
  from the embedded payloads -- the ADR 0029 deferral), so bundled and
  installed inspect output rank equally.
- **Generated Dockerfiles emit `HEALTHCHECK NONE` once, at the tail.** The
  tail placement means a raw block (skill Dockerfile lines,
  `dockerfile_post`) reintroducing a healthcheck still loses -- a probe
  would do network I/O before the launch gate lands -- and single emission
  stops buildkit's MultipleInstructionsDisallowed warning on every build.
- **Skill manifests are validated for mount/volume shape at load**, so
  `byre skill validate` green means the skill's grants can actually run,
  instead of a bad host path surfacing at the next develop. Config-side,
  host paths on mounts and seeds are checked at save (absolute or `~/`,
  no comma) for the same reason.

- **`~/.byre/AGENTS.md`: a byre-owned guide for host-side coding agents.**
  Every store-mutating command now lands (and keeps current) an agent
  guide at the store root: the directory map with per-entry write rules,
  the consent-document rule for `projects/<id>/byre.config`, the byre
  verbs to prefer over `mv`/`cp`, layering over provided packages instead
  of editing in place, and the version-control conventions (git at the
  `skills/<owner>/` level, never the whole store). byre owns the file --
  edits are overwritten whenever it differs from the running binary's
  copy. A pre-existing AGENTS.md that byre never wrote is preserved as
  `AGENTS.md.bak` before the takeover -- preservation is a precondition,
  never best-effort -- and a symlink at the path is replaced as a link,
  never written through.
- **`skill|template pack` output is now a fixed point of pack.** Re-packing
  a previously packed manifest (the documented publishing flow writes pack
  output over the primary in place) used to accrete a duplicate generated
  marker comment per round, and the source file's trailing-blank shape
  leaked into the emitted bytes -- identical payloads could pack to
  different digests. Pack now strips its own stale markers (only when
  attached to a `[[package.files]]` block; lookalike lines in strings or
  author comments survive) and normalizes trailing whitespace, so
  `pack(pack(x)) == pack(x)` byte-for-byte, digest included.

## v0.2.0 -- 2026-07-13

**Breaking:** the `codereview` and `devlog` skills moved out of the binary
(their bare names are retired; the error tells you the one-time install),
and the develop-time adoption prompt is gone in favor of `byre preset
apply`. Both migrations are automatic or one command -- details below.

- **Presets replace the adoption offer.** A repo-shipped config is now like
  `package.json`: cloning gives you a file, not a prompt. The conventional
  name is **`byre.preset`** (`byre.config` is reserved for the box's live
  consent document); a legacy repo `byre.config` still works with a rename
  note. `byre preset apply [<uri>|<path>]` is the one solicited flow: it
  validates the preset, walks you through installing any missing packages it
  references (each install gets its own grant summary and confirm --
  declining any still completes the apply honestly), shows the composed
  box's full grant review with a diff against your current config, and
  writes `byre.config` on confirm. `byre preset inspect` is the same review
  without the write. Drift is passive and legible: develop and status note
  "not applied" and "differs from the version you applied" (steady state is
  silent); the states derive from an `applied` marker recorded at apply
  time. **Migration:** the develop-time "adopt this byre.config?" prompt is
  gone; existing `adopted` records migrate to `applied` markers
  automatically (your history lands in the right drift state), and sticky
  `declined` records are deleted -- with no unsolicited prompt there is
  nothing to decline.
- **Skills are packages; `codereview` and `devlog` moved out of the binary.**
  Byre now has a real package model: bundled packages live inside the byre
  binary (immutable, `byre/*` ids, display mirror at `~/.byre/bundled/`),
  local packages are editable directories under `~/.byre/skills|templates/`,
  and installed packages are content-addressed snapshots acquired with
  `byre skill|template install <manifest-url> [--digest sha256:...]` --
  fetched, hash-verified per file, and inert until a box's config enables
  them. New verbs on both nouns: `install`, `uninstall`, `pack`, plus
  `inspect <url>` to review a package without installing. `[sources]` in a
  config records where its packages come from; missing packages print the
  exact install command.
  **Migration:** the `codereview` and `devlog` skills left the binary and
  live at github.com/pjlsergeant/pjlsergeant-byre-skills. Their bare names
  are permanently retired -- a config naming them gets the pinned install
  command in the error; install once per machine, then reference
  `pjlsergeant/codereview` / `pjlsergeant/devlog` in `skills`. `byre skill
  update` is a no-op stub (bundled packages update with byre itself);
  materialized copies under `~/.byre/skills/` from older releases are never
  loaded -- byre offers `byre skill archive-legacy` to move them aside.
- **Store honesty under failure.** A broken installed snapshot is repairable
  in place (the printed digest-pinned reinstall command re-lands the exact
  verified bytes, with consent) and stays removable; an installed id can
  never change kind; uninstalling one side of a contested id says exactly
  who -- if anyone -- provides the id afterwards. The passive preset drift
  check reads under the same size bound as `preset apply`.
- **Picker polish.** Description-only compatibility stubs (`devloop`,
  `grok-shared-auth`) are no longer offered in pickers -- there is nothing
  to enable; a config already naming one still shows it. Dev builds label
  bundled packages `bundled (devel)` instead of a pseudo-version string.
- **`docker-host` skill**: optional grant of the host's Docker daemon via
  its socket. Installs `docker-ce-cli` + compose + buildx from Docker's
  apt repo; mounts `/var/run/docker.sock`; runner probes the socket gid
  engine-side and injects numeric `--group-add` (no hardcoded gid; works
  on Docker Desktop and native Linux). New skill keys: `sock_groups`
  (attributed grant) and `containment` (warranty disclaimer on status,
  launch, adoption, and config UI -- multi-declarer renders all). Missing
  socket is an attributed warning, not a refusal (engine stays authority;
  Desktop host-stat false-negatives suppressed). Core plumbing:
  `BYRE_PROJECT` + `BYRE_WORKTREE` in every box; compose project name
  defaults to `byre-$BYRE_WORKTREE` so sibling worktrees do not collide.
  User-facing discussion: `docs/DOCKER-HOST.md`; design record: ADR 0027.
- **`env.d` hooks are pure env-setters, and `byre shell` now shares the
  agent's environment.** `env.d` hooks (which set launch-time environment)
  are contractually export-only -- any command, prompt, or file mutation
  belongs in `firstrun.d`. A baked `/etc/profile.d` shim sources `env.d`
  into login shells, so a `byre shell` session gets the same env.d-provided
  environment the agent does (e.g. `COMPOSE_PROJECT_NAME`, and the
  `claude-shared-auth` token). `claude-shared-auth`'s interactive
  stale-login remediation moved from its env hook to its firstrun hook
  accordingly. Design record: ADR 0028.

## v0.1.9 -- 2026-07-12

- **Config UI: ctrl+s and ctrl+q work from every screen.** ctrl+s saves the
  file from anywhere -- on the item and text editors it accepts the open edit
  first (an invalid item keeps its editor open and saves nothing), elsewhere
  it saves in place; save feedback shows on the screen where you pressed it.
  ctrl+q goes up one level from anywhere (screen -> form -> quit), with the
  usual unsaved-changes confirm at the top. In the raw-text overlay ctrl+s
  now writes the file too (it used to only stage the buffer).
- **`byre-codereview --continue` now actually resumes codex sessions**: the
  resume path passed `--sandbox`, which `codex exec resume` rejects, so every
  codex `--continue` silently fell back to a fresh review. The sandbox rides
  a `-c sandbox_mode` override instead (verified enforcing on resume).
- **`byre-codereview --reviewer claude`**: Claude joins codex and grok as a
  reviewer, driven headless (`claude -p`) with edit tools and subagents
  stripped and sessions resumable via `--continue`. Caveat, stated in the
  shipped context too: claude reviewing the claude agent's own work is a
  second pass, not a second opinion -- prefer a different-model reviewer
  when one is installed.
- **`byre-codereview --raw "prompt"`**: sends your prompt verbatim instead
  of the built-in review prompt. Enforcement flags, session resume, the
  tripwire, and the review log (tagged "raw") all still apply; the review
  policy is whatever your prompt says.
- **`byre-codereview` is its own skill** (behavior change for devloop users):
  the review script and its loop conventions moved out of the old devloop
  skill into a new `codereview` builtin. A box that wants the reviewer now
  enables `skills = ["codex", "codereview", ...]`; the dev-workflow half
  (conventions, diary, scratch volume) stays behind in what is now the
  `devlog` skill (renamed in this same release -- next bullet). The two
  skills share the devlog dir without depending on each other.
- **The devloop skill is now `devlog`**: the dev-workflow skill (diary,
  devlog dir bootstrap, scratch volume) is named for the devlog dir it
  curates -- devloop-the-skill next to devlog-the-dir was a two-letter
  confusion generator. Swap `devloop` for `devlog` in your `skills` lists;
  a no-op stub keeps configs naming `devloop` resolving (they launch, but
  contribute no diary/conventions/scratch until renamed -- the stub's
  description says so). Scratch volumes are keyed by volume name, so a
  renamed box picks its data straight back up.
- **Upgrading through both changes above takes two steps.** (1) Run
  `byre skill update`: a store materialized before this release still
  holds the old full devloop -- its `byre-codereview` would silently win
  over the new skill's at rebuild and keep recreating `.devloop/`, and
  the rename stub is not automatic either (materialization never clobbers
  an existing store copy); the update swaps in the stub and installs
  `devlog`. (2) Edit each config's `skills` list: `devloop` -> `devlog`,
  and add `codereview` wherever you want the reviewer -- the update can't
  do that for you, and until it's done the stub means those boxes launch
  without diary/conventions/scratch.
- **`.devloop/` is now `.byre-devlog/`** (breaking, by design): the
  self-ignoring working-tree dir for the agent diary and review log is named
  for byre, not for one skill (glossary: "devlog dir"). There is no automatic
  migration -- an existing `.devloop/` is never read, moved, or deleted;
  rename it by hand (`mv .devloop .byre-devlog`) to keep its history, and
  note a box built from a pre-rename image recreates `.devloop/` until its
  next rebuild.

## v0.1.8 -- 2026-07-12

- **Consent surfaces stop under-stating scope** (doctrine audit,
  2026-07-12). The adoption review's ⚠ summary now covers every Grant
  class: machine-scoped volumes -- the shared-credential shape, the one
  grant that crosses project scope -- get a bold-yellow line whether
  declared by the config or a skill (skill volumes never surfaced at
  all before), ports get a line, and egress entries always appear with
  their honest live/inert status under the resolved posture. The global
  config editor's offered-egress action stops saying "Open in this
  project" while writing default.config: in --global mode it reads
  "⚠ Open for every project on this machine" and the confirmation
  names the file and both undo routes.
- **env_from_host: the host git identity becomes visible, overridable
  config** (ADR 0026). The GIT_AUTHOR/COMMITTER_* passthrough was
  invisible plumbing; it is now byre's shipped default layer of a real
  config key (`env_from_host = { KEY = "git:<config-key>" }`, `""`
  disables a key, explicit `[env]` beats it; `env:` sources are
  reserved and rejected). Counted in the launch exposure tally, one
  attributed `Host env:` row in `byre status`, read-only rows in the
  config editor's Env screen, and flagged at adoption when a proposal
  asks for anything beyond the shipped defaults.
- **Onboarding: explicit beats implicit** (doctrine audit). A non-TTY
  partially-flagged onboarding errors (naming both flags and `none`)
  instead of silently writing your machine favourite into a new
  project's config -- a favourite is what Enter means, and a pipe has
  no Enter. The new tri-state `--shared-auth` flag answers the offer
  for automation (yes opts the box in via its own byre.config, loudly
  refusing agents with no ready companion). And `"none"` is now a
  stored answer that WINS: a template's `agent` can no longer silently
  override a project's explicit no-agent choice.
- **The config UI's Volumes screen sweeps every installed engine** --
  one row per engine copy, clears exactly the row's engine, and says
  the clear is engine-local when both docker and podman are installed
  (the advertised machine-volume delete route could previously leave a
  live login on the engine your config didn't name).
- **Legibility batch**: the --global editor files template/agent under
  ONBOARDING FAVOURITES (they prefill the picker; they configure no
  box) and drops the false "(primary agent)" locked row -- enabling an
  agent's skill machine-wide via the global Skills screen now actually
  works; shared-auth firstrun hooks say aloud when they promote a
  per-box login to the machine credential or replace a fork; devloop
  warns and stands down instead of silently destroying a non-directory
  `.devloop`; assorted doc drift (firewall base list is offered, not
  unioned; real `byre status` output in ARCHITECTURE; grok restored to
  the agent lists).

- **The shared-auth offer is per box; the saved answer is a favourite,
  never a grant** (ADR 0025, rescoping v0.1.7's ADR 0024). Every box's
  onboarding asks "Opt this box into <agent> shared credentials?": yes
  puts the companion skill in **this project's** `byre.config` `skills`
  (the same representation as a hand-enabled skill, written in the same
  atomic byre.config creation) -- the only grant the answer ever makes;
  no writes nothing. Saying yes to "Save these as your default for new
  projects?" saves the answer alongside the template/agent favourites
  (the picker-owned `shared_auth` list): the next box's offer then
  prefills [Y/n], so opting in costs one Enter -- but every box still
  gets its own question and its own byre.config entry. The picker never
  writes `default.config`'s `skills`; that stays a deliberate hand-made
  (or `byre config --global`) machine-wide grant, and is the one thing
  that suppresses the offer (the cascade already covers the box). This
  replaces v0.1.7's behavior, where one project's answer silently set a
  machine-wide default: a "y" enabled the companion for every future
  box and an "n" was a permanent never-ask (`shared_auth_declined` --
  now vestigial: still parsed, read by nothing; old decliners are
  simply asked again per box, default No; an old machine-wide "y" keeps
  working as the hand-grant it is). Unrecognized input at a prefilled
  offer never opts in, and answering `i` ([y/N, i for info]) prints exactly what
  each answer writes -- scopes, the companion skill's name, the save
  question's prefill-only effect -- then re-asks.

## v0.1.7 -- 2026-07-12

- **`byre config`: ctrl+q quits the form** (pairing with ctrl+s save), and
  the dirty-quit confirmation now arms and confirms on any quit key --
  esc, ctrl+c, or ctrl+q; the banner names all three.
- **Onboarding offers shared auth** (ADR 0024): when the first-run
  picker's chosen agent has a ready shared-auth companion skill (one
  declaring the new `shared_auth_for` key -- claude and codex today),
  it asks "Share one <agent> login across all byre projects on this
  machine (<agent>-shared-auth)? [y/N]" once. Yes enables the
  companion machine-wide (`skills` in `~/.byre/default.config`, written
  surgically -- comments preserved, every edit re-parsed and verified
  before writing); no is remembered in the picker-owned
  `shared_auth_declined` list, so the offer never nags. The offer
  follows the agent question directly (before "save as default"), and
  all answers are collected before anything is written -- Ctrl-D
  anywhere in the picker aborts with no side effects. Fully-flagged
  onboardings (`--template` + `--agent`) keep their zero-prompt
  contract and are never asked. Gemini (OAuth gate-pending) and grok
  (retired, ADR 0023) deliberately don't declare the key and are never
  offered; existing installs pick the offer up with `byre skill update`.
- **grok-shared-auth RETIRED** (ADR 0023). The symlinked-credential design
  failed its field gates: grok rotates a single-use refresh token every ~6h
  and writes via temp+rename, so the shared copy forks, dies, and the
  skill's every-launch heal then clobbered working per-box logins with the
  dead credential. The skill is now a resolvable no-op stub (configs naming
  it still launch; the picker shows the retirement); the grok skill's login
  hook removes any symlinked auth.json, healing damaged boxes at next
  launch. Grok logs in per project -- that path is unaffected and correct.
  Rebuild designs (auth broker on `GROK_AUTH_PROVIDER_COMMAND`; watcher +
  refresh jitter) are parked with their gates in
  `wip/grok-shared-auth-v2-designs.md`; mechanics and field evidence in
  `docs/AGENT-CREDENTIAL-MECHANICS.md` §6. Ride-along corrections: the
  "~7 days" grok token lifetime in hooks/messages was wrong (~6h access
  tokens, silent refresh), and `XAI_API_KEY` is a fallback the stored login
  SHADOWS, not an override (vendor auth guide).
- **Lifecycle correctness batch** (2026-07-11 external-review triage):
  - `develop` now creates the session container **under the setup lock**
    and starts it after release, closing the window where a concurrent
    `byre reset`/`forget` saw no live session and wiped freshly seeded
    volumes (or the store) from under a launching session. Cleanup
    commands see containers in every state: a created-but-not-started
    container is a develop's ownership marker -- they remove it
    (forcelessly, so a session that started meanwhile aborts them) and
    that develop's start fails loudly instead of launching against
    wiped state. Sibling-worktree sessions stay concurrent.
  - `reset`/`forget`/`rehome` now inspect **every installed engine**
    (docker and podman), not the configured one: an engine switch or a
    broken config could previously make `forget` clean docker, find
    nothing, delete the store, and strand all podman state -- a false
    "completely removed". Listings are labeled by engine when both are
    installed; `forget` refuses to delete the store if either engine
    can't be queried or cleaned.
  - `rehome` now migrates the **stored config and adoption records**
    alongside volumes (conflict-checked, never clobbering the new id's
    own config) and retires the old id's image and store dir on
    success -- previously the old store haunted `byre rehome`'s
    candidate list forever and a store-only config was silently
    orphaned.
  - Config adoption writes atomically (`AtomicWrite`, like every other
    config writer) and is serialized under the setup lock; the proposal
    is re-read after review and adopted only if it still holds the
    reviewed bytes.
  - Skill resolution rejects a second `netns_init` (the launch gate is
    opened by the hook's own script, so a second hook could run after
    the agent was already released) -- same stance as the single
    `network_posture` rule.
  - `develop` **refuses rootless Podman** instead of warning (the
    baked-UID ownership model is known-broken there);
    `BYRE_ALLOW_ROOTLESS_PODMAN=1` overrides with the warning kept,
    mirroring `BYRE_ALLOW_ROOT`.
  - Port `interface` values must be canonical IPv4 literals; hostnames,
    IPv6, and colon-bearing strings now fail validation instead of
    failing (or changing meaning) inside docker's `-p` grammar.
  - Honest picker descriptions: gemini is labeled EXPERIMENTAL (its
    autonomy flag/auth flow are still unverified), and grok-shared-auth
    is labeled BROKEN pending its API-key rebuild (its symlink design
    failed the field gate).

## v0.1.6 -- 2026-07-11

- **`byre completion --install` removed** (added earlier in v0.1.5,
  walked back the same day): the recommended setup is now one line in
  your shell's startup file -- `eval "$(byre completion bash)"` and
  friends, shown in `byre completion --help`. It regenerates at shell
  startup (~3ms), never goes stale across upgrades, works without the
  bash-completion package (the script carries its own fallback), and
  byre writes no files into shell-managed directories. If you ran
  `--install` while it existed, the written script keeps working --
  delete the path it printed whenever you switch to the eval line.

## v0.1.5 -- 2026-07-11

- **New `byre deliver`** (ADR 0021): get files from the host into a running
  box in one move -- `byre deliver report.pdf` streams into the box's new
  `/inbox` and puts the in-box path on your clipboard, ready to paste into
  the agent prompt. Plain `byre deliver` delivers your *clipboard*: it
  samples what's on offer (files, image, text -- types only, never content),
  waits for a paste gesture (Ctrl-V reads images and copied files directly;
  dragging a file onto the window delivers that file), and confirms kind and
  size, never content. `-` streams stdin (`--name` names it). byre's first
  machine-scoped verb: it finds your running box from anywhere (unique
  `--box` prefix, cwd match from any subdirectory, sole session, or an
  interactive picker), across docker and podman, only boxes you own
  (`--skip-uid-check` widens). Names are preserved and collisions
  uniquified, never overwritten; directories deliver recursively as one
  path; every landed path prints to stdout (the machine contract) with the
  clipboard as best-effort garnish (pbcopy/wl-copy/xclip, OSC 52 over SSH,
  `--no-clip` to skip). The inbox dies with the box -- re-deliver, it's one
  command. Boxes built before this release need one rebuild to gain
  `/inbox`. User guide: docs/DELIVER.md.
- **New `byre deliver --install-app`**: generates the deliver app -- a
  "Byre Deliver" Dock/Finder drag target and a right-click "Deliver to
  Byre" Quick Action on macOS (assembled locally by `osacompile` from a
  readable AppleScript source shipped inside the bundle -- nothing
  prebuilt, nothing to notarize), a `.desktop` launcher on Linux. Drop
  files on it, or open it plain to deliver the clipboard; outcomes
  arrive as OS notifications (which no-TTY graphical launches of
  `byre deliver` now use generally). Re-run after moving the byre
  binary; `--box` bakes a fixed target; uninstall by deleting the
  printed paths. Regeneration never clobbers a same-named artifact
  byre didn't write. **macOS is the tested platform for the graphical
  layer; on Linux the `.desktop` launcher, graphical picker, and
  notifications are experimental and unverified across desktop
  environments -- the terminal `byre deliver` flows are the supported
  Linux path.**
- **Tab completion + restructured help** (ADR 0022): the CLI now rides
  cobra. `byre completion bash|zsh|fish|powershell` prints a completion
  script covering every command and flag, and `--install` (all but
  powershell) writes it where your shell will find it and prints the
  path -- byre never edits shell rc files; when zsh needs one fpath
  line, it prints the line instead. Help gained `Flags:` sections and
  `byre help <command>`; `--flag=value` now works everywhere. Exit
  codes and command behavior are unchanged; error and usage wording
  shifted to cobra's shape.
- **`byre rehome` validates the old id**: a malformed id (anything byre
  couldn't have generated) is refused up front instead of being used as a
  store path component.
- **Security docs sharpened** (external-review follow-up): SECURITY.md now
  states plainly that a skill is trusted code (enabling one hands it the
  box) and that config `env` values are baked into image layers -- with a
  matching don't-put-secrets-in-env warning in the README.
- **claude-shared-auth now offers the shadowing-login fix**: when a leftover
  per-project `/login` credential sits alongside the shared token (the
  combination that 401s the box ~8h later), an interactive launch now asks
  to move `.credentials.json` aside (default yes) instead of only warning.
  Non-interactive launches keep the warn-and-leave behavior.

## v0.1.4 -- 2026-07-08

- **Deny-by-default now means it** (ADR 0020, behavior change): a firewalled
  box opens your agent's own API endpoints and *nothing else*. Git hosting,
  apt mirrors, and language registries -- previously auto-open -- are now
  **offered, not open**: new `egress_offered` key (templates and skills
  declare doors; always inert), shown in the config UI's Egress screen as
  closed switches, one press writing the entry into your own `egress`.
  Expect `git`/`apt`/`go get`/`npm install` to hang on a fresh firewalled
  box until you open their doors -- that's the firewall working.
  (`storage.googleapis.com` is gone entirely: Go's proxy serves content
  directly, and a blanket GCS allowance was an exfiltration-grade hole.)
- **New `egress` config key** (ADR 0019): extra firewall-allowlist entries
  as first-class config -- `egress = ["internal.example.com", "api.stripe.com:8443"]`
  -- unioned across cascade layers like every other list (`!entry` removes),
  shown in the config UI's GRANTS section and in `byre status`, attributed.
  Inert without a posture skill, and the UI says so. **`FIREWALL_ALLOW` is
  retired**: the firewall no longer reads it -- move any value into `egress`.
- **New `byre ejectfirewall`**: prints the firewall sidecar byre runs at
  launch as a standalone script, so leaving byre no longer means leaving
  the walls. With the firewall enabled, `byre dockerfile` and
  `byre dockerrun` now explain the launch gate an ejected image would
  otherwise die at, and the gate's failure message points the same way.
- New `docs/EJECTING.md` + a "Stop using byre?" How-do-I: leaving byre is
  `byre dockerfile` + `byre dockerrun` (+ `byre ejectfirewall` if
  firewalled).
- Config UI fixes: frames now clip to the terminal width (an over-width
  row used to corrupt the repaint and strand stale rows from the previous
  screen), and the item editor's title no longer mangles "Egress".

## v0.1.3 -- 2026-07-08

- **The `byre config` list screens now tell the truth about the whole
  cascade** (ADR 0018): Packages, Env vars, Extra mounts, and Ports show
  the effective merged state -- inherited entries tagged `(default)` /
  `(template:x)`, skill-contributed mounts and env shown read-only as
  `(skill:x)` -- while every edit still writes only the file being
  edited. Enter on a row opens a small action menu offering exactly
  what that row supports (Edit, Delete, Override here, Remove in this
  project, Restore) with a where-it's-set line; `e`/`d` accelerate the
  same actions. Form summaries count effective state too
  (`3 packages (2 inherited)`).
- **Every cascade list now has an off-switch**: `apt`/`npm_global`
  accept the same `!name` removal marker as skills, mounts, and
  volumes; ports gain `remove = true` (keyed by container port alone --
  a port has no name for `!` to ride). Env stays override-only:
  shadow an inherited key's value, including with empty; unset is
  deliberately deferred.

## v0.1.2 -- 2026-07-07

Shared agent logins, and a rebuilt README.

- **Log in once per machine, not once per project** (opt-in): enable
  `claude-shared-auth`, `codex-shared-auth`, or `gemini-shared-auth`
  alongside your agent and one login covers every byre project on the
  machine. byre still reads and copies nothing from your host --
  the credential lives in a shared Docker volume that `byre reset` /
  `byre forget` deliberately never touch (they tell you so, and how to
  delete it on purpose). Gemini note: the API-key path is verified;
  OAuth sharing is still gated (see the skill's description).
- If a box adopting the shared Claude login already had its own
  `/login`, the leftover credential quietly shadows the shared token
  and the box starts failing with 401s about 8h later (Claude prefers
  the stored login and stops refreshing it -- while claiming env-token
  auth). byre now warns at launch when it sees the combination and
  names the one-command fix.
- New `[[volumes]]` grammar: `scope = "machine"` -- one volume per user
  per machine, shared by every project that declares it.
- Skills can carry a one-line `description`, shown in the `byre config`
  skills screen so similar names are tellable apart.
- The `byre config` skills screen now shows INHERITED skills (enabled by
  `default.config` or the template) as on, marked "(inherited)" -- they
  used to render unchecked, which read as off. Toggling one writes the
  cascade's `!name` off-switch into the project layer, and the form's
  skill count now reports the same effective state the checkboxes show.
- Gemini fixes: logins now survive rebuilds (gemini encrypts its
  credential against the hostname; byre boxes now have a stable one),
  and the untrusted-folder and 256-color warnings are gone.
- `byre-codereview` now works in non-git folders.
- New `docs/SECURITY.md`: the security model, stated plainly.
- README rewritten post-release: honest contract renamed to "What's
  boxed, what isn't", toolkit section rebuilt, Homebrew line added
  (the tap itself may lag the release).

## v0.1.1 -- 2026-07-06

First version to actually ship: v0.1.0's release run was (correctly)
refused by the CI gate.

- Fix two seed tests that assumed a real `~/.claude` on the test host.

## v0.1.0 -- 2026-07-06

First public release.
