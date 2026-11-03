# Skill `requires`: a declared dependency the user still enables

Decided 2026-11-02. A skill.toml may list, at top level, the skills it does
not work without: `requires = ["codex"]`. When the cascade resolves, every
name in it must be in the ENABLED set -- the `skills` list or the agent --
or `skills.Resolve` refuses: `skill "pjlsergeant/zai" requires "codex",
which is not enabled -- add it to skills = [...]`. byre never enables a
required skill on the user's behalf.

Principles: P5 (enabling a skill is consent to its egress, mounts, volumes
and build lines, and that consent lives at the scope the user answers for
-- a dependency declared by a third party's manifest cannot write it);
P0 (the Skills screen shows an unmet requirement on the row and leaves the
row switchable); P4 (`skill inspect` and the install grant summary print
the requirement as data before anyone enables the skill). Related: ADR
0005 (agents are skills), ADR 0034 (`companion_for`, the other
skill-to-skill fact), ADR 0058 (`develop -a` resolves exactly like a
written agent key).

## Why a typed field

zai, in the published skills repo, drives the codex binary: without codex
in the box its launcher exits 127 on every start, and its headless command
has no backend. Nothing in a manifest could say so, so a box with zai and
no codex built and launched cleanly and failed at the first exec. zai is
the first consumer; the opencode, mimo and vibe wrappers in the same repo
are candidates. A typed field beats prose in a description because byre
can check it at resolve, and every surface that lists a skill can show it.

## The decisions

**Met only by the enabled set, never enabled.** Names in `requires`
expand through the catalog like every other skill name and are compared
as canonical ids. The remedy names the key, and the user writes it at the
scope they mean (project, layer, or machine-wide).

**Identical under `develop -a`.** The run-scoped agent override resolves
through the same `Resolve`, so an override that drops a config's
implicitly-enabled agent also drops what that agent satisfied, and a skill
requiring it refuses rather than running without it.

**Validate judges what one manifest can say.** Each entry is held to the
skill-name grammar (no `!` marker, not the `none` sentinel, at most one
`/`), and a name listed twice is refused. An exact self-reference is
refused wherever the skill's id is known: the manifest-bytes check
(install, catalog ingest) and load (validate, develop). Aliases and
existence resolve against the catalog, so an alias spelling of the skill
itself refuses at resolve, and an unknown name is NOT a validate error --
the same split `docs/SKILLS.md` records for the `agent` scalar.

**Surfaces.** `skill inspect` and the install grant summary print one
`requires: <name>` line per entry, beside `companion_for` -- a dependency,
not a grant. The config editor's Skills screen annotates an ENABLED row
whose requirement no enabled row meets, `(requires codex -- not enabled)`,
after the row's state; the rows are rebuilt on every update and view, so
turning the requirement on clears the annotation at once.

## Rejected

**Auto-enabling the requirement.** Enabling zai would silently enable
codex: codex's egress opens, its state volume mounts, its installer runs
in the build, and none of it was answered for. A manifest is a third
party's text; it may say what it needs, never grant it (P5).

**The Skills screen's `disabled` slot.** That slot is how the editor marks
a row it cannot toggle (an INVALID package, a conflict): it renders `[-]`
and the toggle reports the reason instead of acting. Putting an unmet
requirement there would mean a user who turned codex off could not then
turn zai off -- an editor that cannot express an off-switch, which
P0 ranks with an engine bug. The annotation rides beside the state
instead.

## Compatibility

byre strict-decodes skill.toml, so a manifest carrying `requires` is
refused by every byre before the release that ships the field. A
published skill that adopts it bumps its `requires_byre` to that release.

[arm: TestResolveRefusesUnmetRequires, TestRequiresSelfRefused,
TestRequiresSelfRefusedAtTheBytesBoundary, TestInstallRefusesSelfRequirement,
TestRequiresUnderAgentOverride, TestSkillsScreenAnnotatesUnmetRequires,
TestSkillInspectEscapesManifestValues]
