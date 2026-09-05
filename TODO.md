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

- [ ] (S) **Retire the shared_auth legacy parser arms when their warnings go
  quiet** (ADR 0049 #1/#2, amended policy): the write side and the warning
  channel shipped 2026-08-23; the array and top-level parse arms
  warn-and-keep-working until Pete calls the drop per path.

- [ ] (XS) **`BYRE_SCRATCH` -> prefix-free rename** in
  `/home/dev/pjlsergeant-byre-skills` (2026-07-29, the out-of-tree remainder of
  the shipped state-model worklist): two sites (`skills/devlog/skill.toml:15`,
  `skills/devlog/context.md:12`), plus the published-package tail -- version
  bump, repack, new digest, `[sources]` pin in this repo's `byre.preset`. Same
  shape as the `INTTEST_*` rename. It is why status shows the devlog skill's
  scratch path as a cautiously-treated reserved key.

- [ ] (S) **Claude agent-contract canary** (security-gap review 2026-09-05):
  the flagship agent is installed by an unpinned live installer and byre
  leans on its CLI flags and state layout, yet the scheduled agent-contract
  matrix (.github/workflows/agents.yml) covers opencode/codex/gemini/grok
  and not Claude. Add the loginless `TestAgentContractClaude` beside the
  four, in the matrix.

- [ ] (S) **GitHub build attestation on releases** (Pete, 2026-09-05): pull
  ADR 0051's deferred trigger early -- the no-key-management option the ADR
  itself names. Checksum wording stays honest (transport integrity); this
  adds provenance. Signing/SBOM remain not done.

- [ ] (L) **Filtering DNS resolver sidecar** (Pete, 2026-07-14; promoted to
  Open 2026-09-05): a resolver byre runs in the box's DNS path, so denials
  are seen as NAMES, not IPs -- it closes the documented DNS-tunneling hole
  (firewall.sh v1 note) and is where denial VISIBILITY (a `byre denials`
  view, counts in status) lands. Design: `wip/filtering-resolver.md` (v11,
  unratified); NEXT STEP is its build-gating probes (Docker Desktop wiring,
  Podman log rotation), not the build. An interim counter-reading tier
  (iptables -vnxL via a post-hoc root helper) was considered and REJECTED
  2026-07-14 -- don't re-propose it; build this instead.

- [ ] (L) **Service sidecars** (Pete, 2026-07-12; promoted 2026-09-05): config
  declares containers byre runs beside the box (postgres, redis, ...) and
  networks in -- the agent gets endpoints, never the daemon. Covers the
  compose-deps case without the docker-host grant. Rides the companion-
  container rails the resolver establishes: read that design for whether it
  already generalises before writing a sidecar design.

- [ ] (M) **Site demos: make them look right, then wire them back in**
  (parked 2026-07-18, Pete: "quite far from how I want them to look";
  the rest of the site is done). The pipeline is BUILT and in-tree --
  BYRE_DEMO_REC=1 scenarios (internal/tuitest/demos_test.go), player +
  shortcode + fonts (site/static/vendor) -- but casts are not recorded,
  all slots render invisibly, and CI's recording steps are removed (git
  history of site.yml/ci.yml has the working wiring). The gap is
  presentation: scene pacing, framing, what each demo shows. Revive =
  polish the scenarios, restore the two workflow blocks, re-place the
  {{</* demo */>}} shortcodes. Media tier after: the VM-recorded casts
  (hero clip on the landing, firewall, worktrees). Two repositioning
  requirements (2026-07-29): the hero clip's storyboard must visibly
  show comfort (familiar tools present, agent already authenticated),
  not only develop → Claude; and on the landing, config-tui-walk
  REPLACES the TUI section's curated text block in place -- one moving
  embed per section, never stacked.

## Standing

Disciplines and tripwires, not tasks.

- **Status/marketing lockstep (PLACEMENT.md P9 -- sweep this list, not memory):** the
  surfaces carrying real byre output, re-verified when that output changes:
  - `byre status` block (status.go): README "Quickstart",
    site/content/docs/quickstart.md -- identical blocks.
  - develop launch banner: README hero console block, site landing
    (site/content/_index.md) hero block.
  - `byre config` console block (configui form): README "Change the box
    in seconds", site landing -- identical curated blocks, captured from
    the real editor (tmux capture-pane against a seeded store).
    Re-verify when the form's sections or footer keys change.
  - install commands: README hero + "Install" (brew, blessed), site
    landing hero (brew), site/content/docs/install.md (all routes).
  - How-do-I tldrs: README index and site cookbook are verbatim-identical
    per entry (PLACEMENT.md P6); grep "tldr:" in both when a recipe changes.
  - commands table: generated -- TestCommandsPagePinsSiteFile enforces,
    regenerate with `go run ./cmd/byre commands-page`.
  - demo casts: PARKED, none published (see the Site item). Scenario
    inventory when they revive: config-tui-walk, quickstart-picker-status,
    deliver-paste-flow, completion-tab-walk (internal/tuitest/
    demos_test.go); VM-recorded tier (hero, firewall, worktrees) after.
- **Post-launch why-not tripwire (2026-07-29):** the comparisons live
  on getbyre.com/why-not; README and landing carry only the one-line
  thesis + pointer strip. If "how is this different from X" becomes the
  recurring cold-reader question, the strip was not enough -- a summary
  table returns to the README.
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
  pass. Runbooks: docs/QA-PLAYBOOK.md, "shared-auth field gates" journey.

- [ ] (M) **Private-https package fetch.** `skill install` has no auth story
  for private hosts (deferred from ADR 0029); design tokens/netrc/redirect
  interaction with the origin-pinning rules before building.

- [ ] (M) **Drag-and-drop into the boxed terminal.** Mostly superseded by
  `byre deliver`; what remains is drop-directly-onto-the-running-terminal
  ergonomics. Needs a design pass: path translation, outside paths as a
  grant question, per-terminal drop behavior.


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
  stop. Reviewers WILL re-find this class -- don't re-fix. CARVE-OUT
  (2026-07-19): byre must still never let agent-writable state amplify
  into host actions BEYOND the grant -- a self-edit agent redirecting
  byre's OWN host-side writes/deletes outside the mounted store (the
  build-context symlink escape) is a confused-deputy bug, fixed, not this
  parked class. The line: inside the grant is the agent's; byre becoming
  a lever on the host beyond it is not.
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
- **Qualifying the README headline** ("without risking the farm") -- Pete,
  2026-09-05: the H1 stays, permanently. It is a safety idiom; the plain
  what-it-is sentence under it and the security-model page carry the
  contract. Reviewers WILL re-find the "promise exceeds threat model" class
  -- don't re-raise, don't hedge the line.
