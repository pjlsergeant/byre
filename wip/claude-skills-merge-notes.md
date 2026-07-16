# Merge notes: claude-skills branch (for the merging agent)

Written 2026-07-16 by the building session. Delete after the merge lands
(wip lifecycle). Design + decisions: docs/adr/0038-claude-skills-delivery.md
(on this branch). Session diary: .byre-devlog/DIARY.md (gitignored, this
box only).

## What this branch delivers

The "Claude Skills delivery" TODO item, complete: the `[[claude_skills]]`
config class (Claude Skills = Anthropic's SKILL.md dirs, shipped into the
box as wiring-not-a-grant), baked to /etc/byre/claude-skills in every
image, injected via the claude skill's `--add-dir` flag with a
`claude_skills = "inject"` vouch, plus status section, `byre claude-skill
add/remove/list`, and the config-UI editor screen. Design was grilled
with Pete and spike-verified in-box against claude 2.1.211 the same day
(spike facts recorded in the ADR).

The work is `eb5f905..dde66b8` (11 commits, one per layer: config ->
skills -> gen/build -> claude skill.toml -> status -> CLI verbs ->
configui -> docs -> 2 codereview fixes + a TODO update). Below it the
branch also carries the earlier wip-doc/spike commits (ea30a4e, ea13aa7 —
the wip design doc those add is DELETED again in b7e43a6, absorbed into
the ADR) and the already-integrated ssh-deliver history incl. a merge of
main (def96bb).

## State at handoff (dde66b8)

- gofmt / go vet / `go test ./...` clean.
- Independent codereview (codex): CLEAN after 3 rounds. The 2 real
  findings were irregular-file hazards in ValidateClaudeSkillDir (a FIFO
  blocking the staging copy / the SKILL.md read); fixed in 274ce59 and
  dde66b8 with tests.
- NOT done: the engine-side gated run (BYRE_DOCKER_TESTS=1). byre-inttest
  is missing from the dogfood box's PATH (tooling gap, flagged to Pete),
  so it must run host-side. TODO.md deliberately keeps the item OPEN
  until that passes — do not strike it at merge.

## Merge watch-list

- **ADR number collision**: this branch claims 0038. If main gained an
  0038 since, renumber (precedent: the deliver ADR was renumbered 0037
  in def96bb) — update the references in GLOSSARY.md, ARCHITECTURE.md,
  SKILLS.md, TODO.md, internal/config/claudeskills.go's comments, and
  cmd/byre/main.go help text.
- **Likely conflict files**: TODO.md (item rewritten), docs/GLOSSARY.md
  (two new entries after "MCP adapter"), docs/ARCHITECTURE.md (new
  subsection after "MCP provisioning", config-vocabulary sample, command
  list), cmd/byre/main.go (claudeSkillCmd registered after mcpCmd),
  internal/commands/status.go + resolve.go (fields beside the MCP trio).
- **New dependency**: gopkg.in/yaml.v3 (go.mod + go.sum) — explicitly
  approved by Pete 2026-07-16 for the SKILL.md frontmatter check. Not
  accidental; keep it.
- **Bundled claude skill changed** (internal/builtins/skills/claude/
  skill.toml): command gains `--add-dir /etc/byre/claude-skills`, new
  vouch key. Bundled manifests regenerate at release build — nothing
  manual.
- **Golden test**: gen's Dockerfile golden now includes the
  unconditional `COPY claude-skills /etc/byre/claude-skills` layer after
  the mcp layer. Any concurrent Dockerfile-shape change on main will
  conflict there — regenerate deliberately, order matters for layer
  caching (claude-skills after skills/agent, like mcp).

## Post-merge obligations

1. Host-side: run the gated integration suite; on pass, strike the TODO
   item (its text says exactly this).
2. Delete this file.
