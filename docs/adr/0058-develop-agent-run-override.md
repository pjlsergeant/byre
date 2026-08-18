# `byre develop --agent` on a configured project is a run-scoped override

Decided 2026-08-18. `develop --agent <name>` (shorthand `-a`) stops being
first-run-only: on a project that already has a byre.config it selects the
agent FOR THAT RUN, resolved exactly as if the config's `agent` key said so,
and writes nothing. On a first run it seeds the new config as before -- the
flag uniformly means "the agent for this run"; whether that also configures
the project is decided by whether the project was configured.

Principles: P1 (the user may point their own box at another agent without a
ceremony; the flag is the user's explicit act, so nothing needs a prompt);
P4 (the launch record and `byre status` say what actually launched, so the
config and the box can never silently disagree); P0 (the `agent` key keeps
its editor row; the flag is run-scoped like `--self-edit` and appears on the
status page, not in the editor). Related: ADR 0025 (shared auth is
picker-owned consent), ADR 0053 (the launch record this extends).

## The decisions

**Broad enable.** The named skill need not be enabled in the config: it is
enabled implicitly for the run, the same mechanism the `agent` key already
uses (`skills.Resolve` appends the agent to the enabled set). Its CLI bakes
into the image, its state volume mounts, its egress and injection adapters
ride the normal paths. Only INSTALLED skills qualify -- a missing package
keeps its install-hint error; acquisition on byre's initiative stays banned.
The explicit flag is the enable-consent, consistent with deny-by-default's
"a skill's own functional endpoints open when the user enables it".

**Replace, not add.** The composition is what `agent = "<name>"` would have
produced: a config agent that was only implicitly enabled drops out of the
run's box. Wanting both is the ride-along pattern (`skills = [...]`), which
composes with the override as it does with the key.

**No shared auth, structurally.** Shared-auth companions enter a box only
through the written config (onboarding writes them into `skills`); nothing
in resolution consults the picker preferences. An override therefore never
enables a companion and never asks the question -- ruled, and pinned by
test rather than by a guard nobody can point at.

**Nothing durable is written, but durable side effects are named.** The
project image tag temporarily becomes the override variant (the next plain
develop rebuilds back, layer-cached); the override agent's state volume
persists like any agent's. The launch announcement says the config is
untouched; `byre status` renders the running box's agent from the launch
record with an override qualifier (record fields `agent` +
`agent_override`; a pre-agent record leaves the row config-derived rather
than claiming "agentless").

**`--template` / `--shared-auth` stay first-run-only.** A template is build
identity, not a session mood; shared auth is a durable credential grant.
Both still refuse on a configured project.

## Mechanics

The override rides the resolved view (`resolveWithAgent`), applied to the
loaded config before `skills.Resolve` and carried through the under-lock
re-read -- a save landing while develop waits for the lock cannot resurrect
the config's agent. `--agent none` runs the box agentless for the run. On
an already-running session the flag has no effect and says so, like
`--self-edit`.

[arm: TestAgentOverrideResolvesLikeAWrittenKeyWithoutWriting,
TestAgentOverrideNoneRunsAgentless,
TestAgentOverrideUnknownSkillFailsNamingIt,
TestOnboardExistingConfigWithFlagErrors,
TestLaunchRecordCapturesWhatTheEngineWasTold,
TestStatusAgentRowSpeaksForTheLaunchedBox]
