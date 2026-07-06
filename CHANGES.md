# Changes

Revision history for byre, newest first. Hand-curated for humans: what a
user would notice, not what the commit log says. (The GitHub Release
changelog is generated from commit messages and is noisier than this
file.)

## v0.1.0 -- unreleased

First public release.

- `byre develop`: run an AI coding agent (Claude, Codex, or Gemini) in a
  throwaway, project-scoped container -- the box sees the project and
  what the config explicitly grants, nothing else.
- Config cascade (global defaults, starter templates, per-project
  config) with an interactive editor (`byre config`). A repo can carry a
  `byre.config` proposal; byre shows its grants and asks before
  adopting.
- Skills: composable capabilities (agents themselves, codex review
  tooling, a default-deny egress firewall with per-skill allowlists)
  that contribute build steps, mounts, and runtime setup.
- Legibility over gates: `byre status` reports exactly what the box is
  granted -- mounts, ports, env, network posture, egress allowlist.
- Git worktree support: worktrees inherit the main tree's config,
  volumes, and image; sessions run concurrently.
- Lifecycle commands: `shell`, `status`, `reset`, `forget`, `rebuild`,
  `rehome`, `skill update`, `dockerfile` (print the generated
  Dockerfile and leave).
- `byre version` / `--version`: release tag, module version, or build
  info -- whichever the binary honestly knows.
- Distribution: single static binary. `go install`, a checksum-verified
  `install.sh`, and a Homebrew cask (`pjlsergeant/tap`).
