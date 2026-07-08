# Changes

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
