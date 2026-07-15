---
title: Commands
weight: 40
description: every byre command in one table
---

| Command | What it does |
|---|---|
| `byre develop` | Generate, build on cache-miss, and run in the foreground. The main entry point. |
| `byre shell` | A second shell in the running session -- for logins, tests, poking around. |
| `byre worktree <name>` | New linked worktree on branch `<name>` + a session in it -- a parallel agent in one step. |
| `byre status` | What can this thing touch? Resolved config, mounts, skills, volumes, session. |
| `byre config [--global]` | Interactive config editor -- packages, mounts, agents, in seconds. |
| `byre dockerfile` | Print the generated Dockerfile. Your exit, whenever you want it. |
| `byre ejectfirewall` | Print the firewall sidecar as a standalone script -- the exit's last piece. |
| `byre reset [--force]` | Wipe this project's volumes. Names what dies first. |
| `byre forget [--force]` | Remove all of byre's host-side state for this directory. Never touches your project tree. |
| `byre rebuild` | Rebuild with `--no-cache` to pull fresh upstream versions. |
| `byre rehome <old-id>` | Re-point a moved/renamed directory's identity onto its new path. |
| `byre mcp add` / `remove` / `list` | Declare MCP servers for the box's agent session (`--global` for every project); remove understands the cascade. |
| `byre skill list` / `inspect` / `fork` | Discover, inspect, and fork skill packages. |
| `byre preset apply` / `inspect` | Review and apply a repo's `byre.preset` (or any path/URI) as this project's config. |
| `byre skill install <uri>` / `uninstall` | Fetch, hash-verify, and snapshot a skill package -- grants nothing until enabled in a box. |
| `byre skill pack <name>` | Emit a local skill's distribution manifest (payload hashes + digest). |
| `byre skill update` | Transitional: bundled packages update with byre itself. |
| `byre template list` / `inspect` / `fork` / `install` / `pack` | Same verbs for template packages. |
| `byre version` | Which byre is this? Release tag, module version, or build info. |
