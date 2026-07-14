# TODO

**This file is authoritative.** It is the single source of truth for what is
open and what was consciously dropped -- and it is *edited directly* to set
direction: whatever this file says the TODO is, that IS the TODO. Agents:
re-read it at the start of a session, and keep it live. Finished and dropped
items are *removed* and take their rationale with them -- git history is the
archive. Don't restructure it unprompted. Rationale lives in the ADRs and
docs linked per item; when one of them disagrees with this file about status
or scope, this file wins.

Every item carries a t-shirt size -- (XS), (S), (M), (L), (XL) -- estimating
effort to ship, and the list is ordered smallest-first. **Order and size say nothing
about importance**; the file deliberately carries no priority signal. Keep
items to 2-3 lines: what it is, who/when if it matters, one pointer to where
the rationale lives.

## Open

- [ ] (S) **Config UI: env secret-masking.** env values render in plaintext
  in the form; mask them (reveal on demand).
- [ ] (S) **`!host` egress closures** (Pete, 2026-07-13): `!host` in the config
  `egress` key closes that endpoint under deny-by-default -- subtracting from
  the derived allowlist INCLUDING skill-declared entries (today skill egress
  unions in after the cascade, out of `!name`'s reach); status shows the entry
  as removed-by-config, not vanished. Use case: claude minus statsig.
- [ ] (S) **gemini OAuth gate.** Two concurrent gemini boxes sharing one
  OAuth credential, run past the ~1h token expiry; neither dying = OAuth
  sharing is safe. API-key path already verified (ADR 0017).
- [ ] (S) **Bundled display digests in inspect** (D5b deferral, ADR 0029):
  inspect on a bundled package shows no digest today; compute and show the
  display digest so bundled/installed inspect output ranks equally.
- [ ] (M) **OpenCode agent skill** (Pete, 2026-07-10): `opencode` +
  `opencode-shared-auth` builtin pair per the grok playbook (0d9f59f..
  2cfd8fb). Establish the per-agent facts empirically first (install shape,
  state-dir env, headless login + rotation, autonomy flag, context file,
  egress, headless permission mode -- grok's silent-death lesson); record in
  docs/agent-credential-mechanics.md. Maybe a third reviewer.
- [ ] (M) **Skill env guidance strings** (Pete, 2026-07-08): skills declare
  env vars they CONSUME with a one-line guidance string (sketch:
  `[[runtime.env_docs]]`); config UI env screen shows a dim suggestion row
  per declared var. Pure documentation, no validation. Example:
  `GEMINI_API_KEY`.
- [ ] (M) **TERM + timezone + host-env passthrough.** Pass host TERM and TZ
  via the chassis, plus a config key for named host env vars. Per
  docs/GLOSSARY.md a passed-through var IS a grant: surface it in `byre
  status` and the config UI GRANTS section.
- [ ] (M) **claude-skills.d / claude-mcp.d convention**: byre/claude owns one
  sync hook; a skill drops Claude Skills / MCP definitions into convention
  dirs and they land in the box -- MCPs as byre skills with legible grants.
  Sketch discussed 2026-07-13 (skills milestone close-out).
- [ ] (M) **Private-https package fetch.** `skill install` has no auth story
  for private hosts (deferred from ADR 0029); design tokens/netrc/redirect
  interaction with the origin-pinning rules before building.
- [ ] (M) **Open-denylist firewall mode** (Pete, 2026-07-13): otherwise-open
  network with the config's `!host` closures enforced (default ACCEPT, DROP
  the named hosts) via the same netns_init vehicle; posture claim like
  "open (N hosts blocked)". Best-effort IP-snapshot blocking -- aimed at
  well-behaved clients (telemetry), worded so.
- [ ] (M) **Host-side test session.** The end-to-end cases that stay manual
  until agent-runnable tests exist: fresh-develop file ownership + launch
  path, builtins fresh-volume UID, concurrent worktree sessions, shared-auth
  coverage (ADR 0017), firewall fail-closed after `docker restart`.
- [ ] (M) **Drag-and-drop into the boxed terminal.** Mostly superseded by
  `byre deliver`; what remains is drop-directly-onto-the-running-terminal
  ergonomics. Needs a design pass: path translation, outside paths as a
  grant question, per-terminal drop behavior.
- [ ] (L) **`byre deliver`: ssh:// remote delivery.** The remaining tranche
  of ADR 0021 (v1 shipped 2026-07-10/11, user guide docs/deliver.md); the
  mini-protocol is frozen there (--proto / --porcelain / --consume). Gated
  deliver test cases ride the agent-runnable-tests item.
- [ ] (L) **Agent-runnable integration tests.** The gated
  `BYRE_DOCKER_TESTS=1` suite needs a Docker host the agent can reach.
  Design pass across nested rootless podman, a CI job, and a docker-capable
  host VM (not mutually exclusive). Unlocks most of the manual test debt.
- [ ] (L) **Site.** Landing page + real docs, devlog demoted to `/devlog/`;
  the decided shape lives in docs/marketing/positioning.md "Site plan".
- [ ] (L) **Rootless Podman keep-id path.** Design settled: generic-UID
  image on the rootless path, `--userns=keep-id`, mode-select on
  `runner.IsRootlessPodman` (background: ADR 0008). Today's
  detect-and-refuse stays until this lands; add integration coverage with
  it.
- [ ] (L) **Service sidecars** (Pete, 2026-07-12): config declares containers
  byre runs beside the box (postgres, redis, ...) and networks in -- the
  agent gets endpoints, never the daemon. Covers the compose-deps case
  without the docker-host grant.

## Standing

Disciplines and tripwires, not tasks.

- **Status/marketing lockstep:** README/site show `byre status` output as
  proof; re-verify against status.go after any status change.
- **Post-launch H1 tripwire:** the H1 is a safety idiom, not a scope
  statement; the plain what-it-is sentence under it is mandatory mitigation.
  If cold readers bounce post-launch, revisit
  (docs/marketing/positioning.md "Copy bank").
- **`internal/commands` split tripwire:** ~25 files, no internal boundaries
  (2026-07-09 external review). Don't split as a project; next substantial
  work there carves the touched area into its own package.

## Parked / consciously not doing

Decided negatives, recorded so they don't get re-raised. Rationale lives in
the docs cited and in git history.

- **grok-shared-auth rebuild** -- PARKED 2026-07-12 (ADR 0023); two gated
  designs in docs/grok-shared-auth-v2-designs.md, run the gates BEFORE
  building. `XAI_API_KEY` stays ruled out on cost.
- **Secret-manager seed backend** -- host-path + config-literal covers the
  single-user case. If revived: the seed-source model reserves a
  resolved-reference kind; don't hardcode new paths to "path".
- **Automatic volume migration** for the baked-UID upgrade -- no-op in
  practice; recovery is `byre reset` + re-login (documented).
- **run_args `--user`/`--userns` detect-and-warn** -- author-only footgun;
  one-sentence spec caveat instead of code.
- ~~**Machine-wide `shared` volume scope**~~ -- REVERSED by ADR 0017;
  machine-scoped volumes shipped with shared auth.
- **Hardening the project store against a --self-edit agent** -- reverted
  (0f35743); `--self-edit` means trusting the agent with the host, full
  stop. Reviewers WILL re-find this class -- don't re-fix.
- **Runtime-only env** -- no security value under the threat model (images
  never leave the machine; daemon access is root-equivalent; documented in
  SECURITY.md). If revived it's build-cache QoL and needs a build_env story.
- **Agent `command` argv validation** -- documented as a deliberate shell
  fragment instead (SECURITY.md "A skill is trusted code"); typed-field
  allowlists are legibility, not containment. Don't re-fix.
- **Structured (field-addressable) config validation errors** -- shared
  predicates + `ValidateLayer` is the completeness gate; prose errors
  attribute well enough. Revive trigger: a consumer needing attribution
  WITHOUT an open editor.
- **Path nannying** (refusing dangerous dirs) -- "a knife needs to be
  sharp"; Pete runs byre on `~/.byre` itself.
- **claude-pod feature steals** -- reviewed, nothing adopted, no public
  mention.
