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

- [ ] (M) **fix shared-auth: gemini, grok, opencode** (rolled up
  2026-07-16 from three items, un-parking grok). gemini: run the OAuth
  gate — two boxes past the ~1h expiry, neither dying (API-key path
  already verified, ADR 0017). opencode: host-verify the EXPERIMENTAL
  skill from a live box (installer path ~/.opencode/bin, Pro/Max login,
  `--auto`, firewalled egress), then run its rotation gate; Pete reports
  the shared login already misbehaving (2026-07-16) — possibly gate 2
  failing in the field, diagnose as part of the gate; on pass, swap
  `companion_for` for `shared_auth_for`. grok: BUILT 2026-07-16 (v2
  auth broker, ADR 0036 — pre-build gates all answered from the
  published grok source); remaining is the FIELD gate: seed a live box,
  watch a ~6h rollover refresh through the broker, then swap
  `companion_for` for `shared_auth_for` (same shape as opencode's).
  `XAI_API_KEY` stays ruled out on cost. Facts + gate records:
  docs/AGENT-CREDENTIAL-MECHANICS.md + each skill.toml.
  Adjacent, ruling pending: $SHARED symlink-target check in the
  shared-auth hooks + codex-login's wildcard carve-out (2026-07-16
  review findings). Carried from the old opencode item: MCP seam probed
  (OPENCODE_CONFIG / OPENCODE_CONFIG_CONTENT exist) but merge-vs-replace
  needs a spike before any `mcp = "inject"` vouch (ADR 0033); gemini's
  seam still unprobed.
- [ ] (M) **Claude Skills delivery** (the untouched half of the old
  claude-skills.d item): skills/config ship Claude Skills (.md) into the box,
  likely via `--plugin-dir` payloads owned by the claude skill. Needs its own
  design pass; deliberately split from the MCP design 2026-07-14.
- [ ] (L) **Site.** Landing page + real docs, devlog demoted to `/devlog/`;
  the decided shape lives in docs/marketing/positioning.md "Site plan".
  v1 skeleton shipped 2026-07-15 (`site/`, hand-rolled Hugo, getbyre.com
  via Pages, docs seeded from the README); logo/favicon and the
  ask-your-agent conceit landed on both surfaces same day. Remaining:
  - DNS + Pages settings (Pete, host-side) -- believed done 2026-07-15,
    the deployed header was verified in-browser; strike on confirm.
  - Trim the README against the site pages: Quickstart, What's boxed,
    Configuration, Commands, Worktrees, Volumes & state, and "How do
    I...?" each have a real page under `/docs/` now; per the site plan
    the README keeps a simplified version + link, not the full text.
  - Landing comparison table: the "Why not…?" material is still
    README-only; the site plan puts the table on the landing page.
  - Screencast hero on the landing (the day-03-style clip -- the media
    the README shouldn't carry).
  - `/devlog/` -- devlog published under the site, linked as "see what's
    being built", never the front door.

## Standing

Disciplines and tripwires, not tasks.

- **Status/marketing lockstep:** README/site show `byre status` output as
  proof; re-verify against status.go after any status change.
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

- [ ] (M) **Agent field-QA pass, release-time, report-only** (Pete,
  2026-07-16; parked to here 2026-07-17): an agent in a byre box drives
  byre's TUI and deliver flows over tmux against the sacrificial inttest
  VM (egress closed except that one ssh endpoint) and reports findings
  with repro keystrokes; NEVER a gate -- findings harden into
  deterministic tuitest regression tests. The rails are already shipped
  (internal/tuitest, ADR 0038; shell vocabulary in BYRE-DEVELOPMENT.md).

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
