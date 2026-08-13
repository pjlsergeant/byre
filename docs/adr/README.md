# The doctrine index -- and how to review against it

One line per standing decision: every ADR in this directory and every
numbered principle in `docs/PRINCIPLES.md`, each ending with its
enforcement marker. `[arm: TestName]` names a test that fails when the
decision is violated; `[no arm]` means the rule is convention only --
nothing automated will catch a breach, so a reviewer is the only
tripwire it has. `[arm(gated): TestName]` is an arm that does not run in
the unit suite: it needs a real engine (`BYRE_DOCKER_TESTS=1`) or a real
tmux (`BYRE_TUI_TESTS=1`), so `go test ./...` on a contributor's machine
says nothing about it and CI's gated job is where it actually fires.
Treat those like `[no arm]` while reviewing a change you have not pushed.

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
- An arm behind `BYRE_DOCKER_TESTS=1` or `BYRE_TUI_TESTS=1` is marked
  `[arm(gated): ...]`, and a gated test lives in a file whose NAME says
  so -- one with `integration` in it.
  `TestDoctrineIndexArmsResolve` checks the marker against that
  convention both ways: a gated marker on a test in an ordinary unit
  file fails, and so does a plain `[arm: ...]` on a test in a gated
  file. So an arm cannot quietly become unenforceable-by-default, or
  keep claiming it is once it runs everywhere. Directory is not the
  rule: `internal/tuitest` holds the harness's own ungated unit tests
  beside the pty ones, so a tuitest arm goes in an integration-named
  file like any other. A gated arm in a wrongly-named file fails with
  the convention in the message: move the test, don't weaken the
  marker.
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

- P0: the TUI is the differentiator, so a config key with no reachable editor row is a hole in the product -- a widget where the editor owns the key, a read-only row naming the owner where a flow does; the only exemptions are retired spellings no byre still writes, each named with its retirement [arm: TestEveryConfigKeyHasAReachableRow, TestFlowOwnedKeysAreShownReadOnlyNotHidden]
- P1: threat model is the agent, never the user -- degrade claims on user choices, never refuse [no arm]
- P2: core ships generic mechanism only; every opinion (agent, policy, endpoints) lives in a skill [no arm]
- P3: raw blocks are first-class and never parsed -- shown verbatim, posture claims degrade [no arm]
- P4: every grant is legible: status names it or the claim degrades; a grant status can't name isn't done; reporting surfaces render externally-sourced strings as data, never as terminal control [arm: TestRenderStatusEscapesExternalValues, TestStatusDevelopWarningsEscapeExternalValues, TestExitReportEscapesWatchedValues, TestSelfEditReportEscapesStoreContent, TestSkillInspectEscapesManifestValues, TestTransportReportsEscapeSourceNames, TestExclusiveRefusalEscapesEngineAndLabelValues, TestDeclinedAndRecordDisclosuresEscapeExternalText]
- P5: consent lives at the scope of its effect; keys with teeth are never written at a scope the user didn't answer for [no arm]
- P6: `byre config` reaches every config feature -- editable, or read-only with its owning flow named; it governs PARSEABLE config; hand-editing is a defended right, not the interface [no arm]
- P7: a dependency gap ends owned-around, replaced, or accepted on the record -- never passed off as design [no arm]

## ADRs

- 0001: no byre-side cache logic; generated Dockerfile output is byte-stable for unchanged config [arm: TestDockerfileGolden]
- 0002: talk to Docker/Podman only by shelling out to their CLIs behind the runner seam, never an SDK [no arm]
- 0003: sandbox config is read from the host-side store only; in-tree config is inert until applied (partly superseded by 0029) [arm: TestPresetApplyDeclineWritesNothing]
- 0004: sessions are found via byre.project/byre.workdir labels; develop refuses a second session per directory [arm: TestDevelopRefusesWhenSessionLive]
- 0005: agent CLI, launch command, and auth volume come from the skill `agent` selects; core hardcodes none [arm: TestResolveSampleAndAgentSkills]
- 0006: raw run_args append last and may override any byre flag, except byre's OWN LABEL SET, re-asserted after them and not overridable -- the exception is the whole of RunParams.Labels (identity, client pid, the netns nonce, the launch record address), not a fixed pair; amended 2026-07-29 [arm: TestRunArgsCoreFlagsAndOrder, TestLaunchLabelSurvivesASpoofingRunArg]
- 0007: never copy host agent credentials into a box; agents log in in-box, the state volume persists it [no arm]
- 0008: host UID/GID bakes at image build (USER dev, uid-qualified tag); no runtime chown, no root after PID 1 [arm(gated): TestIntegrationLaunchPathAndOwnership]
- 0009: worktrees inherit the main tree's project identity; mutating repo git runs in-box, never on the host; annotated -- the creds/history rejection's "agents already handle concurrent access to one state dir" is scoped to the AGENT STATE volume and was never a warranty for every declared volume (amended by 0054, 2026-07-29) [arm(gated): TestIntegrationConcurrentWorktreeSessions]
- 0010: firewall rules enter via an external root+NET_ADMIN helper joining the netns; the box gains no caps; annotated -- a contribution that displaces byre's machinery is disclosed whatever field it rides (0050, 0052), granted-channel consequences are disclaimed once, and the DNS residual is published on the security-model page [arm: TestNetnsInitArgv]
- 0011: the launcher gates on a loopback socket handshake and fails closed on timeout; never a state marker [arm: TestLauncherGateTimesOutClosed]
- 0012: the firewall allowlist derives from enabled skills' declared egress, port-scoped (partly superseded by 0019, 0020) [arm: TestFirewallComposesAgentEgress]
- 0013: seed_prefs copies only a skill-curated per-file allowlist of secret-free files, never a directory copy [arm: TestResolvePrefsRejectsWholeDir]
- 0014: no dockerfile= opt-out -- byre generates the build or isn't involved; the key fails loudly as unknown [arm: TestDockerfileKeyRejectedLoudly]
- 0015: mounts are disabled via a disabled=true field, never a mode value; the entry stays, emits no bind [arm: TestRunParamsSkipsDisabledMounts]
- 0016: one static binary per platform on v-tags; version = stamped tag > buildinfo > (devel), never faked [arm: TestResolve]
- 0017: shared agent login rides an opt-in companion skill plus a machine-scoped uid-qualified volume [arm(gated): TestIntegrationMachineVolumeSharedAcrossProjects]
- 0018: show effective state, edit only the open layer; !name removal for apt, remove=true for ports [arm: TestMergeAptNpmRemoval]
- 0019: user egress rides the `egress` config key (union, !removal); enforcement stays the firewall skill [arm: TestResolvedEgressUnionsConfigKey]
- 0020: only a skill's own functional egress auto-opens; convenience endpoints ship closed in egress_offered [arm: TestFirewallComposesAgentEgress]
- 0021: deliver is machine-scoped discovery, exec-streamed into root-parented /inbox, atomic no-clobber writes [arm(gated): TestIntegrationDeliverTransport]
- 0022: the CLI rides cobra but keeps byre's exit-code contract: usage errors exit 2 and never dispatch [arm: TestRunUsageErrors]
- 0023: grok-shared-auth v1 (symlink-shared auth.json) is retired; a symlinked auth.json never counts (superseded in part by 0036) [arm: TestGrokLoginHookHealsRetiredSymlink]
- 0024: onboarding offers shared auth only when a companion vouches shared_auth_for (partly superseded by 0025) [arm: TestSharedAuthCompanion]
- 0025: the shared-auth offer is per box: yes writes only this project's byre.config; saved answers only prefill, except under the two suppressions, which disclose at the switch [arm: TestOnboardSharedAuthDeclineRecordsNothingAndReasks, TestSkipQuestionsCheckboxDisclosesCredentialsUnticked, TestGlobalSkillsScreenDisclosesSharedAuthSuppression]
- 0026: host values cross only via env_from_host entries (attributed grants); git identity is the core layer [arm: TestResolveHostEnvPrecedenceAndStates]
- 0027: a containment hole gets its own loud 🛑 line; existing status rows stay undegraded (warranty model) [arm: TestRenderStatusContainmentAndSockGroups]
- 0028: env.d hooks: the exported environment is the only lasting effect -- computing an export is fine; output, prompts, mutation, or reconfiguring the sourcing shell belongs in firstrun.d [arm: TestClaudeSharedAuthEnvHookExportsOnly, TestBundledEnvdHooksHavePurityArms, TestDockerHostComposeEnvHookIsPure]
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
- 0047: the project mount can plant host-run code: byre exit-reports noticed changes, never gates; annotated -- the tools BYRE itself spawns resolve through one pinned resolver and decline a binary resolved out of a box-writable directory (engine = hard refusal, the report's own git probes = degrade + one disclosure, and an engine ENUMERATION never reads declined as absent -- totals commands refuse) [arm: TestExitReportGitConfig, TestLookDeclinesShadowedBinary, TestDevelopRefusesShadowedEngine, TestDevelopDisclosesShadowedGitAndContinues, TestInstalledEnginesReportsDeclinedSeparately, TestInstalledEnginesDeclinesRelativePathEntry, TestLifecycleEnginesRefusesOverADeclinedEngine]
- 0048: the four accretion guardrails are standing commitments in PRINCIPLES.md, unnumbered under the principles [no arm]
- 0049: a compat path lives two minors or 90 days from replacement, whichever is longer; the last supported release warns, then it is removed whole with a remedy; a LIVE key gets judgment instead of a window (RELEASING.md) [no arm]
- 0050: every config field classifies against the claim surface; claims render from effect; BYRE_ is reserved vocabulary, never reserved capability; annotated -- the conservative default for a key byre does NOT read is unchanged (network + launch degrade), but the note it prints stops calling such a key "byre runtime control": byre cannot say what a key it has never read does, and a skill's own BYRE_-prefixed variable is not a control of byre's [arm: TestEveryConfigFieldHasClaimClassification, TestChassisScriptKnobsRideReservedPrefix, TestValidateRejectsReservedEnvNamespace, TestValidateRejectsReservedNamespaceInEnvFromHost, TestLaunchBannerAndStatusDegradeOnOneReservedEnvSet, TestEditorExposureDegradesOnTheSameReservedEnvSet, TestReservedEnvNoteDoesNotOverclaimAnUnknownKey, TestReservedEnvUnknownKeyStillDegradesConservatively]
- 0051: the release proves transport integrity, not authenticity -- accepted on the record; -trimpath yes, attestation deferred with a trigger [no arm]
- 0052: a mount/volume over a byre-managed path gets ONE blanket containment disclosure on status, develop and the apply review, skills included and attributed; no per-claim degradation, and `files` keeps its granular reporting [arm: TestManagedPathShadows, TestRenderStatusManagedShadowDisclosure, TestShadowGrantLinesRideContainmentWeight]
- 0053: every container carries byre.launch=<sha256 of its launch record>; the record holds what byre TOLD THE ENGINE (env KEYS only, image digest, run_args verbatim) in the project store, status VERIFIES it by re-hash and a running box is then the page's subject with a Next launch delta whose every line must be a REAL difference (both sides normalized alike, egress compared after closures, base compared by EFFECTIVE value and RECORDED effective so a moved gen.DefaultBase still shows for records written under this rule -- pre-rule records holding "" cannot show it, accepted, run_args compared as argv); the reap's live set spans every engine byre can see and any uncertainty abandons it; only a provable absence is 'missing'; the record drives no host action [arm: TestLaunchRecordSerializationContract, TestLaunchRecordTamperIsDisclosedNotTrusted, TestLaunchRecordRefusesNonDigestLabel, TestLaunchRecordFromNewerByreRendersLivenessOnly, TestLaunchRecordUnreadableIsNotReportedAsMissing, TestLaunchLabelSurvivesASpoofingRunArg, TestDevelopWritesTheLaunchRecordAndLabelsTheContainer, TestLaunchRecordWriteFailureDegradesNeverBlocks, TestReapLaunchRecordsKeepsASiblingOnAnotherEngine, TestReapLaunchRecordsAbortsOverADeclinedEngine, TestStatusRendersTheRunningBoxAndTheDelta, TestStatusRunningBoxWithoutARecordQualifiesTheRows, TestStatusWithNoBoxIsUnchanged, TestNextLaunchEgressSubtractsClosuresLikeTheRecordDid, TestNextLaunchBindsCompareThroughTheSameExpansion, TestNextLaunchBaseClearedIsStillADelta, TestNextLaunchBaseSpellingTheDefaultIsNotADelta, TestNextLaunchBaseSurvivesADefaultBaseChange, TestNextLaunchRunArgsCompareAsArgvNotAsAJoinedString]
- 0054: a volume declares how many boxes may hold it -- `sharing = "shared"` (default, and what byre has always done) or `"exclusive"` (single-writer); develop reads this project's live boxes' LAUNCH RECORDS, never their config, and refuses (exit 3, the session-already-live family) both a proven holder and every state where it cannot establish there is none -- an engine it cannot list or will not run, unreadable labels, a sibling with no usable record; refused outright on machine scope, where the scan could not see the boxes that matter; the status ROWS mark the running box from its record while the Worktrees qualifier speaks for the NEXT develop and follows the current config; gated on the session declaring one, so nothing bundled pays for it [arm: TestValidateVolumeSharing, TestDevelopRefusesASiblingHoldingAnExclusiveVolume, TestExclusiveVolumeRefusesWhatItCannotProve, TestExclusiveVolumeCheckIsSkippedWithoutADeclaration, TestExclusiveVolumeAllowsASiblingHoldingSomethingElse, TestExclusiveVolumeIgnoresThisWorktreesOwnBox, TestExclusiveVolumeSeesAHolderOnAnotherEngine, TestExclusiveVolumeUncertaintyNamesEveryDeclaration, TestRenderStatusMarksExclusiveVolumes, TestWorktreesSharingQualifierFollowsTheNextLaunch, TestStatusDataCarriesVolumeSharing, TestVolumeSharingPickerWritesAndReads, TestSaveWritesVolumeSharing, TestBlockRenderersEmitEveryTaggedField, TestVolumeOverrideCarriesSharing, TestVolumeRowFlagsExclusiveSharing]
- 0055: the authoring round trip is supported end to end -- the store walk follows symlinked package dirs (the user's own tree, judged by what an entry resolves to), a same-id-same-kind local package SHADOWS the installed snapshot with the label announcing which version lost (any other pairing stays a conflict; uninstalling a shadowed snapshot is disclosed as cleanup, and takeover is promised only when the snapshot is actually a claimant), `adopt <dir>` symlinks a directory to the store path its declared id names with a catalog reload as the gate (non-LOCAL landing rolls the link out), and `pack -o` writes only after every read so the manifest inside the packed dir is a safe target [arm: TestSymlinkedPackageDirLoads, TestLocalShadowsInstalled, TestAdoptRoundTrip, TestAdoptShadowsInstalled, TestAdoptRefusals, TestAdoptRollsBackInvalid, TestPackOutIntoPackedDir, TestPackOutRefusesPayloadTarget, TestUninstallShadowedSnapshotIsCleanup, TestUninstallKindMismatchContestPromisesTakeover]
- 0056: two skills claiming one image destination must ship byte-identical staged content (the dual-ship pattern, both COPYs emitted) or the assemble refuses naming both skills and the dest; judged over the STAGED trees at develop, same-dest-STRING granularity, refusal names what differed; staging normalizes file modes git-style (0644/0755 by source exec bit) so umask bits never diverge claims but an exec-bit difference still refuses; resolve-time surfaces do not pre-judge it [arm: TestAssembleCrossSkillDestCollision]
- 0057: project credentials are confidentiality at rest + consent per launch, and NOTHING else -- values age-encrypted in the project store (per-entry to the vault recipient, identity scrypt-wrapped; cold staged writes need no passphrase), [[credentials]] declarations as the cascade-visible consent to the set, the passphrase as the per-launch consent act (three attempts, Enter skips, every skip/fail launches WITHOUT credentials, never a block), delivery over exec-stdin onto the session tmpfs with the manifest before the `.done` sentinel and a bounded FAIL-OPEN launcher wait (0011's deliberate opposite); byre's plaintext lives in exactly one filesystem place (the tmpfs, an 0052 managed path whose durable shadow can re-surface and re-export on bare restart -- disclosed, not gated); in-box code, user mounts, and the vault's own store are NOT defended (the v12 sweep deleted that apparatus; the payload name/project-id stamps are accident guards, not integrity; a store writer is the disclosed residual); the scrypt unwrap runs pre-lock, per-entry decrypts read once under the setup lock, creation is one staged rename, rekey rotates the passphrase only and refuses a vault replaced since its unlock, every pre-launch read is bounded; a value NEVER rides argv; the record carries unlock + decrypt outcomes ("scheduled", never "delivered") and there is NO live-state surface; stderr says plain "delivered" only when the inject provably landed inside the launcher's wait [arm: TestInlineRoundtrip, TestInlineRekeyLeavesValueBlobsByteIdentical, TestInlineKeyBindingMismatch, TestEncryptedRowsAreFileLocal, TestCredentialsSetMintsTheIdentityAndWritesTheRow, TestCredentialWritesCompareAndSwap, TestDevelopDeliversCredentials, TestDevelopNonTTYStopsWithRemedies, TestDevelopWrongPassphraseStopsTheLaunch, TestReceiverWritesValuesAndDoneLast, TestLauncherExportsEnvAndFileKinds, TestLauncherCredWaitFailsClosed, TestLauncherCredExportWinsEnvdCollision, TestInjectDeadlineUnderLauncherWait, TestCredDeliveredLineHonesty, TestManagedPathShadows, TestLauncherRestartWithoutRedeliveryRefuses, TestInjectFailureStopsTheBoxAndFailsTheLaunch]
