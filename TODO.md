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

- [ ] (S) **Uninstall refscan misses layer files** (codex review,
  2026-07-17): `scanReferences` (internal/commands/refscan.go) reads
  default.config + project configs but not `~/.byre/layers/*/layer.config`,
  so a layer-only reference dodges uninstall's warning. Fails loudly at
  the next develop, so no silent breakage. Fix: scan the layers glob too.

- [ ] (S) **Volumes page polish** (Pete, 2026-07-17): make the config
  editor's Volumes screen nicer. The 07-17 pass fixed the mechanics
  (content-sized columns, engine-degrade notes, honest empty states);
  what's left is presentation — grouping/spacing (project vs shared),
  the annotation clutter on orphan rows. View-layer only.

- [ ] (L) **Site.** Landing page + real docs; the decided shape -- surfaces,
  placement principles P1-P11, the demo pipeline -- lives in
  docs/marketing/positioning.md "Site plan" (absorbed from wip 2026-07-17;
  README trim + canonical site docs shipped same day).
  v1 skeleton shipped 2026-07-15 (`site/`, hand-rolled Hugo, getbyre.com
  via Pages, docs seeded from the README); logo/favicon and the
  ask-your-agent conceit landed on both surfaces same day. Remaining:
  - DNS + Pages settings (Pete, host-side) -- believed done 2026-07-15,
    the deployed header was verified in-browser; strike on confirm.
  - Landing comparison table: the "Why not…?" material is still
    README-only; the site plan puts the table on the landing page.
  - Screencast hero on the landing (the day-03-style clip -- the media
    the README shouldn't carry). The demo harness shipped 2026-07-17
    (BYRE_DEMO_REC=1 tests record 4 casts at publish; player + shortcode
    live); what remains here is the VM-recorded tier: hero clip, firewall,
    worktrees -- recorded with the same verbs, committed as .casts.

## Standing

Disciplines and tripwires, not tasks.

- **Status/marketing lockstep (P9 -- sweep this list, not memory):** the
  surfaces carrying real byre output, re-verified when that output changes:
  - `byre status` block (status.go): README "Quickstart",
    site/content/docs/quickstart.md -- identical blocks.
  - develop launch banner: README hero console block, site landing
    (site/content/_index.md) hero block.
  - install commands: README hero + "Install" (brew, blessed), site
    landing hero (brew), site/content/docs/install.md (all routes).
  - How-do-I tldrs: README index and site cookbook are verbatim-identical
    per entry (P6); grep "tldr:" in both when a recipe changes.
  - commands table: generated -- TestCommandsPagePinsSiteFile enforces,
    regenerate with `go run ./cmd/byre commands-page`.
  - demo casts: generated at publish (BYRE_DEMO_REC=1, self-verifying --
    a layout change fails the recording test). Scenario inventory:
    config-tui-walk, quickstart-picker-status, deliver-paste-flow,
    completion-tab-walk (internal/tuitest/demos_test.go); VM-recorded
    (committed .casts, refresh around releases): none yet -- hero,
    firewall, worktrees pending.
- **Post-launch H1 tripwire:** the H1 is a safety idiom, not a scope
  statement; the plain what-it-is sentence under it is mandatory mitigation.
  If cold readers bounce post-launch, revisit
  (docs/marketing/positioning.md "Copy bank").
- **`internal/commands` is never carved (2026-07-16, supersedes the
  carve-as-you-touch tripwire):** commands is the thin adapter layer —
  domain logic lives in domain packages, commands files hold Streams-glue
  only. The reviewable invariant: when a commands file accumulates real
  logic, the LOGIC moves to a domain package; the package itself is never
  split. Full rationale in the package comment (commands.go).

## Maybe someday

Stuff Pete has nixed from the todo list. Not quite WONTFIX, but not something I
plan to get to any time soon:

- [ ] (S) **shared-auth field gates: gemini + grok** (parked long-term
  2026-07-17; opencode's two gates closed same day via the agent-contract
  tier). Two live checks, each needing Pete + a real login host-side; each
  flips `companion_for` -> `shared_auth_for` in the skill's skill.toml on
  pass (plus the `TestBuiltinSharedAuthDeclarations` table and the skill's
  composition pin test). A vouch follows its field gate, never source
  alone (the grok-v1 lesson). Self-contained runbook:
  - **gemini two-box OAuth:** two boxes with gemini + gemini-shared-auth.
    Box A: real "Login with Google" paste-code flow — the seeded
    `selectedType` means NO auth-method picker appears (if it does,
    that's a finding); after login, `~/.gemini/oauth_creds.json` must
    still be a SYMLINK into `~/.byre-identity/gemini/` (a regular file =
    the login-fork came back). Box B, launched after: `gemini -p 'say ok'`
    with no login prompt. GOTCHA: do not open gemini's `/auth` dialog
    after login — it rm's the symlink and re-forks. Rotation is already
    proven safe (Google installed-app refresh tokens don't rotate;
    AGENT-CREDENTIAL-MECHANICS, Gemini §3), and the seed plumbing was
    field-proven credential-less in QA pass #2 — only the live cross-box
    login remains.
  - **grok ~6h broker rollover (ADR 0036):** watch a real box refresh
    through the broker across the access-token lifetime — or force it
    (the broker honors `GROK_AUTH_EXPIRED=1`; see
    `grok-shared-auth/grok-auth-broker.sh`) — and confirm the backend
    accepts the refreshed pair end to end.

- [ ] (M) **Private-https package fetch.** `skill install` has no auth story
  for private hosts (deferred from ADR 0029); design tokens/netrc/redirect
  interaction with the origin-pinning rules before building.

- [ ] (M) **Drag-and-drop into the boxed terminal.** Mostly superseded by
  `byre deliver`; what remains is drop-directly-onto-the-running-terminal
  ergonomics. Needs a design pass: path translation, outside paths as a
  grant question, per-terminal drop behavior.

- [ ] (L) **Service sidecars** (Pete, 2026-07-12): config declares containers
  byre runs beside the box (postgres, redis, ...) and networks in -- the
  agent gets endpoints, never the daemon. Covers the compose-deps case
  without the docker-host grant.

- [ ] (L) **Filtering DNS resolver sidecar** (Pete, 2026-07-14): a resolver
  byre runs in the box's DNS path, so denials are seen as NAMES, not IPs --
  it closes the documented DNS-tunneling hole (firewall.sh v1 note) and is
  where denial VISIBILITY (a `byre denials` view, counts in status) lands.
  An interim counter-reading tier (iptables -vnxL via a post-hoc root helper)
  was considered and REJECTED 2026-07-14: most of the machinery (recorded-ID
  targeting, a privileged read on byre's passive commands) for packet counts
  with no names/timestamps -- and interim scaffolding toward the companion
  service byre deliberately doesn't run yet. Don't re-propose the counter
  tier; build this instead. Not urgent; fine if it waits months.

## Parked / consciously not doing

Decided negatives, recorded so they don't get re-raised. Rationale lives in
the docs cited and in git history.

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
  the security model, site/content/docs/security-model.md). If revived it's
  build-cache QoL and needs a build_env story.
- **Agent `command` argv validation** -- documented as a deliberate shell
  fragment instead (security model, "A skill is trusted code"); typed-field
  allowlists are legibility, not containment. Don't re-fix.
- **Structured (field-addressable) config validation errors** -- shared
  predicates + `ValidateLayer` is the completeness gate; prose errors
  attribute well enough. Revive trigger: a consumer needing attribution
  WITHOUT an open editor.
- **Path nannying** (refusing dangerous dirs) -- "a knife needs to be
  sharp"; Pete runs byre on `~/.byre` itself.
- **claude-pod feature steals** -- reviewed, nothing adopted, no public
  mention.
