# The doctrine index -- and how to review against it

One line per standing decision: every ADR in this directory and every
numbered principle in `docs/PRINCIPLES.md`, each ending with its
enforcement marker. `[arm: TestName]` names a test that fails when the
decision is violated; `[no arm]` means the rule is convention only --
nothing automated will catch a breach, so a reviewer is the only
tripwire it has.

The one-liners are hooks for judging "does this bear on my change",
not summaries -- the ADR or principle stays the record. Where a line
and its ADR disagree, the ADR wins and the line is a bug.

## How to review against it

Every review of a change to this repo (the `byre-codereview` run after
each feature or fix, and any ad-hoc reviewer) checks the diff against
this index:

1. Scan the one-liners. For each entry that could bear on the change,
   read the full ADR or principle and check the change complies.
2. State the result explicitly in the review, e.g.
   `Doctrine: applies -- 0044, P6; compliant` or
   `Doctrine: none apply`. A review without this line has not done
   the check.
3. Give `[no arm]` entries extra suspicion: for those, this check is
   the only enforcement they get.
4. When the diff adds a defensive bound, guard, cap, or re-assertion,
   ask where the sibling is: the same class of input one field or one
   call site over, still uncovered. Require it covered or named as
   consciously out of scope -- a fix that stops at the instance leaves
   the class.

## Maintenance

- A new ADR or principle adds its line here in the same commit --
  `TestDoctrineIndexCoversCorpus` fails otherwise.
- When an enforcement arm is built, renamed, or deleted, the marker
  changes with it -- `TestDoctrineIndexArmsResolve` fails on names
  that no longer match a test function.
- An arm may be partial (a tripwire on the decision's core, not proof
  of every clause); a marker never claims completeness. When in doubt
  whether a test qualifies, it doesn't -- `[no arm]` is the honest
  default.
- An ADR that accepts a residual -- a limitation users are left living
  with -- puts that residual on the user-facing security-model page
  (`site/content/docs/security-model.md`) in the same unit of work; a
  disclosure only contributors can find has not been made. Convention
  by choice: no arm.
- Superseded ADRs keep their line, noted, stating what they decided.

## Principles

- P1: threat model is the agent, never the user -- degrade claims on user choices, never refuse [no arm]
- P2: core ships generic mechanism only; every opinion (agent, policy, endpoints) lives in a skill [no arm]
- P3: raw blocks are first-class and never parsed -- shown verbatim, posture claims degrade [no arm]
- P4: every grant is legible: status names it or the claim degrades; a grant status can't name isn't done [no arm]
- P5: consent lives at the scope of its effect; keys with teeth are never written at a scope the user didn't answer for [no arm]
- P6: `byre config` reaches every config feature; hand-editing is a defended right, not the interface [no arm]
- P7: a dependency gap ends owned-around, replaced, or accepted on the record -- never passed off as design [no arm]

## ADRs

- 0001: no byre-side cache logic; generated Dockerfile output is byte-stable for unchanged config [arm: TestDockerfileGolden]
- 0002: talk to Docker/Podman only by shelling out to their CLIs behind the runner seam, never an SDK [no arm]
- 0003: sandbox config is read from the host-side store only; in-tree config is inert until applied (partly superseded by 0029) [arm: TestPresetApplyDeclineWritesNothing]
- 0004: sessions are found via byre.project/byre.workdir labels; develop refuses a second session per directory [arm: TestDevelopRefusesWhenSessionLive]
- 0005: agent CLI, launch command, and auth volume come from the skill `agent` selects; core hardcodes none [arm: TestResolveSampleAndAgentSkills]
- 0006: raw run_args append last and may override any byre flag, except the re-asserted identity labels [arm: TestRunArgsCoreFlagsAndOrder]
- 0007: never copy host agent credentials into a box; agents log in in-box, the state volume persists it [no arm]
- 0008: host UID/GID bakes at image build (USER dev, uid-qualified tag); no runtime chown, no root after PID 1 [arm: TestIntegrationLaunchPathAndOwnership]
- 0009: worktrees inherit the main tree's project identity; mutating repo git runs in-box, never on the host [arm: TestIntegrationConcurrentWorktreeSessions]
- 0010: firewall rules enter via an external root+NET_ADMIN helper joining the netns; the box gains no caps [arm: TestNetnsInitArgv]
- 0011: the launcher gates on a loopback socket handshake and fails closed on timeout; never a state marker [arm: TestLauncherGateTimesOutClosed]
- 0012: the firewall allowlist derives from enabled skills' declared egress, port-scoped (partly superseded by 0019, 0020) [arm: TestFirewallComposesAgentEgress]
- 0013: seed_prefs copies only a skill-curated per-file allowlist of secret-free files, never a directory [arm: TestResolvePrefsRejectsWholeDir]
- 0014: no dockerfile= opt-out -- byre generates the build or isn't involved; the key fails loudly as unknown [arm: TestDockerfileKeyRejectedLoudly]
- 0015: mounts are disabled via a disabled=true field, never a mode value; the entry stays, emits no bind [arm: TestRunParamsSkipsDisabledMounts]
- 0016: one static binary per platform on v-tags; version = stamped tag > buildinfo > (devel), never faked [arm: TestResolve]
- 0017: shared agent login rides an opt-in companion skill plus a machine-scoped uid-qualified volume [arm: TestIntegrationMachineVolumeSharedAcrossProjects]
- 0018: show effective state, edit only the open layer; !name removal for apt/npm, remove=true for ports [arm: TestMergeAptNpmRemoval]
- 0019: user egress rides the `egress` config key (union, !removal); enforcement stays the firewall skill [arm: TestResolvedEgressUnionsConfigKey]
- 0020: only a skill's own functional egress auto-opens; convenience endpoints ship closed in egress_offered [arm: TestFirewallComposesAgentEgress]
- 0021: deliver is machine-scoped discovery, exec-streamed into root-parented /inbox, atomic no-clobber writes [arm: TestIntegrationDeliverTransport]
- 0022: the CLI rides cobra but keeps byre's exit-code contract: usage errors exit 2 and never dispatch [arm: TestRunUsageErrors]
- 0023: grok-shared-auth v1 (symlink-shared auth.json) is retired; a symlinked auth.json never counts (superseded in part by 0036) [arm: TestGrokLoginHookHealsRetiredSymlink]
- 0024: onboarding offers shared auth only when a companion vouches shared_auth_for (partly superseded by 0025) [arm: TestSharedAuthCompanion]
- 0025: the shared-auth offer is per box: yes writes only this project's byre.config; saved answers only prefill [arm: TestOnboardSharedAuthDeclineRecordsNothingAndReasks]
- 0026: host values cross only via env_from_host entries (attributed grants); git identity is the core layer [arm: TestResolveHostEnvPrecedenceAndStates]
- 0027: a containment hole gets its own loud 🛑 line; existing status rows stay undegraded (warranty model) [arm: TestRenderStatusContainmentAndSockGroups]
- 0028: env.d hooks may only export env -- anything that runs, prompts, or mutates belongs in firstrun.d [arm: TestClaudeSharedAuthEnvHookExportsOnly]
- 0029: packages: bundled ships from embed.FS only, installs are digest-verified; retired names get tombstones [arm: TestRetiredNamesTombstone]
- 0030: egress !closures survive the cascade, subtracting after skill union; portless !host closes every port [arm: TestResolvedEgressClosuresSubtractSkillEntries]
- 0031: env_from_host sources are a closed scheme set (git:/env:/tz:/""); TERM and TZ ship in the core layer [arm: TestEnvFromHostCoreLayerAndValidation]
- 0032: rootless Podman builds the generic 1000:1000 image and runs --userns=keep-id:uid=1000,gid=1000 [arm: TestDevelopKeepIDPath]
- 0033: the MCP set bakes to /etc/byre/mcp.json in every image; adapters inject, never write agent state [arm: TestMCPConfigJSONDeterministicAndShaped]
- 0034: companion_for declares pairing; shared_auth_for stays the vouch (implying it); both set refuses [arm: TestCompanionForSharedAuthForBothSetRefused]
- 0035: layers chain by one scalar `extends` parent, merged root-first; plain live files, never packages [arm: TestLoadCascadeWithExtendsChain]
- 0036: grok shared auth is the flock broker on GROK_AUTH_PROVIDER_COMMAND; the vouch waits on the field gate [arm: TestGrokSharedAuthBrokerShape]
- 0037: remote deliver is two headless ssh execs -- --boxes enumerate, one tar-stream deliver; no staging [arm: TestBoxesGrammar]
- 0038: TUI e2e tests drive the shipped binary in per-test tmux; semantic waits, no golden screens [no arm]
- 0039: Claude Skills bake to /etc/byre/claude-skills in every image; delivered by injection only [arm: TestDockerfileGolden]
- 0040: grab trusts nothing from the box: os.Root-anchored writes, no host file ever overwritten [arm: TestGrabNeverClobbers]
- 0041: skill blocks emit in provenance order (bundled, installed, local); enable order breaks ties [arm: TestSkillBlocksOrderByProvenance]
- 0042: all skill apt hoists above the skill blocks, one RUN per skill in provenance order [arm: TestDockerfileSkillAptHoistsAboveSkillBlocks]
- 0043: operator prose rides [[context]] config; the cascade is the scoping; size disclosed, never capped (delivery mechanics superseded by 0046) [arm: TestContextDeclMergeReplaceByName]
- 0044: config saves splice via internal/tomldoc -- untouched bytes and comments survive; one TOML library [arm: TestSavePreservesHandComments]
- 0045: seed_prefs merges as a tri-state (*bool): an explicit later false wins; unset inherits [arm: TestMergeSeedPrefsTriState]
- 0046: context reaches agents by injection vouched per skill; byre never writes an agent-owned file [arm: TestDockerfileAgentContextAndSelfEditDoc]
- 0047: the project mount can plant host-run code: byre exit-reports noticed changes, never gates [arm: TestExitReportGitConfig]
- 0048: the four accretion guardrails are standing commitments in PRINCIPLES.md, unnumbered under the principles [no arm]
- 0049: a compat path lives two minors or 90 days from replacement, whichever is longer; the last supported release warns, then it is removed whole with a remedy [no arm]
