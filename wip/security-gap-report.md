# Where byre currently falls short

Status: implementation handoff, 2026-09-05. This is a point-in-time review,
not settled project doctrine. Absorb shipped decisions into the relevant ADRs,
docs, and `TODO.md`, then delete this file per `wip/README.md`.

Progress, 2026-09-05: recommended-order items 1-3 shipped (commits 4dca7809
through e3d998ab; TODO items removed). Item 1's stated scenario was not
reachable as written -- templates may not carry `agent` -- the real axis is
an extends flip to a layer that sets one; the same work also fixed the
editor showing default.config's favourite agent as inherited, and item 2's
refusal exposed `template init` and `fork` laying `[package]` over body
keys (now placed like `pack`). Items 4-7 remain and need the maintainer.

## Executive verdict

byre is a thoughtfully engineered personal tool with unusually honest
documentation, strong host-path handling, and serious automated testing. This
review found no obvious new host-escape vulnerability or reachable dependency
vulnerability.

Its largest weaknesses are not sloppy implementation. They are:

- a security ceiling substantially narrower than the headline positioning
  suggests;
- two real configuration correctness defects;
- an incomplete network-containment story;
- several maturity and coverage gaps around agents, platforms, releases, and
  team use.

The concise judgment: byre is strong against an over-eager agent, not a
hardened boundary for a malicious or prompt-injected one; credible for a
single informed operator, not yet a mature multi-user or team platform.

## Ranked findings

### 1. The headline promise exceeds the actual threat model

**Class:** accepted architectural boundary plus positioning mismatch.
**Confidence:** high.

The README says “`--dangerously-skip-permissions`, without risking the farm”
(`README.md:26`). The actual contract explicitly excludes resistance to an
actively malicious agent, including one following hostile instructions, and
shares the host kernel (`site/content/docs/security-model.md:25-31,44-49`).

Two unavoidable channels remain:

- Networking is open by default and can exfiltrate the project and every
  credential given to the box (`security-model.md:153-162`).
- The writable project is a host-execution channel. An agent can plant Git
  hooks/config, editor tasks, `.env` files, filters, or build scripts that
  later execute as the user; some never appear in `git diff`
  (`security-model.md:227-255`, ADR 0047).

The detailed docs are admirably candid. The short-form promise is not. byre
materially reduces accidental reach, but “without risking the farm” does not
survive the prompt-injection case users associate with autonomous agents.

**Suggested disposition:** keep the narrow threat model, but qualify the
headline at its point of use. State that byre contains accidental host reach;
the project tree and, by default, the network remain channels. Do not imply
malicious-agent containment.

### 2. The firewall is not yet exfiltration containment

**Class:** accepted v1 residual; advanced but unshipped replacement design.
**Confidence:** high.

Deny-by-default still permits DNS to the box's configured resolvers, so an
agent can tunnel arbitrary data through DNS (`security-model.md:164-183`).
Hostname grants are also launch-time IP snapshots and become unavailable when
CDN answers rotate (`security-model.md:216-225`). An allowlisted platform or
CDN is a channel, not a permission, and may itself provide upload endpoints.

This matters because the docs recommend pairing credentials with the
firewall. That improves containment considerably, but does not make an
unlocked credential non-exfiltratable.

The filtering-resolver work is not merely an idea. `wip/filtering-resolver.md`
contains a v11 design after nine blinded review rounds. It is still unratified
and carries build-gating probes, notably Docker Desktop wiring and Podman log
rotation (`wip/filtering-resolver.md:1054-1080`).

**Suggested disposition:** promote the filtering resolver from “maybe
someday” to the next security feature. Ratify the design, run every named
probe, build it, and update the firewall/security claims in the same unit.

### 3. The config editor can save the opposite of an explicit choice

**Class:** genuine known product defect.
**Confidence:** high; acknowledged in `TODO.md:50-54` and confirmed in source.

Inheritance is computed once in `configui.model.loadConfig`
(`internal/configui/form.go:510-545`). Switching `template` or `extends`
changes only the picker selection (`form.go:818-833`) and does not recompute
the dependent inheritance rows, labels, or `agentNow()`.

Concrete consequence: after changing a template, selecting agent `none` can
save absence. The next launch then inherits the new template's agent despite
the user's explicit `none` choice.

This is not cosmetic. PRINCIPLES P0 says an editor that misreports effective
state or cannot express an off-switch is equivalent to an engine defect
(`docs/PRINCIPLES.md:50-53`). A changed agent can also change enabled skills,
egress, state, and credentials.

**Suggested disposition:** fix first. Make template/extends transitions
recompute all dependent effective-state rows atomically, while preserving
explicit stored selections. Add model-level transition tests and one tmux TUI
test covering template switch -> agent none -> save -> reload/resolve.

### 4. Skill validation can silently discard intended configuration

**Class:** genuine known product defect.
**Confidence:** high; acknowledged in `TODO.md:25-33` and confirmed in source.

Skill parsing removes the complete `[package]` tree and strictly decodes the
remainder (`internal/skills/skills.go:758-815`; package-tree removal lives in
`internal/packages/manifest.go:136-157`). TOML bare keys written after the
`[package]` header belong to that table, so a misplaced `files = {...}` or
similar key is removed with the manifest. `byre skill validate` then succeeds
while the contribution silently disappears.

This contradicts `decodeSkillFile`'s own invariant that typoed keys must not
silently produce a broken skill. It becomes security-relevant when the
missing contribution was expected to provide runtime protection.

**Suggested disposition:** fix second. Before stripping the package tree,
identify bare keys scoped under `[package]` that match skill/template
vocabulary and refuse with the exact move-above-header/use-real-table remedy.
Pin quoted headers, dotted keys, arrays, and multiline-value false positives.

### 5. Common dependency workflows require an excessively large grant

**Class:** missing capability, consciously parked.
**Confidence:** high.

For an agent that merely needs Postgres, Redis, or another local service,
byre has no managed sidecar mechanism. The practical in-box route is often
the `docker-host` skill, which is host-root-equivalent on native Linux and
bypasses the box firewall for daemon-created containers
(`docs/DOCKER-HOST.md:13-25,37-45`). It also exposes all byre volumes and
shared-auth state and leaves agent-created Docker objects outside byre's
lifecycle (`DOCKER-HOST.md:47-60`).

The docs acknowledge that service sidecars would solve the common dependency
case without granting the daemon, but they remain “maybe someday”
(`TODO.md:133-136`).

**Suggested disposition:** after the resolver, design the generic companion
container mechanism that the resolver can establish. Then implement service
sidecars on those rails. This removes pressure to enable byre's largest
optional grant.

### 6. The flagship agent has the weakest live drift detection

**Class:** test-coverage gap.
**Confidence:** high.

Claude is installed through an unpinned live `curl | bash` installer and byre
depends on changing CLI flags and state-layout assumptions
(`internal/builtins/skills/claude/skill.toml:22-37,61-100`). The scheduled
agent-contract matrix covers OpenCode, Codex, Gemini, and Grok, but not Claude
(`.github/workflows/agents.yml:32-42`). There is no
`TestAgentContractClaude` beside the four existing contract tests.

Other coverage edges:

- macOS runs engine-free tests only; real engine behavior is tested on Linux
  (`.github/workflows/ci.yml:80-97`);
- the real SSH loop is excluded from CI and depends on the sacrificial runner
  (`ci.yml:148-149`);
- rootless Docker is not detected as rootless and receives rootful ownership
  handling (`site/content/docs/install.md:59-66`).

**Suggested disposition:** add the loginless Claude contract canary now. Make
the SSH-loop suite scheduled on trusted disposable infrastructure. Record a
repeatable Docker Desktop manual gate until hosted engine testing is viable.

### 7. Distribution and compatibility remain personal-project grade

**Class:** accepted maturity risk.
**Confidence:** high.

Release binaries and checksums come from the same GitHub release. There is no
signature, SBOM, or provenance attestation
(`docs/adr/0051-release-authenticity-accepted.md:9-15`). The documented
rationale is that nobody currently verifies attestations and signing adds
maintenance. That reasoning is coherent, but byre is a host-side tool that
orchestrates Docker and sensitive host paths, so compromise has unusually
high leverage. GitHub build attestation was explicitly identified as a
low-cost, no-key-management option (`ADR 0051:35-47`).

The project is explicitly single-maintainer (`CONTRIBUTING.md:1-8`), and its
ordinary compatibility window is currently suspended under a “no external
users” assumption (`ADR 0049:19-31`). Live keys may still be removed in a
minor release (`ADR 0049:33-36`).

**Suggested disposition:** add provenance attestation; keep checksum wording
honest. Reinstate normal compatibility windows as soon as anyone else adopts
byre. Recommend version pinning until then.

### 8. Smaller legibility and product gaps

- `byre status` correctly reports an empty `env_from_host` source and excludes
  it from exposure totals (`internal/commands/status.go:504-555`). The
  remaining defect is narrower: `develop` resolves the outcomes but prints no
  explicit warning, so missing Git identity may first surface as a failed
  in-box commit (`internal/commands/develop.go:473-485`; `TODO.md:35-40`).
- Project volumes are not UID-qualified, so Unix users sharing one checkout
  and daemon can accidentally share or destroy each other's agent state
  (`security-model.md:110-122`).
- Shared-auth deliberately lets one compromised project read that agent
  credential across projects (`security-model.md:277-285`).
- The TUI is declared the product's differentiator, but the site's demo
  pipeline is disabled and the demo slots are unpublished (`TODO.md:56-71`).
- Private authenticated package sources are unsupported, limiting
  organizational adoption (`TODO.md:124-126`).

## Recommended implementation order

1. Fix stale TUI inheritance.
2. Fix silent package-key swallowing.
3. Add the develop-time empty-host-value warning.
4. Ratify, probe, and build the filtering resolver.
5. Add the Claude live contract canary and automate the SSH loop.
6. Establish generic companion-container rails and build service sidecars.
7. Add release provenance and qualify the headline security claim.

The first three are bounded correctness/legibility fixes already consistent
with settled doctrine. Items 4 and 6 require their existing design-first
process. Item 7 deliberately reopens accepted positions and therefore needs
the maintainer to rule, not an opportunistic patch.

## Verification performed for this review

Passed locally:

- `go test ./...`
- `go vet ./...`
- race-enabled tests across all packages
- live tmux TUI integration tier
- `bash -n` across tracked shell files
- `gofmt -l .` (no output)
- `govulncheck`: zero reachable vulnerabilities

Passed on the authorized sacrificial runner:

- Docker integration suite, including firewall enforcement and fail-closed
  restart, security-guard behavior, credential delivery, lifecycle, volume
  ownership, worktree concurrency, and TUI flows.

Not independently exercised in this review:

- the Podman integration leg;
- live agent-installer canaries;
- the real SSH-loop tier;
- macOS/Docker Desktop engine behavior.

No product files were changed during the review. The pre-existing untracked
`wip/filtering-resolver.md` was read but not modified.

## Doctrine and contribution notes

This report checked the relevant decisions rather than treating every sharp
edge as an accidental bug. In particular:

- P0/P6 make the editor defect product-critical.
- P1/P4 deliberately prefer truthful legibility over blocking the user's raw
  Docker choices.
- P2 makes skills trusted code and keeps firewall policy outside core.
- ADRs 0010, 0047, 0051, 0052, 0053, 0054, and 0057 explicitly record many
  of the residuals above.
- CONTRIBUTING's settled positions on the flat `internal/commands` package,
  duplicated test helpers, long Cobra help, and ADR count were reviewed and
  are not findings here.

Before filing any newly discovered bug from implementation work, follow
`CONTRIBUTING.md`: trace it to source, obtain an independent confirmation,
and attach `byre version`, `byre status`, and the relevant generated artifact.
