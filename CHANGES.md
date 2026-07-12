# Changes

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
  `docs/grok-shared-auth-v2-designs.md`; mechanics and field evidence in
  `docs/agent-credential-mechanics.md` §6. Ride-along corrections: the
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
  `/inbox`. User guide: docs/deliver.md.
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
