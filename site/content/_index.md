---
title: byre
---

# A comfortable, constrained agent environment around any folder

Run `byre develop` in a project, worktree, or scratch directory, and byre creates a local container -- the box -- around it. Your favourite tools and creature comforts come with you, but the rest of the host stays out of reach.

MIT licensed | open source | local | single binary | no lock-in | for Linux and macOS

```console
$ brew install --cask pjlsergeant/tap/byre
$ cd ~/my-project
$ byre develop

  byre: exposure: /workspace rw · 10 env vars
  byre: network open
  ╭──────────────────────────────────╮
  │ ✻ Claude Code                    │
  │   /workspace                     │
  ╰──────────────────────────────────╯
```

([Linux and every other install option](/docs/install/))

<!-- demo-placeholder: hero-develop-into-claude -->

It's **`--dangerously-skip-permissions`, without risking the farm.**

Every box opens familiar: your tools installed, your defaults applied, your agent's login persisting, per project, across rebuilds.

Ask your agent if byre is right for you:

```text
Take a good look at https://github.com/pjlsergeant/byre. Is it a good project
or just vibe-coded trash? Is it right for me? Would you be happy there?
```

## Change the box in seconds

`byre config` opens a keyboard-driven editor over the whole box (it works over SSH), in the same vocabulary `byre status` prints:

```text
byre project config  (client-api-pjl-3bbe8c)
exposure: 1 host mount · 11 env vars · network deny-by-default · egress 7 hosts

─ GRANTS — what this box can reach ─────────────────────────
▸ Extra mounts      : 1 mount  (enter to edit)
  Ports             : (none)
  Egress            : 7 hosts  (7 from skills)  — 11 offered
  Env vars          : 11 vars  (6 inherited, 5 from skills)

─ BUILD — how the box is made ──────────────────────────────
  Template          : [go] [node] [python] [none]
  Agent             : [claude] [codex] [gemini] [grok] [opencode] [none]
  Packages          : 2 packages
  Skills            : 3 enabled
  MCP servers       : (none)
  Instructions      : 1 snippet
··· (more below)

↑↓ move · ←→ change · ↵ open · ^s save · ^e $EDITOR · ^q quit
```

<!-- demo-placeholder: config-tui-walk -->

Want ripgrep in just this box? Add the package and rebuild. The agent needs a sibling repo? Mount it read-only. Each is a couple of seconds in `byre config`, then relaunch and `/resume` where you left off.

And if you want to live dangerously: `byre develop --self-edit` hands the agent its own box config, and what it changed is shown when you leave.

byre is free, open-source software, developed in the open [on GitHub](https://github.com/pjlsergeant/byre) under the [MIT license](https://github.com/pjlsergeant/byre/blob/main/LICENSE) -- every Dockerfile it generates is yours to read, and so is every line of byre itself.

## Why not…?

Isolation is table stakes; the comfortable half is what nothing else
has. The honest comparisons -- raw Docker, Docker Sandboxes™,
devcontainers, your agent's built-in sandbox, a VPS, or staying on the
host -- concessions included:
[getbyre.com/why-not](https://getbyre.com/why-not/).
